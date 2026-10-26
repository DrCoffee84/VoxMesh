//go:build cgo

package ui

import (
	"testing"
	"time"

	"voxmesh/internal/audio"
)

func TestAudioRecovery(t *testing.T) {
	var r audioRecovery
	start := time.Unix(1000, 0)
	at := func(d time.Duration) time.Time { return start.Add(d) }
	engine := &audio.Engine{}

	if r.check(at(0), true, engine, true) {
		t.Fatal("reabrió un audio sano")
	}
	// Una sola falla no alcanza (al volver de una pausa el monitor puede despertar primero).
	if r.check(at(2*time.Second), true, engine, false) {
		t.Fatal("reabrió con una sola falla")
	}
	if !r.check(at(4*time.Second), true, engine, false) {
		t.Fatal("no reabrió tras dos fallas seguidas")
	}
	// Mientras se abre el Engine nuevo no hay Engine: no reintenta antes de audioMissingAfter.
	if r.check(at(6*time.Second), true, nil, true) {
		t.Fatal("reintentó mientras el audio se abría")
	}
	// El nuevo también falla: backoff de 5 s desde la reapertura anterior (t=4 s → t=9 s).
	next := &audio.Engine{}
	r.check(at(7*time.Second), true, next, false)
	if r.check(at(8*time.Second), true, next, false) {
		t.Fatal("reabrió antes del backoff")
	}
	if !r.check(at(9*time.Second), true, next, false) {
		t.Fatal("no reabrió después del backoff")
	}
	if r.attempts != 2 || r.total != 2 {
		t.Fatalf("attempts=%d total=%d, quería 2 y 2", r.attempts, r.total)
	}
	// Sano 30 s → el backoff vuelve al principio.
	healthy := &audio.Engine{}
	r.check(at(10*time.Second), true, healthy, true)
	r.check(at(41*time.Second), true, healthy, true)
	if r.attempts != 0 {
		t.Fatalf("attempts=%d tras 30 s sano, quería 0", r.attempts)
	}
	// En sala y sin Engine (falló al abrir) → reintenta pasado audioMissingAfter.
	r.check(at(50*time.Second), true, nil, true)
	if !r.check(at(50*time.Second+audioMissingAfter), true, nil, true) {
		t.Fatal("no reintentó abrir el audio que faltaba")
	}
	// Fuera de sala no hace nada y olvida el estado (menos el total).
	if r.check(at(2*time.Minute), false, nil, true) || r.attempts != 0 || r.total != 3 {
		t.Fatalf("fuera de sala: attempts=%d total=%d", r.attempts, r.total)
	}
}
