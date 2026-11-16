package presence

import (
	"crypto/rand"
	"errors"
	"net/netip"
	"time"

	"voxmesh/internal/identity"
)

// Pedir entrar a la sala de alguien de la red (AGENDA F10). El que pide manda "quiero entrar" con
// su llave pública; el de la sala le contesta con un número al azar (el desafío); el que pide lo
// devuelve firmado con su privada. Recién con la firma buena el de la sala se entera
// (JoinRequest) y decide: si la llave está en la lista de confianza le manda la invitación
// (AdmitJoin) sin que nadie toque nada; si no, queda pendiente hasta que alguien acepte o rechace,
// o vence a JoinTimeout. Así nadie se hace pasar por otro (sin la privada no hay firma) ni repite
// un pedido grabado (el número es nuevo en cada pedido y la firma incluye los dos IDs de sesión y
// el del pedido).
//
// Todo directo, de socket directo a socket directo, como la llamada (calls.go), y con topes
// parecidos: un pedido saliente a la vez; del otro lado, uno a la vez por IP, uno nuevo por IP cada
// JoinCooldown y a lo sumo MaxJoins pendientes en total.

const (
	JoinTimeout  = 2 * time.Minute  // pendiente sin contestar: vence (el otro puede estar en una partida)
	JoinCooldown = 10 * time.Second // un pedido nuevo por IP cada tanto
	MaxJoins     = 4                // pedidos pendientes a la vez, de distintas IPs
	// challengeWait: sin la respuesta al desafío, el pedido se descarta sin avisar a nadie.
	challengeWait = 10 * time.Second
	// joinResend: el que pide repite lo último que mandó (el pedido o la respuesta) cada tanto,
	// joinResends veces, por si UDP perdió un paquete. El de la sala ignora los repetidos.
	joinResend  = time.Second
	joinResends = 5
	// joinGrace: el que pide espera un poco más que JoinTimeout (el aviso de vencido llega del otro).
	joinGrace = 5 * time.Second
	// joinContext es el uso de la firma (identity.Sign): no sirve como firma del Hello de la sala.
	joinContext = "join"
)

// JoinEventKind es lo que le pasa a un pedido para entrar (Config.OnJoin).
type JoinEventKind int

const (
	JoinRequest  JoinEventKind = iota + 1 // (al de la sala) alguien que probó su llave pide entrar: Name, Key; contestar con AdmitJoin o RejectJoin
	JoinCanceled                          // (al de la sala) el que pedía canceló
	JoinExpired                           // (al de la sala) venció sin respuesta (Reason ReasonNoAnswer) o salí de la sala (ReasonNoRoom)
	JoinAdmitted                          // (al que pide) llegó la invitación: Invite
	JoinDeclined                          // (al que pide) no entra: Reason (rechazado, ocupado, sin sala, nadie contestó)
)

// JoinEvent es un cambio en un pedido. Llega desde las goroutines del servicio.
type JoinEvent struct {
	Kind   JoinEventKind
	Join   ID
	Peer   netip.AddrPort // el otro
	Name   string         // el que pide (JoinRequest, JoinCanceled, JoinExpired)
	Key    identity.Key   // su llave (JoinRequest)
	Reason Reason
	Invite string
}

type outJoin struct {
	id      ID
	to      netip.AddrPort
	started time.Time
	last    Message // lo último que mandé (se repite hasta joinResends veces)
	sent    time.Time
	resends int
	proved  bool // ya respondí el desafío
}

type inJoin struct {
	id       ID
	from     netip.AddrPort
	sender   ID // el ID de sesión del que pide (va en la firma)
	name     string
	key      identity.Key
	nonce    [32]byte
	started  time.Time // al llegar; con la firma buena, desde ahí cuenta JoinTimeout
	verified bool
}

var (
	ErrNoIdentity = errors.New("sin identidad para firmar")
	ErrJoinGone   = errors.New("el pedido ya no está")
)

// joinProofMessage es lo que firma el que pide: el ID del pedido, el número del desafío y los IDs
// de sesión de los dos. Una respuesta grabada no sirve para otro pedido ni para otra sesión.
func joinProofMessage(join ID, nonce [32]byte, requester, owner ID) []byte {
	out := make([]byte, 0, len(join)+len(nonce)+len(requester)+len(owner))
	out = append(out, join[:]...)
	out = append(out, nonce[:]...)
	out = append(out, requester[:]...)
	return append(out, owner[:]...)
}

// Join le pide a to (la dirección del aviso de alguien que está en una sala) entrar a su sala, con
// mi nombre y mi llave.
func (s *Service) Join(to netip.AddrPort, name string) (ID, error) {
	if !LocalSource(to.Addr()) {
		return ID{}, ErrCallTarget // la invitación nunca viene de internet
	}
	if s.cfg.Identity == nil {
		return ID{}, ErrNoIdentity
	}
	now := time.Now()
	s.callMu.Lock()
	if s.joinOut != nil {
		s.callMu.Unlock()
		return ID{}, ErrCallBusy
	}
	join := &outJoin{id: NewID(), to: to, started: now, sent: now}
	join.last = Message{Kind: KindJoin, From: s.self, Call: join.id, Name: name, Key: s.cfg.Identity.Public()}
	s.joinOut = join
	s.callMu.Unlock()
	if err := s.sendTo(join.last, to); err != nil {
		s.callMu.Lock()
		if s.joinOut == join {
			s.joinOut = nil
		}
		s.callMu.Unlock()
		return ID{}, err
	}
	return join.id, nil
}

// CancelJoin cancela el pedido saliente (el botón Cancelar): el pendiente del otro se borra.
func (s *Service) CancelJoin() {
	s.callMu.Lock()
	join := s.joinOut
	s.joinOut = nil
	s.callMu.Unlock()
	if join != nil {
		_ = s.sendTo(Message{Kind: KindHangup, From: s.self, Call: join.id}, join.to)
	}
}

// AdmitJoin deja entrar al pedido join (después de JoinRequest): le manda la invitación, solo a
// esa dirección, una vez.
func (s *Service) AdmitJoin(join ID, invite string) error {
	in := s.takeJoin(join)
	if in == nil {
		return ErrJoinGone
	}
	return s.sendTo(Message{Kind: KindInvite, From: s.self, Call: join, Invite: invite}, in.from)
}

// RejectJoin no deja entrar al pedido join (Rechazar, o no hay sala para invitar: ReasonNoRoom).
func (s *Service) RejectJoin(join ID, reason Reason) {
	if in := s.takeJoin(join); in != nil {
		_ = s.sendTo(Message{Kind: KindDecline, From: s.self, Call: join, Reason: reason}, in.from)
	}
}

// takeJoin saca el pedido verificado join de los pendientes.
func (s *Service) takeJoin(join ID) *inJoin {
	s.callMu.Lock()
	defer s.callMu.Unlock()
	in := s.joinsIn[join]
	if in == nil || !in.verified {
		return nil
	}
	delete(s.joinsIn, join)
	return in
}

// dropJoins rechaza todos los pedidos que me hicieron (salí de la sala o se apaga el servicio).
func (s *Service) dropJoins(reason Reason) {
	s.callMu.Lock()
	dropped := make([]*inJoin, 0, len(s.joinsIn))
	for id, in := range s.joinsIn {
		dropped = append(dropped, in)
		delete(s.joinsIn, id)
	}
	s.callMu.Unlock()
	for _, in := range dropped {
		_ = s.sendTo(Message{Kind: KindDecline, From: s.self, Call: in.id, Reason: reason}, in.from)
		if in.verified {
			s.joinEvent(&JoinEvent{Kind: JoinExpired, Join: in.id, Peer: in.from, Name: in.name, Reason: reason})
		}
	}
}

// handleJoin procesa un mensaje del pedido para entrar. Devuelve false si no es de un pedido (un
// Decline, Hangup o Invite de una llamada: los ve handleCall).
func (s *Service) handleJoin(m Message, from netip.AddrPort, now time.Time) bool {
	var event *JoinEvent
	var reply *Message
	var bad bool
	s.callMu.Lock()
	switch m.Kind {
	case KindJoin:
		ip := from.Addr()
		if in := s.joinsIn[m.Call]; in != nil {
			// Repetido (el que pide no recibió el desafío): el mismo desafío otra vez.
			if in.from == from && in.sender == m.From && !in.verified {
				reply = &Message{Kind: KindChallenge, From: s.self, Call: in.id, Nonce: in.nonce}
			}
			break
		}
		switch {
		case s.joinFromIP(ip):
			// uno a la vez por IP: ni respuesta
		case now.Sub(s.joinFrom[ip]) < JoinCooldown:
			// tope por IP: ni respuesta
		case !s.cfg.Status().InRoom:
			s.joinFrom[ip] = now
			reply = &Message{Kind: KindDecline, From: s.self, Call: m.Call, Reason: ReasonNoRoom}
		case len(s.joinsIn) >= MaxJoins:
			s.joinFrom[ip] = now
			reply = &Message{Kind: KindDecline, From: s.self, Call: m.Call, Reason: ReasonBusy}
		default:
			s.joinFrom[ip] = now
			in := &inJoin{id: m.Call, from: from, sender: m.From, name: m.Name, key: m.Key, started: now}
			_, _ = rand.Read(in.nonce[:]) // nunca falla (Go 1.24+)
			s.joinsIn[m.Call] = in
			reply = &Message{Kind: KindChallenge, From: s.self, Call: in.id, Nonce: in.nonce}
		}
	case KindProof:
		in := s.joinsIn[m.Call]
		if in == nil || in.from != from || in.sender != m.From || in.verified {
			break // de otro, o repetido: nada
		}
		if !identity.Verify(in.key, joinContext, joinProofMessage(in.id, in.nonce, in.sender, s.self), m.Sig[:]) {
			delete(s.joinsIn, m.Call) // una firma mala termina el pedido, sin avisar
			bad = true
			break
		}
		in.verified, in.started = true, now
		event = &JoinEvent{Kind: JoinRequest, Join: in.id, Peer: from, Name: in.name, Key: in.key}
	case KindChallenge:
		out := s.joinOut
		if out == nil || out.id != m.Call || out.to != from || out.proved {
			break
		}
		sig := s.cfg.Identity.Sign(joinContext, joinProofMessage(out.id, m.Nonce, s.self, m.From))
		proof := Message{Kind: KindProof, From: s.self, Call: out.id}
		copy(proof.Sig[:], sig)
		out.proved, out.last, out.sent, out.resends = true, proof, now, 0
		reply = &proof
	case KindInvite, KindDecline:
		out := s.joinOut
		if out == nil || out.id != m.Call {
			s.callMu.Unlock()
			return false
		}
		if out.to != from {
			break
		}
		s.joinOut = nil
		if m.Kind == KindInvite {
			event = &JoinEvent{Kind: JoinAdmitted, Join: out.id, Peer: from, Invite: m.Invite}
		} else {
			event = &JoinEvent{Kind: JoinDeclined, Join: out.id, Peer: from, Reason: m.Reason}
		}
	case KindHangup:
		in := s.joinsIn[m.Call]
		if in == nil {
			s.callMu.Unlock()
			return false
		}
		if in.from != from {
			break
		}
		delete(s.joinsIn, m.Call)
		if in.verified {
			event = &JoinEvent{Kind: JoinCanceled, Join: in.id, Peer: from, Name: in.name}
		}
	default:
		s.callMu.Unlock()
		return false
	}
	s.callMu.Unlock()
	if bad {
		s.reject(from, errBadProof, now)
	}
	if reply != nil {
		_ = s.sendTo(*reply, from)
	}
	s.joinEvent(event)
	return true
}

var errBadProof = errors.New("la firma del pedido para entrar no corresponde a su llave")

// joinFromIP dice si ya hay un pedido pendiente desde ip (callMu tomado).
func (s *Service) joinFromIP(ip netip.Addr) bool {
	for _, in := range s.joinsIn {
		if in.from.Addr() == ip {
			return true
		}
	}
	return false
}

// checkJoins repite lo que hace falta y vence los pedidos (cada 250 ms, desde announceLoop).
func (s *Service) checkJoins(now time.Time) {
	var events []*JoinEvent
	type send struct {
		m  Message
		to netip.AddrPort
	}
	var sends []send
	s.callMu.Lock()
	if out := s.joinOut; out != nil {
		switch {
		case now.Sub(out.started) > JoinTimeout+joinGrace:
			events = append(events, &JoinEvent{Kind: JoinDeclined, Join: out.id, Peer: out.to, Reason: ReasonNoAnswer})
			sends = append(sends, send{Message{Kind: KindHangup, From: s.self, Call: out.id}, out.to})
			s.joinOut = nil
		case out.resends < joinResends && now.Sub(out.sent) >= joinResend:
			out.resends++
			out.sent = now
			sends = append(sends, send{out.last, out.to})
		}
	}
	for id, in := range s.joinsIn {
		switch {
		case !in.verified && now.Sub(in.started) > challengeWait:
			delete(s.joinsIn, id)
		case in.verified && now.Sub(in.started) > JoinTimeout:
			delete(s.joinsIn, id)
			sends = append(sends, send{Message{Kind: KindDecline, From: s.self, Call: id, Reason: ReasonNoAnswer}, in.from})
			events = append(events, &JoinEvent{Kind: JoinExpired, Join: id, Peer: in.from, Name: in.name, Reason: ReasonNoAnswer})
		}
	}
	for ip, at := range s.joinFrom {
		if now.Sub(at) > JoinCooldown {
			delete(s.joinFrom, ip)
		}
	}
	s.callMu.Unlock()
	for _, out := range sends {
		_ = s.sendTo(out.m, out.to)
	}
	for _, event := range events {
		s.joinEvent(event)
	}
}

func (s *Service) joinEvent(event *JoinEvent) {
	if event != nil && s.cfg.OnJoin != nil {
		s.cfg.OnJoin(*event)
	}
}
