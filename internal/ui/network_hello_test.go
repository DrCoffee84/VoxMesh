//go:build cgo

package ui

import (
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"voxmesh/internal/config"
	"voxmesh/internal/identity"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

// helloHost arma un host con el estado dado y un socket de cliente para mandarle RoomHello.
func helloHost(t *testing.T, participants []room.Participant) (*App, *transport.UDP, *room.Failover) {
	t.Helper()
	test.NewTempApp(t)
	key, err := transport.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	hostUDP, err := transport.Listen("127.0.0.1:0", key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hostUDP.Close() })
	clientUDP, err := transport.Listen("127.0.0.1:0", key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { clientUDP.Close() })

	host := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	host.updateConfig(func(c *config.Config) { c.EventSoundsEnabled = false }) // sin abrir el audio
	host.participantID = "host"
	host.roomState = room.State{Name: "sala", Epoch: 1, HostID: "host", Participants: participants}
	host.transport.Store(hostUDP)
	fo := room.NewFailover("host", room.DefaultFailoverTimings())
	fo.Hosting(host.roomState, time.Now())
	return host, clientUDP, fo
}

// sendHello simula el RoomHello del cliente (id, name) desde su socket, firmado con una llave
// nueva (F10).
func sendHello(t *testing.T, host *App, client *transport.UDP, fo *room.Failover, id, name string) {
	t.Helper()
	sendSignedHello(t, host, client, fo, &room.Participant{ID: id, Username: name, Connected: true}, identity.New(), nil)
}

// sendSignedHello manda el Hello de p con la llave de signer (p.Key = la de signer si viene
// vacía); sig != nil reemplaza la firma.
func sendSignedHello(t *testing.T, host *App, client *transport.UDP, fo *room.Failover, p *room.Participant, signer *identity.Identity, sig []byte) {
	t.Helper()
	if p.Key == "" {
		p.Key = signer.Public().String()
	}
	envelope := room.Envelope{Kind: room.HelloMessage, Participant: p, Signature: room.SignHello(signer, [32]byte(host.transport.Load().Key()), p.ID)}
	if sig != nil {
		envelope.Signature = sig
	}
	payload, err := room.Encode(envelope)
	if err != nil {
		t.Fatal(err)
	}
	addr := client.Address()
	host.handleRoomHello(host.transport.Load(), fo, payload, addr, addr.String(), time.Now())
}

// TestRoomHelloNeedsItsKeySignature (F10): el Hello lleva la llave de la instalación y su firma
// sobre la sesión; con la llave de otro, sin firma o firmado para otra sala no entra. Con la firma
// buena la llave queda en el estado.
func TestRoomHelloNeedsItsKeySignature(t *testing.T) {
	host, client, fo := helloHost(t, []room.Participant{{ID: "host", Username: "Coffee", Address: "127.0.0.1:1", Connected: true}})
	juan, other := identity.New(), identity.New()

	// La llave de Juan, firmado por otro (no tiene la privada de Juan).
	sendSignedHello(t, host, client, fo, &room.Participant{ID: "juan", Username: "Juan", Key: juan.Public().String()}, other, nil)
	// Sin firma.
	sendSignedHello(t, host, client, fo, &room.Participant{ID: "juan", Username: "Juan"}, juan, []byte{})
	// Firmado para otra sala (otra clave).
	sendSignedHello(t, host, client, fo, &room.Participant{ID: "juan", Username: "Juan"}, juan, room.SignHello(juan, [32]byte{7}, "juan"))
	// Sin llave.
	payload, _ := room.Encode(room.Envelope{Kind: room.HelloMessage, Participant: &room.Participant{ID: "juan", Username: "Juan"}})
	host.handleRoomHello(host.transport.Load(), fo, payload, client.Address(), client.Address().String(), time.Now())
	if _, ok := participantByID(host.roomState, "juan"); ok {
		t.Fatal("entró un Hello sin la firma de su llave")
	}

	sendSignedHello(t, host, client, fo, &room.Participant{ID: "juan", Username: "Juan", Connected: true}, juan, nil)
	got, ok := participantByID(host.roomState, "juan")
	if !ok || got.Key != juan.Public().String() {
		t.Fatalf("con la firma buena entra con su llave: %+v", got)
	}
}

func participantByID(state room.State, id string) (room.Participant, bool) {
	for _, participant := range state.Participants {
		if participant.ID == id {
			return participant, true
		}
	}
	return room.Participant{}, false
}

// TestRoomHelloSameAddressReplacesOldRecord (R6): cambió de nombre y volvió desde el mismo puerto.
// El registro viejo sale (no queda otro ID con la misma dirección) y el nuevo ocupa su lugar.
func TestRoomHelloSameAddressReplacesOldRecord(t *testing.T) {
	host, client, fo := helloHost(t, nil)
	address := client.Address().String()
	host.roomState.Participants = []room.Participant{
		{ID: "host", Username: "Coffee", Address: "127.0.0.1:1", Order: 0, Connected: true},
		{ID: "old", Username: "Pepe", Address: address, Order: 1, Connected: true, LastSeenUTC: time.Now().UTC()},
		{ID: "juan", Username: "Juan", Address: "127.0.0.1:2", Order: 2, Connected: true, LastSeenUTC: time.Now().UTC()},
	}

	sendHello(t, host, client, fo, "new", "Pepo")

	if _, ok := participantByID(host.roomState, "old"); ok {
		t.Fatal("el registro viejo sigue en la lista")
	}
	joined, ok := participantByID(host.roomState, "new")
	if !ok || joined.Username != "Pepo" || joined.Order != 1 || !joined.Connected || joined.Address != address {
		t.Fatalf("entró %+v (ok=%v): quería Pepo con el lugar 1", joined, ok)
	}
	if err := host.roomState.Validate(); err != nil {
		t.Fatalf("estado inválido: %v", err)
	}
	if len(host.roomState.Participants) != 3 || host.roomState.Participants[1].ID != "new" {
		t.Fatalf("lista %+v: el nuevo tiene que quedar en el lugar del viejo", host.roomState.Participants)
	}
}

// TestRoomHelloSameNameAndIPKeepsTheName (C2): vuelve desde otro puerto de la misma IP con el mismo
// nombre, antes de que el host lo diera por desconectado. Si el registro viejo está callado, es él:
// sale el viejo, entra sin " (2)" y con su lugar. Si el viejo sigue vivo (otra persona en la misma
// casa con el mismo nombre), " (2)" como siempre.
func TestRoomHelloSameNameAndIPKeepsTheName(t *testing.T) {
	for _, c := range []struct {
		name     string
		lastSeen time.Duration
		want     string
		oldStays bool
	}{
		{"callado", room.ReturnSilence + 3*time.Second, "Pepe", false},
		{"vivo", time.Second, "Pepe (2)", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			host, client, fo := helloHost(t, nil)
			host.roomState.Participants = []room.Participant{
				{ID: "host", Username: "Coffee", Address: "10.0.0.1:47830", Order: 0, Connected: true},
				{ID: "old", Username: "Pepe", Address: "127.0.0.1:1", Order: 1, Connected: true, LastSeenUTC: time.Now().Add(-c.lastSeen).UTC()},
				{ID: "juan", Username: "Juan", Address: "127.0.0.1:2", Order: 2, Connected: true, LastSeenUTC: time.Now().UTC()},
			}

			sendHello(t, host, client, fo, "new", "Pepe")

			joined, ok := participantByID(host.roomState, "new")
			if !ok || joined.Username != c.want {
				t.Fatalf("entró %+v (ok=%v): quería %q", joined, ok, c.want)
			}
			if _, stays := participantByID(host.roomState, "old"); stays != c.oldStays {
				t.Fatalf("el registro viejo sigue=%v, quería %v", stays, c.oldStays)
			}
			if !c.oldStays && joined.Order != 1 {
				t.Fatalf("entró con el lugar %d: quería el 1 del registro viejo", joined.Order)
			}
			if err := host.roomState.Validate(); err != nil {
				t.Fatalf("estado inválido: %v", err)
			}
		})
	}
}
