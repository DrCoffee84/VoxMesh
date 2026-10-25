//go:build cgo

package ui

import (
	"image/color"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

// levelBar es una barra de nivel horizontal (0–1) con una marca opcional: una línea vertical que
// muestra un umbral (modo grito, exigencia del VAD, volumen mínimo de la puerta).
type levelBar struct {
	widget.BaseWidget
	mu          sync.RWMutex
	value       float64
	fill        color.Color // nil = levelFillColor
	markerPos   float64     // 0–1
	markerColor color.Color // nil = sin marca
}

type levelBarRenderer struct {
	bar      *levelBar
	backdrop *canvas.Rectangle
	fill     *canvas.Rectangle
	marker   *canvas.Rectangle
}

var levelFillColor = color.NRGBA{R: 55, G: 190, B: 125, A: 255}

// levelMarkerWidth es el ancho de la línea de umbral.
const levelMarkerWidth = 3

func newLevelBar() *levelBar {
	bar := &levelBar{}
	bar.ExtendBaseWidget(bar)
	return bar
}

func clampUnit(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func (bar *levelBar) SetValue(value float64) {
	bar.mu.Lock()
	bar.value = clampUnit(value)
	bar.mu.Unlock()
	bar.Refresh()
}

// SetColor cambia el color de la barra (estado del medidor, ver renderSettingsView).
func (bar *levelBar) SetColor(fill color.Color) {
	bar.mu.Lock()
	changed := bar.fill != fill
	bar.fill = fill
	bar.mu.Unlock()
	if changed {
		bar.Refresh()
	}
}

// SetMarker pone la línea de umbral en pos (0–1) con ese color; nil la oculta.
func (bar *levelBar) SetMarker(pos float64, markerColor color.Color) {
	pos = clampUnit(pos)
	bar.mu.Lock()
	changed := bar.markerPos != pos || bar.markerColor != markerColor
	bar.markerPos, bar.markerColor = pos, markerColor
	bar.mu.Unlock()
	if changed {
		bar.Refresh()
	}
}

func (bar *levelBar) CreateRenderer() fyne.WidgetRenderer {
	marker := canvas.NewRectangle(color.Transparent)
	marker.Hide()
	return &levelBarRenderer{
		bar:      bar,
		backdrop: canvas.NewRectangle(color.NRGBA{R: 50, G: 55, B: 65, A: 255}),
		fill:     canvas.NewRectangle(levelFillColor),
		marker:   marker,
	}
}

func (renderer *levelBarRenderer) Layout(size fyne.Size) {
	renderer.backdrop.Resize(size)
	renderer.bar.mu.RLock()
	value, markerPos := renderer.bar.value, renderer.bar.markerPos
	renderer.bar.mu.RUnlock()
	renderer.fill.Resize(fyne.NewSize(size.Width*float32(value), size.Height))
	x := size.Width*float32(markerPos) - levelMarkerWidth/2
	x = min(max(x, 0), size.Width-levelMarkerWidth)
	renderer.marker.Move(fyne.NewPos(x, 0))
	renderer.marker.Resize(fyne.NewSize(levelMarkerWidth, size.Height))
}

func (renderer *levelBarRenderer) MinSize() fyne.Size {
	return fyne.NewSize(160, 10)
}

func (renderer *levelBarRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{renderer.backdrop, renderer.fill, renderer.marker}
}

func (renderer *levelBarRenderer) Refresh() {
	renderer.bar.mu.RLock()
	fill, markerColor := renderer.bar.fill, renderer.bar.markerColor
	renderer.bar.mu.RUnlock()
	if fill == nil {
		fill = levelFillColor
	}
	renderer.fill.FillColor = fill
	if markerColor == nil {
		renderer.marker.Hide()
	} else {
		renderer.marker.FillColor = markerColor
		renderer.marker.Show()
	}
	renderer.Layout(renderer.bar.Size())
	renderer.backdrop.Refresh()
	renderer.fill.Refresh()
	renderer.marker.Refresh()
}

func (renderer *levelBarRenderer) Destroy() {}
