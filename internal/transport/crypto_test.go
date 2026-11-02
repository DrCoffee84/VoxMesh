package transport

import (
	"bytes"
	"net"
	"testing"
	"time"
)

var testKey = Key{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32}

func newTestCipher(t testing.TB, key Key) *roomCipher {
	t.Helper()
	c, err := newRoomCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// audioPacket es un paquete de voz típico: ID de 8 bytes + ~120 bytes de Opus.
func audioPacket() Packet {
	payload := make([]byte, 1+8+120)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	return Packet{Kind: PacketAudio, Flags: FlagAudioSequence | FlagShout, Sequence: 123456, Talkspurt: 9, Payload: payload}
}

func TestSealOpenRoundTrip(t *testing.T) {
	c := newTestCipher(t, testKey)
	for _, packet := range []Packet{audioPacket(), {Kind: PacketPing, Sequence: 1}, {Kind: PacketChat, Payload: bytes.Repeat([]byte("x"), MaxPayloadSize)}} {
		data := c.seal(nil, packet)
		if len(data) != sealedSize(len(packet.Payload)) || string(data[:4]) != "VM02" {
			t.Fatalf("datagrama de %d bytes (magic %q), quería %d", len(data), data[:4], sealedSize(len(packet.Payload)))
		}
		if len(packet.Payload) > 16 && bytes.Contains(data, packet.Payload[:16]) {
			t.Fatal("el payload viaja en claro")
		}
		got, err := c.open(nil, data)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if got.Kind != packet.Kind || got.Flags != packet.Flags || got.Sequence != packet.Sequence || got.Talkspurt != packet.Talkspurt || !bytes.Equal(got.Payload, packet.Payload) {
			t.Fatalf("ida y vuelta: %+v, quería %+v", got.Kind, packet.Kind)
		}
	}
}

// Cualquier byte cambiado (cabecera en claro, nonce, texto cifrado o tag) hace que no abra.
func TestOpenRejectsTampering(t *testing.T) {
	c := newTestCipher(t, testKey)
	data := c.seal(nil, audioPacket())
	for i := range data {
		tampered := append([]byte(nil), data...)
		tampered[i] ^= 0x01
		if _, err := c.open(nil, tampered); err == nil {
			t.Fatalf("abrió un paquete con el byte %d alterado", i)
		}
	}
	if _, err := c.open(nil, data[:len(data)-1]); err == nil {
		t.Fatal("abrió un paquete truncado")
	}
	if _, err := c.open(nil, data); err != nil {
		t.Fatalf("el original dejó de abrir: %v", err)
	}
}

func TestOpenRejectsWrongKey(t *testing.T) {
	other := testKey
	other[0] ^= 0xff
	data := newTestCipher(t, testKey).seal(nil, audioPacket())
	if _, err := newTestCipher(t, other).open(nil, data); err == nil {
		t.Fatal("abrió un paquete sellado con otra clave")
	}
}

// El nonce no se repite: ni entre paquetes del mismo socket ni entre dos sockets con la misma
// clave (prefijo aleatorio distinto).
func TestNoncesDoNotRepeat(t *testing.T) {
	first, second := newTestCipher(t, testKey), newTestCipher(t, testKey)
	if first.prefix == second.prefix {
		t.Fatal("dos sockets con el mismo prefijo de nonce")
	}
	seen := make(map[[nonceSize]byte]bool)
	for i := 0; i < 5000; i++ {
		for _, c := range []*roomCipher{first, second} {
			var nonce [nonceSize]byte
			copy(nonce[:], c.seal(nil, audioPacket())[HeaderSize:])
			if seen[nonce] {
				t.Fatalf("nonce repetido en el paquete %d: %x", i, nonce)
			}
			seen[nonce] = true
		}
	}
}

// El camino del audio no asigna memoria al sellar ni al abrir con buffers reutilizados.
func TestSealOpenAudioNoAllocs(t *testing.T) {
	c := newTestCipher(t, testKey)
	packet := audioPacket()
	sealBuf := make([]byte, 0, 2048)
	openBuf := make([]byte, 0, 2048)
	data := c.seal(sealBuf, packet)
	if allocs := testing.AllocsPerRun(200, func() { data = c.seal(sealBuf, packet) }); allocs != 0 {
		t.Fatalf("seal asigna %v veces por paquete", allocs)
	}
	if allocs := testing.AllocsPerRun(200, func() {
		if _, err := c.open(openBuf, data); err != nil {
			t.Fatal(err)
		}
	}); allocs != 0 {
		t.Fatalf("open asigna %v veces por paquete", allocs)
	}
}

// Un paquete de otra clave (invitación equivocada) se descarta en Receive y queda contado con su
// origen; el siguiente válido llega igual.
func TestReceiveDropsWrongKey(t *testing.T) {
	server, err := Listen("127.0.0.1:0", testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	other := testKey
	other[31] ^= 0xff
	intruder, err := Listen("127.0.0.1:0", other)
	if err != nil {
		t.Fatal(err)
	}
	defer intruder.Close()
	friend, err := Listen("127.0.0.1:0", testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer friend.Close()

	if err := intruder.Send(server.Address(), Packet{Kind: PacketRoomHello, Payload: []byte("hola")}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // que el del intruso llegue primero
	if err := friend.Send(server.Address(), Packet{Kind: PacketChat, Payload: []byte("hola")}); err != nil {
		t.Fatal(err)
	}
	_ = server.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, from, err := server.Receive()
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if got.Kind != PacketChat || from.Port != friend.Address().Port {
		t.Fatalf("llegó %d de %v, quería el chat del amigo", got.Kind, from)
	}
	if server.Invalid() != 1 || server.LastInvalid() == nil || server.LastInvalid().Port != intruder.Address().Port {
		t.Fatalf("Invalid() = %d, LastInvalid() = %v", server.Invalid(), server.LastInvalid())
	}
}

// El host reenvía los mismos bytes que recibió (voz con Relay, archivos con TryRelayBulk).
func TestRelaySendsReceivedBytes(t *testing.T) {
	host, err := Listen("127.0.0.1:0", testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	sender, err := Listen("127.0.0.1:0", testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	host.AddPeer(listener.LocalAddr().(*net.UDPAddr))

	_ = host.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_ = listener.SetReadDeadline(time.Now().Add(2 * time.Second))
	readRaw := func() []byte {
		buf := make([]byte, 2048)
		n, _, err := listener.ReadFromUDP(buf)
		if err != nil {
			t.Fatal(err)
		}
		return buf[:n]
	}
	for _, relay := range []func(Packet, *net.UDPAddr){
		host.Relay,
		func(p Packet, except *net.UDPAddr) { host.TryRelayBulk(p, except) },
	} {
		if err := sender.Send(host.Address(), audioPacket()); err != nil {
			t.Fatal(err)
		}
		packet, from, err := host.Receive()
		if err != nil {
			t.Fatal(err)
		}
		relay(packet, from)
		if raw := readRaw(); !bytes.Equal(raw, packet.sealed) {
			t.Fatal("el reenvío no son los mismos bytes que llegaron")
		}
	}
}
