//go:build cgo

package ui

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	"voxmesh/internal/history"
	"voxmesh/internal/identity"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

// trustedIn lee confianza.json de store.
func trustedIn(t *testing.T, store *history.Store) []room.Trusted {
	t.Helper()
	data, err := store.ReadTrust()
	if err != nil {
		t.Fatal(err)
	}
	return room.DecodeTrustFile(data)
}

func writeTrusted(t *testing.T, store *history.Store, list ...room.Trusted) {
	t.Helper()
	data, err := room.EncodeTrustFile(list)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteTrust(data); err != nil {
		t.Fatal(err)
	}
}

// TestTrustListTravelsWithRoom (F10): la lista de confianza es de la sala. El cliente junta la del
// host con la suya y la guarda; lo que tenía guardado y el host no, se lo manda (una vez); el host
// lo suma solo si viene de un participante conectado y lo difunde.
func TestTrustListTravelsWithRoom(t *testing.T) {
	host, clientUDP, _ := helloHost(t, nil)
	now := time.Now().UTC().Truncate(time.Second)
	x := room.Trusted{Key: identity.New().Public().String(), Name: "Juan", By: "Coffee", At: now}
	y := room.Trusted{Key: identity.New().Public().String(), Name: "Pedro", By: "Budo", At: now}

	hostStore, err := history.New(filepath.Join(t.TempDir(), "host"), "sala")
	if err != nil {
		t.Fatal(err)
	}
	writeTrusted(t, hostStore, x)
	host.trust.open(hostStore)
	host.roomState.Trusted = host.trust.snapshot()
	host.roomState.Participants = []room.Participant{
		{ID: "host", Username: "Coffee", Address: "127.0.0.1:1", Order: 0, Connected: true},
		{ID: "budo", Username: "Budo", Address: clientUDP.Address().String(), Order: 1, Connected: true},
	}

	client := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	clientStore, err := history.New(filepath.Join(t.TempDir(), "client"), "sala")
	if err != nil {
		t.Fatal(err)
	}
	writeTrusted(t, clientStore, y)
	client.trust.open(clientStore)
	client.transport.Store(clientUDP)
	client.setEndpoint(host.transport.Load().Address().String())

	// El cliente recibe el estado del host: guarda los dos y le manda Pedro, que el host no tenía.
	hostInbox, clientInbox := inbox(host.transport.Load()), inbox(clientUDP)
	client.syncTrustFromHost(host.roomState, clientUDP)
	if !client.trust.has(mustParseKey(t, x.Key)) || !client.trust.has(mustParseKey(t, y.Key)) {
		t.Fatalf("el cliente no juntó las dos listas: %+v", client.trust.snapshot())
	}
	waitFor(t, "el cliente guarda la lista junta", func() bool { return len(trustedIn(t, clientStore)) == 2 })

	hostUDP := host.transport.Load()
	packet, from := receiveKind(t, hostInbox, transport.PacketRoomTrust)
	// De alguien que no es participante: nada.
	stranger := *from
	stranger.Port++
	host.handleTrustPacket(hostUDP, &stranger, packet.Payload)
	if len(host.roomState.Trusted) != 1 {
		t.Fatalf("sumó lo de alguien de afuera: %+v", host.roomState.Trusted)
	}
	host.handleTrustPacket(hostUDP, from, packet.Payload)
	if !room.TrustedHas(host.roomState.Trusted, mustParseKey(t, y.Key)) || len(host.roomState.Trusted) != 2 {
		t.Fatalf("el host no sumó a Pedro: %+v", host.roomState.Trusted)
	}
	waitFor(t, "el host guarda la lista de la sala", func() bool { return len(trustedIn(t, hostStore)) == 2 })
	// El host la difunde: el estado nuevo le llega al cliente.
	hostUDP.AddPeer(clientUDP.Address())
	host.broadcastRoomState(hostUDP, nil)
	statePacket, _ := receiveKind(t, clientInbox, transport.PacketRoomState)
	envelope, err := room.Decode(statePacket.Payload)
	if err != nil || envelope.Room == nil || len(envelope.Room.Trusted) != 2 {
		t.Fatalf("el estado no lleva la lista: %v %+v", err, envelope.Room)
	}

	// Una segunda vez no se lo vuelve a mandar (aunque al host le siguiera faltando).
	client.syncTrustFromHost(room.State{Name: "sala", Trusted: []room.Trusted{x}}, clientUDP)
	noKind(t, hostInbox, transport.PacketRoomTrust)

	// El que queda de host (takeOverHost) junta la del host viejo con la suya.
	z := room.Trusted{Key: identity.New().Public().String(), Name: "Ana", At: now}
	merged := client.trust.merge([]room.Trusted{z})
	if len(merged) != 3 {
		t.Errorf("la del host viejo más la propia: %+v", merged)
	}
}

func mustParseKey(t *testing.T, s string) identity.Key {
	t.Helper()
	key, err := identity.ParseKey(s)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// received es un paquete que llegó a un socket de prueba.
type received struct {
	packet transport.Packet
	from   *net.UDPAddr
}

// inbox lee todo lo que llega a udp hasta que se cierra (al terminar el test).
func inbox(udp *transport.UDP) chan received {
	ch := make(chan received, 64)
	go func() {
		for {
			packet, from, err := udp.Receive()
			if err != nil {
				return
			}
			ch <- received{packet, from}
		}
	}()
	return ch
}

// receiveKind espera (2 s como mucho) un paquete de kind en ch; los demás se descartan.
func receiveKind(t *testing.T, ch chan received, kind byte) (transport.Packet, *net.UDPAddr) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case r := <-ch:
			if r.packet.Kind == kind {
				return r.packet, r.from
			}
		case <-deadline:
			t.Fatalf("no llegó un paquete tipo %d", kind)
		}
	}
}

// noKind: en 300 ms no llega un paquete de kind.
func noKind(t *testing.T, ch chan received, kind byte) {
	t.Helper()
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case r := <-ch:
			if r.packet.Kind == kind {
				t.Fatalf("llegó un paquete tipo %d que no tenía que salir", kind)
			}
		case <-deadline:
			return
		}
	}
}
