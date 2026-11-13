//go:build cgo

package ui

import (
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"voxmesh/internal/history"
	"voxmesh/internal/room"
)

// TestDeafenRestoresMute: ensordecer también mutea y al volver deja el mute de antes, con y sin
// mute previo; tocar el micrófono estando ensordecido vuelve a escuchar y a hablar (como Discord).
// Los dos botones se tachan en rojo.
func TestDeafenRestoresMute(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	test.NewTempWindow(t, a.content())

	check := func(step string, deafened, muted bool) {
		t.Helper()
		if a.isDeafened.Load() != deafened || a.isMuted.Load() != muted {
			t.Fatalf("%s: ensordecido=%v muteado=%v, quería %v y %v", step, a.isDeafened.Load(), a.isMuted.Load(), deafened, muted)
		}
		var wantDeafen, wantMute fyne.Resource = headsetIcon, micIcon
		if deafened {
			wantDeafen = headsetOffIcon
		}
		if muted {
			wantMute = micOffIcon
		}
		if a.deafenButton.Icon != wantDeafen || a.muteButton.Icon != wantMute {
			t.Fatalf("%s: los íconos no siguen el estado", step)
		}
	}

	check("al abrir", false, false)
	a.toggleDeafen()
	check("ensordecido sin mute previo", true, true)
	a.toggleDeafen()
	check("al volver sin mute previo", false, false)

	a.toggleMute()
	a.toggleDeafen()
	check("ensordecido con mute previo", true, true)
	a.toggleDeafen()
	check("al volver con mute previo", false, true)

	a.toggleDeafen()
	a.toggleMute()
	check("micrófono tocado ensordecido", false, false)
}

// TestDeafenedSoundboardIsSilent: ensordecido, la botonera no suena (ni la propia ni la que
// llega) y un sonido sin audio en disco no queda pendiente para sonar al bajarse; al volver
// funciona como siempre.
func TestDeafenedSoundboardIsSilent(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	store, err := history.New(t.TempDir(), "Sala")
	if err != nil {
		t.Fatal(err)
	}
	a.historyStore = store
	sound := room.Sound{ID: history.NewID("p1"), Ext: ".pcm", Name: "Bocina"}

	a.toggleDeafen()
	if a.playOrFetchSound(sound) {
		t.Fatal("ensordecido no se tiene que pedir el audio para sonar")
	}
	a.soundMu.RLock()
	pending := len(a.pendingPlays)
	a.soundMu.RUnlock()
	if pending != 0 {
		t.Fatalf("ensordecido quedaron %d sonidos pendientes", pending)
	}

	a.toggleDeafen()
	if !a.playOrFetchSound(sound) {
		t.Fatal("al volver, un sonido sin audio en disco se tiene que pedir")
	}
	a.soundMu.RLock()
	_, ok := a.pendingPlays[sound.ID]
	a.soundMu.RUnlock()
	if !ok {
		t.Fatal("al volver, el sonido tiene que quedar pendiente hasta que llegue")
	}
}
