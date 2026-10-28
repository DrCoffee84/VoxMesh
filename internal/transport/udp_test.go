package transport

import (
	"net"
	"sync"
	"testing"
	"time"
)

func TestPeersCopyOnWrite(t *testing.T) {
	udp, err := Listen("127.0.0.1:0", testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()

	a := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40001}
	b := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40002}
	udp.AddPeer(a)
	udp.AddPeer(b)
	udp.AddPeer(&net.UDPAddr{IP: net.ParseIP("::ffff:127.0.0.1"), Port: 40001}) // misma dirección en forma IPv6
	held := udp.snapshot()
	if len(held) != 2 {
		t.Fatalf("peers = %d, quería 2", len(held))
	}
	udp.RemovePeer(a)
	if len(held) != 2 || !sameAddr(held[0], a) {
		t.Fatal("RemovePeer modificó una instantánea ya publicada")
	}
	if peers := udp.Peers(); len(peers) != 1 || !sameAddr(peers[0], b) {
		t.Fatalf("Peers() = %v, quería [%v]", peers, b)
	}
}

// Lectores (Broadcast/AddPeer de peers conocidos) y escritores en paralelo: con -race esto
// detecta cualquier acceso sin sincronizar a la lista.
func TestPeersConcurrentAccess(t *testing.T) {
	udp, err := Listen("127.0.0.1:0", testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()

	var wg sync.WaitGroup
	packet := Packet{Kind: PacketAudio, Payload: make([]byte, 64)}
	for worker := 0; worker < 4; worker++ {
		wg.Add(2)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				addr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 41000 + worker*10 + i%10}
				udp.AddPeer(addr)
				if i%3 == 0 {
					udp.RemovePeer(addr)
				}
			}
		}(worker)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				udp.Broadcast(packet, nil)
				_ = udp.Peers()
			}
		}()
	}
	wg.Wait()
}

// Receive reutiliza el buffer de lectura: el payload devuelto tiene que ser una copia que
// sobreviva a la lectura siguiente (los handlers de chat/assets lo retienen).
func TestReceiveCopiesPayloadAndKeepsTalkspurt(t *testing.T) {
	server, err := Listen("127.0.0.1:0", testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := Listen("127.0.0.1:0", testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	first := Packet{Kind: PacketAudio, Flags: FlagAudioSequence, Sequence: 7, Talkspurt: 3, Payload: []byte("primero")}
	second := Packet{Kind: PacketChat, Sequence: 8, Payload: []byte("SEGUNDO-MAS-LARGO")}
	if err := client.Send(server.Address(), first); err != nil {
		t.Fatal(err)
	}
	if err := client.Send(server.Address(), second); err != nil {
		t.Fatal(err)
	}
	got1, _, err := server.Receive()
	if err != nil {
		t.Fatal(err)
	}
	got2, _, err := server.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if string(got1.Payload) != "primero" || cap(got1.Payload) > 64 {
		t.Fatalf("first payload overwritten or oversized: %q (cap %d)", got1.Payload, cap(got1.Payload))
	}
	if got1.Talkspurt != 3 || got1.Flags != FlagAudioSequence || got1.Sequence != 7 {
		t.Fatalf("header fields lost: %+v", got1)
	}
	if string(got2.Payload) != "SEGUNDO-MAS-LARGO" {
		t.Fatalf("second payload: %q", got2.Payload)
	}
}

// Los archivos salen al ritmo de BulkRateBytes: sin esto un sonido de 8 MB saturaba la subida.
func TestUDP_BulkIsPaced(t *testing.T) {
	sender, err := Listen("127.0.0.1:0", testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	receiver, err := Listen("127.0.0.1:0", testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()

	const packets = 60
	payload := make([]byte, 1200)
	arrivals := make(chan time.Time, packets)
	go func() {
		for {
			if _, _, err := receiver.Receive(); err != nil {
				return
			}
			arrivals <- time.Now()
		}
	}()
	start := time.Now()
	for i := 0; i < packets; i++ {
		if !sender.SendBulk(receiver.Address(), Packet{Kind: PacketAssetChunk, Payload: payload}) {
			t.Fatal("SendBulk on an open socket returned false")
		}
	}
	var last time.Time
	for i := 0; i < packets; i++ {
		select {
		case last = <-arrivals:
		case <-time.After(3 * time.Second):
			t.Fatalf("received %d of %d bulk packets", i, packets)
		}
	}
	// La ráfaga deja pasar bulkBurstBytes de entrada; el resto sale a BulkRateBytes.
	total := packets * sealedSize(len(payload))
	minimum := time.Duration(float64(total-bulkBurstBytes) / BulkRateBytes * float64(time.Second))
	if elapsed := last.Sub(start); elapsed < minimum*9/10 {
		t.Fatalf("bulk not paced: %d bytes in %v, want at least %v", total, elapsed, minimum)
	}
}

// Cerrar el socket destraba a quien espera lugar en la cola de archivos.
func TestUDP_CloseUnblocksSendBulk(t *testing.T) {
	udp, err := Listen("127.0.0.1:0", testKey)
	if err != nil {
		t.Fatal(err)
	}
	dest := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}
	payload := make([]byte, 1200)
	done := make(chan bool, 1)
	go func() {
		// ~1.2 MB a 250 KB/s: la cola se llena enseguida y SendBulk queda esperando.
		for i := 0; i < bulkQueue*4; i++ {
			if !udp.SendBulk(dest, Packet{Kind: PacketAssetChunk, Payload: payload}) {
				done <- false
				return
			}
		}
		done <- true
	}()
	time.Sleep(100 * time.Millisecond)
	_ = udp.Close()
	select {
	case finished := <-done:
		if finished {
			t.Fatal("all bulk packets were queued before Close: the queue is not bounded")
		}
	case <-time.After(time.Second):
		t.Fatal("SendBulk still blocked after Close")
	}
}

// Basura en el puerto (scanners) y el ICMP "port unreachable" de mandarle algo a un peer que ya
// cerró no cortan Receive: antes cualquiera de los dos dejaba sordo a receiveLoop.
func TestReceiveSurvivesJunkAndClosedPeer(t *testing.T) {
	server, err := Listen("127.0.0.1:0", testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := Listen("127.0.0.1:0", testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	gone, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	goneAddr := gone.LocalAddr().(*net.UDPAddr)
	_ = gone.Close()

	if err := server.Send(goneAddr, Packet{Kind: PacketPing}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.conn.WriteToUDP([]byte("no soy VoxMesh"), server.Address()); err != nil {
		t.Fatal(err)
	}
	if err := client.Send(server.Address(), Packet{Kind: PacketChat, Payload: []byte("hola")}); err != nil {
		t.Fatal(err)
	}
	_ = server.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, _, err := server.Receive()
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if got.Kind != PacketChat || string(got.Payload) != "hola" {
		t.Fatalf("paquete = %+v, quería el chat", got)
	}
	if server.Invalid() != 1 {
		t.Fatalf("Invalid() = %d, quería 1", server.Invalid())
	}
}
