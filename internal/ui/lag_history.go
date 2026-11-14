//go:build cgo

package ui

import (
	"image/color"
	"math"
	"sync"
	"time"
)

// Historial de lag por participante (AGENDA C18): lo que mide esta PC, toda la sesión en memoria
// (sin archivos; se borra al cerrar la app). Sobrevive a salir y volver a entrar a la sala, a la
// migración de host y a las reconexiones, porque va por nombre (lo que ve el usuario): con C2 quien
// vuelve desde la misma IP conserva su nombre, y "Pepe (2)" es otra persona con su propio gráfico.
//
// Cada uno guarda solo lo que mide él: el host, el RTT (Ping/Pong) de todos y la pérdida y el jitter
// del audio de cada uno; el cliente, el RTT del host y la pérdida y el jitter de lo que le llega de
// cada uno. Lo alimentan updatePeerLag (receiveLoop) y updatePeerNetStats (audioMonitorLoop), nunca
// el hilo de UI; tiene su propio mutex.

const (
	// lagSlot es el casillero de una muestra: el Pong (cada 2 s) y el monitor de audio (cada 2 s)
	// caen en el mismo. Dos medidas del mismo casillero se quedan con la peor.
	lagSlot = 2 * time.Second
	// lagSampleSpan es lo que cubre una muestra al dibujar: más que lagSlot, así el Pong que llega
	// un poco tarde no inventa un hueco. Un Pong perdido sí deja uno (eso es un hueco de verdad).
	lagSampleSpan = 3 * time.Second
	// lagHistoryMax es el tope por persona (ring): 24 h de muestras de 8 bytes ≈ 340 KB.
	lagHistoryMax = 24 * time.Hour
	// lagStripWindow es lo que muestra la tira del chip.
	lagStripWindow = 5 * time.Minute
	// lagMergeBack es cuántas muestras para atrás se busca el casillero de una medida que llega
	// tarde (receiveLoop y audioMonitorLoop no están sincronizados).
	lagMergeBack = 4
)

// Semáforo del lag (ms de ida y vuelta), el mismo en el chip (formatPeerLag), la tira y el gráfico:
// < lagGoodMS verde, ≤ lagWarnMS amarillo, ≤ lagBadMS naranja, más rojo.
const (
	lagGoodMS = 80
	lagWarnMS = 200
	lagBadMS  = 400
)

var (
	lagYellow = color.NRGBA{R: 241, G: 196, B: 15, A: 255}
	lagOrange = color.NRGBA{R: 230, G: 126, B: 34, A: 255}
	lagRed    = color.NRGBA{R: 231, G: 76, B: 60, A: 255}
)

// lagColor es el color del semáforo para ms de ida y vuelta.
func lagColor(ms int) color.Color {
	switch {
	case ms < lagGoodMS:
		return speakingColor
	case ms <= lagWarnMS:
		return lagYellow
	case ms <= lagBadMS:
		return lagOrange
	}
	return lagRed
}

// Valores de "sin medir" en una muestra guardada.
const (
	lagNoRTT  = math.MaxUint16
	lagNoByte = math.MaxUint8
)

// lagSample es una muestra guardada: 8 bytes (24 h de una persona ≈ 340 KB).
type lagSample struct {
	slot   uint32 // casillero desde el origen del historial
	rtt    uint16 // ms; lagNoRTT = sin medir
	loss   uint8  // % redondeado; lagNoByte = sin medir
	jitter uint8  // ms (tope 254); lagNoByte = sin medir
}

// lagPoint es una muestra con su hora, para dibujar y para los textos. -1 = sin medir.
type lagPoint struct {
	at     time.Time // cero = vacío (una columna sin muestras)
	rtt    int
	loss   int
	jitter int
}

func emptyLagPoint() lagPoint { return lagPoint{rtt: -1, loss: -1, jitter: -1} }

func (p lagPoint) empty() bool { return p.at.IsZero() }

// lagRing guarda las muestras de una persona en orden; lleno, pisa la más vieja.
type lagRing struct {
	buf   []lagSample
	start int // índice de la más vieja cuando está lleno
}

func (r *lagRing) len() int { return len(r.buf) }

// at es la muestra i (0 = la más vieja).
func (r *lagRing) at(i int) *lagSample { return &r.buf[(r.start+i)%len(r.buf)] }

func (r *lagRing) push(sample lagSample, max int) {
	if len(r.buf) < max {
		r.buf = append(r.buf, sample)
		return
	}
	r.buf[r.start] = sample
	r.start = (r.start + 1) % len(r.buf)
}

type lagSeries struct {
	ring    lagRing
	version uint64   // sube con cada medida: la tira se redibuja solo si cambió
	watch   lagWatch // lag prolongado (C13): ve cada casillero cuando se cierra
}

type lagHistory struct {
	mu      sync.Mutex
	origin  time.Time // casillero 0 (la primera medida de la sesión)
	started bool
	max     int // muestras por persona
	series  map[string]*lagSeries
}

func newLagHistory() *lagHistory {
	return newLagHistoryMax(int(lagHistoryMax / lagSlot))
}

// newLagHistoryMax es newLagHistory con otro tope (tests).
func newLagHistoryMax(max int) *lagHistory {
	return &lagHistory{max: max, series: make(map[string]*lagSeries)}
}

// addRTT guarda el ms de ida y vuelta medido a las at con name. Si con eso se cerró un casillero que
// empieza o termina un tramo con lag (C13), devuelve el aviso.
func (h *lagHistory) addRTT(name string, at time.Time, rtt time.Duration) (lagEvent, bool) {
	ms := min(max(int((rtt+time.Millisecond/2)/time.Millisecond), 0), lagNoRTT-1)
	return h.add(name, at, func(s *lagSample) {
		if s.rtt == lagNoRTT || uint16(ms) > s.rtt {
			s.rtt = uint16(ms)
		}
	})
}

// addNet guarda la pérdida (%) y el jitter (ms) del audio de name en el intervalo que terminó a las
// at. Devuelve el aviso de lag, como addRTT.
func (h *lagHistory) addNet(name string, at time.Time, lossPct, jitterMS float64) (lagEvent, bool) {
	loss := uint8(min(max(math.Round(lossPct), 0), 100))
	jitter := uint8(min(max(math.Round(jitterMS), 0), lagNoByte-1))
	return h.add(name, at, func(s *lagSample) {
		if s.loss == lagNoByte || loss > s.loss {
			s.loss = loss
		}
		if s.jitter == lagNoByte || jitter > s.jitter {
			s.jitter = jitter
		}
	})
}

func (h *lagHistory) add(name string, at time.Time, fill func(*lagSample)) (event lagEvent, ok bool) {
	if name == "" {
		return lagEvent{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.started {
		h.origin, h.started = at.Truncate(lagSlot), true
	}
	if at.Before(h.origin) {
		return lagEvent{}, false // antes de la primera medida (un reloj que volvió): no hay casillero
	}
	slot := uint32(at.Sub(h.origin) / lagSlot)
	series := h.series[name]
	if series == nil {
		series = &lagSeries{}
		h.series[name] = series
	}
	series.version++
	// El casillero puede ser el último o, si la medida llegó tarde, uno de los anteriores.
	for back := 0; back < min(lagMergeBack, series.ring.len()); back++ {
		sample := series.ring.at(series.ring.len() - 1 - back)
		if sample.slot == slot {
			fill(sample)
			return lagEvent{}, false
		}
		if sample.slot < slot {
			break
		}
	}
	n := series.ring.len()
	if n > 0 && series.ring.at(n-1).slot > slot {
		return lagEvent{}, false // más vieja que lo que se busca para atrás: se descarta
	}
	if n > 0 {
		// Casillero nuevo: el anterior quedó cerrado y lo ve la máquina del lag prolongado.
		closed := *series.ring.at(n - 1)
		if kind, episode := series.watch.observe(closed.point(h.slotTime(closed.slot))); kind != 0 {
			event, ok = lagEvent{kind: kind, name: name, episode: episode}, true
		}
	}
	sample := lagSample{slot: slot, rtt: lagNoRTT, loss: lagNoByte, jitter: lagNoByte}
	fill(&sample)
	series.ring.push(sample, h.max)
	return event, ok
}

// episodes copia los tramos con lag de name que tocan [from, to) (C13), para marcarlos en el gráfico.
func (h *lagHistory) episodes(name string, from, to time.Time) []lagEpisode {
	h.mu.Lock()
	defer h.mu.Unlock()
	series := h.series[name]
	if series == nil {
		return nil
	}
	var episodes []lagEpisode
	for _, episode := range series.watch.episodes {
		if episode.drawEnd().After(from) && episode.start.Before(to) {
			episodes = append(episodes, episode)
		}
	}
	return episodes
}

// drawEnd es hasta dónde se marca el tramo: su fin o, si sigue, lo que cubre su última muestra.
func (e lagEpisode) drawEnd() time.Time {
	if !e.end.IsZero() {
		return e.end
	}
	return e.last.Add(lagSampleSpan)
}

// version es el contador de medidas de name (0 = ninguna).
func (h *lagHistory) version(name string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if series := h.series[name]; series != nil {
		return series.version
	}
	return 0
}

// first es la hora de la muestra más vieja de name (ok = false si no hay ninguna).
func (h *lagHistory) first(name string) (time.Time, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	series := h.series[name]
	if series == nil || series.ring.len() == 0 {
		return time.Time{}, false
	}
	return h.slotTime(series.ring.at(0).slot), true
}

// points copia las muestras de name que tocan [from, to), en orden.
func (h *lagHistory) points(name string, from, to time.Time) []lagPoint {
	h.mu.Lock()
	defer h.mu.Unlock()
	series := h.series[name]
	if series == nil {
		return nil
	}
	var points []lagPoint
	for i := 0; i < series.ring.len(); i++ {
		sample := series.ring.at(i)
		at := h.slotTime(sample.slot)
		if !at.Add(lagSampleSpan).After(from) || !at.Before(to) {
			continue
		}
		points = append(points, sample.point(at))
	}
	return points
}

func (h *lagHistory) slotTime(slot uint32) time.Time {
	return h.origin.Add(time.Duration(slot) * lagSlot)
}

func (s lagSample) point(at time.Time) lagPoint {
	p := lagPoint{at: at, rtt: -1, loss: -1, jitter: -1}
	if s.rtt != lagNoRTT {
		p.rtt = int(s.rtt)
	}
	if s.loss != lagNoByte {
		p.loss = int(s.loss)
	}
	if s.jitter != lagNoByte {
		p.jitter = int(s.jitter)
	}
	return p
}

// lagColumns reparte points en width columnas iguales de [from, to): cada muestra cubre
// lagSampleSpan desde su hora, y cada columna se queda con lo peor de lo que la toca (el RTT, la
// pérdida y el jitter más altos; la hora es la de la muestra con más RTT, o la primera). Una columna
// que no toca ninguna muestra queda vacía: es un hueco (no se une con una línea).
func lagColumns(points []lagPoint, from, to time.Time, width int) []lagPoint {
	if width <= 0 {
		return nil
	}
	columns := make([]lagPoint, width)
	for i := range columns {
		columns[i] = emptyLagPoint()
	}
	span := to.Sub(from)
	if span <= 0 {
		return columns
	}
	column := func(at time.Time) int {
		return int(at.Sub(from) * time.Duration(width) / span)
	}
	for _, point := range points {
		end := point.at.Add(lagSampleSpan)
		if !end.After(from) || !point.at.Before(to) {
			continue
		}
		first := max(column(point.at), 0)
		last := min(column(end.Add(-time.Nanosecond)), width-1)
		for c := first; c <= last; c++ {
			columns[c].merge(point)
		}
	}
	return columns
}

func (p *lagPoint) merge(other lagPoint) {
	if p.empty() {
		*p = other
		return
	}
	if other.rtt > p.rtt {
		p.rtt, p.at = other.rtt, other.at
	}
	p.loss = max(p.loss, other.loss)
	p.jitter = max(p.jitter, other.jitter)
}

// lagSummary es el resumen de un tramo: el RTT máximo y el promedio, y la pérdida máxima (-1 = sin
// medir).
type lagSummary struct {
	maxRTT, avgRTT, maxLoss int
}

func summarizeLag(points []lagPoint) lagSummary {
	summary := lagSummary{maxRTT: -1, avgRTT: -1, maxLoss: -1}
	sum, count := 0, 0
	for _, point := range points {
		if point.rtt >= 0 {
			summary.maxRTT = max(summary.maxRTT, point.rtt)
			sum += point.rtt
			count++
		}
		summary.maxLoss = max(summary.maxLoss, point.loss)
	}
	if count > 0 {
		summary.avgRTT = (sum + count/2) / count
	}
	return summary
}

// lagScaleMS es el alto del gráfico grande en ms: el primero de la lista que entra el máximo (como
// poco 250, así un buen ping no ocupa toda la altura y se ven los umbrales de 80 y 200).
func lagScaleMS(maxRTT int) int {
	for _, scale := range []int{250, 500, 1000, 2000, 5000, 10000} {
		if maxRTT <= scale {
			return scale
		}
	}
	return lagNoRTT
}
