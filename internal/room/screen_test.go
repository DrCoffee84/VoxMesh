package room

import "testing"

// Pantalla compartida: el host lleva quién comparte y quién mira en el estado de la sala.

func TestSetSharingRequiresMatchingAddressAndCopiesSlice(t *testing.T) {
	state := testRoom(false)
	previous := state.Participants
	if state.SetSharing("A", betoAddr, true) {
		t.Fatal("sharing from a different address must be rejected")
	}
	if !state.SetSharing("A", anaAddr, true) || !state.Participants[1].Sharing {
		t.Fatalf("expected Ana to share: %+v", state.Participants[1])
	}
	if state.SetSharing("A", "", true) {
		t.Fatal("setting the same value again must report no change")
	}
	if previous[1].Sharing {
		t.Fatal("SetSharing wrote into the previous participants slice")
	}
	if state.SetSharing("nobody", "", true) {
		t.Fatal("an unknown participant cannot share")
	}
}

func TestWatchersAddAndRemove(t *testing.T) {
	state := testRoom(true)
	state.SetSharing("A", "", true)
	if !state.SetWatching("B", betoAddr, "A") || !state.SetWatching("C", caroAddr, "A") {
		t.Fatal("expected Beto and Caro to start watching Ana")
	}
	if got := ids(state.Watchers("A")); got != "B,C" {
		t.Fatalf("watchers of Ana = %q, want B,C", got)
	}
	if state.SetWatching("B", caroAddr, "") {
		t.Fatal("stop watching from a different address must be rejected")
	}
	if !state.SetWatching("B", betoAddr, "") {
		t.Fatal("expected Beto to stop watching")
	}
	if got := ids(state.Watchers("A")); got != "C" {
		t.Fatalf("watchers of Ana = %q, want C", got)
	}
}

func TestSetWatchingRejectsSelfAndUnknown(t *testing.T) {
	state := testRoom(false)
	state.SetSharing("A", "", true)
	if state.SetWatching("A", "", "A") {
		t.Fatal("nobody watches their own screen")
	}
	if state.SetWatching("B", "", "ghost") {
		t.Fatal("watching someone who is not in the room must be rejected")
	}
}

func TestWatchersNeedTheSharerConnectedAndSharing(t *testing.T) {
	state := testRoom(false)
	// Beto pide mirar antes de que el Sharing de Ana llegue (o después de que dejó): no cuenta.
	state.SetWatching("B", "", "A")
	if len(state.Watchers("A")) != 0 {
		t.Fatal("watching someone who does not share must not count")
	}
	state.SetSharing("A", "", true)
	if got := ids(state.Watchers("A")); got != "B" {
		t.Fatalf("watchers of Ana = %q, want B once she shares", got)
	}
	state.MarkDisconnected("A")
	if len(state.Watchers("A")) != 0 {
		t.Fatal("a disconnected sharer has no watchers")
	}
}

func TestWatcherDisconnectStopsCounting(t *testing.T) {
	state := testRoom(true)
	state.SetSharing("A", "", true)
	state.SetWatching("B", "", "A")
	state.SetWatching("C", "", "A")
	state.MarkDisconnected("B")
	if got := ids(state.Watchers("A")); got != "C" {
		t.Fatalf("watchers of Ana = %q, want C (Beto is gone)", got)
	}
}

func TestStopSharingClearsWatchers(t *testing.T) {
	state := testRoom(true)
	state.SetSharing("A", "", true)
	state.SetSharing("B", "", true)
	state.SetWatching("H", "", "A")
	state.SetWatching("C", "", "B")
	if !state.SetSharing("A", anaAddr, false) {
		t.Fatal("expected Ana to stop sharing")
	}
	if state.Participants[0].Watching != "" {
		t.Fatalf("the host still points at Ana's screen: %+v", state.Participants[0])
	}
	if state.Participants[3].Watching != "B" {
		t.Fatal("Caro watches Beto: stopping Ana's screen must not touch her")
	}
}

func TestScreenCopiesSkipTheHost(t *testing.T) {
	state := testRoom(true)
	if state.ScreenCopies() != 0 {
		t.Fatal("nobody shares: no copies")
	}
	state.SetSharing("A", "", true)
	state.SetWatching("H", "", "A") // el host lo recibe directo: no es copia
	if got := state.ScreenCopies(); got != 0 {
		t.Fatalf("copies = %d, want 0 (only the host watches)", got)
	}
	state.SetWatching("B", "", "A")
	state.SetWatching("C", "", "A")
	if got := state.ScreenCopies(); got != 2 {
		t.Fatalf("copies = %d, want 2", got)
	}
	// El host comparte también y Ana lo mira: sale del host a Ana.
	state.SetSharing("H", "", true)
	state.SetWatching("A", "", "H")
	if got := state.ScreenCopies(); got != 3 {
		t.Fatalf("copies = %d, want 3 with two screens", got)
	}
}

func TestWatchersSurviveHostMigration(t *testing.T) {
	room := testRoom(true)
	room.SetSharing("B", "", true)
	room.SetWatching("C", "", "B")
	room.SetWatching("H", "", "B")
	f := newClient("A", room)
	f.HostLeft(foT0)
	state := find(t, f.Drain(), ActBecomeHost).State
	// El host nuevo (Ana) hereda quién comparte y quién mira; el viejo ya no cuenta.
	if got := ids(state.Watchers("B")); got != "C" {
		t.Fatalf("watchers of Beto after the migration = %q, want C", got)
	}
	if got := state.ScreenCopies(); got != 1 {
		t.Fatalf("copies after the migration = %d, want 1", got)
	}
}

func TestOldHostSharingStopsWithTheMigration(t *testing.T) {
	room := testRoom(true)
	room.SetSharing("H", "", true)
	room.SetWatching("B", "", "H")
	f := newClient("A", room)
	f.HostLeft(foT0)
	state := find(t, f.Drain(), ActBecomeHost).State
	if len(state.Watchers("H")) != 0 || state.ScreenCopies() != 0 {
		t.Fatal("the old host left: its screen has no watchers in the new room")
	}
}
