//go:build cgo

package ui

import (
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"voxmesh/internal/config"
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

// sendHello simula el RoomHello del cliente (id, name) desde su socket.
func sendHello(t *testing.T, host *App, client *transport.UDP, fo *room.Failover, id, name string) {
	t.Helper()
	payload, err := room.Encode(room.Envelope{Kind: room.HelloMessage, Participant: &room.Participant{ID: id, Username: name, Connected: true}})
	if err != nil {
		t.Fatal(err)
	}
	addr := client.Address()
	host.handleRoomHello(host.transport.Load(), fo, payload, addr, addr.String(), time.Now())
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
