package screen

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"
)

func TestCursorRoundTrip(t *testing.T) {
	for _, c := range []Cursor{
		{Sharer: testSharer, Session: 7, Visible: true, Shape: 0xdeadbeef, X: 1919, Y: 1079},
		{Sharer: testSharer, Session: 7, Visible: true, Shape: 1, X: -5, Y: -12}, // cursor saliendo por el borde
		{Sharer: testSharer, Session: 7},
	} {
		got, err := DecodeCursor(AppendCursor(nil, c))
		if err != nil || got != c {
			t.Fatalf("cursor %+v came back as %+v (err %v)", c, got, err)
		}
	}
	bad := AppendCursor(nil, Cursor{Sharer: testSharer})
	bad[12] = 2
	if _, err := DecodeCursor(bad); err == nil {
		t.Error("a visible byte other than 0/1 must be rejected")
	}
	if _, err := DecodeCursor(bad[:20]); err == nil {
		t.Error("a short cursor packet must be rejected")
	}
}

// renders dibuja una forma "a mano" como la dibujaría Windows sobre negro y sobre blanco.
func renders(w, h int, paint func(x, y int) (c color.RGBA, alpha float64, invert bool)) (*image.RGBA, *image.RGBA) {
	onBlack, onWhite := image.NewRGBA(image.Rect(0, 0, w, h)), image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c, alpha, invert := paint(x, y)
			switch {
			case invert:
				onBlack.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
				onWhite.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
			default:
				mix := func(v uint8, bg float64) uint8 { return uint8(float64(v)*alpha + bg*(1-alpha) + 0.5) }
				onBlack.SetRGBA(x, y, color.RGBA{mix(c.R, 0), mix(c.G, 0), mix(c.B, 0), 255})
				onWhite.SetRGBA(x, y, color.RGBA{mix(c.R, 255), mix(c.G, 255), mix(c.B, 255), 255})
			}
		}
	}
	return onBlack, onWhite
}

func TestCursorFromRendersRecoversAlpha(t *testing.T) {
	// 32×32 con la forma en 10..14 × 8..12: opaco rojo, una columna al 50 %, el resto transparente.
	onBlack, onWhite := renders(32, 32, func(x, y int) (color.RGBA, float64, bool) {
		switch {
		case x >= 10 && x < 13 && y >= 8 && y < 12:
			return color.RGBA{200, 30, 30, 255}, 1, false
		case x == 13 && y >= 8 && y < 12:
			return color.RGBA{0, 0, 255, 255}, 0.5, false
		}
		return color.RGBA{}, 0, false
	})
	shape := CursorFromRenders(onBlack, onWhite, image.Pt(10, 8))
	if shape.Image.Rect != image.Rect(0, 0, 4, 4) {
		t.Fatalf("cropped to %v, want 4×4", shape.Image.Rect)
	}
	if shape.Hotspot != image.Pt(0, 0) {
		t.Fatalf("hotspot = %v, want (0,0) after cropping", shape.Hotspot)
	}
	if got := shape.Image.NRGBAAt(0, 0); got != (color.NRGBA{200, 30, 30, 255}) {
		t.Fatalf("opaque pixel = %v", got)
	}
	if got := shape.Image.NRGBAAt(3, 0); got.A < 120 || got.A > 135 || got.B < 245 {
		t.Fatalf("half-transparent blue pixel = %v", got)
	}
	if shape.ID == 0 {
		t.Fatal("a shape needs a number")
	}
}

// El cursor de texto invierte el fondo: sale negro con borde blanco, visible sobre cualquier color.
func TestCursorFromRendersInvertedPixels(t *testing.T) {
	onBlack, onWhite := renders(16, 16, func(x, y int) (color.RGBA, float64, bool) {
		return color.RGBA{}, 0, x == 8 && y >= 4 && y < 12 // un I-beam de 1×8
	})
	shape := CursorFromRenders(onBlack, onWhite, image.Pt(8, 8))
	if shape.Image.Rect != image.Rect(0, 0, 3, 10) {
		t.Fatalf("I-beam with its outline = %v, want 3×10", shape.Image.Rect)
	}
	if got := shape.Image.NRGBAAt(1, 5); got != (color.NRGBA{0, 0, 0, 255}) {
		t.Fatalf("inverted pixel = %v, want opaque black", got)
	}
	if got := shape.Image.NRGBAAt(0, 5); got != (color.NRGBA{255, 255, 255, 255}) {
		t.Fatalf("outline = %v, want opaque white", got)
	}
	if shape.Hotspot != image.Pt(1, 5) { // el borde suma una fila arriba
		t.Fatalf("hotspot = %v, want (1,5)", shape.Hotspot)
	}
}

func TestScaleCursorKeepsProportions(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 48, 48))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	scaled := ScaleCursor(CursorShape{Image: img, Hotspot: image.Pt(10, 20)}, 0.5)
	if scaled.Image.Rect != image.Rect(0, 0, 24, 24) || scaled.Hotspot != image.Pt(5, 10) {
		t.Fatalf("scaled = %v hotspot %v", scaled.Image.Rect, scaled.Hotspot)
	}
	big := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	big.SetNRGBA(127, 127, color.NRGBA{0, 0, 0, 255})
	big.SetNRGBA(0, 0, color.NRGBA{0, 0, 0, 255})
	if shape := finishShape(big, image.Point{}); shape.Image.Rect.Dx() > CursorMax {
		t.Fatalf("a 128-px cursor must be cut down to %d, got %v", CursorMax, shape.Image.Rect)
	}
}

func TestDecodeCursorImageBounds(t *testing.T) {
	var buf bytes.Buffer
	_ = png.Encode(&buf, DefaultArrow())
	if img, err := DecodeCursorImage(buf.Bytes()); err != nil || img.Rect != DefaultArrow().Rect {
		t.Fatalf("arrow: %v, %v", img, err)
	}
	buf.Reset()
	_ = png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, CursorMax+1, 4)))
	if _, err := DecodeCursorImage(buf.Bytes()); err == nil {
		t.Fatal("a cursor wider than CursorMax must be rejected")
	}
}

// La forma sale antes que los tiles, una sola vez por sesión; vuelve a salir con un pedido de
// reparación o con alguien nuevo mirando; quien mira la arma con su número.
func TestSenderSendsTheCursorShape(t *testing.T) {
	v := newViewer(t)
	var shapes []uint32
	clock := &fakeClock{now: policyT0}
	s := &Sender{ID: testSharer, Now: clock.Now, Emit: func(chunk []byte) bool {
		c, err := DecodeChunk(chunk)
		if err != nil {
			t.Fatal(err)
		}
		if c.Tile == CursorTile {
			r := v.asm.Add(c, time.Now())
			if r.Reset { // la forma es lo primero de la sesión
				v.frame = image.NewRGBA(image.Rect(0, 0, r.Grid.W, r.Grid.H))
			}
			if r.PNG != nil {
				if _, err := DecodeCursorImage(r.PNG); err != nil {
					t.Fatal(err)
				}
				shapes = append(shapes, r.Serial)
			}
			return true
		}
		v.receive(chunk)
		return true
	}}
	arrow := CursorShape{Image: DefaultArrow(), ID: 42}
	s.SetCursorShape(&arrow)
	src := &fakeSource{frames: []*image.RGBA{noise(128, 64, 13)}}
	_ = s.Step(src, []string{"juan"}, clock.now.Add(time.Hour))
	_ = s.Step(src, []string{"juan"}, clock.now.Add(time.Hour))
	if len(shapes) != 1 || shapes[0] != 42 {
		t.Fatalf("shapes sent = %v, want [42] once", shapes)
	}
	s.MarkTiles(s.Session(), []int{0})
	_ = s.Step(src, []string{"juan"}, clock.now.Add(time.Hour))
	_ = s.Step(src, []string{"juan", "pedro"}, clock.now.Add(time.Hour))
	if len(shapes) != 3 {
		t.Fatalf("shapes sent = %v, want it again after a repair and a new watcher", shapes)
	}
}

func TestCursorInFrame(t *testing.T) {
	monitor := image.Rect(1920, 0, 1920+2560, 1440) // segundo monitor a la derecha
	shape := &CursorShape{Hotspot: image.Pt(4, 2)}
	state := CursorState{Visible: true, Pos: image.Pt(1920+100, 50), Shape: shape}
	if x, y, ok := CursorInFrame(state, monitor, 1); !ok || x != 96 || y != 48 {
		t.Fatalf("1:1 = %d,%d,%v, want 96,48", x, y, ok)
	}
	if x, y, ok := CursorInFrame(state, monitor, 0.75); !ok || x != 72 || y != 37 {
		t.Fatalf("0.75 = %d,%d,%v, want 72,37 (75,38 minus the scaled hotspot 3,1)", x, y, ok)
	}
	if _, _, ok := CursorInFrame(CursorState{Visible: true, Pos: image.Pt(10, 10)}, monitor, 1); ok {
		t.Fatal("a cursor on another monitor is not on the shared screen")
	}
	if _, _, ok := CursorInFrame(CursorState{Pos: image.Pt(2000, 10)}, monitor, 1); ok {
		t.Fatal("a hidden cursor is not drawn")
	}
}
