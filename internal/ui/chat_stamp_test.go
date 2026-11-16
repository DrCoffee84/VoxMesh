//go:build cgo

package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/i18n"
)

// TestStampHint (C39): la leyenda de la hora, con el día de la semana, la fecha y los segundos, en
// hora local y en el idioma de la app.
func TestStampHint(t *testing.T) {
	at := time.Date(2026, 11, 16, 22, 53, 7, 0, time.Local)
	if got, want := stampHint(at, i18n.ES), "lunes 16/11/2026 · 22:53:07"; got != want {
		t.Errorf("es: %q, quería %q", got, want)
	}
	if got, want := stampHint(at, i18n.EN), "Monday 11/16/2026 · 22:53:07"; got != want {
		t.Errorf("en: %q, quería %q", got, want)
	}
	if got, want := stampHint(at.AddDate(0, 0, 3).UTC(), i18n.ES), "jueves 19/11/2026 · 22:53:07"; got != want {
		t.Errorf("desde UTC: %q, quería %q (en hora local)", got, want)
	}
}

// TestChatStampTooltip (C39): la hora de un renglón es un widget aparte, encima de la hora que
// dibuja el RichText (el renglón se ve igual que antes), y al dejar el mouse encima sale la leyenda
// con la fecha y la hora exactas, alineada a la izquierda de la hora. Solo sobre la hora.
func TestChatStampTooltip(t *testing.T) {
	a, root := newRoomTestApp(t)
	setLanguage(t, "es")
	at := time.Date(2026, 11, 16, 22, 53, 7, 0, time.Local)
	chatRoomMessages(t, root, "Sala", 1, at, map[int]string{0: "un mensaje bastante largo para que el renglón tenga lugar a la derecha de la hora"})
	a.initHistory("Sala")
	relayout(a.window)

	var stamp *chatStamp
	var text *widget.RichText
	walkObjects(a.chatBox, func(o fyne.CanvasObject) {
		switch o := o.(type) {
		case *chatStamp:
			stamp = o
		case *widget.RichText:
			text = o
		}
	})
	if stamp == nil || text == nil {
		t.Fatal("el renglón no tiene la hora como widget aparte")
	}
	if first := text.Segments[0].(*widget.TextSegment); first.Text != "22:53 " || first.Style.ColorName != theme.ColorNamePlaceHolder {
		t.Fatalf("la hora dibujada cambió: %q (%s)", first.Text, first.Style.ColorName)
	}
	driver := fyne.CurrentApp().Driver()
	offset := driver.AbsolutePositionForObject(stamp).Subtract(driver.AbsolutePositionForObject(text))
	if pad := theme.InnerPadding(); offset != fyne.NewPos(pad, pad) {
		t.Fatalf("la zona de la hora está en %v del texto, quería donde la dibuja (%v)", offset, pad)
	}
	if want := fyne.MeasureText("22:53", theme.TextSize(), fyne.TextStyle{}); stamp.Size() != want {
		t.Fatalf("la zona de la hora mide %v, quería %v", stamp.Size(), want)
	}

	canvas := a.window.Canvas()
	stampAt := driver.AbsolutePositionForObject(stamp)
	test.MoveMouse(canvas, stampAt.AddXY(stamp.Size().Width+20, 5)) // sobre el texto: nada
	time.Sleep(bindTooltipDelay + 200*time.Millisecond)
	if a.tooltip.box.Visible() {
		t.Fatal("la leyenda salió sobre el texto del mensaje")
	}
	test.MoveMouse(canvas, stampAt.AddXY(5, 5))
	deadline := time.Now().Add(bindTooltipDelay + time.Second)
	for !a.tooltip.box.Visible() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if want := "lunes 16/11/2026 · 22:53:07"; !a.tooltip.box.Visible() || a.tooltip.text.Text != want {
		t.Fatalf("leyenda visible=%v texto=%q, quería %q", a.tooltip.box.Visible(), a.tooltip.text.Text, want)
	}
	if boxX := driver.AbsolutePositionForObject(a.tooltip.box).X; boxX != stampAt.X {
		t.Fatalf("la leyenda empieza en x=%v, la hora en %v", boxX, stampAt.X)
	}
	saveShot(t, a.window, "c39_hora_exacta")

	// Si la fila sale mientras el mouse está sobre la hora, la leyenda se cierra.
	a.chatSearch.SetText("x") // rearma las filas
	if a.tooltip.box.Visible() {
		t.Fatal("la leyenda quedó abierta con su fila rearmada")
	}
}
