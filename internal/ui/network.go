//go:build cgo

package ui

import (
	"encoding/binary"
	"net"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"

	"voxmesh/internal/audio"
	"voxmesh/internal/config"
	"voxmesh/internal/logging"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

func (a *App) receiveLoop(udp *transport.UDP) {
	for {
		packet, addr, err := udp.Receive()
		if err != nil {
			logging.Infof("bucle UDP detenido: %s", logging.FormatError(err))
			return
		}
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
			udp.AddPeer(addr)
			for index := range a.roomState.Participants {
				if a.roomState.Participants[index].Address == addrStr {
					a.roomState.Participants[index].LastSeenUTC = time.Now().UTC()
					break
				}
			}
			if packet.Kind == transport.PacketRoomHello {
				logging.Infof("Procesando PacketRoomHello de %s", addrStr)
				envelope, err := room.Decode(packet.Payload)
				if err != nil || envelope.Participant == nil {
					continue
				}
				envelope.Participant.Address = addrStr
				envelope.Participant.Connected = true
				envelope.Participant.LastSeenUTC = time.Now().UTC()

				// Asegurar nombre único: si ya existe otro participante conectado con el mismo nombre, añadir _PUTO
				candidateName := strings.TrimSpace(envelope.Participant.Username)
				if candidateName == "" {
					candidateName = config.DefaultUsername
				}
				for {
					taken := false
					for _, p := range a.roomState.Participants {
						if p.Connected && p.ID != envelope.Participant.ID && strings.EqualFold(strings.TrimSpace(p.Username), candidateName) {
							taken = true
							break
						}
					}
					if !taken {
						break
					}
					candidateName += "_PUTO"
				}
				envelope.Participant.Username = candidateName

				a.roomState.Participants = append([]room.Participant(nil), a.roomState.Participants...)
				if a.roomStore != nil {
					a.roomStore.Replace(a.roomState)
				}
				known := false
				for index := range a.roomState.Participants {
					if a.roomState.Participants[index].ID == envelope.Participant.ID {
						a.roomState.Participants[index] = *envelope.Participant
						known = true
						break
					}
				}
				if !known {
					envelope.Participant.Order = len(a.roomState.Participants)
					a.roomState.Participants = append(a.roomState.Participants, *envelope.Participant)
				}
				statePayload, _ := room.Encode(room.Envelope{Kind: room.StateMessage, Room: &a.roomState})
				_ = udp.Send(addr, transport.Packet{Kind: transport.PacketRoomState, Sequence: a.sequence.Add(1), Payload: statePayload})
				a.refreshPeersFromRoom()
				if candidate, ok := a.roomState.NextHost(); ok {
					if hostIP, _, err := net.SplitHostPort(candidate.Address); err == nil && hostIP != "" {
						candidateTarget := net.JoinHostPort(hostIP, strconv.Itoa(config.DefaultListenPort))
						a.updateConfig(func(c *config.Config) { c.LastPeer = candidateTarget })
						a.saveConfig()
						fyne.Do(func() {
							if a.endpointEntry != nil {
								a.endpointEntry.SetText(candidateTarget)
							}
						})
					}
				}
				logging.Infof("Cliente conectado: %s (%s)", envelope.Participant.Username, addrStr)
				joinedUser := envelope.Participant.Username
				go func(uName string) {
					if a.roomStore != nil {
						a.roomStore.Replace(a.roomState)
					}
					a.addSystemMessage(uName + " se conectó.")
				}(joinedUser)
				a.playEventSound(audio.SFXConnect)
				udp.Broadcast(transport.Packet{Kind: transport.PacketRoomState, Sequence: a.sequence.Add(1), Payload: statePayload}, addr)
			} else if packet.Kind == transport.PacketRoomLeave {
				envelope, err := room.Decode(packet.Payload)
				username := ""
				participantID := ""
				if err == nil && envelope.Participant != nil {
					username = envelope.Participant.Username
					participantID = envelope.Participant.ID
				}
				a.handleParticipantLeave(participantID, username, addr, udp)
			} else if packet.Kind == transport.PacketHello {
				username := strings.TrimSpace(string(packet.Payload))
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
			if packet.Kind == transport.PacketPing && len(packet.Payload) == 8 {
				_ = udp.Send(addr, transport.Packet{Kind: transport.PacketPong, Sequence: packet.Sequence, Payload: packet.Payload})
			}
			if packet.Kind == transport.PacketPong && len(packet.Payload) == 8 {
				timestamp := int64(binary.BigEndian.Uint64(packet.Payload))
				a.updatePeerLag(addrStr, time.Since(time.Unix(0, timestamp)))
			}
			if packet.Kind == transport.PacketAudio {
				a.handleAudioPacket(packet)
			}
			if packet.Kind == transport.PacketChat {
				a.handleChatPacket(packet.Payload)
				udp.Broadcast(packet, addr)
			}
			if packet.Kind == transport.PacketChatSync {
				a.handleSyncPacket(udp, addr, packet.Payload, true)
			}
			if packet.Kind == transport.PacketAssetChunk {
				a.handleAssetChunk(packet.Payload, addrStr)
				udp.Broadcast(packet, addr)
			}
			if packet.Kind == transport.PacketAssetMissing {
				a.handleAssetMissing(udp, addr, packet.Payload)
			}
			if packet.Kind == transport.PacketRoomMute {
				a.handleMutePacket(udp, addr, packet.Payload)
			}
		} else if packet.Kind == transport.PacketPing {
			a.lastHostSeen.Store(time.Now().UnixNano())
			_ = udp.Send(addr, transport.Packet{Kind: transport.PacketPong, Sequence: packet.Sequence, Payload: packet.Payload})
		} else if packet.Kind == transport.PacketPong && len(packet.Payload) == 8 {
			timestamp := int64(binary.BigEndian.Uint64(packet.Payload))
			a.updatePeerLag(addrStr, time.Since(time.Unix(0, timestamp)))
		} else if packet.Kind == transport.PacketRoomState {
			logging.Infof("Procesando PacketRoomState de %s", addrStr)
			envelope, err := room.Decode(packet.Payload)
			if err == nil && envelope.Room != nil {
				a.handleRoomState(*envelope.Room, udp)
			}
		} else if packet.Kind == transport.PacketRoomLeave {
			envelope, err := room.Decode(packet.Payload)
			username := "El host"
			if err == nil && envelope.Participant != nil && envelope.Participant.Username != "" {
				username = envelope.Participant.Username
			}
			logging.Infof("Notificación de desconexión recibida: %s", username)
			go a.addSystemMessage(username + " se desconectó.")
			a.playEventSound(audio.SFXDisconnect)
			if envelope.Participant != nil {
				a.roomState.MarkDisconnected(envelope.Participant.ID)
				a.refreshPeersFromRoom()
			}
		} else if packet.Kind == transport.PacketChat {
			a.handleChatPacket(packet.Payload)
		} else if packet.Kind == transport.PacketChatSync {
			a.handleSyncPacket(udp, addr, packet.Payload, false)
		} else if packet.Kind == transport.PacketAssetChunk {
			a.handleAssetChunk(packet.Payload, addrStr)
		} else if packet.Kind == transport.PacketAssetMissing {
			a.handleAssetMissing(udp, addr, packet.Payload)
		} else if packet.Kind == transport.PacketHello {
			a.setStatus("Conectado a " + addrStr)
		} else if packet.Kind == transport.PacketAudio {
			a.handleAudioPacket(packet)
		}
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

func (a *App) handleRoomState(state room.State, udp *transport.UDP) {
	previousHost := a.roomState.HostID
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
	if previousHost != "" && state.HostID != previousHost {
		for _, participant := range state.Participants {
			if participant.ID == state.HostID {
				address, err := net.ResolveUDPAddr("udp", participant.Address)
				if err == nil && address.IP != nil && !address.IP.IsUnspecified() {
					newEndpoint := address.String()
					a.setEndpoint(newEndpoint)
					a.updateConfig(func(c *config.Config) { c.LastPeer = newEndpoint })
					a.saveConfig()
					fyne.Do(func() {
						if a.endpointEntry != nil {
							a.endpointEntry.SetText(newEndpoint)
						}
					})
					// Abrir malgo tarda cientos de ms (segundos con Bluetooth): fuera de receiveLoop,
					// que tiene que seguir entregando el audio de los demás.
					a.stopEngine()
					go a.startAudio(udp, address, false)
					a.requestSoundSync()
					a.sendRoomHello(udp, address)
					a.sendHistorySync(udp, address)
				}
				break
			}
		}
	}
	go a.maybeSendSoundSync(udp)
}

func (a *App) sendRoomHello(udp *transport.UDP, address *net.UDPAddr) {
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
	payload, _ := room.Encode(room.Envelope{Kind: room.HelloMessage, Room: &a.roomState, Participant: &participant})
	_ = udp.Send(address, transport.Packet{Kind: transport.PacketRoomHello, Sequence: a.sequence.Add(1), Payload: payload})
	a.sendHistorySync(udp, address)
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

// sendMuteState avisa al host el mute propio. UDP puede perderlo: monitorHost lo reenvía
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
		statePayload, _ := room.Encode(room.Envelope{Kind: room.StateMessage, Room: &a.roomState})
		_ = udp.Send(addr, transport.Packet{Kind: transport.PacketRoomState, Sequence: a.sequence.Add(1), Payload: statePayload})
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

func (a *App) monitorHost(udp *transport.UDP, stop <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var failedHost string
	var candidateID string
	var candidateDeadline time.Time
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if !a.hostMode.Load() {
				if hostAddr := a.endpointAddr(); hostAddr != nil {
					payload := make([]byte, 8)
					binary.BigEndian.PutUint64(payload, uint64(time.Now().UnixNano()))
					_ = udp.Send(hostAddr, transport.Packet{Kind: transport.PacketPing, Sequence: a.sequence.Add(1), Payload: payload})
				}
				if !a.selfMuteSynced() {
					a.sendMuteState(udp)
				}
				a.maybeSendSoundSync(udp)
			}
			if a.hostMode.Load() || time.Since(time.Unix(0, a.lastHostSeen.Load())) < 6*time.Second {
				continue
			}
			if failedHost == "" {
				failedHost = a.roomState.HostID
				a.roomState.MarkDisconnected(failedHost)
				a.addSystemMessage("El host se desconectó.")
			}
			candidate, ok := a.roomState.NextHost()
			if !ok {
				return
			}
			if candidate.ID == a.participantID {
				a.becomeHost(udp)
				return
			}
			if candidate.ID != candidateID {
				candidateID = candidate.ID
				candidateDeadline = time.Now().Add(6 * time.Second)
				continue
			}
			if time.Now().After(candidateDeadline) {
				a.addSystemMessage("El candidato " + candidate.Username + " no está disponible.")
				a.roomState.MarkDisconnected(candidate.ID)
				candidateID = ""
			}
		}
	}
}

func (a *App) becomeHost(udp *transport.UDP) {
	logging.Infof("Migración iniciada: asumiendo el rol de host")
	a.hostMode.Store(true)
	a.roomState.HostID = a.participantID
	a.roomState.Epoch++
	for i := range a.roomState.Participants {
		if a.roomState.Participants[i].ID == a.participantID {
			a.roomState.Participants[i].CanBeHost = true
			a.roomState.Participants[i].Muted = a.isMuted.Load()
			break
		}
	}
	go a.configureHostNetwork(udp.Address().Port)
	for _, participant := range a.roomState.Participants {
		if participant.ID == a.participantID {
			continue
		}
		address, err := net.ResolveUDPAddr("udp", participant.Address)
		if err == nil {
			udp.AddPeer(address)
		}
	}
	a.stopEngine()
	a.startAudio(udp, nil, true)
	a.addSystemMessage(a.conf().Username + " será el nuevo host.")
	a.addSystemMessage("Creando nuevamente la sala...")
	payload, _ := room.Encode(room.Envelope{Kind: room.StateMessage, Room: &a.roomState})
	udp.Broadcast(transport.Packet{Kind: transport.PacketRoomState, Sequence: a.sequence.Add(1), Payload: payload}, nil)
	a.addSystemMessage("Sala disponible.")
	a.addSystemMessage("Reconectando participantes...")
	if a.pingStop == nil {
		a.pingStop = make(chan struct{})
	}
	go a.pingPeers(udp, a.pingStop)
	go a.sweepAssetLoop(udp, a.pingStop)
}

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
				payload := make([]byte, 8)
				binary.BigEndian.PutUint64(payload, uint64(time.Now().UnixNano()))
				_ = udp.Send(peer, transport.Packet{Kind: transport.PacketPing, Sequence: a.sequence.Add(1), Payload: payload})
			}
			for index := range a.roomState.Participants {
				p := &a.roomState.Participants[index]
				if p.ID == a.participantID || !p.Connected {
					continue
				}
				if !p.LastSeenUTC.IsZero() && now.Sub(p.LastSeenUTC) > 8*time.Second {
					peerAddr, _ := net.ResolveUDPAddr("udp", p.Address)
					a.handleParticipantLeave(p.ID, p.Username, peerAddr, udp)
				}
			}
		}
	}
}
