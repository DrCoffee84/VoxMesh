package screen

import (
	"image"
	"image/color"
	"math/rand/v2"
	"testing"
)

func noise(w, h int, seed uint64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(seed, 1))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = uint8(rng.Uint32()), uint8(rng.Uint32()), uint8(rng.Uint32()), 255
	}
	return img
}

func TestScaleSameSizeCopies(t *testing.T) {
	src := noise(100, 60, 1)
	dst := image.NewRGBA(src.Rect)
	var s Scaler
	s.Scale(dst, src)
	if string(dst.Pix) != string(src.Pix) {
		t.Fatal("same size must be an exact copy")
	}
}

// Al 50 % cada píxel es el promedio exacto de sus 4 (redondeado).
func TestScaleHalfIsTheExactAverage(t *testing.T) {
	src := noise(64, 32, 2)
	dst := image.NewRGBA(image.Rect(0, 0, 32, 16))
	var s Scaler
	s.Scale(dst, src)
	for y := range 16 {
		for x := range 32 {
			for c := range 4 {
				sum := 0
				for _, p := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
					sum += int(src.Pix[src.PixOffset(2*x+p[0], 2*y+p[1])+c])
				}
				if got, want := int(dst.Pix[dst.PixOffset(x, y)+c]), (sum+2)/4; got != want {
					t.Fatalf("(%d,%d)[%d] = %d, want %d", x, y, c, got, want)
				}
			}
		}
	}
}

func TestScaleKeepsASolidColor(t *testing.T) {
	src := solid(2560, 1440, color.RGBA{200, 100, 50, 255})
	dst := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	var s Scaler
	s.Scale(dst, src)
	for i := 0; i < len(dst.Pix); i += 4 {
		if dst.Pix[i] != 200 || dst.Pix[i+1] != 100 || dst.Pix[i+2] != 50 || dst.Pix[i+3] != 255 {
			t.Fatalf("pixel %d = %v", i/4, dst.Pix[i:i+4])
		}
	}
}

// Una línea de 1 px a 0,75: el área la reparte entre dos columnas sin perder ni inventar tinta
// (un filtro con lóbulos negativos la dejaría con halo).
func TestScaleAreaKeepsTheEnergyOfAThinLine(t *testing.T) {
	src := solid(400, 8, color.RGBA{0, 0, 0, 255})
	for y := range 8 {
		src.SetRGBA(201, y, color.RGBA{255, 255, 255, 255})
	}
	dst := image.NewRGBA(image.Rect(0, 0, 300, 6))
	var s Scaler
	s.Scale(dst, src)
	row := 0
	for x := range 300 {
		row += int(dst.Pix[dst.PixOffset(x, 3)])
	}
	if want := 255 * 300 / 400; row < want-2 || row > want+2 {
		t.Fatalf("ink in a row = %d, want about %d", row, want)
	}
	for x := range 300 {
		if x < 149 || x > 152 {
			if v := dst.Pix[dst.PixOffset(x, 3)]; v != 0 {
				t.Fatalf("x=%d has ink %d: the line spread too far", x, v)
			}
		}
	}
}

// Actualizar solo lo que cambió da lo mismo que achicar todo de nuevo (el visor lo hace por tile).
func TestScaleRectMatchesScale(t *testing.T) {
	var s Scaler
	for _, size := range [][4]int{{1920, 1080, 1092, 614}, {2560, 1440, 1920, 1080}, {640, 480, 640, 480}, {300, 200, 97, 61}} {
		before := noise(size[0], size[1], 10)
		after := image.NewRGBA(before.Rect)
		copy(after.Pix, before.Pix)
		changed := []image.Rectangle{image.Rect(64, 64, 128, 128), image.Rect(size[0]-30, size[1]-20, size[0], size[1]), image.Rect(0, 0, 1, 1)}
		other := noise(size[0], size[1], 11)
		for _, r := range changed {
			for y := r.Min.Y; y < r.Max.Y; y++ {
				o := after.PixOffset(r.Min.X, y)
				copy(after.Pix[o:o+r.Dx()*4], other.Pix[o:o+r.Dx()*4])
			}
		}
		incremental := image.NewRGBA(image.Rect(0, 0, size[2], size[3]))
		s.Scale(incremental, before)
		for _, r := range changed {
			s.ScaleRect(incremental, after, r)
		}
		full := image.NewRGBA(incremental.Rect)
		s.Scale(full, after)
		if string(incremental.Pix) != string(full.Pix) {
			t.Fatalf("%v: incremental downscale differs from a full one", size)
		}
	}
}

func TestFitSize(t *testing.T) {
	cases := []struct{ w, h, maxW, maxH, wantW, wantH int }{
		{1920, 1080, 1920, 1080, 1920, 1080},
		{2560, 1440, 1920, 1440, 1920, 1080},
		{3840, 2160, 1920, 2160, 1920, 1080},
		{1920, 1080, 800, 800, 800, 450},
		{1080, 1920, 800, 800, 450, 800},
		{1280, 720, 1920, 1080, 1280, 720}, // nunca agranda
		{5000, 1, 100, 100, 100, 1},
	}
	for _, c := range cases {
		if w, h := FitSize(c.w, c.h, c.maxW, c.maxH); w != c.wantW || h != c.wantH {
			t.Errorf("FitSize(%d,%d,%d,%d) = %d×%d, want %d×%d", c.w, c.h, c.maxW, c.maxH, w, h, c.wantW, c.wantH)
		}
	}
}

func BenchmarkScaleTile1080ToFit(b *testing.B) {
	src := noise(1920, 1080, 4)
	dst := image.NewRGBA(image.Rect(0, 0, 1092, 614))
	var s Scaler
	s.Scale(dst, src)
	for b.Loop() {
		s.ScaleRect(dst, src, image.Rect(640, 384, 704, 448))
	}
}

func BenchmarkScale1440To1080(b *testing.B) {
	src := noise(2560, 1440, 3)
	dst := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	var s Scaler
	for b.Loop() {
		s.Scale(dst, src)
	}
}
