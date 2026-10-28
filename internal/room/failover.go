package room

import (
	"sort"
	"sync"
	"time"
)

// Failover decide qué hace un participante cuando pierde a su host, y qué hace un host cuando
// aparece otro host: reconectar con el mismo si fue un corte breve, elegir host nuevo (el mismo
// para todos) y pasarse a él abriendo paso entre los routers, o bajarse para que nunca queden dos
// hosts. No hace I/O: recibe eventos con la hora y deja acciones que la UI ejecuta (Drain). Es
// seguro para varias goroutines (receiveLoop y el loop de la conexión).
//
//   - Host callado más de HostSilence: se le reintenta (ping + RoomHello). Hasta Grace un corte no
//     migra: el host puede volver.
//   - Host que avisa que se va (RoomLeave), o callado más de Grace: migración. Los candidatos salen
//     del último estado del host (State.Candidates), así todos calculan la misma lista.
//   - Cada uno le pide entrar al candidato de turno (RoomHello cada Retry: abre su router hacia
//     él). El candidato de turno se hace host si el host avisó que se iba y es el primero, o cuando
//     alguien le pide entrar: si nadie se lo pide, quizá el que perdió la conexión fue él. Mientras
//     espera les manda pings a los demás, que abren su router hacia ellos.
//   - El host nuevo les manda su estado a los que faltan (también al host viejo si solo se calló,
//     por si vuelve) cada Retry y después cada Ping, hasta Announce.
//   - Un host que recibe el estado de otro host con más autoridad (epoch mayor, o igual con ID
//     menor) se baja y se pasa a esa sala. Un cliente se pasa a la sala de epoch mayor.
type Failover struct {
	mu      sync.Mutex
	timings FailoverTimings
	self    string
	phase   failoverPhase
	stopped bool

	hostID   string
	hostAddr string
	lastHost time.Time // último paquete del host
	lastPing time.Time
	epoch    uint64
	state    State // último estado de la sala: el del host (cliente) o el propio (host)

	candidates     []Participant
	candidate      int
	candidateSince time.Time
	leftOnPurpose  bool            // el host avisó que se iba
	hellos         map[string]bool // quiénes me pidieron entrar durante la migración
	lastHello      time.Time
	lastPunch      time.Time

	pending       map[string]string // host nuevo: ID → dirección de los que todavía no entraron
	announceStart time.Time
	lastAnnounce  time.Time

	actions []Action
	wake    chan struct{}
}

// FailoverTimings son los tiempos de la migración de host.
type FailoverTimings struct {
	HostSilence   time.Duration // host callado: avisar y reintentar con el mismo host
	Grace         time.Duration // host callado: migrar (un corte más corto no migra)
	CandidateWait time.Duration // espera por cada candidato antes de pasar al siguiente
	Retry         time.Duration // ritmo de RoomHello, estado y pings durante la migración
	Ping          time.Duration // ping al host
	Announce      time.Duration // cuánto sigue avisando el host nuevo a los que no entraron
}

// DefaultFailoverTimings son los tiempos de la app.
func DefaultFailoverTimings() FailoverTimings {
	return FailoverTimings{
		HostSilence:   6 * time.Second,
		Grace:         20 * time.Second,
		CandidateWait: 10 * time.Second,
		Retry:         500 * time.Millisecond,
		Ping:          2 * time.Second,
		Announce:      2 * time.Minute,
	}
}

// ActionKind es lo que la UI tiene que hacer.
type ActionKind int

const (
	ActPing       ActionKind = iota + 1 // ping a Addr: al host, o para abrir el router hacia otro
	ActHello                            // RoomHello a Addr: reconectar, o pedirle entrar al candidato
	ActSendState                        // mandarle a Addr el estado actual de la sala (host nuevo)
	ActBecomeHost                       // pasar a host con State (epoch ya incrementado)
	ActSwitchHost                       // pasarse como cliente al host de Addr, con su State
	ActStepDown                         // (host) bajarse y pasarse como cliente al host de Addr
	ActGiveUp                           // dejar la sala: no se pudo seguir (Text)
	ActStatus                           // avisarle al usuario (Text)
)

// Action es una acción para la UI.
type Action struct {
	Kind  ActionKind
	Addr  string
	State State
	Text  string
}

// Source dice si un paquete que recibió un cliente es de su host.
type Source int

const (
	FromOther Source = iota
	FromHost
)

type failoverPhase int

const (
	phaseIdle         failoverPhase = iota // sin sala, o se rindió
	phaseClient                            // conectado a un host
	phaseReconnecting                      // host callado: se le reintenta
	phaseMigrating                         // buscando host nuevo
	phaseHost                              // soy el host
)

// NewFailover crea la máquina de un participante (self es su ID).
func NewFailover(self string, timings FailoverTimings) *Failover {
	return &Failover{self: self, timings: timings, wake: make(chan struct{}, 1)}
}

// Wake avisa que hay acciones nuevas para Drain.
func (f *Failover) Wake() <-chan struct{} { return f.wake }

// Drain devuelve las acciones pendientes, en orden, y las saca de la cola.
func (f *Failover) Drain() []Action {
	f.mu.Lock()
	defer f.mu.Unlock()
	actions := f.actions
	f.actions = nil
	return actions
}

// Stop termina (la conexión se cerró): no hay más acciones.
func (f *Failover) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
	f.phase = phaseIdle
	f.actions = nil
}

// JoinedAsClient arranca como cliente del host en hostAddr.
func (f *Failover) JoinedAsClient(hostAddr string, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped {
		return
	}
	f.phase = phaseClient
	f.hostID = ""
	f.hostAddr = hostAddr
	f.lastHost = now
	f.lastPing = now
}

// Hosting dice que soy el host de state (al abrir la sala y cada vez que entra alguien): sirve para
// reconocer a los de la sala y comparar autoridad con otro host.
func (f *Failover) Hosting(state State, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped {
		return
	}
	if f.phase != phaseHost {
		f.phase = phaseHost
		f.hostAddr = ""
	}
	f.hostID = f.self
	f.epoch = state.Epoch
	f.state = cloneState(state)
}

// Seen registra un paquete recibido de from (como cliente) y dice si es del host. Un paquete del
// host lo da por vivo: si se estaba reconectando, o migrando porque se calló (no porque avisó que
// se iba), se vuelve a él.
func (f *Failover) Seen(from string, now time.Time) Source {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped || f.hostAddr == "" || from != f.hostAddr {
		return FromOther
	}
	switch f.phase {
	case phaseClient:
		f.lastHost = now
	case phaseReconnecting, phaseMigrating:
		if f.phase == phaseMigrating && f.leftOnPurpose {
			return FromOther // paquete atrasado de un host que ya se fue
		}
		f.lastHost = now
		f.phase = phaseClient
		f.candidates, f.hellos = nil, nil
		f.status("Volvió la conexión con el host.")
		f.emit(Action{Kind: ActHello, Addr: f.hostAddr}) // pudo habernos dado de baja
	default:
		return FromOther
	}
	return FromHost
}

// HostState guarda el estado que mandó el host (el padrón para elegir host nuevo). Si nombra a
// otro host (el host se bajó y avisa quién quedó), se va hacia ese.
func (f *Failover) HostState(state State, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped || (f.phase != phaseClient && f.phase != phaseReconnecting) {
		return
	}
	if f.hostID != "" && state.HostID != f.hostID && state.HostID != f.self {
		if next, ok := participantByID(state, state.HostID); ok {
			f.state = cloneState(state)
			f.epoch = state.Epoch
			f.startMigration(now, []Participant{next}, true, "El host le pasó la sala a "+displayName(next)+".")
			return
		}
	}
	f.hostID = state.HostID
	f.epoch = state.Epoch
	f.state = cloneState(state)
}

// HostLeft: el host avisó que se va (RoomLeave desde su dirección). Se busca host nuevo ya, sin
// esperar el período de gracia.
func (f *Failover) HostLeft(now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped || (f.phase != phaseClient && f.phase != phaseReconnecting) {
		return
	}
	f.startMigration(now, f.state.Candidates(), true, "El host se fue. Buscando host nuevo…")
}

// Refused: el host no me dejó entrar (sala llena). Solo cuenta antes del primer estado de la sala:
// ya adentro, un rechazo no tiene sentido y se ignora.
func (f *Failover) Refused(text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped || f.phase != phaseClient || f.hostID != "" {
		return
	}
	f.phase = phaseIdle
	f.emit(Action{Kind: ActGiveUp, Text: text})
}

// StateFrom: llegó el estado de sala de from, que no es mi host. Como cliente me paso si es el
// candidato al que le pedí entrar, o una sala con más autoridad (epoch mayor). Como host me bajo si
// el otro host tiene más autoridad: nunca quedan dos hosts. Solo cuenta si el host que nombra es
// alguien de la sala.
func (f *Failover) StateFrom(state State, from string, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped || state.HostID == f.self {
		return
	}
	if f.phase == phaseClient && f.hostID == "" {
		// Primer estado de la sala: es la respuesta del host a mi RoomHello, desde otra dirección
		// que la que marqué (misma red que el host y un router sin hairpin NAT). Esa es la del host.
		f.adopt(state, from, now)
		f.emit(Action{Kind: ActSwitchHost, Addr: from, State: cloneState(state)})
		return
	}
	if !f.known(state.HostID) {
		return
	}
	host, _ := participantByID(state, state.HostID)
	switch f.phase {
	case phaseHost:
		if state.Epoch > f.epoch || (state.Epoch == f.epoch && state.HostID < f.self) {
			f.adopt(state, from, now)
			f.emit(Action{Kind: ActStepDown, Addr: from, State: cloneState(state)})
			f.status(displayName(host) + " ya es el host de la sala: te pasás a su sala.")
		}
	case phaseClient, phaseReconnecting, phaseMigrating:
		asked := f.phase == phaseMigrating && f.candidate < len(f.candidates) && f.candidates[f.candidate].ID == state.HostID
		if asked || state.Epoch > f.epoch {
			f.adopt(state, from, now)
			f.emit(Action{Kind: ActSwitchHost, Addr: from, State: cloneState(state)})
			f.status(displayName(host) + " es el nuevo host.")
		}
	}
}

// HelloFrom: llegó un RoomHello de id. Durante la migración es alguien que también perdió al host
// y me pide entrar: si es mi turno de candidato, soy el host (si no, lo anoto para cuando lo sea).
// Como host, ese ya no necesita que le avise.
func (f *Failover) HelloFrom(id string, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped {
		return
	}
	switch f.phase {
	case phaseMigrating:
		if id == f.self || !f.known(id) {
			return
		}
		f.hellos[id] = true
		if f.candidates[f.candidate].ID == f.self {
			f.becomeHost(now)
		}
	case phaseHost:
		delete(f.pending, id)
	}
}

// Tick avanza los plazos (la UI lo llama seguido, cada ~250 ms).
func (f *Failover) Tick(now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped {
		return
	}
	switch f.phase {
	case phaseClient:
		f.pingHost(now)
		if now.Sub(f.lastHost) > f.timings.HostSilence {
			f.phase = phaseReconnecting
			f.status("Se cortó la conexión con el host. Reconectando…")
			f.lastHello = now
			f.emit(Action{Kind: ActHello, Addr: f.hostAddr})
		}
	case phaseReconnecting:
		f.pingHost(now)
		if now.Sub(f.lastHost) > f.timings.Grace {
			f.startMigration(now, f.state.Candidates(), false, "El host no responde. Buscando host nuevo…")
			return
		}
		if now.Sub(f.lastHello) >= f.timings.Ping {
			f.lastHello = now
			f.emit(Action{Kind: ActHello, Addr: f.hostAddr}) // el host pudo habernos dado de baja
		}
	case phaseMigrating:
		if !f.leftOnPurpose {
			f.pingHost(now) // si el que se cortó era yo y el host sigue, Seen me devuelve a él
		}
		if now.Sub(f.candidateSince) >= f.timings.CandidateWait {
			f.candidate++
			f.turn(now)
			return
		}
		if next := f.candidates[f.candidate]; next.ID != f.self {
			if now.Sub(f.lastHello) >= f.timings.Retry {
				f.lastHello = now
				f.emit(Action{Kind: ActHello, Addr: next.Address})
			}
		} else if now.Sub(f.lastPunch) >= f.timings.Retry {
			f.lastPunch = now
			f.punch()
		}
	case phaseHost:
		if len(f.pending) == 0 || now.Sub(f.announceStart) >= f.timings.Announce {
			return
		}
		interval := f.timings.Ping
		if now.Sub(f.announceStart) < f.timings.CandidateWait {
			interval = f.timings.Retry
		}
		if now.Sub(f.lastAnnounce) >= interval {
			f.lastAnnounce = now
			f.announce()
		}
	}
}

func (f *Failover) startMigration(now time.Time, candidates []Participant, onPurpose bool, text string) {
	f.phase = phaseMigrating
	f.candidates = candidates
	f.candidate = 0
	f.leftOnPurpose = onPurpose
	f.hellos = map[string]bool{}
	f.status(text)
	f.turn(now)
}

// turn arranca el turno del candidato actual.
func (f *Failover) turn(now time.Time) {
	f.candidateSince = now
	if f.candidate >= len(f.candidates) {
		f.phase = phaseIdle
		f.emit(Action{Kind: ActGiveUp, Text: "No se pudo seguir la sala sin el host."})
		return
	}
	if next := f.candidates[f.candidate]; next.ID != f.self {
		f.lastHello = now
		f.emit(Action{Kind: ActHello, Addr: next.Address})
		return
	}
	// Mi turno: soy el host si el host avisó que se iba y soy el primero, o si alguien ya me pidió
	// entrar. Si no, espero: si nadie me lo pide, quizá el que perdió la conexión fui yo.
	if (f.leftOnPurpose && f.candidate == 0) || len(f.hellos) > 0 {
		f.becomeHost(now)
		return
	}
	f.lastPunch = now
	f.punch()
}

func (f *Failover) becomeHost(now time.Time) {
	state := cloneState(f.state)
	oldHost := f.hostID
	state.HostID = f.self
	state.Epoch = f.epoch + 1
	f.pending = map[string]string{}
	for i := range state.Participants {
		participant := &state.Participants[i]
		participant.LastSeenUTC = now.UTC() // cada uno tiene todo el plazo del host nuevo para entrar
		switch {
		case participant.ID == f.self:
			participant.Connected = true
		case participant.ID == oldHost:
			participant.Connected = false
			if !f.leftOnPurpose && participant.Address != "" {
				f.pending[participant.ID] = participant.Address // quizá solo se cortó: que vuelva acá
			}
		case participant.Connected && participant.Address != "":
			f.pending[participant.ID] = participant.Address
		}
	}
	f.phase = phaseHost
	f.hostID = f.self
	f.hostAddr = ""
	f.epoch = state.Epoch
	f.state = state
	f.candidates, f.hellos = nil, nil
	f.announceStart = now
	f.lastAnnounce = now
	f.emit(Action{Kind: ActBecomeHost, State: cloneState(state)})
	f.status("Sos el nuevo host de la sala.")
	f.announce()
}

// announce le manda el estado a los que todavía no entraron, en orden de ID.
func (f *Failover) announce() {
	ids := make([]string, 0, len(f.pending))
	for id := range f.pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		f.emit(Action{Kind: ActSendState, Addr: f.pending[id]})
	}
}

// punch les manda un ping a los demás de la sala (sin mí ni el host viejo): abre mi router hacia
// ellos para que me lleguen sus RoomHello.
func (f *Failover) punch() {
	for _, participant := range f.state.Participants {
		if participant.Connected && participant.ID != f.self && participant.ID != f.hostID && participant.Address != "" {
			f.emit(Action{Kind: ActPing, Addr: participant.Address})
		}
	}
}

func (f *Failover) pingHost(now time.Time) {
	if f.hostAddr != "" && now.Sub(f.lastPing) >= f.timings.Ping {
		f.lastPing = now
		f.emit(Action{Kind: ActPing, Addr: f.hostAddr})
	}
}

// adopt pasa a ser cliente del host de state en from.
func (f *Failover) adopt(state State, from string, now time.Time) {
	f.phase = phaseClient
	f.hostID = state.HostID
	f.hostAddr = from
	f.lastHost = now
	f.lastPing = now
	f.epoch = state.Epoch
	f.state = cloneState(state)
	f.candidates, f.hellos, f.pending = nil, nil, nil
}

// known dice si id es alguien de la sala (del último estado o de la lista de candidatos).
func (f *Failover) known(id string) bool {
	if _, ok := participantByID(f.state, id); ok {
		return true
	}
	for _, candidate := range f.candidates {
		if candidate.ID == id {
			return true
		}
	}
	return false
}

func (f *Failover) status(text string) { f.emit(Action{Kind: ActStatus, Text: text}) }

func (f *Failover) emit(action Action) {
	f.actions = append(f.actions, action)
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

func cloneState(state State) State {
	state.Participants = append([]Participant(nil), state.Participants...)
	return state
}

func participantByID(state State, id string) (Participant, bool) {
	for _, participant := range state.Participants {
		if participant.ID == id {
			return participant, true
		}
	}
	return Participant{}, false
}

func displayName(participant Participant) string {
	if participant.Username != "" {
		return participant.Username
	}
	return "Otro participante"
}
