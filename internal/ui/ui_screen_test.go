//go:build cgo

package ui

import (
	"image"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"voxmesh/internal/screen"
)

// Elegir otra resolución en el banner la aplica a la sesión que se está compartiendo (la captura la
// lee en la vuelta siguiente) y la guarda para la próxima vez. "Completa" solo con monitores de más
// de 1920.
func TestBannerResolutionChangesLive(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	test.NewTempWindow(t, a.content())
	session := &shareSession{stop: make(chan struct{})}
	session.maxWidth.Store(int32(screen.Resolution1080.MaxWidth()))
	a.screenShare.Store(session)

	a.showBannerResolution(session, 2560, screen.Resolution1080)
	if len(a.shareResolution.Options) != 4 || a.shareResolution.Selected != resolutionLabel(screen.Resolution1080) {
		t.Fatalf("banner options %v, selected %q", a.shareResolution.Options, a.shareResolution.Selected)
	}
	if a.conf().ScreenResolution != "" {
		t.Fatal("showing the options must not save anything")
	}
	a.shareResolution.SetSelected(resolutionLabel(screen.Resolution720))
	if got := session.maxWidth.Load(); got != 1280 {
		t.Fatalf("session width = %d after picking 720p, want 1280", got)
	}
	if got := a.conf().ScreenResolution; got != string(screen.Resolution720) {
		t.Fatalf("saved resolution = %q, want 720p", got)
	}
	a.shareResolution.SetSelected(resolutionLabel(screen.ResolutionFull))
	if got := session.maxWidth.Load(); got != 0 {
		t.Fatalf("session width = %d after picking full, want 0", got)
	}

	a.showBannerResolution(session, 1920, screen.Resolution900)
	if slices.Contains(a.shareResolution.Options, resolutionLabel(screen.ResolutionFull)) {
		t.Fatalf("a 1920 monitor offers %v, want no full", a.shareResolution.Options)
	}
}

// stillSource es una pantalla quieta para el Sender.
type stillSource struct{ img *image.RGBA }

func (s stillSource) Capture(dst *image.RGBA) (*image.RGBA, error) {
	if dst == nil || dst.Rect != s.img.Rect {
		dst = image.NewRGBA(s.img.Rect)
	}
	copy(dst.Pix, s.img.Pix)
	return dst, nil
}

// Cambio de resolución a mitad de sesión del lado del que mira: el visor arma el cuadro del tamaño
// nuevo y se reacomoda, en "ajustar" (achicado al área, sin agrandar) y en 1:1 (tal cual).
func TestViewerFollowsResolutionChange(t *testing.T) {
	test.NewTempApp(t)
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	view := a.ensureScreenView()
	if view.areaW.Load() <= 0 || view.areaH.Load() <= 0 {
		view.areaW.Store(1000)
		view.areaH.Store(600)
	}
	peer, _ := screen.ParsePeerID("00000000000000a1")
	watch := newWatchSession("00000000000000a1", "Ana", peer)
	a.screenWatch.Store(watch)
	v := &viewer{app: a, watch: watch, view: view, assembler: screen.NewAssembler(), start: time.Now()}
	v.buffers.shown.Store(-1)

	capture := image.NewRGBA(image.Rect(0, 0, 2560, 1440))
	for i := range capture.Pix {
		capture.Pix[i] = uint8(i * 7 / 3)
	}
	sender := &screen.Sender{ID: peer, Emit: func(chunk []byte) bool {
		v.chunk(append([]byte(nil), chunk...), time.Now())
		return true
	}}
	shown := func() image.Rectangle {
		t.Helper()
		img, ok := view.image.Image.(*image.RGBA)
		if !ok || img == nil {
			t.Fatal("nothing painted")
		}
		return img.Rect
	}
	for _, res := range []screen.Resolution{screen.Resolution1080, screen.Resolution720} {
		sender.MaxWidth = res.MaxWidth()
		if err := sender.Step(stillSource{capture}, []string{"yo"}, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		fw, fh := sender.Size()
		if v.frame == nil || v.frame.Rect.Dx() != fw || v.frame.Rect.Dy() != fh {
			t.Fatalf("%s: viewer frame %v, want %d×%d", res, v.frame.Rect, fw, fh)
		}

		view.setActual(false)
		if !v.paint() {
			t.Fatalf("%s: fit paint skipped", res)
		}
		w, h := screen.FitSize(fw, fh, int(view.areaW.Load()), int(view.areaH.Load()))
		if got := shown(); got.Dx() != w || got.Dy() != h {
			t.Fatalf("%s: fit shows %v, want %d×%d", res, got, w, h)
		}

		view.setActual(true)
		if !v.paint() {
			t.Fatalf("%s: 1:1 paint skipped", res)
		}
		if got := shown(); got.Dx() != fw || got.Dy() != fh {
			t.Fatalf("%s: 1:1 shows %v, want %d×%d", res, got, fw, fh)
		}
		scale := view.window.Canvas().Scale()
		if size := view.image.MinSize(); size != fyne.NewSize(float32(fw)/scale, float32(fh)/scale) {
			t.Fatalf("%s: 1:1 min size %v", res, size)
		}
		if v.displayW != fw {
			t.Fatalf("%s: the cursor layer scales with width %d, want %d", res, v.displayW, fw)
		}
	}
}
