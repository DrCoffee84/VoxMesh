package screen

import (
	"bytes"
	"errors"
	"image"
	"math/rand/v2"
	"slices"
	"time"
)

// Source da el cuadro actual de la pantalla (Capturer en Windows; un falso en los tests).
type Source interface {
	// Capture escribe la pantalla en dst si mide lo mismo, o en uno nuevo, y lo devuelve.
	Capture(dst *image.RGBA) (*image.RGBA, error)
}

// ErrStopped: Emit dijo que hay que cortar (se dejó de compartir o se cerró la sala).
var ErrStopped = errors.New("pantalla detenida")

// Sender es el lado de quien comparte, sin red ni pantalla: en cada vuelta (Step) captura, achica
// si hace falta, compara con lo último que salió de cada tile y manda los tiles pendientes (PNG en
// chunks) hasta el deadline. Lo que no llegó a salir no se encola: queda pendiente porque sigue
// siendo distinto de lo enviado, y en la vuelta siguiente sale con los píxeles de la captura
// nueva. Así gana siempre la captura más nueva y nada crece. No es seguro para goroutines
// concurrentes.
type Sender struct {
	ID       PeerID
	MaxWidth int                     // 0 = resolución completa
	Emit     func(chunk []byte) bool // manda un chunk (payload de PacketScreenTile); false = cortar
	// EmitDigest manda un pedazo del resumen (payload de PacketScreenDigest); nil = sin resúmenes.
	EmitDigest func(digest []byte) bool
	Now        func() time.Time // nil = time.Now

	captured *image.RGBA
	frame    *image.RGBA // captured achicado (o el mismo)
	sent     *image.RGBA // lo último que salió de cada tile
	grid     Grid
	sched    *Scheduler
	changed  []bool
	policy   RefreshPolicy
	scaler   Scaler
	encoder  *Encoder
	session  uint32
	serial   uint32
	payload  []byte
	// lastSerial es el serial del último envío de cada tile (el resumen); lastDigest, cuándo salió
	// el último resumen.
	lastSerial []uint32
	lastDigest time.Time
	digestBuf  []byte
	serials    []uint16
	// cursor es la forma actual del cursor (SetCursorShape); cursorSent, la que ya salió en esta
	// sesión (0 = ninguna: va en la vuelta siguiente, antes que los tiles).
	cursor     *CursorShape
	cursorSent uint32
	cursorPNG  bytes.Buffer
	// lastChange es la última captura en la que algo cambió (NextCapture acelera mientras tanto).
	lastChange time.Time

	Stats SenderStats
}

// SenderStats son contadores para el log (nunca el contenido).
type SenderStats struct {
	Captures   int // vueltas con captura
	Behind     int // vueltas que terminaron con tiles pendientes (los pisa la captura siguiente)
	Tiles      int
	Chunks     int
	Bytes      int // payload de los chunks, sin cabecera ni cifrado
	FullImages int
	Repaired   int // tiles reenviados porque alguien los pidió (Repair)
	Digests    int
}

func (s *Sender) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Size es el tamaño del cuadro que se manda (0×0 antes de la primera captura).
func (s *Sender) Size() (int, int) { return s.grid.W, s.grid.H }

// LastChange es cuándo cambió algo en la pantalla por última vez (cero si nunca).
func (s *Sender) LastChange() time.Time { return s.lastChange }

// SetCursorShape cambia la forma del cursor: sale en la vuelta siguiente.
func (s *Sender) SetCursorShape(shape *CursorShape) { s.cursor = shape }

// Scale es cuánto se achica la captura para mandarla (1 = tamaño real): el cursor se achica igual.
func (s *Sender) Scale() float64 {
	if s.captured == nil || s.frame == nil || s.captured.Rect.Dx() == 0 {
		return 1
	}
	return float64(s.frame.Rect.Dx()) / float64(s.captured.Rect.Dx())
}

// Session es la sesión actual (0 antes de la primera captura).
func (s *Sender) Session() uint32 { return s.session }

// MarkTiles pide reenviar tiles (un Repair de quien mira): salen en la vuelta siguiente. Un pedido
// de otra sesión (otro tamaño, otro "Compartir") se ignora, igual que un tile fuera del cuadro.
func (s *Sender) MarkTiles(session uint32, tiles []int) int {
	if s.sched == nil || session != s.session {
		return 0
	}
	marked := 0
	for _, tile := range tiles {
		if tile >= 0 && tile < s.grid.Count() && s.sched.Mark(tile) {
			marked++
		}
	}
	s.Stats.Repaired += marked
	s.cursorSent = 0 // si se perdieron tiles, quizá también la forma del cursor
	return marked
}

// Step hace una vuelta. watchers son los IDs de los que miran: sin ninguno no captura ni manda
// nada. Devuelve el error de la captura (la vuelta se pierde, lo enviado queda) o ErrStopped.
func (s *Sender) Step(src Source, watchers []string, deadline time.Time) error {
	if len(watchers) == 0 {
		s.policy.Due(nil, 0, 0) // el que vuelva a mirar cuenta como nuevo
		return nil
	}
	captured, err := src.Capture(s.captured)
	if err != nil {
		return err
	}
	s.captured = captured
	s.Stats.Captures++
	s.frame = s.scaled(captured)
	w, h := s.frame.Rect.Dx(), s.frame.Rect.Dy()
	if w != s.grid.W || h != s.grid.H {
		s.grid = NewGrid(w, h)
		s.sent = image.NewRGBA(image.Rect(0, 0, w, h))
		s.sched = NewScheduler(s.grid)
		s.changed = make([]bool, s.grid.Count())
		s.lastSerial = make([]uint32, s.grid.Count())
		s.session = newSession(s.session)
		s.cursorSent = 0
	}
	if s.encoder == nil {
		s.encoder = NewEncoder()
	}
	Diff(s.frame, s.sent, s.grid, s.changed)
	s.sched.Update(s.changed)
	if slices.Contains(s.changed, true) {
		s.lastChange = s.now()
	}
	if s.policy.Due(watchers, w, h) {
		s.sched.MarkAll()
		s.Stats.FullImages++
		s.cursorSent = 0 // el que se sumó tampoco tiene la forma del cursor
	}
	if s.cursor != nil && s.cursorSent != s.cursor.ID {
		if err := s.sendCursorShape(); err != nil {
			return err
		}
	}
	for _, tile := range s.sched.Order() {
		if !s.now().Before(deadline) {
			s.Stats.Behind++
			break
		}
		if err := s.sendTile(tile); err != nil {
			return err
		}
	}
	// El resumen va detrás de los tiles de la vuelta (misma cola: llega después). Durante una
	// imagen completa no hace falta: va a salir todo igual.
	if s.EmitDigest != nil && !s.sched.Refreshing() && s.now().Sub(s.lastDigest) >= DigestInterval {
		s.lastDigest = s.now()
		return s.sendDigest()
	}
	return nil
}

// sendCursorShape manda la forma del cursor como un tile especial (CursorTile, Serial = número de
// forma), achicada como el cuadro. El número es el de la forma original: es el que va en la
// posición (PacketScreenCursor).
func (s *Sender) sendCursorShape() error {
	shape := *s.cursor
	if scale := s.Scale(); scale < 1 {
		shape = ScaleCursor(shape, scale)
	}
	s.cursorPNG.Reset()
	if err := s.encoder.png.Encode(&s.cursorPNG, shape.Image); err != nil || s.cursorPNG.Len() > MaxTileChunks*ChunkDataSize {
		s.cursorSent = s.cursor.ID // no se puede: queda la flecha del visor
		return nil
	}
	data := s.cursorPNG.Bytes()
	header := TileChunk{Sharer: s.ID, Session: s.session, Width: s.grid.W, Height: s.grid.H, Tile: CursorTile, Serial: s.cursor.ID}
	for index := range ChunkCount(len(data)) {
		s.payload = AppendChunk(s.payload[:0], header, data, index)
		if !s.Emit(s.payload) {
			return ErrStopped
		}
		s.Stats.Chunks++
		s.Stats.Bytes += len(s.payload)
	}
	s.cursorSent = s.cursor.ID
	return nil
}

// sendDigest manda el resumen en pedazos de DigestMaxTiles tiles.
func (s *Sender) sendDigest() error {
	for first := 0; first < len(s.lastSerial); first += DigestMaxTiles {
		s.serials = s.serials[:0]
		for _, serial := range s.lastSerial[first:min(first+DigestMaxTiles, len(s.lastSerial))] {
			s.serials = append(s.serials, uint16(serial))
		}
		s.digestBuf = AppendDigest(s.digestBuf[:0], Digest{Sharer: s.ID, Session: s.session, Width: s.grid.W, Height: s.grid.H, First: first, Serials: s.serials})
		if !s.EmitDigest(s.digestBuf) {
			return ErrStopped
		}
	}
	s.Stats.Digests++
	return nil
}

func (s *Sender) scaled(captured *image.RGBA) *image.RGBA {
	cw, ch := captured.Rect.Dx(), captured.Rect.Dy()
	if s.MaxWidth <= 0 || cw <= s.MaxWidth {
		return captured
	}
	w, h := FitSize(cw, ch, s.MaxWidth, ch)
	if s.frame == nil || s.frame == captured || s.frame.Rect.Dx() != w || s.frame.Rect.Dy() != h {
		s.frame = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	s.scaler.Scale(s.frame, captured)
	return s.frame
}

func (s *Sender) sendTile(tile int) error {
	r := s.grid.Rect(tile)
	data, err := s.encoder.Encode(s.frame, r)
	if err != nil {
		// No pasa con un tile de 64×64 (MaxTileChunks tiene margen); si pasara, ese tile queda
		// viejo hasta que vuelva a cambiar y el resto sigue.
		copyTile(s.sent, s.frame, r)
		s.sched.Sent(tile)
		return nil
	}
	s.serial++
	header := TileChunk{Sharer: s.ID, Session: s.session, Width: s.grid.W, Height: s.grid.H, Tile: tile, Serial: s.serial}
	for index := range ChunkCount(len(data)) {
		s.payload = AppendChunk(s.payload[:0], header, data, index)
		if !s.Emit(s.payload) {
			return ErrStopped
		}
		s.Stats.Chunks++
		s.Stats.Bytes += len(s.payload)
	}
	s.lastSerial[tile] = s.serial
	copyTile(s.sent, s.frame, r)
	s.sched.Sent(tile)
	s.Stats.Tiles++
	return nil
}

// newSession es un número de sesión al azar, distinto de 0 y del anterior.
func newSession(previous uint32) uint32 {
	for {
		if session := rand.Uint32(); session != 0 && session != previous {
			return session
		}
	}
}
