package room

import (
	"net"
	"testing"
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
