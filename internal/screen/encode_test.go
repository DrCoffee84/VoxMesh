package screen

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

// Sin pérdida: el tile vuelve idéntico, píxel por píxel (el texto queda igual de nítido).
func TestEncodeDrawTileIsLossless(t *testing.T) {
	frame := noise(200, 100, 4)
	g := NewGrid(200, 100)
	enc := NewEncoder()
	out := image.NewRGBA(frame.Rect)
	for i := range g.Count() {
		data, err := enc.Encode(frame, g.Rect(i))
		if err != nil {
			t.Fatal(err)
		}
		if err := DrawTile(out, g.Rect(i), data); err != nil {
			t.Fatal(err)
		}
	}
	if string(out.Pix) != string(frame.Pix) {
		t.Fatal("the frame did not come back identical")
	}
}

// El peor caso (ruido: no comprime) entra en MaxTileChunks.
func TestEncodeNoiseFitsMaxTileChunks(t *testing.T) {
	frame := noise(TileSize, TileSize, 5)
	data, err := NewEncoder().Encode(frame, frame.Rect)
	if err != nil {
		t.Fatal(err)
	}
	if n := ChunkCount(len(data)); n > MaxTileChunks {
		t.Fatalf("a noise tile takes %d chunks (%d bytes), cap %d", n, len(data), MaxTileChunks)
	}
}

func TestEncodeManyColorsIsRGBWithoutAlpha(t *testing.T) {
	frame := noise(TileSize, TileSize, 6)
	data, err := NewEncoder().Encode(frame, frame.Rect)
	if err != nil {
		t.Fatal(err)
	}
	// IHDR: color type en el byte 25 del archivo (2 = RGB, 3 = paleta, 6 = RGBA).
	if data[25] != 2 {
		t.Fatalf("PNG color type = %d, want 2 (RGB)", data[25])
	}
}

// Un tile liso son 4 bytes y vuelve igual.
func TestEncodeSolidTile(t *testing.T) {
	frame := solid(200, 100, color.RGBA{10, 20, 30, 255})
	r := image.Rect(192, 64, 200, 100) // tile parcial del borde
	data, err := NewEncoder().Encode(frame, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 4 || data[0] != solidTag {
		t.Fatalf("solid tile encoded as %d bytes: % x", len(data), data[:min(8, len(data))])
	}
	out := solid(200, 100, color.RGBA{0, 0, 0, 255})
	if err := DrawTile(out, r, data); err != nil {
		t.Fatal(err)
	}
	if !sameTile(out, frame, r) {
		t.Fatal("solid tile did not come back identical")
	}
	if err := DrawTile(out, r, []byte{solidTag, 1, 2}); err == nil {
		t.Fatal("a short solid tile must be rejected")
	}
}

// Texto: fondo, letra y su suavizado (pocos colores) → PNG con paleta, más chico que RGB e idéntico.
func TestEncodeFewColorsUsesAPalette(t *testing.T) {
	frame := solid(TileSize, TileSize, color.RGBA{30, 30, 30, 255})
	shades := []color.RGBA{{230, 230, 230, 255}, {150, 150, 160, 255}, {90, 90, 100, 255}, {200, 60, 60, 255}}
	for y := 8; y < 56; y++ {
		for x := 4; x < 60; x++ {
			if (x*7+y*3)%5 < 2 {
				frame.SetRGBA(x, y, shades[(x+y)%len(shades)])
			}
		}
	}
	enc := NewEncoder()
	data, err := enc.Encode(frame, frame.Rect)
	if err != nil {
		t.Fatal(err)
	}
	if data[25] != 3 {
		t.Fatalf("PNG color type = %d, want 3 (palette)", data[25])
	}
	var rgb bytes.Buffer
	if err := enc.png.Encode(&rgb, frame); err != nil {
		t.Fatal(err)
	}
	if len(data) >= rgb.Len() {
		t.Fatalf("palette PNG %d bytes, RGB %d: no saving", len(data), rgb.Len())
	}
	out := image.NewRGBA(frame.Rect)
	if err := DrawTile(out, frame.Rect, data); err != nil {
		t.Fatal(err)
	}
	if string(out.Pix) != string(frame.Pix) {
		t.Fatal("palette tile did not come back identical")
	}
}

// 256 colores todavía entran en la paleta; 257 ya no.
func TestEncodePaletteLimit(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, TileSize, TileSize))
	for i := range TileSize * TileSize {
		v := i % 257
		frame.Pix[i*4], frame.Pix[i*4+1], frame.Pix[i*4+2], frame.Pix[i*4+3] = uint8(v), uint8(v>>8), 7, 255
	}
	enc := NewEncoder()
	if _, ok := enc.colors.index(frame, frame.Rect, &enc.paletted); ok {
		t.Fatal("257 colors must not use a palette")
	}
	for i := range TileSize * TileSize {
		frame.Pix[i*4], frame.Pix[i*4+1] = uint8(i%256), 0
	}
	if n, ok := enc.colors.index(frame, frame.Rect, &enc.paletted); !ok || n != 256 {
		t.Fatalf("256 colors: n=%d ok=%v", n, ok)
	}
	data, err := enc.Encode(frame, frame.Rect)
	if err != nil {
		t.Fatal(err)
	}
	out := image.NewRGBA(frame.Rect)
	if err := DrawTile(out, frame.Rect, data); err != nil || string(out.Pix) != string(frame.Pix) {
		t.Fatalf("256-color tile did not come back identical (err %v)", err)
	}
}

func BenchmarkEncodeTextTile(b *testing.B) {
	frame := solid(TileSize, TileSize, color.RGBA{30, 30, 30, 255})
	for y := 8; y < 56; y++ {
		for x := 4; x < 60; x++ {
			if (x*7+y*3)%5 < 2 {
				frame.SetRGBA(x, y, color.RGBA{uint8(200 + x%3), 200, 200, 255})
			}
		}
	}
	enc := NewEncoder()
	for b.Loop() {
		if _, err := enc.Encode(frame, frame.Rect); err != nil {
			b.Fatal(err)
		}
	}
}

func TestDrawTileRejectsAnotherSize(t *testing.T) {
	frame := noise(TileSize, TileSize, 7) // PNG (un tile liso no lleva tamaño: llena el que le toca)
	data, err := NewEncoder().Encode(frame, frame.Rect)
	if err != nil {
		t.Fatal(err)
	}
	out := image.NewRGBA(image.Rect(0, 0, 128, 128))
	if err := DrawTile(out, image.Rect(0, 0, 32, 32), data); err == nil {
		t.Fatal("a PNG of another size must be rejected before decoding")
	}
	if err := DrawTile(out, image.Rect(0, 0, 64, 64), []byte("no es un png")); err == nil {
		t.Fatal("garbage must be rejected")
	}
}
