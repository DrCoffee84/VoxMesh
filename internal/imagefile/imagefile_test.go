package imagefile

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"voxmesh/internal/history"
)

// pngHeader arma solo la firma y el IHDR (RGBA, 8 bits) con CRC válido: sin IDAT.
func pngHeader(w, h uint32) []byte {
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:], w)
	binary.BigEndian.PutUint32(data[4:], h)
	data[8], data[9] = 8, 6
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	_ = binary.Write(&out, binary.BigEndian, uint32(len(data)))
	chunk := append([]byte("IHDR"), data...)
	out.Write(chunk)
	_ = binary.Write(&out, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return out.Bytes()
}

func TestCheckHugeHeaderReadsOnlyHeader(t *testing.T) {
	data := pngHeader(100000, 100000)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err := Check(data)
	runtime.ReadMemStats(&after)
	// ErrTooLarge y no un EOF: se decidió con la cabecera, sin buscar los datos.
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Check = %v, quiero ErrTooLarge", err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<20 {
		t.Fatalf("Check reservó %d bytes", allocated)
	}
}

func TestCheckLimits(t *testing.T) {
	cases := []struct {
		w, h uint32
		err  error
	}{
		{MaxSide, 100, nil},
		{MaxSide + 1, 1, ErrTooLarge},
		{1, MaxSide + 1, ErrTooLarge},
		{8000, 5000, nil},
		{8000, 5001, ErrTooLarge},
	}
	for _, c := range cases {
		if err := Check(pngHeader(c.w, c.h)); !errors.Is(err, c.err) {
			t.Errorf("%dx%d: Check = %v, quiero %v", c.w, c.h, err, c.err)
		}
	}
}

func TestCheckRejectsNonImages(t *testing.T) {
	bmp := append([]byte("BM"), make([]byte, 60)...)
	for name, data := range map[string][]byte{
		"vacío":    nil,
		"texto":    []byte("hola, esto no es una imagen"),
		"bmp":      bmp,
		"png roto": pngHeader(10, 10)[:20],
	} {
		if err := Check(data); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: Check = %v, quiero ErrFormat", name, err)
		}
	}
}

func sampleImage() image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 32))
	for y := range 32 {
		for x := range 64 {
			img.Set(x, y, color.NRGBA{R: uint8(x * 4), G: uint8(y * 8), B: 128, A: 255})
		}
	}
	return img
}

// samples devuelve una imagen chica válida por cada extensión de la lista blanca de history.
func samples(t *testing.T) map[string][]byte {
	t.Helper()
	img := sampleImage()
	var pngBuf, jpegBuf, gifBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&jpegBuf, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := gif.Encode(&gifBuf, img, nil); err != nil {
		t.Fatal(err)
	}
	webpData, err := os.ReadFile(filepath.Join("testdata", "gopher.webp"))
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{
		".png":  pngBuf.Bytes(),
		".jpg":  jpegBuf.Bytes(),
		".jpeg": jpegBuf.Bytes(),
		".gif":  gifBuf.Bytes(),
		".webp": webpData,
	}
}

func TestWhitelistMatchesDecoders(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	sampled := samples(t)
	for ext, data := range sampled {
		if err := history.CheckImage(id, ext); err != nil {
			t.Errorf("history rechaza %s: %v", ext, err)
		}
		if err := Check(data); err != nil {
			t.Errorf("%s: Check = %v", ext, err)
		}
	}
	for _, ext := range []string{".bmp", ".tif", ".ico", ".svg"} {
		if history.CheckImage(id, ext) == nil {
			t.Errorf("history acepta %s y no hay decodificador", ext)
		}
	}
}

func TestLoadScalesDown(t *testing.T) {
	dir := t.TempDir()
	for ext, data := range samples(t) {
		path := filepath.Join(dir, "img"+ext)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		img, err := Load(path, 16, 16)
		if err != nil {
			t.Fatalf("%s: Load = %v", ext, err)
		}
		if b := img.Bounds(); b.Dx() > 16 || b.Dy() > 16 || b.Dx() < 1 || b.Dy() < 1 {
			t.Errorf("%s: %dx%d no entra en 16x16", ext, b.Dx(), b.Dy())
		}
		full, err := Load(path, 4096, 4096)
		if err != nil {
			t.Fatalf("%s: Load sin reducir = %v", ext, err)
		}
		if full.Bounds().Dx() < 16 {
			t.Errorf("%s: se redujo sin hacer falta (%v)", ext, full.Bounds())
		}
	}
}

func TestLoadRejectsHugeHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bomba.png")
	if err := os.WriteFile(path, pngHeader(60000, 60000), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, 100, 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Load = %v, quiero ErrTooLarge", err)
	}
}
