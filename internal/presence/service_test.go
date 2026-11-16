package presence

import (
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"voxmesh/internal/netinfo"
)

var loopback = netip.MustParseAddr("127.0.0.1")

// testNode es un servicio en 127.0.0.1 con su estado y adónde avisa, cambiables durante el test.
type testNode struct {
	*Service
	mu      sync.Mutex
	status  Status
	targets []netip.AddrPort
	changes atomic.Int32
}

func startNode(t *testing.T, name string, interval, ttl time.Duration, targets ...netip.AddrPort) *testNode {
	t.Helper()
	node := &testNode{status: Status{Name: name}, targets: targets}
	service, err := Start(Config{
		ListenIP: loopback,
		Targets: func() []netip.AddrPort {
			node.mu.Lock()
			defer node.mu.Unlock()
			return append([]netip.AddrPort(nil), node.targets...)
		},
		Status: func() Status {
			node.mu.Lock()
			defer node.mu.Unlock()
			return node.status
		},
		OnChange: func() { node.changes.Add(1) },
		Interval: interval,
		TTL:      ttl,
	})
	if err != nil {
		t.Fatal(err)
	}
	node.Service = service
	t.Cleanup(service.Stop)
	return node
}

func (n *testNode) addr() netip.AddrPort {
	return netip.AddrPortFrom(loopback, uint16(n.ListenPort()))
}

func (n *testNode) setTargets(targets ...netip.AddrPort) {
	n.mu.Lock()
	n.targets = targets
	n.mu.Unlock()
}

func (n *testNode) setInRoom(inRoom bool) {
	n.mu.Lock()
	n.status.InRoom, n.status.Room = inRoom, "Mi sala"
	n.mu.Unlock()
}

// waitFor espera hasta 2 s a que cond se cumpla.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("no pasó: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func peerNamed(peers []Peer, name string) (Peer, bool) {
	for _, peer := range peers {
		if peer.Name == name {
			return peer, true
		}
	}
	return Peer{}, false
}

func TestServicesSeeEachOther(t *testing.T) {
	a := startNode(t, "Coffee", 50*time.Millisecond, time.Hour)
	b := startNode(t, "Juan", 50*time.Millisecond, time.Hour)
	// Cada uno avisa también a su propio puerto: su aviso le vuelve, como con el broadcast.
	a.setTargets(a.addr(), b.addr())
	b.setTargets(b.addr(), a.addr())

	waitFor(t, "A ve a B y B ve a A", func() bool {
		_, aSees := peerNamed(a.Peers(), "Juan")
		_, bSees := peerNamed(b.Peers(), "Coffee")
		return aSees && bSees
	})
	juan, _ := peerNamed(a.Peers(), "Juan")
	if juan.ID != b.ID() || juan.InRoom {
		t.Errorf("A ve a Juan así: %+v", juan)
	}
	// La dirección es el socket directo de B (adonde van las llamadas), no el puerto de los avisos.
	if juan.Addr.Addr() != loopback || int(juan.Addr.Port()) == b.ListenPort() {
		t.Errorf("dirección de Juan: %s (avisos en %d)", juan.Addr, b.ListenPort())
	}
	if len(a.Peers()) != 1 || len(b.Peers()) != 1 {
		t.Errorf("cada uno se ignora a sí mismo: A %+v, B %+v", a.Peers(), b.Peers())
	}
	if a.changes.Load() == 0 {
		t.Error("A no avisó el cambio de la lista")
	}
}

func TestServiceAnnouncesStatusChangeAtOnce(t *testing.T) {
	// Sin aviso periódico (cada una hora): después del primero, solo el cambio de estado lo hace
	// llegar.
	a := startNode(t, "Coffee", time.Hour, time.Hour)
	b := startNode(t, "Juan", time.Hour, time.Hour, a.addr())
	waitFor(t, "el primer aviso de B", func() bool {
		_, ok := peerNamed(a.Peers(), "Juan")
		return ok
	})
	if mustPeer(t, a, "Juan").InRoom {
		t.Fatal("B arrancó libre")
	}
	b.setInRoom(true)
	waitFor(t, "A ve que B entró a una sala", func() bool { return mustPeer(t, a, "Juan").InRoom })
	if room := mustPeer(t, a, "Juan").Room; room != "Mi sala" {
		t.Errorf("el aviso dice la sala (F10): %q", room)
	}
	b.setInRoom(false)
	waitFor(t, "A ve que B salió", func() bool { return !mustPeer(t, a, "Juan").InRoom })
	if room := mustPeer(t, a, "Juan").Room; room != "" {
		t.Errorf("libre, sin sala: %q", room)
	}
}

func mustPeer(t *testing.T, n *testNode, name string) Peer {
	t.Helper()
	peer, ok := peerNamed(n.Peers(), name)
	if !ok {
		t.Fatalf("no está %s: %+v", name, n.Peers())
	}
	return peer
}

func TestServiceByeRemovesAtOnce(t *testing.T) {
	a := startNode(t, "Coffee", 50*time.Millisecond, time.Hour)
	b := startNode(t, "Juan", 50*time.Millisecond, time.Hour)
	b.setTargets(a.addr())
	waitFor(t, "A ve a B", func() bool { return len(a.Peers()) == 1 })
	b.Stop()
	b.Stop() // dos veces no rompe nada
	waitFor(t, "el \"me voy\" de B lo saca (sin esperar el vencimiento de una hora)", func() bool { return len(a.Peers()) == 0 })
}

// sendRaw manda un paquete a node desde un socket aparte (alguien que no es un Service).
func sendRaw(t *testing.T, to netip.AddrPort, packet []byte) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(loopback, 0)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.WriteToUDPAddrPort(packet, to); err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestServiceExpiresSilentPeers(t *testing.T) {
	a := startNode(t, "Coffee", 50*time.Millisecond, 300*time.Millisecond)
	packet, _ := Encode(Message{Kind: KindPresence, From: ID{7}, Name: "Fantasma"})
	sendRaw(t, a.addr(), packet)
	waitFor(t, "aparece", func() bool { return len(a.Peers()) == 1 })
	before := a.changes.Load()
	waitFor(t, "se va solo al no avisar más", func() bool { return len(a.Peers()) == 0 })
	if a.changes.Load() == before {
		t.Error("vencer también avisa el cambio")
	}
}

func TestServiceDropsJunkAndHijacks(t *testing.T) {
	a := startNode(t, "Coffee", 50*time.Millisecond, time.Hour)
	b := startNode(t, "Juan", 50*time.Millisecond, time.Hour)
	b.setTargets(a.addr())
	waitFor(t, "A ve a B", func() bool { return len(a.Peers()) == 1 })
	original := mustPeer(t, a, "Juan")

	sendRaw(t, a.addr(), []byte("hola"))
	sendRaw(t, a.addr(), make([]byte, MaxPacket+100))
	// Otro de la red copia el ID de Juan del aviso, para quedarse con sus llamadas.
	fake, _ := Encode(Message{Kind: KindPresence, From: b.ID(), Name: "Juan", InRoom: true})
	sendRaw(t, a.addr(), fake)
	bye, _ := Encode(Message{Kind: KindBye, From: b.ID()})
	sendRaw(t, a.addr(), bye)

	waitFor(t, "cuenta la basura", func() bool {
		a.rejectMu.Lock()
		defer a.rejectMu.Unlock()
		return a.rejected >= 2
	})
	time.Sleep(100 * time.Millisecond) // que lleguen también el falso y el "me voy"
	if got := mustPeer(t, a, "Juan"); got != original {
		t.Errorf("Juan quedó %+v, era %+v", got, original)
	}
}

func TestLocalSource(t *testing.T) {
	for _, ip := range []string{"192.168.1.5", "10.0.0.2", "172.20.1.1", "26.1.2.3", "100.101.2.3", "127.0.0.1"} {
		if !LocalSource(netip.MustParseAddr(ip)) {
			t.Errorf("%s es de la red o VPN", ip)
		}
	}
	for _, ip := range []string{"181.10.20.30", "8.8.8.8"} {
		if LocalSource(netip.MustParseAddr(ip)) {
			t.Errorf("%s es de internet", ip)
		}
	}
	if LocalSource(netip.Addr{}) {
		t.Error("una IP vacía no se acepta")
	}
}

func TestServiceDropsInternetSources(t *testing.T) {
	a := startNode(t, "Coffee", time.Hour, time.Hour)
	packet, _ := Encode(Message{Kind: KindPresence, From: ID{7}, Name: "De afuera"})
	a.handle(packet, addr("181.10.20.30:50000"), time.Now())
	if len(a.Peers()) != 0 {
		t.Errorf("entró desde internet: %+v", a.Peers())
	}
	a.handle(packet, addr("192.168.1.5:50000"), time.Now())
	if len(a.Peers()) != 1 {
		t.Error("desde la LAN tiene que entrar")
	}
}

func TestSharedPortForTwoInstances(t *testing.T) {
	first, err := listenUDP(netip.AddrPortFrom(loopback, 0), true)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	port := first.LocalAddr().(*net.UDPAddr).Port
	second, err := listenUDP(netip.AddrPortFrom(loopback, uint16(port)), true)
	if err != nil {
		t.Fatalf("una segunda instancia tiene que poder escuchar el mismo puerto: %v", err)
	}
	second.Close()
}

func TestLANTargets(t *testing.T) {
	mask := func(ones int) net.IPMask { return net.CIDRMask(ones, 32) }
	interfaces := []netinfo.InterfaceIP{
		{Name: "Ethernet", IP: net.ParseIP("192.168.1.39"), Mask: mask(24)},
		{Name: "Wi-Fi", IP: net.ParseIP("192.168.1.40"), Mask: mask(24)}, // la misma red: un solo aviso
		{Name: "Radmin VPN", IP: net.ParseIP("26.77.45.242"), Mask: mask(8)},
		{Name: "Tailscale", IP: net.ParseIP("100.101.2.3"), Mask: mask(32)}, // sin broadcast
		{Name: "Módem", IP: net.ParseIP("181.10.20.30"), Mask: mask(24)},    // IP de internet: nunca
		{Name: "Sin máscara", IP: net.ParseIP("10.0.0.5")},
	}
	got := LANTargets(interfaces, Port)
	want := []netip.AddrPort{addr("192.168.1.255:47832"), addr("26.255.255.255:47832")}
	if len(got) != len(want) {
		t.Fatalf("destinos: %v, quería %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("destino %d: %s, quería %s", i, got[i], want[i])
		}
	}
	if Port == 47830 || Port == 47831 {
		t.Error("el puerto de los avisos no puede ser el de la sala ni el del celular")
	}
}
