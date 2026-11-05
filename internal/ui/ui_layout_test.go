//go:build cgo

package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"voxmesh/internal/config"
	"voxmesh/internal/history"
	"voxmesh/internal/i18n"
	"voxmesh/internal/room"
)

// TestMainViewIgnoresLongText: ningún texto largo ensancha la ventana principal. Antes la
// invitación pegada estiraba la columna izquierda y la botonera quedaba fuera de la ventana. En
// los dos idiomas: los textos de los botones de las columnas fijas tienen que entrar.
func TestMainViewIgnoresLongText(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) { testMainViewIgnoresLongText(t, lang) })
	}
}

func testMainViewIgnoresLongText(t *testing.T, lang string) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	setLanguage(t, lang) // después de New, que aplica el de la config
	view := a.content()
	window := test.NewTempWindow(t, view)
	window.Resize(fyne.NewSize(960, 700))
	base := view.MinSize().Width
	if base > config.DefaultWindowWidth {
		t.Fatalf("ancho mínimo %v: no entra en la ventana por defecto (%v)", base, config.DefaultWindowWidth)
	}

	long := strings.Repeat("palabra ", 40)
	roomName := strings.Repeat("S", 64)
	steps := []struct {
		name   string
		change func()
	}{
		{"invitación", func() { a.endpointEntry.SetText("VM1-" + strings.Repeat("xK3f", 18)) }},
		{"mensaje", func() { a.chatInput.SetText(long) }},
		{"buscador del chat", func() { a.chatSearch.SetText(long) }},
		{"barra de estado", func() { a.status.SetText(long) }},
		{"imagen pegada", func() {
			a.stagedLabel.SetText(long)
			a.stagedBar.Show()
		}},
		{"registro de actividad", func() { a.addActivity(strings.Repeat("x", 300)) }},
		{"sala", func() {
			a.roomSelect.Options = []string{roomName}
			a.roomSelect.SetSelected(roomName)
		}},
		{"nombre propio", func() { a.selfNameLabel.SetText(strings.Repeat("N", 32) + " (16)") }},
		{"mensaje con un link largo", func() {
			a.chatBox.Add(a.buildChatMessageObject(history.Message{
				Username: strings.Repeat("U", 32),
				Text:     "mirá https://example.com/" + strings.Repeat("camino/", 30),
			}, false))
		}},
		{"sonido", func() {
			a.sounds = append(a.sounds, room.Sound{ID: "0123456789abcdef0123456789abcdef", Ext: ".pcm", Name: strings.Repeat("Bocina ", 9)})
			a.rebuildSoundboard()
		}},
		{"banner de pantalla compartida", func() {
			a.screenState.Store(&screenState{myWatcherNames: strings.Split(strings.Repeat("Participante,", 15), ",")})
			a.screenShare.Store(&shareSession{stop: make(chan struct{})})
			a.refreshShareControls()
		}},
	}
	for _, step := range steps {
		step.change()
		if got := view.MinSize().Width; got > base {
			t.Fatalf("%s: ancho mínimo %v, quería a lo sumo %v", step.name, got, base)
		}
	}

	// Lo de adentro de cada lateral entra en su ancho fijo (si no, quedaría recortado).
	for _, column := range []struct {
		name  string
		panel *fyne.Container
		width float32
	}{
		{"panel de conexión", a.connectionPanel, connectionPanelWidth},
		{"botonera", a.soundPanel, soundPanelWidth},
	} {
		if got := column.panel.Objects[0].MinSize().Width; got > column.width {
			t.Errorf("%s: el contenido pide %v de ancho y la columna mide %v", column.name, got, column.width)
		}
	}
}

// TestFitButtonText: el atajo entra en su botón (recortado con "…") y uno corto queda igual.
func TestFitButtonText(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	if got := fitButtonText("F9", bindButtonWidth); got != "F9" {
		t.Fatalf("atajo corto: %q", got)
	}
	got := fitButtonText("Ctrl+Shift+Alt+NumPad 9", bindButtonWidth)
	available := bindButtonWidth - 2*theme.InnerPadding()
	if !strings.HasSuffix(got, "…") || fyne.MeasureText(got, theme.TextSize(), fyne.TextStyle{Bold: true}).Width > available {
		t.Fatalf("atajo largo: %q no entra en %v", got, available)
	}
}

// TestBindTooltip: con el mouse sobre un atajo aparece la leyenda con el atajo completo después
// de bindTooltipDelay, y se va al sacar el mouse.
func TestBindTooltip(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	const combo = "Ctrl+Shift+Alt+F12"
	a.updateConfig(func(c *config.Config) { c.StopSoundBind = combo })
	window := test.NewTempWindow(t, a.content())
	window.Resize(fyne.NewSize(960, 700))

	at := fyne.CurrentApp().Driver().AbsolutePositionForObject(a.stopBindBtn)
	test.MoveMouse(window.Canvas(), at.AddXY(10, 10))
	if a.tooltip.box.Visible() {
		t.Fatal("la leyenda apareció antes de tiempo")
	}
	deadline := time.Now().Add(bindTooltipDelay + time.Second)
	for !a.tooltip.box.Visible() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !a.tooltip.box.Visible() || a.tooltip.text.Text != combo {
		t.Fatalf("leyenda visible=%v texto=%q, quería %q", a.tooltip.box.Visible(), a.tooltip.text.Text, combo)
	}
	test.MoveMouse(window.Canvas(), fyne.NewPos(1, 1))
	if a.tooltip.box.Visible() {
		t.Fatal("la leyenda sigue después de sacar el mouse")
	}
}

// TestShareBannerPushesContentDown: el banner de "Estás compartiendo tu pantalla" baja el resto de
// la ventana en vez de taparlo (mostrar u ocultar no rearma el Border de afuera por sí solo). El
// título de la ventana (el aviso que se ve minimizada) cambia junto con el banner.
func TestShareBannerPushesContentDown(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	window := test.NewTempWindow(t, a.content())
	window.SetTitle(appTitle)
	a.window = window
	window.Resize(fyne.NewSize(960, 700))
	center := a.mainFrame.Objects[0]
	before := center.Position().Y

	a.screenShare.Store(&shareSession{stop: make(chan struct{})})
	a.refreshShareControls()
	if !a.shareBanner.Visible() {
		t.Fatal("the banner must show while sharing")
	}
	if got, want := center.Position().Y, before+a.shareBanner.MinSize().Height; got < want {
		t.Fatalf("content starts at y=%v with the banner, want at least %v", got, want)
	}
	if got := window.Title(); got != i18n.T("main.title_sharing") {
		t.Fatalf("title while sharing = %q", got)
	}

	a.screenShare.Store(nil)
	a.refreshShareControls()
	if a.shareBanner.Visible() || center.Position().Y != before {
		t.Fatalf("after stopping: banner visible=%v, content at y=%v (want %v)", a.shareBanner.Visible(), center.Position().Y, before)
	}
	if got := window.Title(); got != appTitle {
		t.Fatalf("title after stopping = %q, want %q", got, appTitle)
	}
}
