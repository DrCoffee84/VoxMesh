//go:build cgo

package ui

import (
	"image"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"

	"voxmesh/internal/i18n"
	"voxmesh/internal/logging"
	"voxmesh/internal/screen"
	"voxmesh/internal/transport"
)

// watchSession es la pantalla que estoy mirando. receiveLoop le pasa los chunks por chunks sin
// esperar; la goroutine runScreenViewer arma el cuadro y lo pinta en la ventana del visor.
type watchSession struct {
	target   string // ID de quien comparte
	name     string
	peer     screen.PeerID
	chunks   chan screenPacket
	dropped  atomic.Uint64 // chunks descartados con el canal lleno
	stop     chan struct{}
	stopOnce sync.Once
}

// watchQueue es cuántos chunks pueden esperar al visor (~2,4 MB): a ritmo de LAN una imagen
// completa (~600 chunks) llega en ~0,3 s.
const watchQueue = 2048

// viewerPoll es cada cuánto se revisa si toca pintar mientras hay cambios sin pintar
// (screen.PaintDue decide).
const viewerPoll = 20 * time.Millisecond

func newWatchSession(target, name string, peer screen.PeerID) *watchSession {
	return &watchSession{target: target, name: name, peer: peer, chunks: make(chan screenPacket, watchQueue), stop: make(chan struct{})}
}

func (w *watchSession) close() { w.stopOnce.Do(func() { close(w.stop) }) }

// viewerBuffers son las dos imágenes que se turnan en la ventana: la goroutine escribe solo en la
// que no se ve y solo cuando el fyne.Do anterior ya corrió, así Fyne nunca lee una imagen mientras
// se escribe.
type viewerBuffers struct {
	images  [2]*image.RGBA
	shown   atomic.Int32 // la que tiene la ventana (-1 = ninguna)
	pending atomic.Bool  // hay un fyne.Do encolado que todavía no corrió
}

// viewer es el estado de la goroutine del visor (solo ella lo toca).
type viewer struct {
	app   *App
	watch *watchSession
	view  *screenView

	assembler *screen.Assembler
	frame     *image.RGBA // la pantalla a tamaño real
	// fit es el cuadro achicado al área (modo ajustar). Se actualiza solo en los tiles que llegaron
	// (Scaler.ScaleRect); fitFull pide rehacerlo entero (tamaño nuevo, sesión nueva, modo).
	fit      *image.RGBA
	fitDirty []image.Rectangle
	fitFull  bool
	scaler   screen.Scaler
	buffers  viewerBuffers

	dirtySince, lastChunk, lastPaint time.Time

	// Para el log: bytes, tiles, y cuándo quedó completa la primera imagen de cada sesión.
	start, sessionAt time.Time
	seen             []bool
	seenCount        int
	bytes, tiles     int
	bad              int
	repairs          int // tiles pedidos de nuevo (el resumen los delató)
	repairBuf        []byte

	// Cursor de quien comparte: la última posición, las formas que llegaron (por número) y el
	// tamaño de lo último que se pintó (la capa del cursor va escalada igual).
	cursor             screen.Cursor
	shapes             map[uint32]*image.NRGBA
	arrow              *image.NRGBA
	cursorDirty        bool
	lastCursor         time.Time
	displayW, displayH int
}

// cursorShapesMax acota las formas guardadas (flecha, texto, mano, esperas…).
const cursorShapesMax = 16

// cursorPacket guarda la posición nueva del cursor.
func (v *viewer) cursorPacket(payload []byte, now time.Time) {
	c, err := screen.DecodeCursor(payload)
	if err != nil || c.Sharer != v.watch.peer {
		v.bad++
		return
	}
	if c != v.cursor {
		v.cursor, v.cursorDirty = c, true
		v.pushCursor(now)
	}
}

// cursorShape guarda una forma que llegó entera (CursorTile).
func (v *viewer) cursorShape(serial uint32, data []byte, now time.Time) {
	img, err := screen.DecodeCursorImage(data)
	if err != nil {
		v.bad++
		return
	}
	if v.shapes == nil || len(v.shapes) >= cursorShapesMax {
		v.shapes = make(map[uint32]*image.NRGBA)
	}
	v.shapes[serial] = img
	if serial == v.cursor.Shape {
		v.cursorDirty = true
		v.pushCursor(now)
	}
}

// pushCursor mueve la capa del cursor en la ventana, como mucho cada screen.CursorRate: solo mueve un
// objeto chico, no vuelve a subir la pantalla. Antes de la primera pintada no hay dónde.
func (v *viewer) pushCursor(now time.Time) {
	if !v.cursorDirty || v.displayW == 0 || v.frame == nil || now.Sub(v.lastCursor) < screen.CursorRate {
		return
	}
	v.cursorDirty, v.lastCursor = false, now
	img := v.shapes[v.cursor.Shape]
	if img == nil {
		if v.arrow == nil {
			v.arrow = screen.DefaultArrow()
		}
		img = v.arrow
	}
	factor := float64(v.displayW) / float64(v.frame.Rect.Dx())
	x, y := int(float64(v.cursor.X)*factor), int(float64(v.cursor.Y)*factor)
	w, h := max(1, int(float64(img.Rect.Dx())*factor+0.5)), max(1, int(float64(img.Rect.Dy())*factor+0.5))
	visible := v.cursor.Visible
	a, watch, view := v.app, v.watch, v.view
	fyne.Do(func() {
		if a.screenWatch.Load() == watch {
			view.placeCursor(img, x, y, w, h, visible)
		}
	})
}

// digest compara un resumen de quien comparte con lo que tengo y pide los tiles viejos.
func (v *viewer) digest(payload []byte) {
	d, err := screen.DecodeDigest(payload)
	if err != nil || d.Sharer != v.watch.peer {
		v.bad++
		return
	}
	stale := v.assembler.Stale(d)
	if len(stale) == 0 {
		return
	}
	v.repairs += len(stale)
	v.repairBuf = screen.AppendRepair(v.repairBuf[:0], screen.Repair{Sharer: d.Sharer, Session: d.Session, Tiles: stale})
	v.app.sendScreenRepair(v.watch, v.repairBuf)
}

// fitDirtyMax: con más zonas sucias que esto se achica el cuadro entero (sale igual o más barato).
const fitDirtyMax = 256

// runScreenViewer arma la pantalla que miro: chunks → tiles (screen.Assembler) → cuadro, y la pinta
// cuando termina una tanda (screen.PaintDue). Nunca en el hilo de UI: achicar al tamaño de la
// ventana también se hace acá, y solo en lo que cambió.
func (a *App) runScreenViewer(watch *watchSession, view *screenView) {
	now := time.Now()
	v := &viewer{app: a, watch: watch, view: view, assembler: screen.NewAssembler(), start: now}
	v.buffers.shown.Store(-1)
	poll := time.NewTicker(viewerPoll)
	defer poll.Stop()
	sweep := time.NewTicker(time.Second)
	defer sweep.Stop()
	logging.Infof("Pantalla de %s: mirando", watch.name)
	defer func() {
		logging.Infof("Pantalla de %s: dejó de mirar (%s, recibido %s, tiles=%d incompletos=%d pedidos de nuevo=%d inválidos=%d descartados=%d)",
			watch.name, time.Since(v.start).Round(time.Second), formatMB(uint64(v.bytes)), v.tiles, v.assembler.Incomplete, v.repairs, v.bad, watch.dropped.Load())
	}()
	for {
		var tick <-chan time.Time
		if !v.dirtySince.IsZero() || v.cursorDirty {
			tick = poll.C // solo mientras hay algo sin pintar
		}
		select {
		case <-watch.stop:
			return
		case packet := <-watch.chunks:
			switch packet.kind {
			case transport.PacketScreenTile:
				v.chunk(packet.payload, time.Now())
			case transport.PacketScreenDigest:
				v.digest(packet.payload)
			case transport.PacketScreenCursor:
				v.cursorPacket(packet.payload, time.Now())
			}
		case <-view.resize:
			if v.frame != nil {
				v.fitFull = true
				v.markDirty(time.Now())
			}
		case now := <-tick:
			if screen.PaintDue(v.dirtySince, v.lastChunk, v.lastPaint, now) && v.paint() {
				v.lastPaint, v.dirtySince = now, time.Time{}
				v.cursorDirty = true // el tamaño de la imagen pudo cambiar
			}
			v.pushCursor(now)
		case now := <-sweep.C:
			v.assembler.Sweep(now)
		}
	}
}

func (v *viewer) markDirty(now time.Time) {
	if v.dirtySince.IsZero() {
		v.dirtySince = now
	}
}

func (v *viewer) chunk(payload []byte, now time.Time) {
	v.bytes += len(payload)
	v.lastChunk = now
	chunk, err := screen.DecodeChunk(payload)
	if err != nil || chunk.Sharer != v.watch.peer {
		v.bad++
		return
	}
	result := v.assembler.Add(chunk, now)
	if result.Reset {
		v.frame = image.NewRGBA(image.Rect(0, 0, result.Grid.W, result.Grid.H))
		v.fitFull = true
		v.sessionAt = now
		v.seen, v.seenCount = make([]bool, result.Grid.Count()), 0
		v.markDirty(now)
	}
	if result.PNG == nil {
		return
	}
	if result.Tile == screen.CursorTile {
		v.cursorShape(result.Serial, result.PNG, now)
		return
	}
	r := result.Grid.Rect(result.Tile)
	if err := screen.DrawTile(v.frame, r, result.PNG); err != nil {
		v.bad++
		return
	}
	v.tiles++
	if !v.fitFull {
		if v.fitDirty = append(v.fitDirty, r); len(v.fitDirty) > fitDirtyMax {
			v.fitFull, v.fitDirty = true, v.fitDirty[:0]
		}
	}
	v.markDirty(now)
	if !v.seen[result.Tile] {
		v.seen[result.Tile] = true
		if v.seenCount++; v.seenCount == len(v.seen) {
			from := v.sessionAt
			if from.Sub(v.start) < 5*time.Second {
				from = v.start // la primera sesión cuenta desde que tocó "Ver pantalla"
			}
			logging.Infof("Pantalla de %s: primera imagen completa en %d ms (%dx%d)", v.watch.name, now.Sub(from).Milliseconds(), result.Grid.W, result.Grid.H)
		}
	}
}

// paint pasa el cuadro a la ventana: en "ajustar" achicado (filtro de área) al tamaño del área en
// píxeles, sin agrandar; en 1:1 tal cual. Devuelve false si todavía no se puede (la pintada
// anterior no llegó a la ventana o el área no tiene tamaño): queda para la próxima.
func (v *viewer) paint() bool {
	if v.frame == nil || v.buffers.pending.Load() {
		return false
	}
	fw, fh := v.frame.Rect.Dx(), v.frame.Rect.Dy()
	source := v.frame
	if !v.view.actual.Load() {
		areaW, areaH := int(v.view.areaW.Load()), int(v.view.areaH.Load())
		if areaW <= 0 || areaH <= 0 {
			return false
		}
		w, h := screen.FitSize(fw, fh, areaW, areaH)
		if v.fit == nil || v.fit.Rect.Dx() != w || v.fit.Rect.Dy() != h {
			v.fit, v.fitFull = image.NewRGBA(image.Rect(0, 0, w, h)), true
		}
		if v.fitFull {
			v.scaler.Scale(v.fit, v.frame)
		} else {
			for _, r := range v.fitDirty {
				v.scaler.ScaleRect(v.fit, v.frame, r)
			}
		}
		v.fitFull, v.fitDirty = false, v.fitDirty[:0]
		source = v.fit
	} else {
		v.fitFull, v.fitDirty = true, v.fitDirty[:0] // al volver a ajustar se rehace entero
	}
	next := 0
	if v.buffers.shown.Load() == 0 {
		next = 1
	}
	target := v.buffers.images[next]
	if target == nil || target.Rect != source.Rect {
		target = image.NewRGBA(source.Rect)
		v.buffers.images[next] = target
	}
	copy(target.Pix, source.Pix)
	v.displayW, v.displayH = target.Rect.Dx(), target.Rect.Dy()
	v.buffers.pending.Store(true)
	status := i18n.T("screen.size", fw, fh)
	a, watch, view, buffers := v.app, v.watch, v.view, &v.buffers
	fyne.Do(func() {
		defer buffers.pending.Store(false)
		if a.screenWatch.Load() != watch {
			return // ya no miro esta pantalla
		}
		view.show(target, status)
		buffers.shown.Store(int32(next))
	})
	return true
}
