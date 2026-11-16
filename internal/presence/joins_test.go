package presence

import (
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"voxmesh/internal/identity"
)

// joinNode es un servicio en 127.0.0.1 con su identidad, que junta los eventos de pedidos y puede
// entrar y salir de una sala durante el test.
type joinNode struct {
	*Service
	id     *identity.Identity
	inRoom atomic.Bool
	events chan JoinEvent
}

func startJoinNode(t *testing.T, name string, inRoom bool) *joinNode {
	t.Helper()
	node := &joinNode{id: identity.New(), events: make(chan JoinEvent, 16)}
	node.inRoom.Store(inRoom)
	s, err := Start(Config{
		ListenIP: loopback,
		Targets:  func() []netip.AddrPort { return nil },
		Status:   func() Status { return Status{Name: name, InRoom: node.inRoom.Load(), Room: "Mi sala"} },
		OnJoin:   func(e JoinEvent) { node.events <- e },
		Identity: node.id,
		Interval: time.Hour,
		TTL:      time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	node.Service = s
	t.Cleanup(s.Stop)
	return node
}

func nextJoin(t *testing.T, n *joinNode, kind JoinEventKind) JoinEvent {
	t.Helper()
	select {
	case e := <-n.events:
		if e.Kind != kind {
			t.Fatalf("evento %v, quería %v (%+v)", e.Kind, kind, e)
		}
		return e
	case <-time.After(2 * time.Second):
		t.Fatalf("no llegó el evento %v", kind)
	}
	return JoinEvent{}
}

func noJoin(t *testing.T, n *joinNode) {
	t.Helper()
	select {
	case e := <-n.events:
		t.Fatalf("no tenía que pasar nada: %+v", e)
	case <-time.After(300 * time.Millisecond):
	}
}

// Con la firma buena el de la sala se entera con la llave del que pide; dejarlo entrar le manda la
// invitación, una sola vez. Es lo que hace la app con alguien de confianza, sin que nadie toque nada.
func TestJoinAdmitSendsInvite(t *testing.T) {
	budo := startJoinNode(t, "Budo", true)
	juan := startJoinNode(t, "Juan", false)
	id, err := juan.Join(directAddr(budo.Service), "Juan")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := juan.Join(directAddr(budo.Service), "Juan"); err != ErrCallBusy {
		t.Errorf("un segundo pedido a la vez: %v", err)
	}
	req := nextJoin(t, budo, JoinRequest)
	if req.Join != id || req.Name != "Juan" || req.Key != juan.id.Public() || req.Peer != directAddr(juan.Service) {
		t.Fatalf("pedido: %+v", req)
	}
	if err := budo.AdmitJoin(req.Join, "VM1-abc"); err != nil {
		t.Fatal(err)
	}
	if got := nextJoin(t, juan, JoinAdmitted); got.Invite != "VM1-abc" || got.Join != id {
		t.Errorf("invitación: %+v", got)
	}
	if err := budo.AdmitJoin(req.Join, "VM1-abc"); err == nil {
		t.Error("la invitación sale una sola vez")
	}
	noJoin(t, juan)
}

// Sin confianza queda pendiente hasta que se contesta: Rechazar le avisa al que pide; Cancelar del
// que pide borra el pendiente del otro.
func TestJoinPendingRejectAndCancel(t *testing.T) {
	budo := startJoinNode(t, "Budo", true)
	juan := startJoinNode(t, "Juan", false)
	juan.Join(directAddr(budo.Service), "Juan")
	req := nextJoin(t, budo, JoinRequest)
	noJoin(t, juan) // pendiente: el que pide sigue esperando
	budo.RejectJoin(req.Join, ReasonRejected)
	if got := nextJoin(t, juan, JoinDeclined); got.Reason != ReasonRejected {
		t.Errorf("rechazo: %+v", got)
	}

	pedro := startJoinNode(t, "Pedro", false)
	ana := startJoinNode(t, "Ana", true)
	pedro.Join(directAddr(ana.Service), "Pedro")
	nextJoin(t, ana, JoinRequest)
	pedro.CancelJoin()
	if got := nextJoin(t, ana, JoinCanceled); got.Name != "Pedro" {
		t.Errorf("cancelado: %+v", got)
	}
	if err := ana.AdmitJoin(got(t, ana), "VM1-abc"); err == nil {
		t.Error("un pedido cancelado no se puede aceptar")
	}
}

func got(t *testing.T, n *joinNode) ID {
	t.Helper()
	n.callMu.Lock()
	defer n.callMu.Unlock()
	for id := range n.joinsIn {
		return id
	}
	return ID{}
}

// Nadie se hace pasar por otro: con la llave de Juan pero firmado con otra privada, el de la sala
// no se entera. Y una respuesta grabada no sirve para otro pedido (el número es otro).
func TestJoinChallenge(t *testing.T) {
	budo := startJoinNode(t, "Budo", true)
	juan := startJoinNode(t, "Juan", false)
	impostor := startJoinNode(t, "Juan", false)

	// El impostor manda la llave de Juan y contesta el desafío con la suya.
	fake := NewID()
	impostor.callMu.Lock()
	impostor.joinOut = &outJoin{id: fake, to: directAddr(budo.Service), started: time.Now(), sent: time.Now(), resends: joinResends}
	impostor.callMu.Unlock()
	impostor.sendTo(Message{Kind: KindJoin, From: impostor.self, Call: fake, Name: "Juan", Key: juan.id.Public()}, directAddr(budo.Service))
	noJoin(t, budo)
	budo.callMu.Lock()
	left := len(budo.joinsIn)
	budo.callMu.Unlock()
	if left != 0 {
		t.Error("el pedido con la firma mala sigue pendiente")
	}

	// La respuesta buena de un pedido, repetida en otro: no pasa.
	from := netip.MustParseAddrPort("127.0.0.3:9") // otra IP: los nodos de arriba usan el tope de 127.0.0.1
	requester := ID{4, 4}
	now := time.Now()
	first := ID{1}
	budo.handleJoin(Message{Kind: KindJoin, From: requester, Call: first, Name: "Juan", Key: juan.id.Public()}, from, now)
	nonce := budo.joinsIn[first].nonce
	var sig [64]byte
	copy(sig[:], juan.id.Sign(joinContext, joinProofMessage(first, nonce, requester, budo.self)))
	budo.handleJoin(Message{Kind: KindProof, From: requester, Call: first, Sig: sig}, from, now)
	if e := nextJoin(t, budo, JoinRequest); e.Key != juan.id.Public() {
		t.Fatalf("la firma buena pasa: %+v", e)
	}
	budo.RejectJoin(first, ReasonRejected)

	second := ID{2}
	later := now.Add(JoinCooldown + time.Second)
	budo.handleJoin(Message{Kind: KindJoin, From: requester, Call: second, Name: "Juan", Key: juan.id.Public()}, from, later)
	if budo.joinsIn[second] == nil {
		t.Fatal("el segundo pedido no quedó")
	}
	budo.handleJoin(Message{Kind: KindProof, From: requester, Call: second, Sig: sig}, from, later)
	// Firmado con el número viejo para el pedido nuevo: tampoco.
	copy(sig[:], juan.id.Sign(joinContext, joinProofMessage(second, nonce, requester, budo.self)))
	budo.handleJoin(Message{Kind: KindProof, From: requester, Call: second, Sig: sig}, from, later)
	noJoin(t, budo)
}

// Topes: uno a la vez por IP, uno nuevo por IP cada JoinCooldown, MaxJoins pendientes; sin sala,
// "no está en una sala".
func TestJoinLimits(t *testing.T) {
	budo := startJoinNode(t, "Budo", true)
	key := identity.New().Public()
	now := time.Now()
	join := func(call ID, ip string, at time.Time) {
		budo.handleJoin(Message{Kind: KindJoin, From: ID{call[0]}, Call: call, Name: "X", Key: key}, netip.MustParseAddrPort(ip), at)
	}
	pending := func() int {
		budo.callMu.Lock()
		defer budo.callMu.Unlock()
		return len(budo.joinsIn)
	}
	join(ID{1}, "127.0.0.2:1", now)
	join(ID{2}, "127.0.0.2:2", now.Add(JoinCooldown+time.Second)) // la misma IP con uno pendiente
	if pending() != 1 {
		t.Errorf("uno a la vez por IP: %d", pending())
	}
	budo.callMu.Lock()
	delete(budo.joinsIn, ID{1})
	budo.callMu.Unlock()
	join(ID{3}, "127.0.0.2:3", now.Add(time.Second)) // antes del cooldown
	if pending() != 0 {
		t.Errorf("tope por IP: %d", pending())
	}
	for i := range MaxJoins + 1 {
		join(ID{byte(10 + i)}, netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 1, byte(i + 1)}), 1).String(), now)
	}
	if pending() != MaxJoins {
		t.Errorf("tope total: %d", pending())
	}

	// Sin sala: el que pide se entera.
	ana := startJoinNode(t, "Ana", false)
	juan := startJoinNode(t, "Juan", false)
	juan.Join(directAddr(ana.Service), "Juan")
	if got := nextJoin(t, juan, JoinDeclined); got.Reason != ReasonNoRoom {
		t.Errorf("sin sala: %+v", got)
	}
	noJoin(t, ana)

	if _, err := juan.Join(netip.MustParseAddrPort("181.10.20.30:47832"), "Juan"); err != ErrCallTarget {
		t.Errorf("a una IP de internet nunca: %v", err)
	}
}

// Vence a JoinTimeout sin respuesta (los dos lados se enteran) y salir de la sala rechaza los
// pendientes.
func TestJoinExpiresAndLeavingRoom(t *testing.T) {
	budo := startJoinNode(t, "Budo", true)
	juan := startJoinNode(t, "Juan", false)
	juan.Join(directAddr(budo.Service), "Juan")
	nextJoin(t, budo, JoinRequest)
	budo.checkJoins(time.Now().Add(JoinTimeout + time.Second))
	if got := nextJoin(t, budo, JoinExpired); got.Reason != ReasonNoAnswer || got.Name != "Juan" {
		t.Errorf("vencido: %+v", got)
	}
	if got := nextJoin(t, juan, JoinDeclined); got.Reason != ReasonNoAnswer {
		t.Errorf("al que pide: %+v", got)
	}

	// Si la respuesta de vencido se pierde, el que pide se da por vencido solo.
	pedro := startJoinNode(t, "Pedro", false)
	pedro.Join(directAddr(budo.Service), "Pedro")
	nextJoin(t, budo, JoinRequest)
	pedro.checkJoins(time.Now().Add(JoinTimeout + joinGrace + time.Second))
	if got := nextJoin(t, pedro, JoinDeclined); got.Reason != ReasonNoAnswer {
		t.Errorf("vencido del lado del que pide: %+v", got)
	}
	nextJoin(t, budo, JoinCanceled) // y le avisa al otro

	budo.checkJoins(time.Now().Add(JoinCooldown + time.Second)) // todos los nodos son 127.0.0.1: que pase el tope por IP
	ana := startJoinNode(t, "Ana", false)
	ana.Join(directAddr(budo.Service), "Ana")
	nextJoin(t, budo, JoinRequest)
	budo.inRoom.Store(false)
	if got := nextJoin(t, budo, JoinExpired); got.Reason != ReasonNoRoom {
		t.Errorf("salí de la sala: %+v", got)
	}
	if got := nextJoin(t, ana, JoinDeclined); got.Reason != ReasonNoRoom {
		t.Errorf("al que pide: %+v", got)
	}
}
