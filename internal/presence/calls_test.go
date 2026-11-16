package presence

import (
	"net"
	"net/netip"
	"testing"
	"time"
)

// startCallNode es un servicio en 127.0.0.1 que junta los eventos de llamada.
func startCallNode(t *testing.T, name string, inRoom bool) (*Service, chan CallEvent) {
	t.Helper()
	events := make(chan CallEvent, 16)
	s, err := Start(Config{
		ListenIP: loopback,
		Targets:  func() []netip.AddrPort { return nil },
		Status:   func() Status { return Status{Name: name, InRoom: inRoom} },
		OnCall:   func(e CallEvent) { events <- e },
		Interval: time.Hour,
		TTL:      time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	return s, events
}

func directAddr(s *Service) netip.AddrPort {
	return netip.AddrPortFrom(loopback, uint16(s.direct.LocalAddr().(*net.UDPAddr).Port))
}

func nextCall(t *testing.T, events chan CallEvent, kind CallEventKind) CallEvent {
	t.Helper()
	select {
	case e := <-events:
		if e.Kind != kind {
			t.Fatalf("evento %v, quería %v (%+v)", e.Kind, kind, e)
		}
		return e
	case <-time.After(2 * time.Second):
		t.Fatalf("no llegó el evento %v", kind)
	}
	return CallEvent{}
}

func noCall(t *testing.T, events chan CallEvent) {
	t.Helper()
	select {
	case e := <-events:
		t.Fatalf("no tenía que pasar nada: %+v", e)
	case <-time.After(300 * time.Millisecond):
	}
}

// Llamar → atender → la invitación llega solo al que atendió, una vez.
func TestCallAcceptSendsInvite(t *testing.T) {
	a, aEvents := startCallNode(t, "Coffee", false)
	b, bEvents := startCallNode(t, "Juan", false)
	id, err := a.Call(directAddr(b), "Coffee", "Los pibes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Call(directAddr(b), "Coffee", "Otra"); err != ErrCallBusy {
		t.Errorf("una segunda llamada saliente: %v", err)
	}
	in := nextCall(t, bEvents, CallIncoming)
	if in.Name != "Coffee" || in.Room != "Los pibes" || in.Peer != directAddr(a) {
		t.Errorf("llamada entrante: %+v", in)
	}
	if err := a.SendInvite(id, "VM1-abc"); err == nil {
		t.Error("sin atender no sale la invitación")
	}
	b.Answer(true)
	if got := nextCall(t, aEvents, CallAccepted); got.Call != id {
		t.Errorf("atendió otra llamada: %+v", got)
	}
	if err := a.SendInvite(id, "VM1-abc"); err != nil {
		t.Fatal(err)
	}
	if got := nextCall(t, bEvents, CallInvite); got.Invite != "VM1-abc" {
		t.Errorf("invitación: %q", got.Invite)
	}
	if err := a.SendInvite(id, "VM1-abc"); err == nil {
		t.Error("la invitación sale una sola vez")
	}
}

// Rechazar, cortar, en una sala, ocupado y el tope por IP.
func TestCallDeclinesAndLimits(t *testing.T) {
	a, aEvents := startCallNode(t, "Coffee", false)
	b, bEvents := startCallNode(t, "Juan", false)
	a.Call(directAddr(b), "Coffee", "Los pibes")
	nextCall(t, bEvents, CallIncoming)
	// Otro de la red quiere meter una llamada mientras: ocupado, el cartel sigue siendo el de A.
	b.handleCall(Message{Kind: KindCall, From: ID{9}, Call: ID{9}, Name: "Extraño"}, netip.MustParseAddrPort("127.0.0.2:9"), time.Now())
	b.callMu.Lock()
	if b.in == nil || b.in.name != "Coffee" {
		t.Error("una segunda llamada no reemplaza a la primera")
	}
	b.callMu.Unlock()
	// Un "corto" de otra dirección no cierra el cartel; el de A sí.
	b.handleCall(Message{Kind: KindHangup, From: ID{9}, Call: b.in.id}, netip.MustParseAddrPort("127.0.0.2:9"), time.Now())
	noCall(t, bEvents)
	a.CancelCall()
	nextCall(t, bEvents, CallIncomingEnded)

	// Desde la misma IP, antes de CallCooldown: ni cartel ni respuesta.
	a.Call(directAddr(b), "Coffee", "Los pibes")
	noCall(t, bEvents)
	a.CancelCall()

	c, cEvents := startCallNode(t, "Pedro", false)
	a.Call(directAddr(c), "Coffee", "Los pibes")
	nextCall(t, cEvents, CallIncoming)
	c.Answer(false)
	if got := nextCall(t, aEvents, CallDeclined); got.Reason != ReasonRejected {
		t.Errorf("rechazo: %+v", got)
	}

	d, dEvents := startCallNode(t, "Ana", true)
	a.Call(directAddr(d), "Coffee", "Los pibes")
	if got := nextCall(t, aEvents, CallDeclined); got.Reason != ReasonInRoom {
		t.Errorf("en una sala: %+v", got)
	}
	noCall(t, dEvents)

	if _, err := a.Call(netip.MustParseAddrPort("181.10.20.30:47832"), "Coffee", "x"); err != ErrCallTarget {
		t.Errorf("a una IP de internet nunca: %v", err)
	}
}
