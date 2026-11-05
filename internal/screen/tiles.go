package screen

import (
	"bytes"
	"cmp"
	"image"
	"slices"
)

// Grid parte un cuadro de W×H en tiles de TileSize, en orden de filas. Los del borde derecho y de
// abajo pueden ser más chicos.
type Grid struct {
	W, H       int
	Cols, Rows int
}

func NewGrid(w, h int) Grid {
	return Grid{W: w, H: h, Cols: (w + TileSize - 1) / TileSize, Rows: (h + TileSize - 1) / TileSize}
}

func (g Grid) Count() int { return g.Cols * g.Rows }

// Rect es el rectángulo del tile i dentro del cuadro.
func (g Grid) Rect(i int) image.Rectangle {
	x, y := (i%g.Cols)*TileSize, (i/g.Cols)*TileSize
	return image.Rect(x, y, min(x+TileSize, g.W), min(y+TileSize, g.H))
}

// Diff marca en changed (len = g.Count()) qué tiles de cur son distintos de sent. Los dos cuadros
// miden lo mismo que g.
func Diff(cur, sent *image.RGBA, g Grid, changed []bool) {
	for i := range g.Count() {
		changed[i] = !sameTile(cur, sent, g.Rect(i))
	}
}

func sameTile(a, b *image.RGBA, r image.Rectangle) bool {
	width := r.Dx() * 4
	for y := r.Min.Y; y < r.Max.Y; y++ {
		ao, bo := a.PixOffset(r.Min.X, y), b.PixOffset(r.Min.X, y)
		if !bytes.Equal(a.Pix[ao:ao+width], b.Pix[bo:bo+width]) {
			return false
		}
	}
	return true
}

// copyTile copia el tile r de src a dst (mismo tamaño de cuadro).
func copyTile(dst, src *image.RGBA, r image.Rectangle) {
	width := r.Dx() * 4
	for y := r.Min.Y; y < r.Max.Y; y++ {
		do, so := dst.PixOffset(r.Min.X, y), src.PixOffset(r.Min.X, y)
		copy(dst.Pix[do:do+width], src.Pix[so:so+width])
	}
}

// Scheduler decide qué tiles salen y en qué orden. Un tile está pendiente si es distinto de lo
// último que salió de él (Update) o si le toca la imagen completa (MarkAll). Salen primero los
// distintos, del que cambió hace más tiempo al más reciente (una zona que se mueve todo el tiempo
// no deja sin turno al resto), y después los de la imagen completa en orden de filas.
type Scheduler struct {
	since   []uint64 // vuelta en que el tile empezó a ser distinto de lo enviado (0 = igual)
	refresh []bool   // le toca la imagen completa
	left    int      // tiles de la imagen completa que faltan
	round   uint64
	order   []int
}

func NewScheduler(g Grid) *Scheduler {
	return &Scheduler{since: make([]uint64, g.Count()), refresh: make([]bool, g.Count())}
}

// Update registra el diff de una captura nueva.
func (s *Scheduler) Update(changed []bool) {
	s.round++
	for i, differs := range changed {
		switch {
		case !differs:
			s.since[i] = 0 // volvió a lo que ya tienen los que miran
		case s.since[i] == 0:
			s.since[i] = s.round
		}
	}
}

// MarkAll pide la imagen completa: todos los tiles salen una vez, aunque no hayan cambiado.
func (s *Scheduler) MarkAll() {
	for i := range s.refresh {
		s.refresh[i] = true
	}
	s.left = len(s.refresh)
}

// Order devuelve los tiles pendientes en el orden en que tienen que salir. El slice se reusa:
// vale hasta la próxima llamada.
func (s *Scheduler) Order() []int {
	s.order = s.order[:0]
	for i, since := range s.since {
		if since != 0 {
			s.order = append(s.order, i)
		}
	}
	slices.SortStableFunc(s.order, func(a, b int) int { return cmp.Compare(s.since[a], s.since[b]) })
	for i, refresh := range s.refresh {
		if refresh && s.since[i] == 0 {
			s.order = append(s.order, i)
		}
	}
	return s.order
}

// Mark pide reenviar el tile i aunque no haya cambiado (lo pidió quien mira). Devuelve false si ya
// estaba pedido.
func (s *Scheduler) Mark(i int) bool {
	if s.refresh[i] {
		return false
	}
	s.refresh[i] = true
	s.left++
	return true
}

// Sent marca que el tile i salió con el contenido de la captura actual.
func (s *Scheduler) Sent(i int) {
	s.since[i] = 0
	if s.refresh[i] {
		s.refresh[i] = false
		s.left--
	}
}

// Refreshing dice si todavía falta mandar parte de la imagen completa.
func (s *Scheduler) Refreshing() bool { return s.left > 0 }

// RefreshPolicy decide cuándo se manda la imagen completa: cuando aparece alguien nuevo mirando
// (incluido el primero) y cuando cambia el tamaño del cuadro. Lo que se pierde en el camino lo
// arreglan los resúmenes (Digest → Repair), no una imagen completa cada tanto.
type RefreshPolicy struct {
	watchers map[string]bool
	w, h     int
}

// Due se llama en cada vuelta con los que miran ahora y el tamaño del cuadro (0×0 si no hubo
// captura). Devuelve true si hay que empezar una imagen completa (Scheduler.MarkAll).
func (p *RefreshPolicy) Due(watchers []string, w, h int) bool {
	due := false
	next := make(map[string]bool, len(watchers))
	for _, id := range watchers {
		next[id] = true
		if !p.watchers[id] {
			due = true // alguien empezó a mirar: no tiene nada
		}
	}
	p.watchers = next
	if len(watchers) == 0 {
		return false
	}
	if w != p.w || h != p.h {
		p.w, p.h = w, h
		due = true
	}
	return due
}
