//go:build cgo

package ui

import (
	"image/color"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

type levelBar struct {
	widget.BaseWidget
	mu    sync.RWMutex
	value float64
}

type levelBarRenderer struct {
	bar      *levelBar
	backdrop *canvas.Rectangle
	fill     *canvas.Rectangle
}

func newLevelBar() *levelBar {
	bar := &levelBar{}
	bar.ExtendBaseWidget(bar)
	return bar
}

func (bar *levelBar) SetValue(value float64) {
	if value < 0 {
		value = 0
	}
	if value > 1 {
		value = 1
	}
	bar.mu.Lock()
	bar.value = value
	bar.mu.Unlock()
	bar.Refresh()
}

func (bar *levelBar) CreateRenderer() fyne.WidgetRenderer {
	return &levelBarRenderer{
		bar:      bar,
		backdrop: canvas.NewRectangle(color.NRGBA{R: 50, G: 55, B: 65, A: 255}),
		fill:     canvas.NewRectangle(color.NRGBA{R: 55, G: 190, B: 125, A: 255}),
	}
}

func (renderer *levelBarRenderer) Layout(size fyne.Size) {
	renderer.backdrop.Resize(size)
	renderer.bar.mu.RLock()
	value := renderer.bar.value
	renderer.bar.mu.RUnlock()
	renderer.fill.Resize(fyne.NewSize(size.Width*float32(value), size.Height))
}

func (renderer *levelBarRenderer) MinSize() fyne.Size {
	return fyne.NewSize(160, 6)
}

func (renderer *levelBarRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{renderer.backdrop, renderer.fill}
}

func (renderer *levelBarRenderer) Refresh() {
	renderer.Layout(renderer.bar.Size())
	renderer.backdrop.Refresh()
	renderer.fill.Refresh()
}

func (renderer *levelBarRenderer) Destroy() {}
