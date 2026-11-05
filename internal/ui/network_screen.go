//go:build cgo

package ui

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"voxmesh/internal/i18n"
	"voxmesh/internal/logging"
	"voxmesh/internal/room"
	"voxmesh/internal/screen"
	"voxmesh/internal/transport"
)

// Pantalla compartida del lado de la red. Quién comparte y quién mira viaja en el estado de la sala
// (Participant.Sharing / Watching), como el mute: el host lo aplica (PacketScreenShare /
// PacketScreenWatch) y lo difunde con PacketRoomState; failoverLoop reenvía mientras el estado del
// host no tenga lo local, y el Hello lo lleva, así sobrevive a la migración. Los tiles
// (PacketScreenTile) van de quien comparte al host y del host solo a los que miran.

// screenState es la instantánea de la sala para la pantalla compartida. Se rearma en
// refreshPeersFromRoom (cada cambio de a.roomState) y se lee sin lock desde receiveLoop, la
// goroutine de quien comparte y el visor. Inmutable una vez publicada.
type screenState struct {
	sharers        map[string]string // ID → nombre de los conectados que comparten
	myWatcherIDs   []string          // los que miran mi pantalla (según el host)
	myWatcherNames []string
	copies         int // copias de pantalla que manda el host (room.State.ScreenCopies)
	// Solo en el host: rutas por dirección "IP:puerto" de quien comparte, y los destinos de la
	// pantalla propia.
	routes      map[string]screenRoute
	selfTargets []*net.UDPAddr
	// sharerAddrs: dirección de cada cliente que comparte (los pedidos de reparación van ahí).
	sharerAddrs map[string]*net.UDPAddr
}

type screenRoute struct {
	sharer  string
	peer    screen.PeerID
	targets []*net.UDPAddr // los que miran, sin el host (solo lectura: va a la cola tal cual)
	local   bool           // el host también mira esta pantalla
}

var emptyScreenState = &screenState{}

func (a *App) screenSnapshot() *screenState {
	if state := a.screenState.Load(); state != nil {
		return state
	}
	return emptyScreenState
}

// buildScreenState arma la instantánea desde el estado de la sala. Las direcciones son "IP:puerto"
// literales (sin DNS).
func buildScreenState(state room.State, selfID string, host bool) *screenState {
	next := &screenState{sharers: map[string]string{}, copies: state.ScreenCopies()}
	if host {
		next.routes = map[string]screenRoute{}
		next.sharerAddrs = map[string]*net.UDPAddr{}
	}
	for _, participant := range state.Participants {
		if !participant.Connected || !participant.Sharing {
			continue
		}
		next.sharers[participant.ID] = participant.Username
		watchers := state.Watchers(participant.ID)
		if participant.ID == selfID {
			for _, watcher := range watchers {
				next.myWatcherIDs = append(next.myWatcherIDs, watcher.ID)
				next.myWatcherNames = append(next.myWatcherNames, watcher.Username)
			}
		}
		if !host {
			continue
		}
		route := screenRoute{sharer: participant.ID}
		route.peer, _ = screen.ParsePeerID(participant.ID)
		for _, watcher := range watchers {
			if watcher.ID == selfID {
				route.local = true
				continue
			}
			if addr, err := net.ResolveUDPAddr("udp", watcher.Address); err == nil {
				route.targets = append(route.targets, addr)
			}
		}
		if participant.ID == selfID {
			next.selfTargets = route.targets
		} else {
			next.routes[participant.Address] = route
			if addr, err := net.ResolveUDPAddr("udp", participant.Address); err == nil {
				next.sharerAddrs[participant.ID] = addr
			}
		}
	}
	return next
}

// publishScreenState rearma la instantánea (desde refreshPeersFromRoom). Si la pantalla que miro
// dejó de compartirse (o se fue quien compartía), cierra el visor; si comparto, actualiza el
// banner con quién mira.
func (a *App) publishScreenState() {
	host := a.hostMode.Load()
	next := buildScreenState(a.roomState, a.participantID, host)
	a.screenState.Store(next)
	if udp := a.transport.Load(); host && udp != nil {
		a.updateScreenRates(udp) // alguien empezó a mirar: que la primera imagen salga ya al ritmo justo
	}
	if watch := a.screenWatch.Load(); watch != nil {
		if _, sharing := next.sharers[watch.target]; !sharing {
			logging.Infof("Pantalla de %s: dejó de compartir", watch.name)
			a.stopWatching()
			a.setStatus(i18n.M("status.screen_peer_stopped", watch.name))
		}
	}
	a.refreshShareControls()
}

// publishScreenIntent comunica lo local (comparto / a quién miro) a la sala: el host lo aplica a su
// participante y difunde; un cliente se lo manda al host. Fuera del hilo de UI.
func (a *App) publishScreenIntent() {
	udp := a.transport.Load()
	if udp == nil {
		return
	}
	if !a.hostMode.Load() {
		a.sendShareState(udp)
		a.sendWatchState(udp)
		return
	}
	sharing, target := a.screenShare.Load() != nil, a.watchTarget()
	shareChanged := a.roomState.SetSharing(a.participantID, "", sharing)
	watchChanged := a.roomState.SetWatching(a.participantID, "", target)
	if !shareChanged && !watchChanged {
		return
	}
	if shareChanged {
		a.announceSharing(a.conf().Username, sharing)
	}
	a.refreshPeersFromRoom()
	a.broadcastRoomState(udp, nil)
}

// watchTarget es el ID de la pantalla que miro ("" = ninguna).
func (a *App) watchTarget() string {
	if watch := a.screenWatch.Load(); watch != nil {
		return watch.target
	}
	return ""
}

// announceSharing (host) avisa en el registro de actividad de todos que alguien empezó o dejó de
// compartir. No se guarda (aviso del sistema).
func (a *App) announceSharing(username string, sharing bool) {
	if username == "" {
		username = i18n.T("default.username")
	}
	if sharing {
		go a.addSystemMessage("chat.sys.screen_started", username)
	} else {
		go a.addSystemMessage("chat.sys.screen_stopped", username)
	}
}

func (a *App) sendShareState(udp *transport.UDP) {
	if hostAddr := a.endpointAddr(); hostAddr != nil {
		participant := room.Participant{ID: a.participantID, Sharing: a.screenShare.Load() != nil}
		payload, _ := room.Encode(room.Envelope{Kind: room.ShareMessage, Participant: &participant})
		_ = udp.Send(hostAddr, transport.Packet{Kind: transport.PacketScreenShare, Sequence: a.sequence.Add(1), Payload: payload})
	}
}

func (a *App) sendWatchState(udp *transport.UDP) {
	if hostAddr := a.endpointAddr(); hostAddr != nil {
		participant := room.Participant{ID: a.participantID, Watching: a.watchTarget()}
		payload, _ := room.Encode(room.Envelope{Kind: room.WatchMessage, Participant: &participant})
		_ = udp.Send(hostAddr, transport.Packet{Kind: transport.PacketScreenWatch, Sequence: a.sequence.Add(1), Payload: payload})
	}
}

// resyncScreenState (cliente, failoverLoop): reenvía lo que el estado del host todavía no refleja.
// Sin participante propio en el estado (antes de la respuesta al Hello) no hay nada que reenviar.
func (a *App) resyncScreenState(udp *transport.UDP) {
	for _, participant := range a.roomState.Participants {
		if participant.ID != a.participantID {
			continue
		}
		if participant.Sharing != (a.screenShare.Load() != nil) {
			a.sendShareState(udp)
		}
		if target := a.watchTarget(); participant.Watching != target {
			a.sendWatchState(udp)
		}
		return
	}
}

// screenRates (host) elige el ritmo de las pantallas compartidas: si todos los que participan
// (quien comparte y los que miran, sin el host) son cercanos (near: IP privada y ping de LAN), el de
// LAN; si no, el de internet, para todos (un grupo mixto se cuida por el más lejano). total es lo
// que puede sacar la cola del host sumando destinos; perCopy, lo que puede mandar al host cada
// cliente que comparte (el host reenvía una copia por cada uno que mira). active: hay alguien
// mirando alguna pantalla.
func screenRates(state room.State, near func(addr string) bool) (total, perCopy int, lan, active bool) {
	lan = true
	for _, sharer := range state.Participants {
		watchers := state.Watchers(sharer.ID)
		if len(watchers) == 0 {
			continue
		}
		active = true
		for _, participant := range append(watchers, sharer) {
			if participant.ID != state.HostID && !near(participant.Address) {
				lan = false
			}
		}
	}
	total = transport.ScreenRateBytes
	if active && lan {
		total = transport.ScreenLANRateBytes
	}
	return total, total / max(1, state.ScreenCopies()), active && lan, active
}

// updateScreenRates (host, desde pingPeers cada 2 s y al cambiar quién mira) aplica screenRates:
// fija la cola propia y le dice a cada cliente que comparte cuánto puede mandar
// (PacketScreenRate, se repite cada 2 s: si se pierde uno llega el próximo).
func (a *App) updateScreenRates(udp *transport.UDP) {
	state := a.roomState
	total, perCopy, lan, active := screenRates(state, a.rttFloor.Near)
	udp.SetScreenRate(total)
	if !active {
		a.screenRateLogged.Store(false) // la próxima vez que alguien mire, se vuelve a anotar
	} else {
		changed := a.screenRateLAN.Swap(lan) != lan
		if first := !a.screenRateLogged.Swap(true); first || changed {
			mode := "internet"
			if lan {
				mode = "LAN"
			}
			logging.Infof("Pantalla: ritmo de %s (%.1f Mbps en total, %.1f Mbps por copia)", mode, float64(total)*8/1e6, float64(perCopy)*8/1e6)
		}
	}
	var payload [4]byte
	binary.BigEndian.PutUint32(payload[:], uint32(perCopy))
	for _, participant := range state.Participants {
		if participant.ID == state.HostID || len(state.Watchers(participant.ID)) == 0 {
			continue
		}
		if addr, err := net.ResolveUDPAddr("udp", participant.Address); err == nil {
			_ = udp.Send(addr, transport.Packet{Kind: transport.PacketScreenRate, Sequence: a.sequence.Add(1), Payload: payload[:]})
		}
	}
}

// screenHostRateTTL: el ritmo que mandó el host vale este tiempo (llega cada 2 s).
const screenHostRateTTL = 6 * time.Second

// handleScreenRate (cliente, del host): el ritmo que puede usar para su pantalla compartida.
func (a *App) handleScreenRate(payload []byte) {
	if len(payload) != 4 {
		return
	}
	a.screenHostRate.Store(int64(binary.BigEndian.Uint32(payload)))
	a.screenHostRateAt.Store(time.Now().UnixNano())
}

// clientScreenRate es el ritmo de quien comparte desde un cliente: el que mandó el host si es
// reciente; si no, el de internet repartido entre las copias (lo seguro).
func (a *App) clientScreenRate(copies int) int {
	if at := a.screenHostRateAt.Load(); at != 0 && time.Since(time.Unix(0, at)) < screenHostRateTTL {
		return int(a.screenHostRate.Load())
	}
	return transport.ScreenRateBytes / max(1, copies)
}

// handleSharePacket (host): un cliente empieza o deja de compartir. Solo desde la dirección que
// registró en el Hello; sin cambios (un reenvío) le vuelve el estado como confirmación.
func (a *App) handleSharePacket(udp *transport.UDP, addr *net.UDPAddr, payload []byte) {
	envelope, err := room.Decode(payload)
	if err != nil || envelope.Participant == nil {
		return
	}
	id, sharing := envelope.Participant.ID, envelope.Participant.Sharing
	if !a.roomState.SetSharing(id, addr.String(), sharing) {
		a.sendRoomState(udp, addr)
		return
	}
	username := ""
	for _, participant := range a.roomState.Participants {
		if participant.ID == id {
			username = participant.Username
		}
	}
	logging.Infof("Pantalla: %s (%s) comparte=%v", username, addr, sharing)
	a.announceSharing(username, sharing)
	a.refreshPeersFromRoom()
	a.broadcastRoomState(udp, nil)
}

// handleWatchPacket (host): un cliente empieza a mirar una pantalla o deja de mirarla.
func (a *App) handleWatchPacket(udp *transport.UDP, addr *net.UDPAddr, payload []byte) {
	envelope, err := room.Decode(payload)
	if err != nil || envelope.Participant == nil {
		return
	}
	if !a.roomState.SetWatching(envelope.Participant.ID, addr.String(), envelope.Participant.Watching) {
		a.sendRoomState(udp, addr)
		return
	}
	logging.Infof("Pantalla: %s mira %q", addr, envelope.Participant.Watching)
	a.refreshPeersFromRoom()
	a.broadcastRoomState(udp, nil)
}

// handleScreenFromSharer recibe lo que manda quien comparte: tiles (15), cursor (17) y resúmenes
// (18). Desde receiveLoop: nada pesado acá. El host lo acepta solo de la dirección de alguien que
// comparte y con su ID (la identidad sale de la dirección), lo reenvía sin esperar a los que miran
// (el cursor sin cola: es chico y urgente) y, si él también mira, lo entrega al visor. Un cliente
// lo acepta solo de su host. Todos los payloads empiezan con el ID de quien comparte (8 bytes).
func (a *App) handleScreenFromSharer(udp *transport.UDP, packet transport.Packet, addrStr string, fromHost bool) {
	if len(packet.Payload) < len(screen.PeerID{}) {
		return
	}
	if !a.hostMode.Load() {
		if fromHost {
			a.deliverScreen(packet.Kind, packet.Payload)
		}
		return
	}
	route, ok := a.screenSnapshot().routes[addrStr]
	if !ok || !bytes.Equal(packet.Payload[:len(route.peer)], route.peer[:]) {
		a.screenRejects.note(addrStr)
		return
	}
	if packet.Kind == transport.PacketScreenCursor {
		udp.RelayTo(packet, route.targets)
	} else {
		udp.TryRelayScreen(packet, route.targets)
	}
	if route.local {
		a.deliverScreen(packet.Kind, packet.Payload)
	}
}

// screenPacket es lo que le llega al visor: el kind (tile, cursor, resumen) y su payload.
type screenPacket struct {
	kind    byte
	payload []byte
}

// deliverScreen le pasa un paquete al visor si es de la pantalla que miro, sin esperar: con el
// canal lleno se descarta (un tile queda viejo hasta que el resumen lo delate y se pida de nuevo).
// payload es una copia propia (Receive), así que no hace falta copiarlo.
func (a *App) deliverScreen(kind byte, payload []byte) {
	watch := a.screenWatch.Load()
	if watch == nil || !bytes.Equal(payload[:len(watch.peer)], watch.peer[:]) {
		return
	}
	select {
	case watch.chunks <- screenPacket{kind, payload}:
	default:
		watch.dropped.Add(1)
	}
}

// sendScreenRepair (visor) pide reenviar tiles: a mi host, o directo a quien comparte si el host
// soy yo.
func (a *App) sendScreenRepair(watch *watchSession, payload []byte) {
	udp := a.transport.Load()
	if udp == nil {
		return
	}
	target := a.endpointAddr()
	if a.hostMode.Load() {
		target = a.screenSnapshot().sharerAddrs[watch.target]
	}
	if target != nil {
		_ = udp.Send(target, transport.Packet{Kind: transport.PacketScreenRepair, Sequence: a.sequence.Add(1), Payload: payload})
	}
}

// handleScreenRepair recibe un pedido de reparación. El host lo acepta solo de alguien que mira esa
// pantalla y lo pasa a quien comparte (o a su propia sesión si comparte él); un cliente que
// comparte lo acepta solo de su host.
func (a *App) handleScreenRepair(udp *transport.UDP, packet transport.Packet, addrStr string, fromHost bool) {
	repair, err := screen.DecodeRepair(packet.Payload)
	if err != nil {
		return
	}
	if !a.hostMode.Load() {
		if fromHost {
			a.deliverRepair(repair)
		}
		return
	}
	state := a.screenSnapshot()
	self, _ := screen.ParsePeerID(a.participantID)
	if repair.Sharer == self {
		if addrIn(state.selfTargets, addrStr) {
			a.deliverRepair(repair)
		}
		return
	}
	for _, route := range state.routes {
		if route.peer == repair.Sharer && addrIn(route.targets, addrStr) {
			if sharer := state.sharerAddrs[route.sharer]; sharer != nil {
				udp.RelayTo(packet, []*net.UDPAddr{sharer})
			}
			return
		}
	}
}

func addrIn(targets []*net.UDPAddr, addr string) bool {
	for _, target := range targets {
		if target.String() == addr {
			return true
		}
	}
	return false
}

// deliverRepair le pasa el pedido a mi pantalla compartida, sin esperar.
func (a *App) deliverRepair(repair screen.Repair) {
	session := a.screenShare.Load()
	self, _ := screen.ParsePeerID(a.participantID)
	if session == nil || repair.Sharer != self {
		return
	}
	select {
	case session.repairs <- repair:
	default:
	}
}

// rejectedTiles cuenta los tiles que el host no reenvía (de alguien que no comparte o con otro ID)
// y los loguea como mucho una vez cada receiveErrorLogEvery. Solo receiveLoop.
type rejectedTiles struct {
	count   int
	last    string
	lastLog time.Time
}

func (r *rejectedTiles) note(addr string) {
	r.count++
	r.last = addr
	if time.Since(r.lastLog) >= receiveErrorLogEvery {
		logging.Infof("Pantalla: %d tiles descartados (no comparte o ID equivocado; último de %s)", r.count, r.last)
		r.count, r.lastLog = 0, time.Now()
	}
}

// screenRelayLog (host, desde pingPeers) resume lo que salió por la cola de pantalla del socket
// (reenvíos y la pantalla propia) cada netStatsSummaryInterval, si salió algo.
type screenRelayLog struct {
	since          time.Time
	bytes, dropped uint64
}

func (r *screenRelayLog) check(udp *transport.UDP, now time.Time) {
	sent, dropped := udp.ScreenCounters()
	if r.since.IsZero() {
		r.since, r.bytes, r.dropped = now, sent, dropped
		return
	}
	elapsed := now.Sub(r.since)
	if elapsed < netStatsSummaryInterval {
		return
	}
	if sent > r.bytes || dropped > r.dropped {
		logging.Infof("Resumen cola de pantalla (%s): salió %s (%.0f kbps) descartados=%d",
			elapsed.Round(time.Second), formatMB(sent-r.bytes), float64(sent-r.bytes)*8/elapsed.Seconds()/1000, dropped-r.dropped)
	}
	r.since, r.bytes, r.dropped = now, sent, dropped
}

func formatMB(bytes uint64) string { return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20)) }
