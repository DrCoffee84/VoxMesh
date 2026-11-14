//go:build cgo

package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

// C29: el renglón se ilumina (poca opacidad, color de resaltado) mientras el mouse está sobre su
// ícono de copiar, vuelve al salir y destella al copiar.
func TestCopyButtonHighlightsRow(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	highlight := newRowHighlight()
	button := newCopyButton(highlight, nil)
	test.NewTempWindow(t, button)
	alpha := func() uint8 { return color.NRGBAModel.Convert(highlight.rect.FillColor).(color.NRGBA).A }
	if alpha() != 0 {
		t.Fatal("sin el mouse encima el renglón no se ilumina")
	}
	button.MouseIn(nil)
	primary := color.NRGBAModel.Convert(theme.Color(theme.ColorNamePrimary)).(color.NRGBA)
	if got := color.NRGBAModel.Convert(highlight.rect.FillColor).(color.NRGBA); got.A != rowHoverAlpha || got.R != primary.R || got.G != primary.G || got.B != primary.B {
		t.Fatalf("con el mouse encima: %v, quería el resaltado con opacidad %#x", got, rowHoverAlpha)
	}
	// El destello arranca fuerte y vuelve al reposo (con el mouse encima, el hover).
	if flashAlpha(rowHoverAlpha, 0) != rowFlashAlpha || flashAlpha(rowHoverAlpha, 1) != rowHoverAlpha || flashAlpha(0, 1) != 0 {
		t.Fatal("curva del destello")
	}
	if mid := flashAlpha(0, 0.5); mid <= 0 || mid >= rowFlashAlpha {
		t.Fatalf("a mitad del destello: %#x", mid)
	}
	highlight.flash() // el driver de test corre la animación entera
	if alpha() != rowHoverAlpha || highlight.anim != nil {
		t.Fatalf("después del destello: opacidad %#x, animación %v", alpha(), highlight.anim)
	}
	button.MouseOut()
	if alpha() != 0 {
		t.Fatal("al salir el mouse el renglón no se apagó")
	}
}
