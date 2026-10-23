//go:build cgo

package ui

import (
	"testing"
	"time"
)

func TestAudioTimeline(t *testing.T) {
	var timeline audioTimeline
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(frames int) time.Time { return t0.Add(time.Duration(frames) * captureFrameDuration) }
	check := func(step string, gotSeq uint32, gotSpurt uint8, wantSeq uint32, wantSpurt uint8) {
		t.Helper()
		if gotSeq != wantSeq || gotSpurt != wantSpurt {
			t.Fatalf("%s: seq %d frase %d, want seq %d frase %d", step, gotSeq, gotSpurt, wantSeq, wantSpurt)
		}
	}

	seq, spurt := timeline.next(1, at(0), 0)
	check("primer frame", seq, spurt, 1, 1)
	seq, spurt = timeline.next(1, at(1), 0)
	check("frame siguiente", seq, spurt, 2, 1)

	// La captura tiró 2 frames (goroutine de envío atrasada): misma frase y la secuencia los salta,
	// así el receptor los oculta como pérdida en vez de quedarse corto de audio.
	seq, spurt = timeline.next(1, at(4), 2)
	check("después de 2 tirados", seq, spurt, 5, 1)

	// Frames procesados tarde pero capturados seguidos: siguen en la misma frase aunque se manden
	// juntos (la frase se mide con la hora de captura, no la de envío).
	seq, spurt = timeline.next(1, at(5), 0)
	check("atrasado al procesar", seq, spurt, 6, 1)

	// Compuerta cerrada 5 frames (100 ms sin mandar): frase nueva con el pre-roll (2) más el actual,
	// secuencia contigua.
	seq, spurt = timeline.next(3, at(11), 0)
	check("frase nueva con pre-roll", seq, spurt, 7, 2)
	seq, spurt = timeline.next(1, at(12), 0)
	check("sigue la frase", seq, spurt, 10, 2)
}
