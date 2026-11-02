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
	"voxmesh/internal/room"
)

// TestMainViewIgnoresLongText: ningún texto largo ensancha la ventana principal. Antes la
// invitación pegada estiraba la columna izquierda y la botonera quedaba fuera de la ventana.
func TestMainViewIgnoresLongText(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	view := a.content()
	window := test.NewTempWindow(t, view)
	window.Resize(fyne.NewSize(960, 700))
	base := view.MinSize().Width

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
