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

// thumbStore guarda las miniaturas ya reducidas (~0,6 MB cada una), solo las de las filas armadas
// del chat (C38): renderChat y appendChatRows llaman a keep con las de sus filas, y reloadChat
// (cambio de sala, colgar) la vacía. Antes era un sync.Map que nunca se vaciaba.
type thumbStore struct {
	mu   sync.Mutex
	imgs map[string]image.Image // ruta -> miniatura
	// want son las rutas de las filas armadas: una miniatura que termina de decodificarse para una
	// fila que ya no está (otra sala, recortada) no se guarda.
	want map[string]bool
}

var thumbCache = &thumbStore{imgs: map[string]image.Image{}, want: map[string]bool{}}

// get da la miniatura guardada de path y la marca como de una fila armada.
func (s *thumbStore) get(path string) (image.Image, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.want[path] = true
	img, ok := s.imgs[path]
	return img, ok
}

// put guarda la miniatura si su fila sigue armada y lo dice.
func (s *thumbStore) put(path string, img image.Image) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.want[path] {
		s.imgs[path] = img
	}
	return s.want[path]
}

// keep deja solo las miniaturas de paths (las de las filas armadas).
func (s *thumbStore) keep(paths map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.want = paths
	for path := range s.imgs {
		if !paths[path] {
			delete(s.imgs, path)
		}
	}
}

// clear suelta todas las miniaturas.
func (s *thumbStore) clear() { s.keep(map[string]bool{}) }

// len es cuántas miniaturas hay guardadas.
func (s *thumbStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.imgs)
}

// newChatThumbnail devuelve un canvas.Image vacío que se completa en segundo plano.
// Con canvas.Image{File: ...} Fyne decodifica la imagen completa en el hilo de UI al pintar,
// y refreshChat recrea las filas al conectar y en cada tecla del buscador: una captura de
// varios MB congelaba la ventana. Acá se decodifica y reduce una vez, fuera del hilo de UI.
func newChatThumbnail(path string) *canvas.Image {
	img := &canvas.Image{FillMode: canvas.ImageFillContain}
	img.SetMinSize(fyne.NewSize(240, 160))
	if cached, ok := thumbCache.get(path); ok {
		img.Image = cached
		return img
	}
	go func() {
		thumb, err := imagefile.Load(path, thumbMaxW, thumbMaxH)
		if err != nil {
			logging.Errorf("miniatura %q: %s", path, logging.FormatError(err))
			return
		}
		if !thumbCache.put(path, thumb) {
			return // la fila ya salió del chat (releaseChatImages)
		}
		fyne.Do(func() {
			img.Image = thumb
			img.Refresh()
		})
	}()
	return img
}

// releaseChatImages suelta los píxeles de las miniaturas de filas que salen del chat (C38): Fyne
// guarda un rato el renderer de cada widget que se sacó, y con él la imagen, así que la memoria no
// bajaba hasta su limpieza. De paso cierra la leyenda de una hora que tenía el mouse encima (C39):
// sacada, no le llega el MouseOut. Hilo de UI.
func releaseChatImages(rows []fyne.CanvasObject) {
	for _, row := range rows {
		switch o := row.(type) {
		case *tappableImage:
			o.image.Image = nil
		case *chatStamp:
			if o.hovered {
				o.MouseOut()
			}
		case *fyne.Container:
			releaseChatImages(o.Objects)
		}
	}
}
