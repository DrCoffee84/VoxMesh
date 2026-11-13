//go:build cgo

package ui

import (
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

// TestSoundStopForEveryone: "Detener para todos" en el host manda sound_stop por PacketChat, y
// quien lo recibe (y el que cortó) descarta los sonidos que se estaban bajando para sonar
// (pendingPlays).
func TestSoundStopForEveryone(t *testing.T) {
	test.NewTempApp(t)
	key, err := transport.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	hostUDP, err := transport.Listen("127.0.0.1:0", key)
	if err != nil {
		t.Fatal(err)
	}
	defer hostUDP.Close()
	peerUDP, err := transport.Listen("127.0.0.1:0", key)
	if err != nil {
		t.Fatal(err)
	}
	defer peerUDP.Close()
	hostUDP.AddPeer(peerUDP.Address())

	host := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	host.participantID = "00112233aabbccdd"
	host.roomState = room.State{HostID: host.participantID}
	host.transport.Store(hostUDP)
	host.pendingPlays = map[string]time.Time{"aaaa": time.Now()}

	received := make(chan transport.Packet, 1)
	go func() {
		packet, _, err := peerUDP.Receive()
		if err == nil {
			received <- packet
		}
	}()
	host.stopSoundsForAll()

	var packet transport.Packet
	select {
	case packet = <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("no llegó el sound_stop")
	}
	envelope, err := room.Decode(packet.Payload)
	if packet.Kind != transport.PacketChat || err != nil || envelope.Kind != room.SoundStopMessage {
		t.Fatalf("paquete tipo %d, sobre %q (%v): quería PacketChat con sound_stop", packet.Kind, envelope.Kind, err)
	}
	host.soundMu.RLock()
	hostPending := len(host.pendingPlays)
	host.soundMu.RUnlock()
	if hostPending != 0 {
		t.Fatalf("el que cortó sigue con %d sonidos por sonar", hostPending)
	}

	// Del otro lado: el despacho del sobre vacía lo pendiente.
	peer := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	peer.pendingPlays = map[string]time.Time{"bbbb": time.Now(), "cccc": time.Now()}
	peer.handleChatPacket(packet.Payload)
	if len(peer.pendingPlays) != 0 {
		t.Fatalf("después de sound_stop quedan %d sonidos por sonar", len(peer.pendingPlays))
	}
}
