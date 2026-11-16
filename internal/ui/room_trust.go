//go:build cgo

package ui

import (
	"net"
	"sync"
	"time"

	"voxmesh/internal/config"
	"voxmesh/internal/history"
	"voxmesh/internal/identity"
	"voxmesh/internal/logging"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

// Identidad y lista de confianza en la sala (AGENDA F10, AGENT.md §3.N y §K).
//
// Cada instalación tiene su par de llaves (internal/identity, en config.AppDir). El Hello de la
// sala lleva la llave pública firmada sobre la sesión (room.SignHello) y el host solo lo acepta con
// la firma válida: el estado de la sala lleva la llave de cada participante.
//
// La lista de confianza ("Dejar entrar siempre") es de la sala: viaja en el estado (State.Trusted)
// y cada miembro la guarda en salas/<sala>/confianza.json. trustBook es la de la sala en la que
// estoy: la del archivo junta con la que manda el host. Un cliente le manda al host lo que tenía en
// su archivo y el host no (una vez por conexión), así la lista de la sala tiene todo y el que quede
// de host la sigue teniendo.

// loadIdentity lee (o crea la primera vez) la identidad de esta instalación. Si no se puede
// guardar, se usa una nueva solo por esta sesión: para los demás sos alguien nuevo.
func (a *App) loadIdentity() {
	dir, err := config.AppDir()
	if err != nil {
		logging.Errorf("identidad: carpeta de datos: %s", logging.FormatError(err))
		return
	}
	id, created, err := identity.LoadOrCreate(dir)
	switch {
	case err != nil:
		logging.Errorf("identidad: %s (se usa una nueva solo por esta sesión)", logging.FormatError(err))
	case created:
		logging.Infof("Identidad nueva de esta instalación: %s", id.Public().Short())
	default:
		logging.Infof("Identidad de esta instalación: %s", id.Public().Short())
	}
	a.identity.Store(id)
}

// ident es la identidad de esta instalación. Sin loadIdentity (los tests, o si falló la carpeta)
// una nueva en memoria.
func (a *App) ident() *identity.Identity {
	if id := a.identity.Load(); id != nil {
		return id
	}
	a.identity.CompareAndSwap(nil, identity.New())
	return a.identity.Load()
}

// helloEnvelope es el RoomHello de participant firmado con la llave de esta instalación para la
// sala de udp (le pone participant.Key).
func (a *App) helloEnvelope(udp *transport.UDP, participant *room.Participant) room.Envelope {
	id := a.ident()
	participant.Key = id.Public().String()
	return room.Envelope{
		Kind:        room.HelloMessage,
		Participant: participant,
		Signature:   room.SignHello(id, [32]byte(udp.Key()), participant.ID),
	}
}

// helloSigned dice si el Hello de envelope trae la firma de su llave para esta sala. Uno sin firma
// válida se descarta (a lo sumo una línea de log cada 10 s): nadie se anota con la llave de otro.
func (a *App) helloSigned(udp *transport.UDP, envelope room.Envelope, from string, now time.Time) bool {
	if room.VerifyHello(*envelope.Participant, [32]byte(udp.Key()), envelope.Signature) {
		return true
	}
	if now.Sub(a.helloRejectLogged) >= receiveErrorLogEvery {
		a.helloRejectLogged = now
		logging.Infof("RoomHello sin firma válida de %s: descartado", from)
	}
	return false
}

// trustBook es la lista de confianza de la sala en la que estoy, con su archivo. Segura para
// varias goroutines; el disco se escribe en otra goroutine (saveMu: la última escritura es la de
// la lista de ese momento).
type trustBook struct {
	mu     sync.Mutex
	store  *history.Store // la sala (nil = ninguna)
	list   []room.Trusted
	sent   bool // ya le mandé al host lo que le faltaba (una vez por conexión)
	saveMu sync.Mutex
}

// open carga la lista de la sala de store (confianza.json; roto o sin archivo = vacía).
func (b *trustBook) open(store *history.Store) {
	var list []room.Trusted
	if data, err := store.ReadTrust(); err != nil {
		logging.Errorf("lista de confianza: leer: %s", logging.FormatError(err))
	} else if data != nil {
		list = room.DecodeTrustFile(data)
	}
	b.mu.Lock()
	b.store, b.list, b.sent = store, list, false
	b.mu.Unlock()
}

// close la suelta al salir de la sala.
func (b *trustBook) close() {
	b.mu.Lock()
	b.store, b.list, b.sent = nil, nil, false
	b.mu.Unlock()
}

// snapshot es la lista de ahora (una copia).
func (b *trustBook) snapshot() []room.Trusted {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]room.Trusted(nil), b.list...)
}

// has dice si key está en la lista.
func (b *trustBook) has(key identity.Key) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return room.TrustedHas(b.list, key)
}

// merge junta other con la lista y, si cambió, la guarda. Devuelve la lista junta. Sin sala
// abierta no guarda nada (devuelve other limpia).
func (b *trustBook) merge(other []room.Trusted) []room.Trusted {
	b.mu.Lock()
	merged := room.MergeTrusted(b.list, other)
	changed := !room.SameTrusted(merged, b.list)
	store := b.store
	if store != nil {
		b.list = merged
	}
	b.mu.Unlock()
	if changed && store != nil {
		go b.save(store)
	}
	return append([]room.Trusted(nil), merged...)
}

// markSent dice si todavía no se le mandó al host lo que le faltaba, y lo marca.
func (b *trustBook) markSent() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	first := !b.sent
	b.sent = true
	return first
}

// save escribe la lista de ese momento en el archivo de store (si sigue siendo la sala abierta).
func (b *trustBook) save(store *history.Store) {
	b.saveMu.Lock()
	defer b.saveMu.Unlock()
	b.mu.Lock()
	if b.store != store {
		b.mu.Unlock()
		return
	}
	list := append([]room.Trusted(nil), b.list...)
	b.mu.Unlock()
	data, err := room.EncodeTrustFile(list)
	if err == nil {
		err = store.WriteTrust(data)
	}
	if err != nil {
		logging.Errorf("lista de confianza: guardar: %s", logging.FormatError(err))
	}
}

// syncTrustFromHost (cliente, receiveLoop) junta la lista del estado del host con la propia y le
// manda al host, una vez por conexión, lo que tenía guardado y él no.
func (a *App) syncTrustFromHost(state room.State, udp *transport.UDP) {
	merged := a.trust.merge(state.Trusted)
	extra := room.MissingTrusted(merged, room.CleanTrusted(state.Trusted))
	if len(extra) == 0 || !a.trust.markSent() {
		return
	}
	a.sendTrust(udp, extra)
}

// sendTrust (cliente) le pide al host sumar entries a la lista de la sala.
func (a *App) sendTrust(udp *transport.UDP, entries []room.Trusted) {
	hostAddr := a.endpointAddr()
	if hostAddr == nil {
		return
	}
	payload, err := room.Encode(room.Envelope{Kind: room.TrustMessage, Trusted: entries})
	if err != nil {
		return
	}
	_ = udp.Send(hostAddr, transport.Packet{Kind: transport.PacketRoomTrust, Sequence: a.sequence.Add(1), Payload: payload})
}

// handleTrustPacket (host, receiveLoop): un participante conectado suma a la lista de la sala.
// Solo desde la dirección registrada en su Hello (§K regla 1); la lista sigue acotada
// (room.MaxTrusted) y se difunde a todos.
func (a *App) handleTrustPacket(udp *transport.UDP, addr *net.UDPAddr, payload []byte) {
	from := addr.String()
	known := false
	for _, participant := range a.roomState.Participants {
		if participant.Connected && participant.Address == from {
			known = true
			break
		}
	}
	if !known {
		return
	}
	envelope, err := room.Decode(payload)
	if err != nil || envelope.Kind != room.TrustMessage || len(envelope.Trusted) > 2*room.MaxTrusted {
		return
	}
	merged := a.trust.merge(room.MergeTrusted(a.roomState.Trusted, envelope.Trusted))
	if room.SameTrusted(merged, room.CleanTrusted(a.roomState.Trusted)) {
		return
	}
	a.roomState.Trusted = merged
	logging.Infof("Lista de confianza de la sala: %d (sumó %s)", len(merged), from)
	a.broadcastRoomState(udp, nil)
}

// addTrusted suma entry a la lista de la sala ("Dejar entrar siempre"): la guarda y, si soy el
// host, la difunde en el estado; si soy cliente, se la pide al host.
func (a *App) addTrusted(entry room.Trusted) {
	merged := a.trust.merge([]room.Trusted{entry})
	udp := a.transport.Load()
	if udp == nil {
		return
	}
	if a.hostMode.Load() {
		a.roomState.Trusted = room.MergeTrusted(a.roomState.Trusted, merged)
		a.broadcastRoomState(udp, nil)
		return
	}
	a.sendTrust(udp, []room.Trusted{entry})
}
