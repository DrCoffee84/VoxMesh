//go:build cgo

package ui

import (
	"encoding/binary"
	"errors"
	"net"
	"runtime/debug"
	"strings"
	"time"

	"fyne.io/fyne/v2"

	"voxmesh/internal/audio"
	"voxmesh/internal/config"
	"voxmesh/internal/logging"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

// participantTimeout es cuánto puede estar callado un participante antes de que el host lo dé por
// desconectado. Es el mismo período de gracia en el que el cliente le reintenta al host
// (room.Failover): un corte breve no lo saca de la sala.
const participantTimeout = 20 * time.Second

// receiveLoop es el único lector del socket de la conexión. fo es la migración de host de esta
// conexión: dice qué paquetes de un cliente vienen de su host y decide qué hacer con el estado de
// otra sala o con el aviso de que el host se va.
//
// Solo termina cuando se cierra el socket. Cualquier otro error de lectura se registra (máx. una
// línea cada receiveErrorLogEvery) y se sigue leyendo: antes el primer error dejaba sordo al
// host y la sala migraba sola.
func (a *App) receiveLoop(udp *transport.UDP, fo *room.Failover) {
	var lastErrorLog time.Time
	var errorsSinceLog int
	for {
		packet, addr, err := udp.Receive()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				logging.Infof("bucle UDP detenido: %s", logging.FormatError(err))
				return
			}
			errorsSinceLog++
			if time.Since(lastErrorLog) >= receiveErrorLogEvery {
				logging.Errorf("leer UDP (%d errores): %s", errorsSinceLog, logging.FormatError(err))
				lastErrorLog, errorsSinceLog = time.Now(), 0
			}
			time.Sleep(receiveErrorPause) // un error que se repite no gira en vacío
			continue
		}
		a.handlePacket(udp, fo, packet, addr)
	}
}

const (
	receiveErrorLogEvery = 10 * time.Second
	receiveErrorPause    = 50 * time.Millisecond
)

// handlePacket procesa un paquete recibido. Un panic al procesarlo (payload malformado de un
// peer) pierde ese paquete y queda en el log, pero no mata la app.
func (a *App) handlePacket(udp *transport.UDP, fo *room.Failover, packet transport.Packet, addr *net.UDPAddr) {
	defer func() {
		if value := recover(); value != nil {
			logging.Errorf("panic procesando paquete tipo %d de %s: %v\n%s", packet.Kind, addr, value, debug.Stack())
		}
	}()
	now := time.Now()
	// Audio: el host lo reenvía antes que nada, sin esperar a procesarlo.
	if packet.Kind == transport.PacketAudio && a.hostMode.Load() {
		udp.AddPeer(addr)
		udp.Broadcast(packet, addr)
	}
	addrStr := addr.String() // una sola vez por paquete
	if logging.IsTrace() {
		logging.Tracef("Tráfico UDP recibido: tipo=%d, origen=%s, tamaño=%d bytes", packet.Kind, addrStr, len(packet.Payload))
	}
	if a.hostMode.Load() {
		a.handleHostPacket(udp, fo, packet, addr, addrStr, now)
	} else {
		a.handleClientPacket(udp, fo, packet, addr, addrStr, now)
	}
}

func (a *App) handleHostPacket(udp *transport.UDP, fo *room.Failover, packet transport.Packet, addr *net.UDPAddr, addrStr string, now time.Time) {
	udp.AddPeer(addr)
	for index := range a.roomState.Participants {
		if a.roomState.Participants[index].Address == addrStr {
			a.roomState.Participants[index].LastSeenUTC = now.UTC()
			break
		}
	}
	switch packet.Kind {
	case transport.PacketRoomHello:
		a.handleRoomHello(udp, fo, packet.Payload, addr, addrStr, now)
	case transport.PacketRoomState:
		// Estado de otro host (una migración mientras yo estaba cortado, o dos hosts que se
		// encuentran): si tiene más autoridad, fo decide bajarse y failoverLoop lo hace.
		if envelope, err := room.Decode(packet.Payload); err == nil && envelope.Room != nil {
			fo.StateFrom(*envelope.Room, addrStr, now)
		}
	case transport.PacketRoomLeave:
		envelope, err := room.Decode(packet.Payload)
		username, participantID := "", ""
		if err == nil && envelope.Participant != nil {
			username = envelope.Participant.Username
			participantID = envelope.Participant.ID
		}
		a.handleParticipantLeave(participantID, username, addr, udp)
	case transport.PacketHello:
		a.handleLegacyHello(packet.Payload, addrStr)
	case transport.PacketPing:
		if len(packet.Payload) == 8 {
			_ = udp.Send(addr, transport.Packet{Kind: transport.PacketPong, Sequence: packet.Sequence, Payload: packet.Payload})
		}
	case transport.PacketPong:
		if len(packet.Payload) == 8 {
			timestamp := int64(binary.BigEndian.Uint64(packet.Payload))
			a.updatePeerLag(addrStr, time.Since(time.Unix(0, timestamp)))
		}
	case transport.PacketAudio:
		a.handleAudioPacket(packet)
	case transport.PacketChat:
		a.handleChatPacket(packet.Payload)
		udp.Broadcast(packet, addr)
	case transport.PacketChatSync:
		a.handleSyncPacket(udp, addr, packet.Payload, true)
	case transport.PacketAssetChunk:
		a.handleAssetChunk(packet.Payload, addrStr)
		// Reenvío con ritmo y sin esperar: receiveLoop sigue entregando la voz. Si la cola
		// está llena, a quien le falte el chunk lo pide de nuevo (AssetMissing).
		udp.TryBroadcastBulk(packet, addr)
	case transport.PacketAssetMissing:
		a.handleAssetMissing(udp, addr, packet.Payload)
	case transport.PacketRoomMute:
		a.handleMutePacket(udp, addr, packet.Payload)
	}
}

// handleClientPacket: del host cuenta todo; de otros solo lo que sirve para migrar (el estado de
// un host nuevo, el RoomHello de alguien que me pide entrar, pings). Así nadie ajeno mete estado,
// audio ni chat, y un ping de cualquiera no hace creer que el host sigue vivo.
func (a *App) handleClientPacket(udp *transport.UDP, fo *room.Failover, packet transport.Packet, addr *net.UDPAddr, addrStr string, now time.Time) {
	fromHost := fo.Seen(addrStr, now) == room.FromHost
	switch packet.Kind {
	case transport.PacketPing:
		// A cualquiera: durante una migración el Pong abre el router de los dos lados.
		_ = udp.Send(addr, transport.Packet{Kind: transport.PacketPong, Sequence: packet.Sequence, Payload: packet.Payload})
	case transport.PacketPong:
		if len(packet.Payload) == 8 {
			timestamp := int64(binary.BigEndian.Uint64(packet.Payload))
			a.updatePeerLag(addrStr, time.Since(time.Unix(0, timestamp)))
		}
	case transport.PacketRoomState:
		envelope, err := room.Decode(packet.Payload)
		if err != nil || envelope.Room == nil {
			return
		}
		if !fromHost {
			fo.StateFrom(*envelope.Room, addrStr, now)
			return
		}
		logging.Infof("Procesando PacketRoomState de %s", addrStr)
		a.handleRoomState(*envelope.Room, udp)
		fo.HostState(*envelope.Room, now)
	case transport.PacketRoomHello:
		// Alguien que también perdió al host me pide entrar: si me toca, soy el host nuevo.
		if envelope, err := room.Decode(packet.Payload); err == nil && envelope.Participant != nil {
			fo.HelloFrom(envelope.Participant.ID, now)
		}
	case transport.PacketRoomLeave:
		if fromHost {
			a.handleHostLeave(packet.Payload, fo, now)
		}
	case transport.PacketHello:
		a.setStatus("Conectado a " + addrStr)
	case transport.PacketAudio:
		if fromHost {
			a.handleAudioPacket(packet)
		}
	case transport.PacketChat:
		if fromHost {
			a.handleChatPacket(packet.Payload)
		}
	case transport.PacketChatSync:
		if fromHost {
			a.handleSyncPacket(udp, addr, packet.Payload, false)
		}
	case transport.PacketAssetChunk:
		if fromHost {
			a.handleAssetChunk(packet.Payload, addrStr)
		}
	case transport.PacketAssetMissing:
		if fromHost {
			a.handleAssetMissing(udp, addr, packet.Payload)
		}
	}
}

// handleRoomHello (host): alguien entra, vuelve después de un corte o llega tras una migración.
// Un RoomHello repetido de alguien que ya está (los reintentos de la reconexión y la migración)
// solo recibe el estado de nuevo: sin aviso, sonido ni difusión.
func (a *App) handleRoomHello(udp *transport.UDP, fo *room.Failover, payload []byte, addr *net.UDPAddr, addrStr string, now time.Time) {
	envelope, err := room.Decode(payload)
	if err != nil || envelope.Participant == nil {
		return
	}
	joining := envelope.Participant
	fo.HelloFrom(joining.ID, now)
	for _, participant := range a.roomState.Participants {
		if participant.ID == joining.ID && participant.Connected && participant.Address == addrStr {
			a.sendRoomState(udp, addr)
			return
		}
	}
	logging.Infof("Procesando PacketRoomHello de %s", addrStr)
	joining.Address = addrStr
	joining.Connected = true
	joining.LastSeenUTC = now.UTC()

	// Asegurar nombre único: si ya existe otro participante conectado con el mismo nombre, añadir _PUTO
	candidateName := strings.TrimSpace(joining.Username)
	if candidateName == "" {
		candidateName = config.DefaultUsername
	}
	for {
		taken := false
		for _, p := range a.roomState.Participants {
			if p.Connected && p.ID != joining.ID && strings.EqualFold(strings.TrimSpace(p.Username), candidateName) {
				taken = true
				break
			}
		}
		if !taken {
			break
		}
		candidateName += "_PUTO"
	}
	joining.Username = candidateName

	a.roomState.Participants = append([]room.Participant(nil), a.roomState.Participants...)
	known := false
	for index := range a.roomState.Participants {
		if a.roomState.Participants[index].ID == joining.ID {
			joining.Order = a.roomState.Participants[index].Order
			a.roomState.Participants[index] = *joining
			known = true
			break
		}
	}
	if !known {
		joining.Order = len(a.roomState.Participants)
		a.roomState.Participants = append(a.roomState.Participants, *joining)
	}
	if a.roomStore != nil {
		a.roomStore.Replace(a.roomState)
	}
	fo.Hosting(a.roomState, now)
	a.sendRoomState(udp, addr)
	a.refreshPeersFromRoom()
	// Si me voy, la dirección para volver es la del que queda de host: la que vio este socket (su
	// salida a internet), no la IP con el puerto por defecto.
	if candidate, ok := a.roomState.NextHost(); ok && candidate.Address != "" {
		candidateTarget := candidate.Address
		a.updateConfig(func(c *config.Config) { c.LastPeer = candidateTarget })
		a.saveConfig()
		fyne.Do(func() {
			if a.endpointEntry != nil {
				a.endpointEntry.SetText(candidateTarget)
			}
		})
	}
	logging.Infof("Cliente conectado: %s (%s)", joining.Username, addrStr)
	joinedUser := joining.Username
	go a.addSystemMessage(joinedUser + " se conectó.")
	a.playEventSound(audio.SFXConnect)
	a.broadcastRoomState(udp, addr)
}

// handleLegacyHello (host): PacketHello de versiones viejas, solo para mostrar el nombre.
func (a *App) handleLegacyHello(payload []byte, addrStr string) {
	username := strings.TrimSpace(string(payload))
	if username == "" {
		username = config.DefaultUsername
	}
	candidate := username
	selfName := a.conf().Username
	a.peerMu.Lock()
	for {
		taken := false
		for _, p := range a.peerNames {
			if p.address != addrStr && strings.EqualFold(p.name, candidate) {
				taken = true
				break
			}
		}
		if !taken && strings.EqualFold(selfName, candidate) {
			taken = true
		}
		if !taken {
			break
		}
		candidate += "_PUTO"
	}
	a.peerMu.Unlock()
	if a.addPeer(candidate, addrStr) {
		a.playEventSound(audio.SFXConnect)
	}
}

func (a *App) handleParticipantLeave(participantID, username string, addr *net.UDPAddr, udp *transport.UDP) {
	if addr != nil {
		udp.RemovePeer(addr)
	}
	found := false
	for index := range a.roomState.Participants {
		p := &a.roomState.Participants[index]
		if (participantID != "" && p.ID == participantID) || (addr != nil && p.Address == addr.String()) {
			if !p.Connected {
				return
			}
			p.Connected = false
			p.LastSeenUTC = time.Now().UTC()
			if username == "" {
				username = p.Username
			}
			found = true
			break
		}
	}
	if !found && username == "" {
		if addr != nil {
			username = addr.String()
		} else {
			username = config.DefaultUsername
		}
	}
	addrStr := ""
	if addr != nil {
		addrStr = addr.String()
	}
	logging.Infof("Cliente desconectado: %s (%s)", username, addrStr)
	a.refreshPeersFromRoom()
	leftName := username
	go func(uName string) {
		a.addSystemMessage(uName + " se desconectó.")
		if a.roomStore != nil {
			a.roomStore.Replace(a.roomState)
		}
	}(leftName)
	a.playEventSound(audio.SFXDisconnect)
	statePayload, _ := room.Encode(room.Envelope{Kind: room.StateMessage, Room: &a.roomState})
	udp.Broadcast(transport.Packet{Kind: transport.PacketRoomState, Sequence: a.sequence.Add(1), Payload: statePayload}, addr)
}

// handleHostLeave (cliente): el host avisa que se va. Llega repetido (UDP puede perder uno): se
// atiende una vez. fo arranca la migración ya, sin esperar el período de gracia.
func (a *App) handleHostLeave(payload []byte, fo *room.Failover, now time.Time) {
	envelope, err := room.Decode(payload)
	if err != nil || envelope.Participant == nil {
		return
	}
	leaving := envelope.Participant
	connected := false
	for _, participant := range a.roomState.Participants {
		if participant.ID == leaving.ID {
			connected = participant.Connected
			break
		}
	}
	if !connected {
		return
	}
	username := leaving.Username
	if username == "" {
		username = "El host"
	}
	logging.Infof("Notificación de desconexión recibida: %s", username)
	go a.addSystemMessage(username + " se desconectó.")
	a.playEventSound(audio.SFXDisconnect)
	a.roomState.MarkDisconnected(leaving.ID)
	a.refreshPeersFromRoom()
	if leaving.ID == a.roomState.HostID {
		fo.HostLeft(now)
	}
}

// handleRoomState (cliente) aplica el estado que manda el host. El cambio de host lo decide
// room.Failover (HostState), no acá.
func (a *App) handleRoomState(state room.State, udp *transport.UDP) {
	for _, p := range state.Participants {
		if p.ID == a.participantID {
			if oldName := a.conf().Username; p.Username != "" && p.Username != oldName {
				newName := p.Username
				a.updateConfig(func(c *config.Config) { c.Username = newName })
				a.saveConfig()
				fyne.Do(func() {
					if a.selfNameLabel != nil {
						a.selfNameLabel.SetText(newName)
					}
				})
				a.setStatus("Nombre en uso. Tu nuevo nombre es: " + p.Username)
				a.addSystemMessage("Tu nombre de usuario se cambió a " + p.Username + " porque ya existía otro en la sala.")
				logging.Infof("Nombre cambiado por el host de '%s' a '%s' por duplicado", oldName, p.Username)
			}
			continue
		}
		if !p.Connected {
			continue
		}
		wasConnected := false
		for _, prev := range a.roomState.Participants {
			if prev.ID == p.ID && prev.Connected {
				wasConnected = true
				break
			}
		}
		if !wasConnected && len(a.roomState.Participants) > 1 {
			a.playEventSound(audio.SFXConnect)
		}
	}
	a.roomState = state
	a.refreshPeersFromRoom()
	if state.Name != "" && state.Name != a.activeRoomName {
		a.activeRoomName = state.Name
		roomName := state.Name
		a.updateConfig(func(c *config.Config) { c.RoomName = roomName })
		a.initHistory(state.Name)
	}
	if state.HostID == a.participantID || a.hostMode.Load() {
		return
	}
	go a.maybeSendSoundSync(udp)
}

// sendRoomHello se presenta al host (al conectar y al pasarse a un host nuevo) y le pide el
// historial que falta.
func (a *App) sendRoomHello(udp *transport.UDP, address *net.UDPAddr) {
	a.sendHelloOnly(udp, address)
	a.sendHistorySync(udp, address)
}

// sendHelloOnly es el RoomHello solo: los reintentos de la reconexión y la migración (cada 500 ms
// a 2 s) no repiten el pedido de historial.
func (a *App) sendHelloOnly(udp *transport.UDP, address *net.UDPAddr) {
	cfg := a.conf()
	participant := room.Participant{
		ID:          a.participantID,
		Username:    cfg.Username,
		Address:     udp.Address().String(),
		Connected:   true,
		CanBeHost:   cfg.AllowHostMigration,
		Muted:       a.isMuted.Load(),
		LastSeenUTC: time.Now().UTC(),
	}
	payload, _ := room.Encode(room.Envelope{Kind: room.HelloMessage, Participant: &participant})
	_ = udp.Send(address, transport.Packet{Kind: transport.PacketRoomHello, Sequence: a.sequence.Add(1), Payload: payload})
}

// sendPing manda un ping con la hora (el Pong de vuelta mide el lag).
func (a *App) sendPing(udp *transport.UDP, address *net.UDPAddr) {
	payload := make([]byte, 8)
	binary.BigEndian.PutUint64(payload, uint64(time.Now().UnixNano()))
	_ = udp.Send(address, transport.Packet{Kind: transport.PacketPing, Sequence: a.sequence.Add(1), Payload: payload})
}

// sendRoomState le manda el estado de la sala a uno solo.
func (a *App) sendRoomState(udp *transport.UDP, address *net.UDPAddr) {
	payload, err := room.Encode(room.Envelope{Kind: room.StateMessage, Room: &a.roomState})
	if err != nil {
		return
	}
	_ = udp.Send(address, transport.Packet{Kind: transport.PacketRoomState, Sequence: a.sequence.Add(1), Payload: payload})
}

// publishMuteState comunica el mute propio a la sala (correr fuera del hilo de UI). Muteado no
// sale audio, así que el estado viaja por señalización: el host lo aplica a su participante y
// difunde PacketRoomState; un cliente se lo manda al host con PacketRoomMute.
func (a *App) publishMuteState() {
	muted := a.isMuted.Load()
	a.setSelfMuted(muted)
	udp := a.transport.Load()
	if udp == nil {
		return
	}
	if a.hostMode.Load() {
		if a.roomState.SetMuted(a.participantID, "", muted) {
			a.broadcastRoomState(udp, nil)
		}
		return
	}
	a.sendMuteState(udp)
}

// sendMuteState avisa al host el mute propio. UDP puede perderlo: failoverLoop lo reenvía
// mientras el PacketRoomState del host no refleje el valor local (ver selfMuteSynced).
func (a *App) sendMuteState(udp *transport.UDP) {
	hostAddr := a.endpointAddr()
	if hostAddr == nil {
		return
	}
	participant := room.Participant{ID: a.participantID, Muted: a.isMuted.Load()}
	payload, _ := room.Encode(room.Envelope{Kind: room.MuteMessage, Participant: &participant})
	_ = udp.Send(hostAddr, transport.Packet{Kind: transport.PacketRoomMute, Sequence: a.sequence.Add(1), Payload: payload})
}

// selfMuteSynced informa si el estado de sala recibido del host ya tiene el mute local.
// Sin participante propio en el estado (todavía sin respuesta al Hello) no hay nada que reenviar.
func (a *App) selfMuteSynced() bool {
	muted := a.isMuted.Load()
	for _, participant := range a.roomState.Participants {
		if participant.ID == a.participantID {
			return participant.Muted == muted
		}
	}
	return true
}

// handleMutePacket (host) aplica el mute de un cliente. Solo se acepta desde la dirección que
// el participante registró en el Hello, así nadie puede mutear a otro. Si no cambió nada (un
// reenvío porque se perdió el estado) responde el estado solo al emisor como confirmación.
func (a *App) handleMutePacket(udp *transport.UDP, addr *net.UDPAddr, payload []byte) {
	envelope, err := room.Decode(payload)
	if err != nil || envelope.Participant == nil {
		return
	}
	if !a.roomState.SetMuted(envelope.Participant.ID, addr.String(), envelope.Participant.Muted) {
		a.sendRoomState(udp, addr)
		return
	}
	logging.Infof("Mute de %s (%s): %v", envelope.Participant.ID, addr.String(), envelope.Participant.Muted)
	a.refreshPeersFromRoom()
	a.broadcastRoomState(udp, nil)
}

// broadcastRoomState publica a.roomState en el store y lo difunde a los peers excepto except.
func (a *App) broadcastRoomState(udp *transport.UDP, except *net.UDPAddr) {
	if a.roomStore != nil {
		a.roomStore.Replace(a.roomState)
	}
	statePayload, _ := room.Encode(room.Envelope{Kind: room.StateMessage, Room: &a.roomState})
	udp.Broadcast(transport.Packet{Kind: transport.PacketRoomState, Sequence: a.sequence.Add(1), Payload: statePayload}, except)
}

// pingPeers (host) mide el lag de cada peer y da de baja a los que se callaron más de
// participantTimeout.
func (a *App) pingPeers(udp *transport.UDP, stop <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			now := time.Now().UTC()
			for _, peer := range udp.Peers() {
				a.sendPing(udp, peer)
			}
			for index := range a.roomState.Participants {
				p := &a.roomState.Participants[index]
				if p.ID == a.participantID || !p.Connected {
					continue
				}
				if !p.LastSeenUTC.IsZero() && now.Sub(p.LastSeenUTC) > participantTimeout {
					peerAddr, _ := net.ResolveUDPAddr("udp", p.Address)
					a.handleParticipantLeave(p.ID, p.Username, peerAddr, udp)
				}
			}
		}
	}
}
