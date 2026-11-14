//go:build cgo

package ui

import (
	"testing"
	"time"
)

var lagT0 = time.Date(2026, 11, 13, 21, 34, 0, 0, time.Local)

func lagTime(seconds float64) time.Time {
	return lagT0.Add(time.Duration(seconds * float64(time.Second)))
}

// TestLagHistorySlotMergesBothSources: el Pong y el monitor de audio del mismo casillero de 2 s
// quedan en una sola muestra, con lo peor de cada uno; una medida que llega tarde va a su casillero.
func TestLagHistorySlotMergesBothSources(t *testing.T) {
	h := newLagHistory()
	h.addRTT("Pepe", lagTime(0.1), 40*time.Millisecond)
	h.addNet("Pepe", lagTime(1.2), 3.4, 6.6)
	h.addRTT("Pepe", lagTime(1.9), 90*time.Millisecond) // otro Pong en el mismo casillero: el peor
	h.addRTT("Pepe", lagTime(2.3), 50*time.Millisecond)
	h.addNet("Pepe", lagTime(1.5), 8, 2) // llegó después que el Pong de 2.3: va al casillero 0

	points := h.points("Pepe", lagTime(0), lagTime(10))
	if len(points) != 2 {
		t.Fatalf("muestras %+v, quería 2", points)
	}
	first, second := points[0], points[1]
	if !first.at.Equal(lagTime(0)) || first.rtt != 90 || first.loss != 8 || first.jitter != 7 {
		t.Fatalf("primer casillero %+v: quería 21:34:00, 90 ms, 8 %%, 7 ms", first)
	}
	if !second.at.Equal(lagTime(2)) || second.rtt != 50 || second.loss != -1 || second.jitter != -1 {
		t.Fatalf("segundo casillero %+v: quería 21:34:02, 50 ms y sin pérdida medida", second)
	}
	if h.points("Juan", lagTime(0), lagTime(10)) != nil || h.version("Juan") != 0 {
		t.Fatal("Juan no tiene medidas")
	}
	if h.version("Pepe") != 5 {
		t.Fatalf("versión %d, quería 5 (una por medida)", h.version("Pepe"))
	}
	if first, ok := h.first("Pepe"); !ok || !first.Equal(lagTime(0)) {
		t.Fatalf("first = %v, %v", first, ok)
	}
}

// TestLagHistoryCap: lleno, el ring pisa lo más viejo y mantiene el orden.
func TestLagHistoryCap(t *testing.T) {
	h := newLagHistoryMax(3)
	for i := range 5 {
		h.addRTT("Pepe", lagTime(float64(2*i)), time.Duration(10*(i+1))*time.Millisecond)
	}
	points := h.points("Pepe", lagTime(-10), lagTime(100))
	if len(points) != 3 || points[0].rtt != 30 || points[1].rtt != 40 || points[2].rtt != 50 {
		t.Fatalf("muestras %+v, quería las últimas 3 (30, 40, 50 ms)", points)
	}
	if first, _ := h.first("Pepe"); !first.Equal(lagTime(4)) {
		t.Fatalf("la más vieja es de %v, quería 21:34:04", first)
	}
	if got := newLagHistory().max; got != 43200 {
		t.Fatalf("tope de %d muestras, quería 24 h / 2 s = 43200", got)
	}
}

// TestLagHistoryPointsWindow: solo las muestras que tocan la ventana; una medida de antes de la
// primera (reloj que volvió) se descarta.
func TestLagHistoryPointsWindow(t *testing.T) {
	h := newLagHistory()
	for i := range 10 {
		h.addRTT("Pepe", lagTime(float64(2*i)), 30*time.Millisecond)
	}
	h.addRTT("Pepe", lagTime(-30), time.Second)
	points := h.points("Pepe", lagTime(5), lagTime(9))
	// 21:34:04 cubre hasta :07 (lagSampleSpan), así que toca la ventana; :06 y :08 también. La de
	// :02 termina justo a las :05 y no.
	if len(points) != 3 || !points[0].at.Equal(lagTime(4)) || !points[2].at.Equal(lagTime(8)) {
		t.Fatalf("ventana %+v", points)
	}
	if all := h.points("Pepe", lagTime(-100), lagTime(100)); len(all) != 10 {
		t.Fatalf("hay %d muestras, quería 10 (la de antes del origen no entra)", len(all))
	}
}

// TestLagColumns: Pongs cada 2 s con jitter no dejan huecos; un Pong perdido sí; cada columna se
// queda con lo peor que la toca.
func TestLagColumns(t *testing.T) {
	h := newLagHistory()
	// Cada 2 s con hasta 0,6 s de atraso, y falta el de las :20 a las :26 (corte).
	for _, s := range []float64{0, 2.6, 4.1, 6.5, 8, 10.4, 12, 14.2, 16, 18.3, 26, 28} {
		h.addRTT("Pepe", lagTime(s), 40*time.Millisecond)
		switch s {
		case 12:
			h.addRTT("Pepe", lagTime(12.5), 450*time.Millisecond) // pico en el mismo casillero
		case 14.2:
			h.addNet("Pepe", lagTime(14.1), 12, 9)
		}
	}
	points := h.points("Pepe", lagTime(0), lagTime(30))
	columns := lagColumns(points, lagTime(0), lagTime(30), 30) // una columna por segundo
	for c, column := range columns {
		hole := c >= 21 && c <= 25 // la última muestra antes del corte (:18) cubre hasta :21
		if column.empty() != hole {
			t.Errorf("columna %d (21:34:%02d) vacía=%v, quería %v", c, c, column.empty(), hole)
		}
	}
	if columns[13].rtt != 450 || !columns[13].at.Equal(lagTime(12)) {
		t.Fatalf("columna 13 = %+v: quería el pico de 450 ms del casillero de las :12", columns[13])
	}
	if columns[15].loss != 12 || columns[15].jitter != 9 {
		t.Fatalf("columna 15 = %+v: quería la pérdida del casillero de las :14", columns[15])
	}
	// Más columnas que muestras (5 min en una tira ancha) tampoco inventa huecos.
	for c, column := range lagColumns(points, lagTime(0), lagTime(20), 400) {
		if column.empty() {
			t.Fatalf("columna %d de 400 vacía antes del corte", c)
		}
	}
	if lagColumns(points, lagTime(0), lagTime(30), 0) != nil {
		t.Fatal("ancho 0: sin columnas")
	}
	for _, column := range lagColumns(points, lagTime(30), lagTime(30), 5) {
		if !column.empty() {
			t.Fatal("rango vacío: columnas vacías")
		}
	}
}

func TestSummarizeLag(t *testing.T) {
	summary := summarizeLag([]lagPoint{
		{at: lagTime(0), rtt: 40, loss: -1, jitter: -1},
		{at: lagTime(2), rtt: 100, loss: 3, jitter: 5},
		{at: lagTime(4), rtt: -1, loss: 12, jitter: 2},
		{at: lagTime(6), rtt: 61, loss: 0, jitter: 1},
	})
	if summary != (lagSummary{maxRTT: 100, avgRTT: 67, maxLoss: 12}) {
		t.Fatalf("resumen %+v, quería máx 100, promedio 67, pérdida 12", summary)
	}
	if empty := summarizeLag(nil); empty != (lagSummary{maxRTT: -1, avgRTT: -1, maxLoss: -1}) {
		t.Fatalf("sin muestras = %+v", empty)
	}
}

func TestLagScaleMS(t *testing.T) {
	for _, c := range []struct{ max, want int }{{-1, 250}, {40, 250}, {250, 250}, {251, 500}, {900, 1000}, {7000, 10000}, {20000, lagNoRTT}} {
		if got := lagScaleMS(c.max); got != c.want {
			t.Errorf("lagScaleMS(%d) = %d, quería %d", c.max, got, c.want)
		}
	}
}

// TestLagColorMatchesChip: el semáforo de la tira y el del chip son el mismo.
func TestLagColorMatchesChip(t *testing.T) {
	for _, c := range []struct {
		ms   int
		want any
	}{{0, speakingColor}, {79, speakingColor}, {80, lagYellow}, {200, lagYellow}, {201, lagOrange}, {400, lagOrange}, {401, lagRed}, {5000, lagRed}} {
		if got := lagColor(c.ms); got != c.want {
			t.Errorf("lagColor(%d) = %v, quería %v", c.ms, got, c.want)
		}
		_, chip := formatPeerLag(peerView{lag: time.Duration(c.ms)*time.Millisecond + time.Microsecond})
		if chip != lagColor(c.ms) {
			t.Errorf("chip con %d ms = %v, la tira %v", c.ms, chip, lagColor(c.ms))
		}
	}
}
