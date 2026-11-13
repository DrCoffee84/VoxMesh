//go:build cgo

package ui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// La imagen ampliada se cierra con Esc (el popup toma el foco al abrirse), con la cruz y tocando
// la imagen; al cerrarla el foco vuelve a donde estaba (la entrada de la invitación).
func TestLightboxCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "imagen.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 40, 30))); err != nil {
		t.Fatal(err)
	}
	file.Close()

	for _, how := range []string{"esc", "cruz", "imagen"} {
		t.Run(how, func(t *testing.T) {
			a := newPromptApp(t)
			a.window.Canvas().Focus(a.endpointEntry)
			if a.window.Canvas().Focused() != a.endpointEntry {
				t.Fatal("no pude enfocar la entrada de la invitación")
			}
			// Se retiene la decodificación hasta cerrar: después la goroutine ve el popup cerrado
			// y termina dentro del test.
			lightboxDecode <- struct{}{}
			a.showLightbox(path)
			pop, ok := a.window.Canvas().Overlays().Top().(*widget.PopUp)
			if !ok {
				t.Fatal("no se abrió el popup")
			}
			view, ok := pop.Content.(*lightboxView)
			if !ok {
				t.Fatalf("contenido %T", pop.Content)
			}
			if a.window.Canvas().Focused() != view {
				t.Fatalf("foco en %T, quería la imagen ampliada", a.window.Canvas().Focused())
			}
			var closeButton *widget.Button
			var image *tappableImage
			walkObjects(view.content, func(object fyne.CanvasObject) {
				switch o := object.(type) {
				case *widget.Button:
					closeButton = o
				case *tappableImage:
					image = o
				}
			})
			if closeButton == nil || image == nil {
				t.Fatalf("cruz %v, imagen %v", closeButton, image)
			}
			switch how {
			case "esc":
				a.window.Canvas().Focused().TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
			case "cruz":
				test.Tap(closeButton)
			case "imagen":
				test.Tap(image)
			}
			<-lightboxDecode
			lightboxDecode <- struct{}{} // espera a que termine la goroutine
			<-lightboxDecode
			if pop.Visible() || a.window.Canvas().Overlays().Top() != nil {
				t.Fatal("la imagen ampliada sigue abierta")
			}
			if a.window.Canvas().Focused() != a.endpointEntry {
				t.Errorf("foco en %T, quería la entrada de la invitación", a.window.Canvas().Focused())
			}
		})
	}
}
