package screen

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// Encoder codifica tiles sin pérdida (el texto queda igual que en la pantalla), lo más chico que
// puede: un tile de un solo color son 4 bytes; con hasta 256 colores (la mayoría en una interfaz:
// fondo, texto y su suavizado), PNG con paleta; si no, PNG RGB. Medido sobre capturas reales:
// ~30 % menos que todo en PNG RGB. Reusa el compresor, la tabla de colores y los buffers. No es
// seguro para goroutines concurrentes.
type Encoder struct {
	png      png.Encoder
	pool     encoderPool
	out      bytes.Buffer
	colors   colorTable
	paletted image.Paletted
}

// solidTag empieza los datos de un tile de un solo color: [solidTag R G B]. Un PNG empieza con 0x89.
const solidTag = 0x00

// encoderPool guarda un solo buffer: hay un Encoder por pantalla compartida.
type encoderPool struct{ buffer *png.EncoderBuffer }

func (p *encoderPool) Get() *png.EncoderBuffer  { return p.buffer }
func (p *encoderPool) Put(b *png.EncoderBuffer) { p.buffer = b }

func NewEncoder() *Encoder {
	e := &Encoder{}
	e.png = png.Encoder{CompressionLevel: png.DefaultCompression, BufferPool: &e.pool}
	return e
}

// Encode devuelve el tile r de frame codificado (frame opaco, como sale de la captura). El
// resultado vale hasta la próxima llamada.
func (e *Encoder) Encode(frame *image.RGBA, r image.Rectangle) ([]byte, error) {
	e.out.Reset()
	count, ok := e.colors.index(frame, r, &e.paletted)
	switch {
	case ok && count == 1:
		c := e.paletted.Palette[0].(color.RGBA)
		e.out.Write([]byte{solidTag, c.R, c.G, c.B})
		return e.out.Bytes(), nil
	case ok:
		if err := e.png.Encode(&e.out, &e.paletted); err != nil {
			return nil, err
		}
	default:
		if err := e.png.Encode(&e.out, frame.SubImage(r)); err != nil {
			return nil, err
		}
	}
	if e.out.Len() > MaxTileChunks*ChunkDataSize {
		return nil, errTileTooBig
	}
	return e.out.Bytes(), nil
}

// colorTable cuenta los colores de un tile con una tabla abierta (sin mapas ni asignaciones por
// tile) y corta al pasar de 256.
type colorTable struct {
	keys  [512]uint32 // RGB + 1 (0 = libre)
	slots [512]uint8  // índice en la paleta
	used  []int
}

// index arma en p el tile r con paleta si tiene ≤ 256 colores opacos y devuelve cuántos. ok =
// false si tiene más o algún píxel no es opaco.
func (t *colorTable) index(frame *image.RGBA, r image.Rectangle, p *image.Paletted) (int, bool) {
	for _, slot := range t.used {
		t.keys[slot] = 0
	}
	t.used = t.used[:0]
	w, h := r.Dx(), r.Dy()
	if cap(p.Pix) < w*h {
		p.Pix = make([]uint8, w*h)
	}
	p.Pix, p.Stride, p.Rect = p.Pix[:w*h], w, image.Rect(0, 0, w, h)
	p.Palette = p.Palette[:0]
	for y := range h {
		row := frame.Pix[frame.PixOffset(r.Min.X, r.Min.Y+y):][:w*4]
		out := p.Pix[y*w:][:w]
		for x := range w {
			px := row[x*4:][:4]
			if px[3] != 0xff {
				return 0, false
			}
			key := uint32(px[0])<<16 | uint32(px[1])<<8 | uint32(px[2]) + 1
			slot := int((key * 2654435761) >> 23) // hash multiplicativo → 9 bits
			for t.keys[slot] != 0 && t.keys[slot] != key {
				slot = (slot + 1) & 511
			}
			if t.keys[slot] == 0 {
				if len(p.Palette) == 256 {
					return 0, false
				}
				t.keys[slot], t.slots[slot] = key, uint8(len(p.Palette))
				t.used = append(t.used, slot)
				p.Palette = append(p.Palette, color.RGBA{px[0], px[1], px[2], 0xff})
			}
			out[x] = t.slots[slot]
		}
	}
	return len(p.Palette), true
}

var (
	errTileTooBig  = errors.New("el tile codificado no entra en MaxTileChunks")
	errTileBadSize = errors.New("el PNG del tile no tiene el tamaño del tile")
	errTileBadData = errors.New("tile inválido")
)

// DrawTile decodifica un tile (Encode) y lo copia en el rectángulo r de frame. Antes de decodificar
// un PNG mira la cabecera: el tamaño tiene que ser exactamente el del tile.
func DrawTile(frame *image.RGBA, r image.Rectangle, data []byte) error {
	if len(data) > 0 && data[0] == solidTag {
		if len(data) != 4 {
			return errTileBadData
		}
		draw.Draw(frame, r, image.NewUniform(color.RGBA{data[1], data[2], data[3], 0xff}), image.Point{}, draw.Src)
		return nil
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if config.Width != r.Dx() || config.Height != r.Dy() {
		return errTileBadSize
	}
	tile, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if paletted, ok := tile.(*image.Paletted); ok {
		drawPaletted(frame, r, paletted)
		return nil
	}
	draw.Draw(frame, r, tile, tile.Bounds().Min, draw.Src)
	return nil
}

// drawPaletted copia un tile con paleta (image/draw no tiene camino rápido de Paletted a RGBA).
func drawPaletted(frame *image.RGBA, r image.Rectangle, tile *image.Paletted) {
	var palette [256][4]byte
	for i, c := range tile.Palette {
		cr, cg, cb, ca := c.RGBA()
		palette[i] = [4]byte{byte(cr >> 8), byte(cg >> 8), byte(cb >> 8), byte(ca >> 8)}
	}
	for y := range r.Dy() {
		src := tile.Pix[tile.PixOffset(tile.Rect.Min.X, tile.Rect.Min.Y+y):][:r.Dx()]
		dst := frame.Pix[frame.PixOffset(r.Min.X, r.Min.Y+y):][:r.Dx()*4]
		for x, index := range src {
			copy(dst[x*4:x*4+4], palette[index][:])
		}
	}
}
