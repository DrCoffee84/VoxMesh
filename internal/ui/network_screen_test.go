//go:build cgo

package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

// Sala con host H (el propio en el lado host), Ana comparte y la miran Beto, Caro y el host;
// el host también comparte y lo mira Ana. IDs de 16 hex como room.ParticipantID.
func screenTestRoom() room.State {
	const (
		hostID = "0000000000000001"
		anaID  = "00000000000000a1"
		betoID = "00000000000000b2"
		caroID = "00000000000000c3"
	)
	state := room.State{Name: "sala", Epoch: 1, HostID: hostID, Participants: []room.Participant{
		{ID: hostID, Username: "Host", Address: "10.0.0.1:47830", Order: 0, Connected: true, Sharing: true, Watching: anaID},
		{ID: anaID, Username: "Ana", Address: "10.0.0.2:47830", Order: 1, Connected: true, Sharing: true, Watching: hostID},
		{ID: betoID, Username: "Beto", Address: "10.0.0.3:47830", Order: 2, Connected: true, Watching: anaID},
		{ID: caroID, Username: "Caro", Address: "10.0.0.4:47830", Order: 3, Connected: false, Watching: anaID},
	}}
	return state
}

func TestBuildScreenStateHostRoutes(t *testing.T) {
	state := screenTestRoom()
	got := buildScreenState(state, state.HostID, true)
	route, ok := got.routes["10.0.0.2:47830"]
	if !ok || route.sharer != "00000000000000a1" || !route.local {
		t.Fatalf("route of Ana = %+v, ok=%v (the host watches her)", route, ok)
	}
	// Caro se desconectó y el host recibe directo: solo Beto.
	if len(route.targets) != 1 || route.targets[0].String() != "10.0.0.3:47830" {
		t.Fatalf("targets of Ana = %v, want only Beto", route.targets)
	}
	if len(got.selfTargets) != 1 || got.selfTargets[0].String() != "10.0.0.2:47830" {
		t.Fatalf("host screen targets = %v, want Ana", got.selfTargets)
	}
	if len(got.myWatcherNames) != 1 || got.myWatcherNames[0] != "Ana" {
		t.Fatalf("host watchers = %v", got.myWatcherNames)
	}
	if got.copies != 2 || len(got.sharers) != 2 {
		t.Fatalf("copies = %d sharers = %v", got.copies, got.sharers)
	}
}

func TestBuildScreenStateClientHasNoRoutes(t *testing.T) {
	state := screenTestRoom()
	got := buildScreenState(state, "00000000000000a1", false)
	if got.routes != nil || got.selfTargets != nil {
		t.Fatal("a client never relays")
	}
	if len(got.myWatcherIDs) != 2 { // Beto y el host
		t.Fatalf("Ana's watchers = %v, want Beto and the host", got.myWatcherIDs)
	}
}

// El área del visor anota ancho y alto siempre: minimizada Fyne la achica a −8×−48 y al volver
// cambian los dos (antes un `||` dejaba el alto viejo y el visor no se pintaba más).
func TestScreenAreaLayoutTracksBothSides(t *testing.T) {
	test.NewTempApp(t)
	view := &screenView{window: test.NewTempWindow(t, nil), resize: make(chan struct{}, 1)}
	layout := &screenAreaLayout{view: view}
	for _, size := range []fyne.Size{fyne.NewSize(1092, 652), fyne.NewSize(-8, -48), fyne.NewSize(1092, 652), fyne.NewSize(800, 652)} {
		layout.Layout(nil, size)
		if w, h := view.areaW.Load(), view.areaH.Load(); w != int32(size.Width) || h != int32(size.Height) {
			t.Fatalf("after Layout(%v) the area is %d×%d", size, w, h)
		}
	}
	select {
	case <-view.resize:
	default:
		t.Fatal("a new size must wake the viewer up")
	}
}

// El host elige el ritmo LAN solo si todos los que participan en pantallas son cercanos.
func TestScreenRatesLANOnlyWhenEveryoneIsNear(t *testing.T) {
	state := screenTestRoom() // Ana y el host comparten; Beto (conectado) y Caro (desconectada) miran a Ana
	all := func(string) bool { return true }
	total, perCopy, lan, active := screenRates(state, all)
	if !active || !lan || total != transport.ScreenLANRateBytes || perCopy != transport.ScreenLANRateBytes/2 {
		t.Fatalf("all near: total=%d perCopy=%d lan=%v active=%v", total, perCopy, lan, active)
	}
	farBeto := func(addr string) bool { return addr != "10.0.0.3:47830" }
	if total, perCopy, lan, _ := screenRates(state, farBeto); lan || total != transport.ScreenRateBytes || perCopy != transport.ScreenRateBytes/2 {
		t.Fatalf("one watcher far: total=%d perCopy=%d lan=%v", total, perCopy, lan)
	}
	// El host no cuenta: su propia latencia no se mide.
	farHost := func(addr string) bool { return addr != "10.0.0.1:47830" }
	if _, _, lan, _ := screenRates(state, farHost); !lan {
		t.Fatal("the host itself must not make the room slow")
	}
	// Caro (desconectada) mirando no cuenta.
	farCaro := func(addr string) bool { return addr != "10.0.0.4:47830" }
	if _, _, lan, _ := screenRates(state, farCaro); !lan {
		t.Fatal("a disconnected watcher must not make the room slow")
	}
}

func TestScreenRatesIdleWithoutWatchers(t *testing.T) {
	state := screenTestRoom()
	for i := range state.Participants {
		state.Participants[i].Watching = ""
	}
	if total, _, lan, active := screenRates(state, func(string) bool { return true }); active || lan || total != transport.ScreenRateBytes {
		t.Fatalf("nobody watching: total=%d lan=%v active=%v", total, lan, active)
	}
}
