package presence

import (
	"errors"
	"net/netip"
	"time"
)

// La llamada (F9): "te llamo" → "atiendo" o "rechazo" → recién con "atiendo" el que llama manda la
// invitación, solo a esa dirección y con el ID de esa llamada. Una llamada saliente y una entrante
// a la vez; lo demás contesta "ocupado". Todo directo, de socket directo a socket directo.

const (
	CallTimeout  = 30 * time.Second // sin atender: "no atiende" (y el cartel del llamado se cierra)
	InviteWait   = 5 * time.Second  // atendí y la invitación no llegó: aviso
	CallCooldown = 10 * time.Second // una llamada por IP cada tanto: un desconocido no llena la pantalla de carteles
)

// CallEventKind es lo que le pasa a una llamada (Config.OnCall).
type CallEventKind int

const (
	CallIncoming      CallEventKind = iota + 1 // me llaman: Name, Room
	CallIncomingEnded                          // el que llamaba cortó o se venció (sin atender)
	CallAccepted                               // (al que llama) atendió: mandar la invitación con SendInvite
	CallDeclined                               // (al que llama) Reason
	CallNoAnswer                               // (al que llama) CallTimeout sin respuesta
	CallInvite                                 // (al llamado) llegó la invitación: Invite
	CallInviteMissing                          // (al llamado) atendí y la invitación no llegó en InviteWait
)

// CallEvent es un cambio en una llamada. Llega desde las goroutines del servicio.
type CallEvent struct {
	Kind   CallEventKind
	Call   ID
	Peer   netip.AddrPort // el otro
	Name   string
	Room   string
	Reason Reason
	Invite string
}

type outCall struct {
	id       ID
	to       netip.AddrPort
	started  time.Time
	accepted time.Time // cero = todavía no atendió
}

type inCall struct {
	id       ID
	from     netip.AddrPort
	name     string
	started  time.Time
	accepted time.Time
}

// ErrCallBusy: ya hay una llamada saliente.
var (
	ErrCallBusy   = errors.New("ya hay una llamada en curso")
	ErrCallTarget = errors.New("solo se llama a alguien de la misma red o VPN")
)

// Call llama a to (la dirección de su aviso) con mi nombre y la sala.
func (s *Service) Call(to netip.AddrPort, name, room string) (ID, error) {
	if !LocalSource(to.Addr()) {
		return ID{}, ErrCallTarget // la invitación nunca sale para internet
	}
	s.callMu.Lock()
	if s.out != nil {
		s.callMu.Unlock()
		return ID{}, ErrCallBusy
	}
	call := &outCall{id: NewID(), to: to, started: time.Now()}
	s.out = call
	s.callMu.Unlock()
	if err := s.sendTo(Message{Kind: KindCall, From: s.self, Call: call.id, Name: name, Room: room}, to); err != nil {
		s.callMu.Lock()
		s.out = nil
		s.callMu.Unlock()
		return ID{}, err
	}
	return call.id, nil
}

// CancelCall corta la llamada saliente (el botón Cortar): el cartel del otro se cierra.
func (s *Service) CancelCall() {
	s.callMu.Lock()
	call := s.out
	s.out = nil
	s.callMu.Unlock()
	if call != nil && call.accepted.IsZero() {
		_ = s.sendTo(Message{Kind: KindHangup, From: s.self, Call: call.id}, call.to)
	}
}

// SendInvite manda la invitación a quien atendió la llamada call (CallAccepted). Solo a esa
// dirección, una vez.
func (s *Service) SendInvite(call ID, invite string) error {
	s.callMu.Lock()
	out := s.out
	if out == nil || out.id != call || out.accepted.IsZero() {
		s.callMu.Unlock()
		return ErrCallBusy
	}
	s.out = nil
	s.callMu.Unlock()
	return s.sendTo(Message{Kind: KindInvite, From: s.self, Call: call, Invite: invite}, out.to)
}

// Answer atiende (true) o rechaza la llamada entrante.
func (s *Service) Answer(accept bool) {
	s.callMu.Lock()
	call := s.in
	if call == nil {
		s.callMu.Unlock()
		return
	}
	if accept {
		call.accepted = time.Now()
	} else {
		s.in = nil
	}
	s.callMu.Unlock()
	if accept {
		_ = s.sendTo(Message{Kind: KindAccept, From: s.self, Call: call.id}, call.from)
		return
	}
	_ = s.sendTo(Message{Kind: KindDecline, From: s.self, Call: call.id, Reason: ReasonRejected}, call.from)
}

// handleCall procesa un mensaje de llamada que llegó desde from.
func (s *Service) handleCall(m Message, from netip.AddrPort, now time.Time) {
	var event *CallEvent
	var reply *Message
	s.callMu.Lock()
	switch m.Kind {
	case KindCall:
		switch {
		case s.in != nil && s.in.id == m.Call:
			// repetida: nada
		case now.Sub(s.callFrom[from.Addr()]) < CallCooldown:
			// tope por IP: ni cartel ni respuesta
		case s.in != nil || s.out != nil:
			s.callFrom[from.Addr()] = now
			reply = &Message{Kind: KindDecline, From: s.self, Call: m.Call, Reason: ReasonBusy}
		case s.cfg.Status().InRoom:
			s.callFrom[from.Addr()] = now
			reply = &Message{Kind: KindDecline, From: s.self, Call: m.Call, Reason: ReasonInRoom}
		default:
			s.callFrom[from.Addr()] = now
			s.in = &inCall{id: m.Call, from: from, name: m.Name, started: now}
			event = &CallEvent{Kind: CallIncoming, Call: m.Call, Peer: from, Name: m.Name, Room: m.Room}
		}
	case KindHangup:
		if s.in != nil && s.in.id == m.Call && s.in.from == from && s.in.accepted.IsZero() {
			event = &CallEvent{Kind: CallIncomingEnded, Call: m.Call, Peer: from, Name: s.in.name}
			s.in = nil
		}
	case KindInvite:
		if s.in != nil && s.in.id == m.Call && s.in.from == from && !s.in.accepted.IsZero() {
			event = &CallEvent{Kind: CallInvite, Call: m.Call, Peer: from, Name: s.in.name, Invite: m.Invite}
			s.in = nil
		}
	case KindAccept:
		if s.out != nil && s.out.id == m.Call && s.out.to == from && s.out.accepted.IsZero() {
			s.out.accepted = now
			event = &CallEvent{Kind: CallAccepted, Call: m.Call, Peer: from}
		}
	case KindDecline:
		if s.out != nil && s.out.id == m.Call && s.out.to == from {
			event = &CallEvent{Kind: CallDeclined, Call: m.Call, Peer: from, Reason: m.Reason}
			s.out = nil
		}
	}
	s.callMu.Unlock()
	if reply != nil {
		_ = s.sendTo(*reply, from)
	}
	s.callEvent(event)
}

// checkCalls vence las llamadas (cada 250 ms, desde announceLoop).
func (s *Service) checkCalls(now time.Time) {
	var events []*CallEvent
	var hangup *outCall
	s.callMu.Lock()
	if out := s.out; out != nil {
		switch {
		case out.accepted.IsZero() && now.Sub(out.started) > CallTimeout:
			events = append(events, &CallEvent{Kind: CallNoAnswer, Call: out.id, Peer: out.to})
			hangup, s.out = out, nil
		case !out.accepted.IsZero() && now.Sub(out.accepted) > InviteWait:
			s.out = nil // la UI nunca mandó la invitación (la sala no abrió)
		}
	}
	if in := s.in; in != nil {
		switch {
		case in.accepted.IsZero() && now.Sub(in.started) > CallTimeout+2*time.Second:
			events = append(events, &CallEvent{Kind: CallIncomingEnded, Call: in.id, Peer: in.from, Name: in.name})
			s.in = nil
		case !in.accepted.IsZero() && now.Sub(in.accepted) > InviteWait:
			events = append(events, &CallEvent{Kind: CallInviteMissing, Call: in.id, Peer: in.from, Name: in.name})
			s.in = nil
		}
	}
	for ip, at := range s.callFrom {
		if now.Sub(at) > CallCooldown {
			delete(s.callFrom, ip)
		}
	}
	s.callMu.Unlock()
	if hangup != nil {
		_ = s.sendTo(Message{Kind: KindHangup, From: s.self, Call: hangup.id}, hangup.to)
	}
	for _, event := range events {
		s.callEvent(event)
	}
}

func (s *Service) callEvent(event *CallEvent) {
	if event != nil && s.cfg.OnCall != nil {
		s.cfg.OnCall(*event)
	}
}

// sendTo manda m a una sola dirección desde el socket directo.
func (s *Service) sendTo(m Message, to netip.AddrPort) error {
	packet, err := Encode(m)
	if err != nil {
		return err
	}
	_, err = s.direct.WriteToUDPAddrPort(packet, to)
	return err
}
