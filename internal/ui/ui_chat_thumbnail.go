//go:build cgo

package ui

import (
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

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
		thumb, err := loadThumbnail(path)
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

func loadThumbnail(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	src, _, err := image.Decode(file)
	if err != nil {
		return nil, err
	}
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= thumbMaxW && h <= thumbMaxH {
		return src, nil
	}
	scale := min(float64(thumbMaxW)/float64(w), float64(thumbMaxH)/float64(h))
	dstW, dstH := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, bounds, draw.Src, nil)
	return dst, nil
}
