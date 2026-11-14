//go:build cgo

package ui

import (
	"testing"
	"time"
)

// lagStep es una muestra cerrada para lagWatch: segundos desde lagT0, ms y pérdida (-1 = sin medir).
type lagStep struct {
	s         float64
	rtt, loss int
}

type lagSeen struct {
	at      float64 // segundo de la muestra que lo disparó
	kind    lagEventKind
	episode lagEpisode
}

func runLagWatch(w *lagWatch, steps []lagStep) []lagSeen {
	var seen []lagSeen
	for _, step := range steps {
		if kind, episode := w.observe(lagPoint{at: lagTime(step.s), rtt: step.rtt, loss: step.loss, jitter: -1}); kind != 0 {
			seen = append(seen, lagSeen{step.s, kind, episode})
		}
	}
	return seen
}

// every arma muestras cada 2 s de from a to (inclusive) con el mismo valor.
func every(from, to float64, rtt, loss int) []lagStep {
	var steps []lagStep
	for s := from; s <= to; s += 2 {
		steps = append(steps, lagStep{s, rtt, loss})
	}
	return steps
}

func concat(parts ...[]lagStep) []lagStep {
	var all []lagStep
	for _, part := range parts {
		all = append(all, part...)
	}
	return all
}

// TestLagWatchEntersAndLeaves: con 350 ms sostenido avisa a los 10 s, desde la primera muestra; con
// 10 s normales avisa el fin, con la duración y lo peor.
func TestLagWatchEntersAndLeaves(t *testing.T) {
	var w lagWatch
	seen := runLagWatch(&w, concat(
		every(0, 6, 40, -1),
		every(8, 30, 350, -1),
		[]lagStep{{32, 600, 12}},
		every(34, 60, 40, 0),
	))
	if len(seen) != 2 {
		t.Fatalf("avisos %+v, quería empezó y terminó", seen)
	}
	started, ended := seen[0], seen[1]
	if started.kind != lagStarted || started.at != 18 || !started.episode.start.Equal(lagTime(8)) {
		t.Fatalf("empezó %+v: quería a las :18 (10 s después de las :08), desde las :08", started)
	}
	if ended.kind != lagEnded || ended.at != 44 || !ended.episode.end.Equal(lagTime(34)) {
		t.Fatalf("terminó %+v: quería a las :44, con fin a las :34 (primera normal)", ended)
	}
	if ended.episode.worstRTT != 600 || ended.episode.worstLoss != 12 {
		t.Fatalf("lo peor %d ms %d %%, quería 600 y 12", ended.episode.worstRTT, ended.episode.worstLoss)
	}
	if len(w.episodes) != 1 || w.episodes[0].end.IsZero() {
		t.Fatalf("tramos %+v, quería uno cerrado", w.episodes)
	}
}

// TestLagWatchShortSpike: 8 s de lag (5 muestras) no avisan nada; tampoco 4 % de pérdida (debajo
// del umbral) ni 200 ms justos (amarillo, no naranja).
func TestLagWatchShortSpike(t *testing.T) {
	var w lagWatch
	seen := runLagWatch(&w, concat(
		every(0, 8, 450, -1),
		every(10, 20, 40, -1),
		every(22, 60, 200, 4),
	))
	if len(seen) != 0 || len(w.episodes) != 0 {
		t.Fatalf("avisó %+v (tramos %+v)", seen, w.episodes)
	}
}

// TestLagWatchDoesNotAlternate: con lag, una muestra normal de por medio no lo termina (hacen falta
// 10 s seguidos) y una con lag tampoco lo vuelve a empezar.
func TestLagWatchDoesNotAlternate(t *testing.T) {
	var w lagWatch
	steps := every(0, 10, 350, -1)
	for s := 12.0; s <= 120; s += 2 {
		rtt := 40
		if int(s)%6 == 0 {
			rtt = 350
		}
		steps = append(steps, lagStep{s, rtt, -1})
	}
	steps = append(steps, every(122, 140, 40, -1)...)
	seen := runLagWatch(&w, steps)
	if len(seen) != 2 || seen[0].kind != lagStarted || seen[1].kind != lagEnded {
		t.Fatalf("avisos %+v, quería uno de inicio y uno de fin", seen)
	}
}

// TestLagWatchGapCutsStreak: un hueco de más de 6 s sin muestras corta la racha: no se sabe qué pasó.
func TestLagWatchGapCutsStreak(t *testing.T) {
	var w lagWatch
	seen := runLagWatch(&w, concat(every(0, 6, 350, -1), every(14, 30, 350, -1)))
	if len(seen) != 1 || seen[0].at != 24 || !seen[0].episode.start.Equal(lagTime(14)) {
		t.Fatalf("avisos %+v: quería empezar a contar de nuevo a las :14 y avisar a las :24", seen)
	}
}

// TestLagWatchLossOnly: el cliente, de los que no son el host, solo mide la pérdida.
func TestLagWatchLossOnly(t *testing.T) {
	var w lagWatch
	seen := runLagWatch(&w, concat(every(0, 12, -1, 8), every(14, 20, -1, -1), every(22, 40, -1, 0)))
	if len(seen) != 2 || seen[0].episode.worstRTT != -1 || seen[0].episode.worstLoss != 8 {
		t.Fatalf("avisos %+v: quería inicio y fin con 8 %% y sin ms", seen)
	}
	// Las muestras sin nada medido (:14 a :20) no cuentan como normales: el fin cuenta desde las :22.
	if !seen[1].episode.end.Equal(lagTime(22)) {
		t.Fatalf("fin a las %v, quería :22", seen[1].episode.end)
	}
}

// TestLagWatchCooldown: como mucho un aviso por persona cada 5 min. El tramo del medio se marca
// (para el gráfico) pero no avisa ni el inicio ni el fin.
func TestLagWatchCooldown(t *testing.T) {
	var w lagWatch
	seen := runLagWatch(&w, concat(
		every(0, 10, 350, -1), every(12, 30, 40, -1), // 1: avisa
		every(60, 70, 350, -1), every(72, 90, 40, -1), // 2: a 1 min del primero, callado
		every(320, 330, 350, -1), every(332, 350, 40, -1), // 3: más de 5 min después del primero
	))
	if len(seen) != 4 || !seen[2].episode.start.Equal(lagTime(320)) {
		t.Fatalf("avisos %+v: quería inicio y fin del 1 y del 3", seen)
	}
	if len(w.episodes) != 3 || !w.episodes[1].silent || w.episodes[0].silent || w.episodes[2].silent {
		t.Fatalf("tramos %+v: quería 3, solo el del medio callado", w.episodes)
	}
}

// TestLagHistoryReportsOnSlotClose: el historial le pasa a la máquina cada casillero cuando se cierra
// (al abrirse el siguiente) y devuelve el aviso; los tramos quedan para el gráfico.
func TestLagHistoryReportsOnSlotClose(t *testing.T) {
	h := newLagHistory()
	var events []lagEvent
	for s := 0.0; s <= 14; s += 2 {
		if event, ok := h.addRTT("Pepe", lagTime(s+0.3), 350*time.Millisecond); ok {
			events = append(events, event)
			if s != 12 {
				t.Fatalf("avisó al agregar la de las :%v, quería al abrir el casillero de las :12 (cierra el de las :10)", s)
			}
		}
	}
	if len(events) != 1 || events[0].kind != lagStarted || events[0].name != "Pepe" || !events[0].episode.start.Equal(lagTime(0)) {
		t.Fatalf("avisos %+v", events)
	}
	episodes := h.episodes("Pepe", lagTime(0), lagTime(60))
	if len(episodes) != 1 || !episodes[0].end.IsZero() || !episodes[0].drawEnd().Equal(lagTime(12+3)) {
		t.Fatalf("tramos %+v: quería uno abierto, marcado hasta lo que cubre la muestra de las :12", episodes)
	}
	if h.episodes("Pepe", lagTime(100), lagTime(200)) != nil || h.episodes("Juan", lagTime(0), lagTime(60)) != nil {
		t.Fatal("fuera de rango o de otro: sin tramos")
	}
	if spans := lagEpisodeSpans(episodes, lagTime(0), lagTime(30), 30); len(spans) != 1 || spans[0] != [2]int{0, 14} {
		t.Fatalf("columnas del tramo %v, quería [0 14]", spans)
	}
}

func TestLagEventMsg(t *testing.T) {
	setLanguage(t, "es")
	start := time.Date(2026, 11, 13, 21, 34, 10, 0, time.Local)
	episode := lagEpisode{start: start, end: start.Add(80 * time.Second), worstRTT: 600, worstLoss: 12}
	started := lagEventMsg(lagEvent{kind: lagStarted, name: "Pepe", episode: lagEpisode{start: start, worstRTT: 350, worstLoss: 8}}, false).String()
	if want := "Hay lag con Pepe desde las 21:34 (350 ms, 8 % de pérdida). Si es siempre la misma persona"; len(started) < len(want) || started[:len(want)] != want {
		t.Errorf("inicio: %q", started)
	}
	if got := lagEventMsg(lagEvent{kind: lagEnded, name: "Pepe", episode: episode}, true).String(); got != "Se normalizó la conexión con el host (Pepe) (duró 1 min 20 s; lo peor: 600 ms, 12 % de pérdida)." {
		t.Errorf("fin con el host: %q", got)
	}
	lossOnly := lagEpisode{start: start, end: start.Add(45 * time.Second), worstRTT: -1, worstLoss: 9}
	if got := lagEventMsg(lagEvent{kind: lagEnded, name: "Juan", episode: lossOnly}, false).String(); got != "Se normalizó la conexión con Juan (duró 45 s; lo peor: 9 % de pérdida)." {
		t.Errorf("solo pérdida: %q", got)
	}
	long := lagEpisode{start: start, end: start.Add(65 * time.Minute), worstRTT: 420, worstLoss: -1}
	setLanguage(t, "en")
	if got := lagEventMsg(lagEvent{kind: lagEnded, name: "Juan", episode: long}, false).String(); got != "Connection with Juan back to normal (lasted 1 h 5 min; worst: 420 ms)." {
		t.Errorf("en: %q", got)
	}
	if got := lagEventMsg(lagEvent{kind: lagStarted, name: "Pepe", episode: long}, true).String(); len(got) < 40 || got[:40] != "Lag with the host (Pepe) since 21:34 (42" {
		t.Errorf("en, host: %q", got)
	}
}
