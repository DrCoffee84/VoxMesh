package room

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"voxmesh/internal/transport"
)

func TestCandidatesUseParticipantOrder(t *testing.T) {
	address := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1000}
	state := New("gaming", "Daniel", address)
	state.Upsert(Participant{ID: "juan", Username: "Juan", Address: "127.0.0.1:1001", Connected: true, CanBeHost: true})
	state.Upsert(Participant{ID: "pedro", Username: "Pedro", Address: "127.0.0.1:1002", Connected: true, CanBeHost: true})

	candidates := state.Candidates()
	if len(candidates) != 2 || candidates[0].ID != "juan" || candidates[1].ID != "pedro" {
		t.Fatalf("expected Juan then Pedro (the host is never a candidate), got %#v", candidates)
	}
	if next, ok := state.NextHost(); !ok || next.ID != "juan" {
		t.Fatalf("expected Juan as next host, got %#v, ok=%v", next, ok)
	}
}

func TestCandidatesSkipDisconnectedParticipants(t *testing.T) {
	address := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1000}
	state := New("gaming", "Daniel", address)
	state.Upsert(Participant{ID: "juan", Username: "Juan", Address: "127.0.0.1:1001", Connected: false, CanBeHost: true})
	state.Upsert(Participant{ID: "pedro", Username: "Pedro", Address: "127.0.0.1:1002", Connected: true, CanBeHost: true})

	if candidates := state.Candidates(); len(candidates) != 1 || candidates[0].ID != "pedro" {
		t.Fatalf("expected only Pedro, got %#v", candidates)
	}
}

func TestCandidatesSkipIneligibleParticipants(t *testing.T) {
	address := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1000}
	state := New("gaming", "Daniel", address)
	// Juan desactivó "Permitir ser host de respaldo".
	state.Upsert(Participant{ID: "juan", Username: "Juan", Address: "127.0.0.1:1001", Connected: true, CanBeHost: false})
	state.Upsert(Participant{ID: "pedro", Username: "Pedro", Address: "127.0.0.1:1002", Connected: true, CanBeHost: true})

	if candidates := state.Candidates(); len(candidates) != 1 || candidates[0].ID != "pedro" {
		t.Fatalf("expected only Pedro because Juan opted out of being host, got %#v", candidates)
	}
}

func TestValidateRequiresUniqueOrderAndKnownHost(t *testing.T) {
	address := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1000}
	state := New("gaming", "Daniel", address)
	state.Participants = append(state.Participants, Participant{ID: "juan", Username: "Juan", Order: 0})
	if err := state.Validate(); err == nil {
		t.Fatal("expected duplicate participant order to be rejected")
	}

	state = New("gaming", "Daniel", address)
	state.HostID = "missing"
	if err := state.Validate(); err == nil {
		t.Fatal("expected unknown host to be rejected")
	}
}

func TestSetMutedRequiresMatchingAddressAndCopiesSlice(t *testing.T) {
	address := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1000}
	state := New("gaming", "Daniel", address)
	state.Upsert(Participant{ID: "juan", Username: "Juan", Address: "127.0.0.1:1001", Connected: true})
	previous := state.Participants

	if state.SetMuted("juan", "127.0.0.1:9999", true) {
		t.Fatal("mute from a different address must be rejected")
	}
	if !state.SetMuted("juan", "127.0.0.1:1001", true) {
		t.Fatal("expected mute to change the state")
	}
	if state.SetMuted("juan", "", true) {
		t.Fatal("setting the same mute again must report no change")
	}
	if !state.Participants[1].Muted {
		t.Fatalf("participant not muted: %#v", state.Participants[1])
	}
	if previous[1].Muted {
		t.Fatal("SetMuted wrote into the previous participants slice")
	}
}

var pruneT0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// pruneRoom es una sala con host H (conectado hace mucho), Ana conectada hace mucho, Beto
// desconectado hace 11 min y Caro desconectada hace 30 s.
func pruneRoom() State {
	return State{Name: "sala", Epoch: 1, HostID: "H", Participants: []Participant{
		{ID: "H", Username: "Host", Order: 0, Connected: true, LastSeenUTC: pruneT0.Add(-time.Hour)},
		{ID: "A", Username: "Ana", Order: 1, Connected: true, LastSeenUTC: pruneT0.Add(-time.Hour)},
		{ID: "B", Username: "Beto", Order: 2, Connected: false, LastSeenUTC: pruneT0.Add(-11 * time.Minute)},
		{ID: "C", Username: "Caro", Order: 3, Connected: false, LastSeenUTC: pruneT0.Add(-30 * time.Second)},
	}}
}

func ids(participants []Participant) string {
	var out []string
	for _, participant := range participants {
		out = append(out, participant.ID)
	}
	return strings.Join(out, ",")
}

func TestPruneDisconnectedDropsOnlyOldDisconnected(t *testing.T) {
	state := pruneRoom()
	previous := state.Participants
	removed := state.PruneDisconnected(pruneT0, DisconnectedTTL)
	if ids(removed) != "B" {
		t.Fatalf("expected only Beto pruned, got %q", ids(removed))
	}
	if ids(state.Participants) != "H,A,C" {
		t.Fatalf("expected H,A,C to stay (connected and recent are kept), got %q", ids(state.Participants))
	}
	if len(previous) != 4 || previous[2].ID != "B" {
		t.Fatal("PruneDisconnected wrote into the previous participants slice")
	}
	if removed := state.PruneDisconnected(pruneT0, DisconnectedTTL); removed != nil {
		t.Fatalf("second prune must be a no-op, got %q", ids(removed))
	}
}

func TestPruneDisconnectedNeverDropsTheHost(t *testing.T) {
	state := pruneRoom()
	// Un estado heredado donde el host figura desconectado (no debería pasar): igual no se poda.
	state.Participants[0].Connected = false
	state.PruneDisconnected(pruneT0, DisconnectedTTL)
	if _, ok := participantByID(state, "H"); !ok {
		t.Fatal("the host was pruned")
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("pruned state must stay valid: %v", err)
	}
}

func TestMakeRoomWithSpaceDoesNothing(t *testing.T) {
	state := pruneRoom()
	removed, ok := state.MakeRoom(pruneT0, 5, 20*time.Second)
	if !ok || removed != nil || len(state.Participants) != 4 {
		t.Fatalf("expected room without pruning, got ok=%v removed=%q", ok, ids(removed))
	}
}

func TestMakeRoomPrunesOldestDisconnectedFirst(t *testing.T) {
	state := pruneRoom()
	removed, ok := state.MakeRoom(pruneT0, 4, 20*time.Second)
	if !ok || ids(removed) != "B" {
		t.Fatalf("expected Beto (oldest) pruned to make room, got ok=%v removed=%q", ok, ids(removed))
	}
	if ids(state.Participants) != "H,A,C" {
		t.Fatalf("unexpected participants %q", ids(state.Participants))
	}
	// Con lugar para uno solo menos: también sale Caro (desconectada hace 30 s > 20 s).
	state = pruneRoom()
	removed, ok = state.MakeRoom(pruneT0, 3, 20*time.Second)
	if !ok || ids(removed) != "B,C" {
		t.Fatalf("expected Beto then Caro pruned, got ok=%v removed=%q", ok, ids(removed))
	}
}

func TestMakeRoomRejectsWhenOnlyConnectedOrRecent(t *testing.T) {
	state := pruneRoom()
	// Caro (30 s) está dentro del minAge: no se toca. Beto sí sale, pero no alcanza.
	removed, ok := state.MakeRoom(pruneT0, 3, time.Minute)
	if ok {
		t.Fatal("expected the room to stay full")
	}
	if ids(removed) != "B" || ids(state.Participants) != "H,A,C" {
		t.Fatalf("expected only Beto pruned, got removed=%q participants=%q", ids(removed), ids(state.Participants))
	}
	if removed, ok := state.MakeRoom(pruneT0, 3, time.Minute); ok || removed != nil {
		t.Fatalf("connected and recent participants must never be pruned, got ok=%v removed=%q", ok, ids(removed))
	}
}

func TestNextOrderAfterPruneKeepsStateValid(t *testing.T) {
	state := pruneRoom()
	state.Participants = append(state.Participants, Participant{ID: "D", Username: "Dani", Order: 4, Connected: true})
	state.PruneDisconnected(pruneT0, DisconnectedTTL) // saca a Beto (Order 2): quedan 0,1,3,4
	state.Participants = append(state.Participants, Participant{ID: "E", Username: "Eli", Order: state.NextOrder(), Connected: true})
	if err := state.Validate(); err != nil {
		t.Fatalf("len(Participants) would repeat an Order after pruning: %v", err)
	}
	if got := state.Participants[len(state.Participants)-1].Order; got != 5 {
		t.Fatalf("expected Order 5, got %d", got)
	}
}

// El host viejo que vuelve tras una migración tiene que seguir en la lista mientras el host nuevo
// le avisa.
func TestDisconnectedTTLOutlivesTheMigrationAnnounce(t *testing.T) {
	if DisconnectedTTL <= DefaultFailoverTimings().Announce {
		t.Fatalf("DisconnectedTTL %v must be longer than Announce %v", DisconnectedTTL, DefaultFailoverTimings().Announce)
	}
}

// Una sala llena tiene que seguir entrando en un datagrama (y lejos del límite: más grande se
// fragmenta y algunos routers descartan los fragmentos).
func TestFullRoomStateFitsInOneDatagram(t *testing.T) {
	state := State{Name: strings.Repeat("s", 64), Epoch: 1 << 40}
	for index := 0; index < MaxParticipants; index++ {
		state.Participants = append(state.Participants, Participant{
			ID:          fmt.Sprintf("%016x", index),
			Username:    strings.Repeat("ñ", 32),
			Address:     fmt.Sprintf("[2800:abcd:1234:5678:9abc:def0:1234:%04x]:65535", index),
			Order:       index,
			Connected:   true,
			CanBeHost:   true,
			Muted:       true,
			LastSeenUTC: pruneT0.Add(123456789 * time.Nanosecond),
		})
	}
	state.HostID = state.Participants[0].ID
	payload, err := Encode(Envelope{Kind: StateMessage, Room: &state})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > 8192 || len(payload) > transport.MaxPayloadSize {
		t.Fatalf("full room state is %d bytes", len(payload))
	}
}
