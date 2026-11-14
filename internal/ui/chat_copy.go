//go:build cgo

package ui

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Iluminar el renglón del chat que se va a copiar (AGENDA C29): mientras el mouse está sobre su
// ícono de copiar, el renglón toma el color de resaltado con poca opacidad; al copiar, un
// destello que se apaga solo. El color sale de la paleta al momento (C15).

const (
	rowHoverAlpha = 0x2e                   // mientras el mouse está sobre el ícono
	rowFlashAlpha = 0x80                   // al copiar
	rowFlashTime  = 450 * time.Millisecond // lo que tarda el destello en volver
)

// rowHighlight es el fondo de un renglón del chat (transparente salvo hover o destello). Solo el
// hilo de UI.
type rowHighlight struct {
	rect    *canvas.Rectangle
	hovered bool
	anim    *fyne.Animation
}

func newRowHighlight() *rowHighlight {
	rect := canvas.NewRectangle(color.Transparent)
	rect.CornerRadius = 4
	return &rowHighlight{rect: rect}
}

// setAlpha pinta el fondo con el resaltado de la paleta y esa opacidad (0 = transparente).
func (h *rowHighlight) setAlpha(alpha uint8) {
	fill := color.Color(color.Transparent)
	if alpha > 0 {
		c := color.NRGBAModel.Convert(theme.Color(theme.ColorNamePrimary)).(color.NRGBA)
		c.A = alpha
		fill = c
	}
	h.rect.FillColor = fill
	h.rect.Refresh()
}

// restAlpha es la opacidad sin destello.
func (h *rowHighlight) restAlpha() uint8 {
	if h.hovered {
		return rowHoverAlpha
	}
	return 0
}

func (h *rowHighlight) hover(on bool) {
	h.hovered = on
	if h.anim == nil {
		h.setAlpha(h.restAlpha())
	}
}

// flash destella y vuelve a la opacidad de reposo en rowFlashTime.
func (h *rowHighlight) flash() {
	if h.anim != nil {
		h.anim.Stop()
	}
	h.setAlpha(rowFlashAlpha)
	h.anim = fyne.NewAnimation(rowFlashTime, func(done float32) {
		h.setAlpha(flashAlpha(h.restAlpha(), done))
		if done >= 1 {
			h.anim = nil
		}
	})
	h.anim.Start()
}

// flashAlpha es la opacidad del destello cuando pasó la fracción done (0–1) de rowFlashTime: de
// rowFlashAlpha a rest.
func flashAlpha(rest uint8, done float32) uint8 {
	done = min(max(done, 0), 1)
	return uint8(float32(rowFlashAlpha) + (float32(rest)-float32(rowFlashAlpha))*done + 0.5)
}

// copyButton es el ícono de copiar de un mensaje: avisa al fondo de su renglón cuando el mouse
// entra y sale.
type copyButton struct {
	widget.Button
	highlight *rowHighlight
}

func newCopyButton(highlight *rowHighlight, tapped func()) *copyButton {
	b := &copyButton{highlight: highlight}
	b.Icon = theme.ContentCopyIcon()
	b.Importance = widget.LowImportance
	b.OnTapped = tapped
	b.ExtendBaseWidget(b)
	return b
}

func (b *copyButton) MouseIn(event *desktop.MouseEvent) {
	b.Button.MouseIn(event)
	b.highlight.hover(true)
}

func (b *copyButton) MouseOut() {
	b.Button.MouseOut()
	b.highlight.hover(false)
}
