package room

import (
	"net"
	"testing"
)

func TestElectNextHostUsesParticipantOrder(t *testing.T) {
	address := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1000}
	state := New("gaming", "Daniel", address)
	state.Upsert(Participant{ID: "juan", Username: "Juan", Address: "127.0.0.1:1001", Connected: true, CanBeHost: true})
	state.Upsert(Participant{ID: "pedro", Username: "Pedro", Address: "127.0.0.1:1002", Connected: true, CanBeHost: true})
	state.MarkDisconnected(state.HostID)

	previousEpoch := state.Epoch
	candidate, ok := state.ElectNextHost()
	if !ok || candidate.ID != "juan" {
		t.Fatalf("expected Juan as next host, got %#v, ok=%v", candidate, ok)
	}
	if state.HostID != "juan" || state.Epoch != previousEpoch+1 {
		t.Fatalf("host election did not update state: host=%q epoch=%d", state.HostID, state.Epoch)
	}
}

func TestElectNextHostSkipsDisconnectedCandidate(t *testing.T) {
	address := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1000}
	state := New("gaming", "Daniel", address)
	state.Upsert(Participant{ID: "juan", Username: "Juan", Address: "127.0.0.1:1001", Connected: false, CanBeHost: true})
	state.Upsert(Participant{ID: "pedro", Username: "Pedro", Address: "127.0.0.1:1002", Connected: true, CanBeHost: true})
	state.MarkDisconnected(state.HostID)

	candidate, ok := state.ElectNextHost()
	if !ok || candidate.ID != "pedro" {
		t.Fatalf("expected Pedro as fallback host, got %#v, ok=%v", candidate, ok)
	}
}

func TestElectNextHostSkipsIneligibleCandidate(t *testing.T) {
	address := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1000}
	state := New("gaming", "Daniel", address)
	// Juan has CanBeHost: false, Pedro has CanBeHost: true
	state.Upsert(Participant{ID: "juan", Username: "Juan", Address: "127.0.0.1:1001", Connected: true, CanBeHost: false})
	state.Upsert(Participant{ID: "pedro", Username: "Pedro", Address: "127.0.0.1:1002", Connected: true, CanBeHost: true})
	state.MarkDisconnected(state.HostID)

	candidate, ok := state.ElectNextHost()
	if !ok || candidate.ID != "pedro" {
		t.Fatalf("expected Pedro to be elected because Juan opted out of being host, got %#v, ok=%v", candidate, ok)
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
