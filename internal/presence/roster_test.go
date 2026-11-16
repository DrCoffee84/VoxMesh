package presence

import (
	"net/netip"
	"testing"
	"time"
)

func addr(s string) netip.AddrPort { return netip.MustParseAddrPort(s) }

func TestRosterAddsAndExpires(t *testing.T) {
	self := ID{0xff}
	r := NewRoster(self, 10*time.Second)
	start := time.Unix(1000, 0)
	juan := ID{1}
	if !r.Seen(juan, "Juan", false, "", addr("192.168.1.5:50000"), start) {
		t.Fatal("alguien nuevo cambia la lista")
	}
	if r.Seen(juan, "Juan", false, "", addr("192.168.1.5:50000"), start.Add(3*time.Second)) {
		t.Error("el mismo aviso otra vez no cambia nada")
	}
	if !r.Seen(juan, "Juan", true, "", addr("192.168.1.5:50000"), start.Add(6*time.Second)) {
		t.Error("pasar a una sala cambia la lista")
	}
	if r.Expire(start.Add(16 * time.Second)) {
		t.Error("10 s desde el último aviso todavía no vence")
	}
	if !r.Expire(start.Add(16*time.Second + time.Millisecond)) {
		t.Error("más de 10 s sin avisos: sale")
	}
	if len(r.List()) != 0 {
		t.Errorf("lista: %+v", r.List())
	}
	if r.Seen(self, "Yo", false, "", addr("192.168.1.2:50000"), start) || len(r.List()) != 0 {
		t.Error("el aviso propio se ignora")
	}
	if r.Seen(ID{}, "Nadie", false, "", addr("192.168.1.2:50000"), start) {
		t.Error("un ID vacío se ignora")
	}
}

func TestRosterSameNameDifferentIDs(t *testing.T) {
	r := NewRoster(ID{0xff}, 10*time.Second)
	now := time.Unix(1000, 0)
	r.Seen(ID{2}, "Juan", false, "", addr("192.168.1.6:50000"), now)
	r.Seen(ID{1}, "juan", true, "", addr("192.168.1.5:50000"), now)
	r.Seen(ID{3}, "Ana", false, "", addr("26.1.2.3:50001"), now)
	list := r.List()
	if len(list) != 3 {
		t.Fatalf("lista: %+v", list)
	}
	// Por nombre sin mayúsculas y, con el mismo nombre, por ID.
	if list[0].Name != "Ana" || list[1].ID != (ID{1}) || list[2].ID != (ID{2}) {
		t.Errorf("orden: %+v", list)
	}
	if !list[1].InRoom || list[1].Addr != addr("192.168.1.5:50000") {
		t.Errorf("datos de juan: %+v", list[1])
	}
}

func TestRosterKeepsAddressOfLiveID(t *testing.T) {
	r := NewRoster(ID{0xff}, 10*time.Second)
	now := time.Unix(1000, 0)
	juan := ID{1}
	r.Seen(juan, "Juan", false, "", addr("192.168.1.5:50000"), now)
	// Otro de la red copia su ID del aviso: no se queda con sus llamadas.
	if r.Seen(juan, "Juan", true, "", addr("192.168.1.66:40000"), now.Add(time.Second)) {
		t.Error("un ID vivo no cambia de dirección")
	}
	if got := r.List()[0]; got.Addr != addr("192.168.1.5:50000") || got.InRoom {
		t.Errorf("quedó %+v", got)
	}
	if r.Bye(juan, addr("192.168.1.66:40000")) {
		t.Error("un \"me voy\" de otra dirección no saca a nadie")
	}
	// Vencido, el ID es de quien avise.
	if !r.Seen(juan, "Juan", false, "", addr("192.168.1.66:40000"), now.Add(11*time.Second)) {
		t.Error("vencido, entra con la dirección nueva")
	}
	if !r.Bye(juan, addr("192.168.1.66:40000")) || len(r.List()) != 0 {
		t.Error("\"me voy\" desde su dirección lo saca")
	}
}

func TestRosterCaps(t *testing.T) {
	r := NewRoster(ID{0xff}, 10*time.Second)
	now := time.Unix(1000, 0)
	for i := range MaxPeersPerIP + 2 {
		r.Seen(ID{1, byte(i)}, "Mismo", false, "", netip.AddrPortFrom(netip.MustParseAddr("192.168.1.5"), uint16(50000+i)), now)
	}
	if got := len(r.List()); got != MaxPeersPerIP {
		t.Errorf("desde una IP entraron %d, quería %d", got, MaxPeersPerIP)
	}
	for i := range MaxPeers * 2 {
		ip := netip.AddrFrom4([4]byte{10, 0, byte(i / 200), byte(i%200 + 1)})
		r.Seen(ID{2, byte(i)}, "Otro", false, "", netip.AddrPortFrom(ip, 50000), now)
	}
	if got := len(r.List()); got != MaxPeers {
		t.Errorf("en total entraron %d, quería %d", got, MaxPeers)
	}
	// Con la lista llena, los de siempre siguen actualizándose.
	if !r.Seen(ID{1, 0}, "Mismo", true, "Sala", addr("192.168.1.5:50000"), now.Add(time.Second)) {
		t.Error("el que ya estaba se actualiza aunque la lista esté llena")
	}
}
