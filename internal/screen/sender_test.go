package screen

import (
	"image"
	"image/color"
	"testing"
	"time"
)

// fakeSource devuelve los cuadros de frames en orden (el último se repite) y cuenta las capturas.
type fakeSource struct {
	frames []*image.RGBA
	calls  int
}

func (f *fakeSource) Capture(dst *image.RGBA) (*image.RGBA, error) {
	frame := f.frames[min(f.calls, len(f.frames)-1)]
	f.calls++
	if dst == nil || dst.Rect != frame.Rect {
		dst = image.NewRGBA(frame.Rect)
	}
	copy(dst.Pix, frame.Pix)
	return dst, nil
}

// viewer rearma lo que sale del Sender como lo haría quien mira.
type viewer struct {
	t     *testing.T
	asm   *Assembler
	frame *image.RGBA
	tiles []int // tiles completos, en orden de llegada
}

func newViewer(t *testing.T) *viewer { return &viewer{t: t, asm: NewAssembler()} }

func (v *viewer) receive(payload []byte) {
	c, err := DecodeChunk(payload)
	if err != nil {
		v.t.Fatal(err)
	}
	r := v.asm.Add(c, time.Now())
	if r.Reset {
		v.frame = image.NewRGBA(image.Rect(0, 0, r.Grid.W, r.Grid.H))
	}
	if r.PNG != nil {
		if err := DrawTile(v.frame, r.Grid.Rect(r.Tile), r.PNG); err != nil {
			v.t.Fatal(err)
		}
		v.tiles = append(v.tiles, r.Tile)
	}
}

// fakeClock avanza step con cada chunk que sale: modela una salida lenta.
type fakeClock struct {
	now  time.Time
	step time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }

func newTestSender(v *viewer, clock *fakeClock) *Sender {
	return &Sender{
		ID:  testSharer,
		Now: clock.Now,
		Emit: func(chunk []byte) bool {
			v.receive(chunk)
			clock.now = clock.now.Add(clock.step)
			return true
		},
	}
}

func TestSenderIdleWithoutWatchers(t *testing.T) {
	v := newViewer(t)
	clock := &fakeClock{now: policyT0}
	s := newTestSender(v, clock)
	src := &fakeSource{frames: []*image.RGBA{noise(300, 200, 1)}}
	if err := s.Step(src, nil, clock.now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if src.calls != 0 || len(v.tiles) != 0 {
		t.Fatalf("nobody watching: %d captures, %d tiles sent", src.calls, len(v.tiles))
	}
}

// Con la salida lenta, lo que no salió de la captura A sale con los píxeles de B: ningún tile de
// A sale después de que llegó B, cada vuelta manda poco (nada se acumula) y al final quien mira
// tiene B exacto.
func TestSenderNewestCaptureWins(t *testing.T) {
	a := solid(64*8, 64*4, color.RGBA{200, 0, 0, 255})
	b := solid(64*8, 64*4, color.RGBA{0, 0, 200, 255})
	v := newViewer(t)
	clock := &fakeClock{now: policyT0, step: 10 * time.Millisecond}
	s := newTestSender(v, clock)
	src := &fakeSource{frames: []*image.RGBA{a, b}}
	watchers := []string{"juan"}

	if err := s.Step(src, watchers, clock.now.Add(35*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	sentFromA := len(v.tiles)
	if sentFromA == 0 || sentFromA >= 32 {
		t.Fatalf("first round sent %d of 32 tiles: the deadline must cut it", sentFromA)
	}
	if s.Stats.Behind != 1 {
		t.Fatalf("Behind = %d, want 1", s.Stats.Behind)
	}
	for round := 0; len(s.sched.Order()) > 0 || round == 0; round++ {
		if round == 40 {
			t.Fatal("never caught up with capture B")
		}
		before := len(v.tiles)
		if err := s.Step(src, watchers, clock.now.Add(35*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if n := len(v.tiles) - before; n > 4 {
			t.Fatalf("round %d sent %d tiles past the deadline", round, n)
		}
		for _, tile := range v.tiles[before:] {
			r := s.grid.Rect(tile)
			if got := v.frame.RGBAAt(r.Min.X, r.Min.Y); got.B != 200 {
				t.Fatalf("tile %d went out with capture A's pixels after B arrived", tile)
			}
		}
	}
	if s.sched.Refreshing() {
		t.Fatal("the full image never finished")
	}
	if string(v.frame.Pix) != string(b.Pix) {
		t.Fatal("the viewer does not have capture B")
	}
	// Los tiles de A que ya habían salido salen de nuevo con B; los demás, una sola vez.
	if want := 32 + sentFromA; len(v.tiles) != want {
		t.Fatalf("tiles sent = %d, want %d", len(v.tiles), want)
	}
}

// Pantalla quieta: después de la imagen completa no sale nada hasta que algo cambia, y entonces
// sale solo ese tile.
func TestSenderSendsOnlyChangedTiles(t *testing.T) {
	still := noise(64*4, 64*2, 7)
	changed := image.NewRGBA(still.Rect)
	copy(changed.Pix, still.Pix)
	changed.SetRGBA(130, 70, color.RGBA{1, 2, 3, 255}) // tile 6
	v := newViewer(t)
	clock := &fakeClock{now: policyT0}
	s := newTestSender(v, clock)
	src := &fakeSource{frames: []*image.RGBA{still, still, changed}}
	watchers := []string{"juan"}
	for range 2 {
		if err := s.Step(src, watchers, clock.now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if len(v.tiles) != 8 {
		t.Fatalf("full image + still frame sent %d tiles, want 8", len(v.tiles))
	}
	if err := s.Step(src, watchers, clock.now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(v.tiles) != 9 || v.tiles[8] != 6 {
		t.Fatalf("tiles after one change = %v, want only tile 6 more", v.tiles)
	}
	if string(v.frame.Pix) != string(changed.Pix) {
		t.Fatal("the viewer is not up to date")
	}
}

func TestSenderFullImageForANewWatcher(t *testing.T) {
	still := noise(64*2, 64*2, 8)
	v := newViewer(t)
	clock := &fakeClock{now: policyT0}
	s := newTestSender(v, clock)
	src := &fakeSource{frames: []*image.RGBA{still}}
	_ = s.Step(src, []string{"juan"}, clock.now.Add(time.Second))
	_ = s.Step(src, []string{"juan", "pedro"}, clock.now.Add(time.Second))
	if len(v.tiles) != 8 || s.Stats.FullImages != 2 {
		t.Fatalf("tiles = %d, full images = %d; want 8 and 2", len(v.tiles), s.Stats.FullImages)
	}
}

func TestSenderDownscalesWideScreens(t *testing.T) {
	v := newViewer(t)
	clock := &fakeClock{now: policyT0}
	s := newTestSender(v, clock)
	s.MaxWidth = 1920
	src := &fakeSource{frames: []*image.RGBA{solid(2560, 1440, color.RGBA{9, 9, 9, 255})}}
	if err := s.Step(src, []string{"juan"}, clock.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if w, h := s.Size(); w != 1920 || h != 1080 {
		t.Fatalf("frame size = %d×%d, want 1920×1080", w, h)
	}
	if v.frame.Rect.Dx() != 1920 || v.frame.RGBAAt(1000, 500).R != 9 {
		t.Fatal("the viewer did not get the downscaled frame")
	}
}

// pattern es una imagen sintética con detalle en todos los tiles que se comprime bien (no ruido).
func pattern(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			o := img.PixOffset(x, y)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = uint8(x^y), uint8(x*3), uint8(y*5), 255
		}
	}
	return img
}

// Cambiar la resolución a mitad de sesión (banner): cada tamaño nuevo es una sesión nueva con la
// imagen completa, quien mira arma exactamente la captura achicada a ese tamaño y el cursor usa la
// escala nueva. Pasa por achicar → más chico → completa → achicar otra vez.
func TestSenderResolutionChangeMidSession(t *testing.T) {
	capture := pattern(2560, 1440)
	v := newViewer(t)
	clock := &fakeClock{now: policyT0}
	s := newTestSender(v, clock)
	src := &fakeSource{frames: []*image.RGBA{capture}}
	watchers := []string{"juan"}
	steps := []struct {
		res          Resolution
		wantW, wantH int
	}{{Resolution1080, 1920, 1080}, {Resolution720, 1280, 720}, {ResolutionFull, 2560, 1440}, {Resolution900, 1600, 900}}
	sessions := map[uint32]bool{}
	for i, step := range steps {
		s.MaxWidth = step.res.MaxWidth()
		before := len(v.tiles)
		if err := s.Step(src, watchers, clock.now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if w, h := s.Size(); w != step.wantW || h != step.wantH {
			t.Fatalf("%s: frame %d×%d, want %d×%d", step.res, w, h, step.wantW, step.wantH)
		}
		if sessions[s.Session()] {
			t.Fatalf("%s: the new size kept an old session", step.res)
		}
		sessions[s.Session()] = true
		if s.Stats.FullImages != i+1 || len(v.tiles)-before != s.grid.Count() {
			t.Fatalf("%s: full images = %d, tiles sent = %d of %d; want a full image", step.res, s.Stats.FullImages, len(v.tiles)-before, s.grid.Count())
		}
		want := image.NewRGBA(image.Rect(0, 0, step.wantW, step.wantH))
		var scaler Scaler
		scaler.Scale(want, capture)
		if v.frame.Rect != want.Rect || string(v.frame.Pix) != string(want.Pix) {
			t.Fatalf("%s: the viewer did not rebuild the new frame", step.res)
		}
		// El cursor en (1280, 720) del monitor (el centro) cae en el centro del cuadro nuevo.
		x, y, ok := CursorInFrame(CursorState{Visible: true, Pos: image.Pt(1280, 720)}, capture.Rect, s.Scale())
		if !ok || x != step.wantW/2 || y != step.wantH/2 {
			t.Fatalf("%s: cursor at (%d,%d) with scale %.3f, want (%d,%d)", step.res, x, y, s.Scale(), step.wantW/2, step.wantH/2)
		}
		// Quieta: el tamaño no cambió, no sale nada más.
		before = len(v.tiles)
		if err := s.Step(src, watchers, clock.now.Add(time.Hour)); err != nil || len(v.tiles) != before {
			t.Fatalf("%s: still screen sent %d tiles (err %v)", step.res, len(v.tiles)-before, err)
		}
	}
}

func TestSenderStopsWhenEmitFails(t *testing.T) {
	s := &Sender{ID: testSharer, Emit: func([]byte) bool { return false }}
	src := &fakeSource{frames: []*image.RGBA{noise(128, 128, 9)}}
	if err := s.Step(src, []string{"juan"}, time.Now().Add(time.Hour)); err != ErrStopped {
		t.Fatalf("err = %v, want ErrStopped", err)
	}
}
