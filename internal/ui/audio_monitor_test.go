//go:build cgo

package ui

import (
	"testing"
	"time"

	"voxmesh/internal/audio"
)

func TestReceiveProblem(t *testing.T) {
	if receiveProblem(audio.JitterStats{Played: 500, Phrases: 3}) {
		t.Fatal("un intervalo limpio (con frases nuevas) no es un problema")
	}
	for name, delta := range map[string]audio.JitterStats{
		"pérdida":     {Played: 499, Concealed: 1},
		"recuperados": {Played: 499, Recovered: 1},
		"tardíos":     {Played: 500, Late: 1},
		"descartados": {Played: 500, Dropped: 1},
		"resets":      {Played: 500, Resets: 1},
		"cortes":      {Played: 500, Underruns: 1, UnderrunTime: 20 * time.Millisecond},
	} {
		if !receiveProblem(delta) {
			t.Fatalf("%s tiene que dejar línea", name)
		}
	}
}

func TestCaptureIntervalProblem(t *testing.T) {
	ok := captureInterval{elapsed: 10 * time.Second, frames: 500, sent: 120, maxProc: 3 * time.Millisecond}
	if ok.problem() {
		t.Fatalf("captura a ritmo real marcada como problema: %s", ok)
	}
	// 505 frames en 10.1 s (tick que se atrasó) sigue siendo ritmo real.
	if late := (captureInterval{elapsed: 10100 * time.Millisecond, frames: 505}); late.problem() {
		t.Fatalf("tick atrasado marcado como problema: %s", late)
	}
	for name, c := range map[string]captureInterval{
		"lenta":       {elapsed: 10 * time.Second, frames: 470},
		"descartados": {elapsed: 10 * time.Second, frames: 500, dropped: 1},
		"cola":        {elapsed: 10 * time.Second, frames: 500, maxQueue: 2},
		"proc":        {elapsed: 10 * time.Second, frames: 500, maxProc: 39 * time.Millisecond},
	} {
		if !c.problem() {
			t.Fatalf("captura %s tiene que dejar línea: %s", name, c)
		}
	}

	var sum captureInterval
	sum.add(captureInterval{elapsed: 10 * time.Second, frames: 500, sent: 10, maxQueue: 1, maxProc: 2 * time.Millisecond})
	sum.add(captureInterval{elapsed: 10 * time.Second, frames: 500, dropped: 2, sent: 5, maxProc: 9 * time.Millisecond})
	if sum.elapsed != 20*time.Second || sum.frames != 1000 || sum.dropped != 2 || sum.sent != 15 || sum.maxQueue != 1 || sum.maxProc != 9*time.Millisecond {
		t.Fatalf("resumen de envío mal acumulado: %+v", sum)
	}
}

func TestLogJitterReportSummaries(t *testing.T) {
	a := &App{}
	var st audioLogState
	start := time.Unix(1000, 0)

	// Budo habla, Ana está callada (su stream sigue vivo sin frames nuevos).
	report := map[string]audio.JitterStats{
		"budo": {Played: 400, Concealed: 2, Underruns: 1, UnderrunTime: 20 * time.Millisecond, Phrases: 2, JitterMS: 3, DelayP95MS: 10, TargetMS: 40},
		"ana":  {},
	}
	a.logJitterReport(&st, start, report)
	if len(st.senders) != 1 || st.senders["budo"] == nil {
		t.Fatalf("esperaba resumen solo de budo: %v", st.senders)
	}

	report = map[string]audio.JitterStats{
		"budo": {Played: 900, Concealed: 2, Underruns: 1, UnderrunTime: 20 * time.Millisecond, Phrases: 5, JitterMS: 6, DelayP95MS: 15, TargetMS: 60},
		"ana":  {},
	}
	a.logJitterReport(&st, start.Add(netStatsLogInterval), report)
	got := st.senders["budo"]
	if got == nil || got.frames != 902 || got.lost != 2 || got.underruns != 1 || got.phrases != 5 ||
		got.maxJitterMS != 6 || got.maxP95MS != 15 || got.maxTargetMS != 60 {
		t.Fatalf("resumen de budo mal acumulado: %+v", got)
	}

	// Pasado el intervalo de resumen se escribe y arranca de nuevo.
	a.logJitterReport(&st, start.Add(netStatsSummaryInterval), report)
	if len(st.senders) != 0 {
		t.Fatalf("el resumen vencido tenía que salir: %v", st.senders)
	}

	// Un emisor cuyo stream desaparece (se fue o lo desalojó el silencio) cierra su resumen.
	report = map[string]audio.JitterStats{"budo": {Played: 1000, Concealed: 2, Underruns: 1, Phrases: 6}}
	a.logJitterReport(&st, start.Add(netStatsSummaryInterval+netStatsLogInterval), report)
	if st.senders["budo"] == nil {
		t.Fatal("budo volvió a hablar: tiene que tener resumen")
	}
	a.logJitterReport(&st, start.Add(netStatsSummaryInterval+2*netStatsLogInterval), map[string]audio.JitterStats{})
	if len(st.senders) != 0 {
		t.Fatalf("el stream desapareció: el resumen tenía que salir: %v", st.senders)
	}

	flushAudioLog(&st, start)
	if st.senders != nil || st.prev != nil {
		t.Fatalf("flushAudioLog tiene que dejar el estado vacío: %+v", st)
	}
}
