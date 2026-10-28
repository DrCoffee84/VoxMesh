//go:build cgo

package ui

import (
	"image"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

	"voxmesh/internal/imagefile"
	"voxmesh/internal/logging"
)

// Miniatura al doble del tamaño mostrado (240x160) para que se vea nítida en pantallas HiDPI.
const thumbMaxW, thumbMaxH = 480, 320

var thumbCache sync.Map // ruta -> image.Image ya reducida

// newChatThumbnail devuelve un canvas.Image vacío que se completa en segundo plano.
// Con canvas.Image{File: ...} Fyne decodifica la imagen completa en el hilo de UI al pintar,
// y refreshChat recrea las filas al conectar y en cada tecla del buscador: una captura de
// varios MB congelaba la ventana. Acá se decodifica y reduce una vez, fuera del hilo de UI.
func newChatThumbnail(path string) *canvas.Image {
	img := &canvas.Image{FillMode: canvas.ImageFillContain}
	img.SetMinSize(fyne.NewSize(240, 160))
	if cached, ok := thumbCache.Load(path); ok {
		img.Image = cached.(image.Image)
		return img
	}
	go func() {
		thumb, err := imagefile.Load(path, thumbMaxW, thumbMaxH)
		if err != nil {
			logging.Errorf("miniatura %q: %s", path, logging.FormatError(err))
			return
		}
		thumbCache.Store(path, thumb)
		fyne.Do(func() {
			img.Image = thumb
			img.Refresh()
		})
	}()
	return img
}
