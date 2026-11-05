package screen

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"time"
)

// Cursor aparte: en vez de dibujarlo en los tiles (cada movimiento mandaba 2–4 tiles y llegaba con
// la latencia de todo lo demás), quien comparte manda la posición hasta CursorRate veces por
// segundo en un paquete chico sin cola, y la forma una sola vez (como un tile especial, CursorTile,
// con Serial = número de forma). El visor la dibuja encima.
const (
	CursorTile = 0xFFFF // tile especial: la forma del cursor (PNG RGBA)
	CursorMax  = 64     // lado máximo de la forma (una más grande se achica)
	// CursorRate es cada cuánto se mira el cursor; CursorResend, cada cuánto se repite la posición
	// aunque no se mueva (si se pierde un paquete).
	CursorRate   = 50 * time.Millisecond
	CursorResend = time.Second
	cursorSize   = 21
)

// Cursor es la posición del cursor de quien comparte, en píxeles del cuadro que se manda: X, Y es
// la esquina superior izquierda del dibujo (el hotspot ya restado).
//
//	[0:8] ID  [8:12] sesión  [12] visible  [13:17] forma u32  [17:19] x i16  [19:21] y i16
type Cursor struct {
	Sharer  PeerID
	Session uint32
	Visible bool
	Shape   uint32
	X, Y    int
}

var errBadCursor = errors.New("cursor de pantalla inválido")

func AppendCursor(dst []byte, c Cursor) []byte {
	var b [cursorSize]byte
	copy(b[0:8], c.Sharer[:])
	binary.BigEndian.PutUint32(b[8:12], c.Session)
	if c.Visible {
		b[12] = 1
	}
	binary.BigEndian.PutUint32(b[13:17], c.Shape)
	binary.BigEndian.PutUint16(b[17:19], uint16(int16(max(min(c.X, 32767), -32768))))
	binary.BigEndian.PutUint16(b[19:21], uint16(int16(max(min(c.Y, 32767), -32768))))
	return append(dst, b[:]...)
}

func DecodeCursor(payload []byte) (Cursor, error) {
	if len(payload) != cursorSize || payload[12] > 1 {
		return Cursor{}, errBadCursor
	}
	var c Cursor
	copy(c.Sharer[:], payload[0:8])
	c.Session = binary.BigEndian.Uint32(payload[8:12])
	c.Visible = payload[12] == 1
	c.Shape = binary.BigEndian.Uint32(payload[13:17])
	c.X = int(int16(binary.BigEndian.Uint16(payload[17:19])))
	c.Y = int(int16(binary.BigEndian.Uint16(payload[19:21])))
	return c, nil
}

// CursorShape es la forma de un cursor: la imagen (con alfa) y dónde está la punta.
type CursorShape struct {
	ID      uint32 // hash de los píxeles: el mismo cursor da el mismo número
	Image   *image.NRGBA
	Hotspot image.Point
}

// CursorState es lo que ve CursorTracker: si el sistema muestra el cursor, dónde está la punta
// (escritorio virtual, píxeles físicos) y su forma (nil si no se pudo dibujar).
type CursorState struct {
	Visible bool
	Pos     image.Point
	Shape   *CursorShape
}

// CursorInFrame pasa la punta del cursor a la esquina del dibujo en el cuadro que se manda (el
// monitor compartido, achicado por scale como la forma: ScaleCursor). visible es false si está
// oculto o en otro monitor.
func CursorInFrame(state CursorState, monitor image.Rectangle, scale float64) (x, y int, visible bool) {
	if !state.Visible || !state.Pos.In(monitor) {
		return 0, 0, false
	}
	var hotspot image.Point
	if state.Shape != nil {
		hotspot = state.Shape.Hotspot
	}
	x = int(float64(state.Pos.X-monitor.Min.X)*scale+0.5) - int(float64(hotspot.X)*scale)
	y = int(float64(state.Pos.Y-monitor.Min.Y)*scale+0.5) - int(float64(hotspot.Y)*scale)
	return x, y, true
}

// CursorFromRenders arma la forma a partir del cursor dibujado sobre negro y sobre blanco (Windows
// no da el alfa de los cursores viejos). Sobre negro queda color·α y sobre blanco color·α +
// 255·(1−α): α = 1 − (blanco − negro)/255. Los píxeles que invierten el fondo (el cursor de texto
// es XOR: blanco sobre negro y negro sobre blanco) no tienen color fijo: salen negros con un borde
// blanco, que se ve sobre cualquier fondo. Recorta lo transparente y corre el hotspot.
func CursorFromRenders(onBlack, onWhite *image.RGBA, hotspot image.Point) CursorShape {
	r := onBlack.Rect
	out := image.NewNRGBA(r)
	invert := make([]bool, r.Dx()*r.Dy())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			b, w := onBlack.RGBAAt(x, y), onWhite.RGBAAt(x, y)
			i := out.PixOffset(x, y)
			if b == w {
				out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = b.R, b.G, b.B, 255
				continue
			}
			db, dg, dr := int(w.B)-int(b.B), int(w.G)-int(b.G), int(w.R)-int(b.R)
			if dr < -128 && dg < -128 && db < -128 { // claro sobre negro, oscuro sobre blanco
				invert[(y-r.Min.Y)*r.Dx()+x-r.Min.X] = true
				out.Pix[i+3] = 255 // negro opaco
				continue
			}
			alpha := 255 - (max(dr, 0)+max(dg, 0)+max(db, 0))/3
			if alpha <= 0 {
				continue // transparente
			}
			unpremultiply := func(c uint8) uint8 { return uint8(min(255, int(c)*255/alpha)) }
			out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = unpremultiply(b.R), unpremultiply(b.G), unpremultiply(b.B), uint8(alpha)
		}
	}
	// Borde blanco alrededor de lo invertido, donde era transparente.
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if !invert[(y-r.Min.Y)*r.Dx()+x-r.Min.X] {
				continue
			}
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					p := image.Pt(x+dx, y+dy)
					if p.In(r) && out.NRGBAAt(p.X, p.Y).A == 0 {
						out.SetNRGBA(p.X, p.Y, color.NRGBA{255, 255, 255, 255})
					}
				}
			}
		}
	}
	return finishShape(out, hotspot)
}

// finishShape recorta lo transparente, achica a CursorMax si hace falta y calcula el número.
func finishShape(img *image.NRGBA, hotspot image.Point) CursorShape {
	bounds := image.Rectangle{}
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			if img.NRGBAAt(x, y).A != 0 {
				bounds = bounds.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	if bounds.Empty() {
		bounds = image.Rect(img.Rect.Min.X, img.Rect.Min.Y, img.Rect.Min.X+1, img.Rect.Min.Y+1)
	}
	cropped := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := range bounds.Dy() {
		copy(cropped.Pix[y*cropped.Stride:][:bounds.Dx()*4], img.Pix[img.PixOffset(bounds.Min.X, bounds.Min.Y+y):][:bounds.Dx()*4])
	}
	shape := CursorShape{Image: cropped, Hotspot: hotspot.Sub(bounds.Min)}
	if bounds.Dx() > CursorMax || bounds.Dy() > CursorMax {
		shape = ScaleCursor(shape, float64(CursorMax)/float64(max(bounds.Dx(), bounds.Dy())))
	}
	shape.ID = shapeID(shape)
	return shape
}

// ScaleCursor achica la forma por factor (< 1), como se achica el cuadro, con el filtro de área.
func ScaleCursor(shape CursorShape, factor float64) CursorShape {
	if factor >= 1 {
		return shape
	}
	w := max(1, int(float64(shape.Image.Rect.Dx())*factor+0.5))
	h := max(1, int(float64(shape.Image.Rect.Dy())*factor+0.5))
	// El Scaler trabaja con RGBA premultiplicado: se pasa, se achica y se vuelve.
	src := image.NewRGBA(shape.Image.Rect)
	for i := 0; i < len(src.Pix); i += 4 {
		a := uint32(shape.Image.Pix[i+3])
		src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = uint8(uint32(shape.Image.Pix[i])*a/255), uint8(uint32(shape.Image.Pix[i+1])*a/255), uint8(uint32(shape.Image.Pix[i+2])*a/255), uint8(a)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	var scaler Scaler
	scaler.Scale(dst, src)
	out := image.NewNRGBA(dst.Rect)
	for i := 0; i < len(dst.Pix); i += 4 {
		a := uint32(dst.Pix[i+3])
		if a == 0 {
			continue
		}
		out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = uint8(min(255, uint32(dst.Pix[i])*255/a)), uint8(min(255, uint32(dst.Pix[i+1])*255/a)), uint8(min(255, uint32(dst.Pix[i+2])*255/a)), uint8(a)
	}
	scaled := CursorShape{Image: out, Hotspot: image.Pt(int(float64(shape.Hotspot.X)*factor), int(float64(shape.Hotspot.Y)*factor))}
	scaled.ID = shapeID(scaled)
	return scaled
}

func shapeID(shape CursorShape) uint32 {
	h := fnv.New32a()
	_, _ = h.Write(shape.Image.Pix)
	var size [8]byte
	binary.BigEndian.PutUint16(size[0:2], uint16(shape.Image.Rect.Dx()))
	binary.BigEndian.PutUint16(size[2:4], uint16(shape.Image.Rect.Dy()))
	binary.BigEndian.PutUint16(size[4:6], uint16(shape.Hotspot.X))
	binary.BigEndian.PutUint16(size[6:8], uint16(shape.Hotspot.Y))
	_, _ = h.Write(size[:])
	if id := h.Sum32(); id != 0 {
		return id
	}
	return 1
}

// DecodeCursorImage decodifica la forma que llegó como CursorTile (PNG con alfa), mirando antes la
// cabecera: no más de CursorMax por lado.
func DecodeCursorImage(data []byte) (*image.NRGBA, error) {
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > CursorMax || config.Height > CursorMax {
		return nil, errBadCursor
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	out := image.NewNRGBA(image.Rect(0, 0, config.Width, config.Height))
	draw.Draw(out, out.Rect, decoded, decoded.Bounds().Min, draw.Src)
	return out, nil
}

// DefaultArrow es la flecha que dibuja el visor mientras no tiene la forma de verdad.
func DefaultArrow() *image.NRGBA {
	rows := []string{
		"X           ",
		"XX          ",
		"X.X         ",
		"X..X        ",
		"X...X       ",
		"X....X      ",
		"X.....X     ",
		"X......X    ",
		"X.......X   ",
		"X........X  ",
		"X.....XXXXX ",
		"X..X..X     ",
		"X.X X..X    ",
		"XX  X..X    ",
		"X    X..X   ",
		"     X..X   ",
		"      XX    ",
	}
	img := image.NewNRGBA(image.Rect(0, 0, len(rows[0]), len(rows)))
	for y, row := range rows {
		for x, c := range row {
			switch c {
			case 'X':
				img.SetNRGBA(x, y, color.NRGBA{0, 0, 0, 255})
			case '.':
				img.SetNRGBA(x, y, color.NRGBA{255, 255, 255, 255})
			}
		}
	}
	return img
}
