// Package imagefile valida y decodifica las imágenes del chat sin CGO (AGENDA S5).
// Toda imagen pasa por la cabecera (image.DecodeConfig) antes de decodificarse: un archivo
// de pocos KB puede declarar dimensiones que, al decodificarlo, reservan gigas.
package imagefile

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Topes de una imagen decodificada: 40 MP en RGBA son ~160 MB.
const (
	MaxPixels = 40_000_000
	MaxSide   = 16384
)

var (
	ErrFormat   = errors.New("no es una imagen PNG, JPG, GIF o WebP")
	ErrTooLarge = fmt.Errorf("imagen demasiado grande (máximo %d MP y %d px por lado)", MaxPixels/1_000_000, MaxSide)
)

// formats son los que acepta la lista blanca de history (.png .jpg .jpeg .gif .webp).
// Se compara el nombre que devuelve DecodeConfig por si otra dependencia registra más decodificadores.
var formats = map[string]struct{}{"png": {}, "jpeg": {}, "gif": {}, "webp": {}}

// Check lee solo la cabecera y dice si la imagen se puede decodificar dentro de los topes.
func Check(data []byte) error {
	_, err := checkHeader(bytes.NewReader(data))
	return err
}

func checkHeader(r io.Reader) (image.Config, error) {
	cfg, format, err := image.DecodeConfig(r)
	if err != nil {
		return cfg, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	if _, ok := formats[format]; !ok {
		return cfg, fmt.Errorf("%w: %s", ErrFormat, format)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return cfg, fmt.Errorf("%w: %dx%d", ErrFormat, cfg.Width, cfg.Height)
	}
	if cfg.Width > MaxSide || cfg.Height > MaxSide || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return cfg, fmt.Errorf("%w: %dx%d", ErrTooLarge, cfg.Width, cfg.Height)
	}
	return cfg, nil
}

// Load decodifica la imagen de path (después de Check sobre su cabecera) y la reduce para
// que entre en maxW x maxH, conservando la proporción. Hace E/S y decodifica: nunca en el hilo de UI.
func Load(path string, maxW, maxH int) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := checkHeader(file); err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	src, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= maxW && h <= maxH {
		return src, nil
	}
	scale := min(float64(maxW)/float64(w), float64(maxH)/float64(h))
	dstW, dstH := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, bounds, draw.Src, nil)
	return dst, nil
}
