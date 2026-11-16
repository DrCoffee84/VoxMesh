//go:build cgo

package ui

import (
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"voxmesh/internal/config"
	"voxmesh/internal/history"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

// TestPeerSlidersToggle (C36): el ícono oculta los sliders de todos los chips (el volumen de cada uno
// y el propio), suelta el medidor del propio (no se pinta nada) y lo guarda; los chips se achican y un
// chip que entra después respeta el estado; la tira de lag queda bajo el nombre. Al mostrarlos vuelve
// el oyente y el slider propio toma lo que se cambió en Ajustes mientras tanto.
func TestPeerSlidersToggle(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := newAppWithOwnConfig(t)
	setLanguage(t, "es")
	window := test.NewTempWindow(t, a.content())
	window.Resize(fyne.NewSize(960, 700))
	a.peerNames = []peerView{{id: lagSelfID, name: "Yo", isSelf: true}, {id: lagPepeID, name: "Pepe"}}
	a.refreshPeerAvatars()
	wantSliders := func(when string, shown bool) {
		t.Helper()
		for _, chip := range a.peerChips {
			if chip.sliders.Visible() != shown {
				t.Fatalf("%s: el slider de %s visible=%v, quería %v", when, chip.name, chip.sliders.Visible(), shown)
			}
		}
	}
	wantSliders("al arrancar", true)
	if a.slidersToggle.hint != "Ocultar volúmenes" || a.selfGate != a.peerChips[0].gate || a.selfGateMeter.Load() == nil {
		t.Fatalf("al arrancar: leyenda %q, slider propio enganchado %v", a.slidersToggle.hint, a.selfGateMeter.Load() != nil)
	}
	tall := a.peerChips[1].object.MinSize().Height

	test.Tap(a.slidersToggle)
	wantSliders("ocultos", false)
	if !a.conf().HidePeerSliders || a.slidersToggle.hint != "Mostrar volúmenes" {
		t.Fatalf("ocultos: config %v, leyenda %q", a.conf().HidePeerSliders, a.slidersToggle.hint)
	}
	if a.selfGate != nil || a.selfGateMeter.Load() != nil {
		t.Fatal("ocultos: el nivel propio sigue enganchado al medidor")
	}
	self, pepe := a.peerChips[0].object.MinSize().Height, a.peerChips[1].object.MinSize().Height
	if pepe >= tall || pepe != self {
		t.Fatalf("ocultos: el chip de Pepe mide %v de alto (antes %v, el propio %v): quería más chico e igual al propio", pepe, tall, self)
	}

	// Uno que entra después sale sin slider, y el propio rearmado sigue suelto.
	a.peerNames = append(a.peerNames, peerView{id: lagHostID, name: "Coffee"})
	a.refreshPeerAvatars()
	wantSliders("con uno nuevo", false)
	if a.selfGateMeter.Load() != nil {
		t.Fatal("al rearmar los chips se volvió a enganchar el nivel propio")
	}

	// Con la tira de lag: queda debajo del nombre.
	test.Tap(a.lagToggle)
	if chip := a.peerChips[1]; !chip.stripRow.Visible() || !chip.strip.Visible() || chip.sliders.Visible() {
		t.Fatal("con la tira y los sliders ocultos: quería la tira sola debajo del nombre")
	}
	test.Tap(a.lagToggle)

	// Mientras estaban ocultos se cambió el umbral en Ajustes.
	a.updateConfig(func(c *config.Config) { c.ThresholdDB = -30 })
	test.Tap(a.slidersToggle)
	wantSliders("de nuevo a la vista", true)
	if a.selfGate != a.peerChips[0].gate || a.selfGateMeter.Load() == nil || a.selfGate.slider.Value != -30 {
		t.Fatalf("a la vista: enganchado %v, slider en %v (quería -30)", a.selfGateMeter.Load() != nil, a.peerChips[0].gate.slider.Value)
	}

	// Queda guardado para la próxima vez.
	test.Tap(a.slidersToggle)
	waitConfigSaved(a)
	if saved, err := config.Load(a.cfgPath); err != nil || !saved.HidePeerSliders {
		t.Fatalf("en disco: %v, oculto=%v", err, saved.HidePeerSliders)
	}
}

// TestPeerChipShowsHost (C35): "Host" sale solo en el chip del host, delante del lag; si el host es
// esta PC, en el propio y detrás: "Local · Host". Cuando cambia el host la etiqueta pasa al nuevo,
// también sin rearmar los chips (mismos participantes en el mismo orden).
func TestPeerChipShowsHost(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	setLanguage(t, "es")
	test.NewTempWindow(t, a.content())
	key, err := transport.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	udp, err := transport.Listen("127.0.0.1:0", key)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	a.transport.Store(udp) // conectado: hay chip propio
	defer a.transport.Store(nil)
	a.updateConfig(func(c *config.Config) { c.Username = "Yo" }) // el chip propio lleva el de la config
	a.participantID = lagSelfID
	a.roomState = room.State{Name: "sala", Epoch: 1, HostID: lagHostID, Participants: []room.Participant{
		{ID: lagHostID, Username: "Coffee", Address: "9.9.9.9:47830", Order: 0, Connected: true},
		{ID: lagSelfID, Username: "Yo", Address: "[::]:47830", Order: 1, Connected: true},
		{ID: lagPepeID, Username: "Pepe", Address: "8.8.8.8:47830", Order: 2, Connected: true},
	}}
	a.refreshPeersFromRoom()

	chipOf := func(name string) *peerChip {
		t.Helper()
		for _, chip := range a.peerChips {
			if chip.name == name {
				return chip
			}
		}
		t.Fatalf("no hay chip de %s", name)
		return nil
	}
	wantHost := func(when, host string) {
		t.Helper()
		for _, chip := range a.peerChips {
			if chip.hostTag.Visible() != (chip.name == host) {
				t.Fatalf("%s: el chip de %s muestra Host=%v, quería solo en %s", when, chip.name, chip.hostTag.Visible(), host)
			}
		}
	}
	wantHost("al entrar", "Coffee")
	coffee := chipOf("Coffee")
	if coffee.lagLine.Objects[0] != coffee.hostTag || coffee.lagLine.Objects[1] != coffee.lagTxt {
		t.Fatal("en los demás, Host va delante del lag")
	}
	if text := coffee.hostTag.Objects[0].(*canvas.Text); text.Text != "Host" || !text.TextStyle.Bold || text.Color != theme.Color(theme.ColorNamePrimary) {
		t.Fatalf("la etiqueta dice %q (negrita %v, color %v), quería Host en negrita con el resaltado", text.Text, text.TextStyle.Bold, text.Color)
	}

	// Otro host, los mismos participantes: se actualizan los chips que ya estaban.
	before := chipOf("Pepe")
	a.roomState.HostID = lagPepeID
	a.refreshPeersFromRoom()
	if chipOf("Pepe") != before {
		t.Fatal("se rearmaron los chips: quería actualizarlos")
	}
	wantHost("con Pepe de host", "Pepe")

	// Esta PC queda de host (takeOverHost): el host viejo se desconecta.
	a.roomState.HostID = lagSelfID
	a.roomState.Participants[2].Connected = false
	a.refreshPeersFromRoom()
	wantHost("de host esta PC", "Yo")
	self := chipOf("Yo")
	if self.lagTxt.Text != "Local" || self.lagLine.Objects[0] != self.lagTxt || self.lagLine.Objects[1] != self.hostTag {
		t.Fatalf("el propio dice %q: quería «Local · Host»", self.lagTxt.Text)
	}
	if sep := self.hostTag.Objects[0].(*canvas.Text); sep.Text != " · " {
		t.Fatalf("separador %q", sep.Text)
	}
}

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
	a.historyStore.Store(store)
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
