package transport

import (
	"net"
	"sync"
	"testing"
)

func TestPeersCopyOnWrite(t *testing.T) {
	udp, err := Listen("127.0.0.1:0")
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
	udp, err := Listen("127.0.0.1:0")
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
	server, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := Listen("127.0.0.1:0")
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
