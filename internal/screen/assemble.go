package screen

import "time"

// Assembler rearma los PNG de los tiles del lado del que mira (una pantalla). Por tile gana el
// serial más alto: un chunk de un serial viejo se descarta y uno nuevo reemplaza al que estaba a
// medio llegar. Si se pierde un chunk, ese tile queda como estaba hasta que vuelva a cambiar o
// hasta la próxima imagen completa. Una sesión nueva (empezó a compartir otra vez, cambió el
// tamaño) resetea todo. No es seguro para goroutines concurrentes.
type Assembler struct {
	session uint32
	retired [4]uint32 // sesiones anteriores: un chunk atrasado de ellas no resetea de nuevo
	grid    Grid
	applied []uint32 // serial del último PNG completo de cada tile
	pending map[int]*pendingTile
	cursor  *pendingTile // la forma del cursor a medio llegar (Serial = número de forma)
	png     []byte

	Incomplete int // tiles que se descartaron a medio llegar (log)
}

type pendingTile struct {
	serial  uint32
	total   int
	have    int
	chunks  [MaxTileChunks][]byte
	started time.Time
}

func NewAssembler() *Assembler { return &Assembler{pending: make(map[int]*pendingTile)} }

// Result es lo que pasó con un chunk. Si Reset, el cuadro cambió de sesión o de tamaño (Grid) y
// lo que se ve hay que borrarlo. Si PNG no es nil, el tile Tile se completó (vale hasta el próximo
// Add).
type Result struct {
	Reset  bool
	Grid   Grid
	Tile   int // CursorTile: PNG es la forma del cursor número Serial
	Serial uint32
	PNG    []byte
}

// Add incorpora un chunk ya validado por DecodeChunk.
func (a *Assembler) Add(c TileChunk, now time.Time) Result {
	var result Result
	if c.Session != a.session {
		for _, old := range a.retired {
			if old == c.Session {
				return result // atrasado de una sesión que ya pasó
			}
		}
		if a.session != 0 {
			copy(a.retired[1:], a.retired[:])
			a.retired[0] = a.session
		}
		a.session = c.Session
		a.grid = NewGrid(c.Width, c.Height)
		a.applied = make([]uint32, a.grid.Count())
		clear(a.pending)
		a.cursor = nil
		result.Reset = true
	} else if c.Width != a.grid.W || c.Height != a.grid.H {
		return result // una sesión tiene un solo tamaño
	}
	result.Grid = a.grid
	if c.Tile == CursorTile {
		return a.addCursor(c, now, result)
	}
	if c.Serial <= a.applied[c.Tile] {
		return result // ya está este o uno más nuevo
	}
	entry := a.pending[c.Tile]
	switch {
	case entry != nil && c.Serial < entry.serial:
		return result
	case entry != nil && c.Serial > entry.serial:
		a.Incomplete++ // el nuevo pisa al que no terminó de llegar
		entry = nil
	}
	if entry == nil {
		if len(a.pending) >= MaxPending {
			return result
		}
		entry = &pendingTile{serial: c.Serial, total: c.Total, started: now}
		a.pending[c.Tile] = entry
	}
	if c.Total != entry.total || entry.chunks[c.Index] != nil {
		return result
	}
	entry.chunks[c.Index] = append([]byte(nil), c.Data...)
	entry.have++
	if entry.have < entry.total {
		return result
	}
	a.png = a.png[:0]
	for _, chunk := range entry.chunks[:entry.total] {
		a.png = append(a.png, chunk...)
	}
	delete(a.pending, c.Tile)
	a.applied[c.Tile] = c.Serial
	result.Tile, result.Serial, result.PNG = c.Tile, c.Serial, a.png
	return result
}

// addCursor arma la forma del cursor: una sola a la vez (una forma nueva reemplaza a la que no
// terminó de llegar).
func (a *Assembler) addCursor(c TileChunk, now time.Time, result Result) Result {
	if a.cursor == nil || a.cursor.serial != c.Serial {
		a.cursor = &pendingTile{serial: c.Serial, total: c.Total, started: now}
	}
	entry := a.cursor
	if c.Total != entry.total || entry.chunks[c.Index] != nil {
		return result
	}
	entry.chunks[c.Index] = append([]byte(nil), c.Data...)
	if entry.have++; entry.have < entry.total {
		return result
	}
	a.png = a.png[:0]
	for _, chunk := range entry.chunks[:entry.total] {
		a.png = append(a.png, chunk...)
	}
	a.cursor = nil
	result.Tile, result.Serial, result.PNG = CursorTile, c.Serial, a.png
	return result
}

// Sweep descarta los tiles que quedaron a medio llegar más de PendingTTL y devuelve cuántos.
func (a *Assembler) Sweep(now time.Time) int {
	if a.cursor != nil && now.Sub(a.cursor.started) > PendingTTL {
		a.cursor = nil
	}
	dropped := 0
	for tile, entry := range a.pending {
		if now.Sub(entry.started) > PendingTTL {
			delete(a.pending, tile)
			dropped++
		}
	}
	a.Incomplete += dropped
	return dropped
}
