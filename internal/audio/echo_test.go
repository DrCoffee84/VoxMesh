package audio

import (
	"testing"
)

// writeRef publica n muestras con valor = su índice absoluto (mod 32768), como un callback.
func writeRef(ref *echoReference, start, n int, at int64) {
	ref.begin()
	for i := range n {
		ref.put(i, int16((start+i)%32768))
	}
	ref.publish(n, at)
}

func TestEchoReader_TakesOnlyWhatHadPlayed(t *testing.T) {
	ref := new(echoReference)
	var r echoReader
	r.reset(ref)
	// Callbacks de 441 muestras a t = 1, 2, 3, 4 (ms ficticios).
	for i := range 4 {
		writeRef(ref, i*441, 441, int64(i+1))
	}
	got := r.take(ref, 2) // sonaron 882: un bloque de 480, quedan 402 para la próxima
	if len(got) != echoBlock {
		t.Fatalf("take(2) = %d muestras, quiero %d", len(got), echoBlock)
	}
	for i, v := range got {
		if int(v) != i {
			t.Fatalf("muestra %d = %d", i, v)
		}
	}
	got = r.take(ref, 4) // sonaron 1764: falta un bloque (480..959); 1440..1763 queda
	if len(got) != 2*echoBlock || got[0] != echoBlock || got[len(got)-1] != 3*echoBlock-1 {
		t.Fatalf("take(4) = %d muestras desde %d", len(got), got[0])
	}
	if got := r.take(ref, 4); len(got) != 0 {
		t.Fatalf("sin audio nuevo take = %d muestras", len(got))
	}
}

func TestEchoReader_ResetStartsAtLatest(t *testing.T) {
	ref := new(echoReference)
	writeRef(ref, 0, 4800, 1)
	var r echoReader
	r.reset(ref)
	writeRef(ref, 4800, 480, 2)
	got := r.take(ref, 2)
	if len(got) != echoBlock || got[0] != 4800%32768 {
		t.Fatalf("después del reset take = %d muestras desde %d, quiero 480 desde 4800", len(got), got[0])
	}
}

func TestEchoReader_SkipsWhenFarBehind(t *testing.T) {
	ref := new(echoReference)
	var r echoReader
	r.reset(ref)
	total := 0
	for i := range 100 { // 1 s de callbacks de 10 ms sin leer
		writeRef(ref, total, 480, int64(i+1))
		total += 480
	}
	got := r.take(ref, 100)
	if len(got) != echoMaxRenderBlocks*echoBlock {
		t.Fatalf("take = %d muestras, quiero el tope %d", len(got), echoMaxRenderBlocks*echoBlock)
	}
	if want := int16((total - len(got)) % 32768); got[0] != want {
		t.Fatalf("primera muestra %d, quiero %d (lo más nuevo)", got[0], want)
	}
	if r.skipped != uint64(total-len(got)) {
		t.Fatalf("salteadas %d, quiero %d", r.skipped, total-len(got))
	}
}

func TestEchoReference_WrapsAround(t *testing.T) {
	ref := new(echoReference)
	var r echoReader
	r.reset(ref)
	total := 0
	at := int64(0)
	for range 3 * echoRefSamples / 512 { // tres vueltas del ring, leyendo a la par
		at++
		writeRef(ref, total, 512, at)
		total += 512
		start := r.pos
		for i, v := range r.take(ref, at) {
			if want := int16((start + uint64(i)) % 32768); v != want {
				t.Fatalf("muestra %d = %d, quiero %d", start+uint64(i), v, want)
			}
		}
	}
	if got, want := r.pos, uint64(total-total%echoBlock); got != want {
		t.Fatalf("leídas %d, quiero %d", got, want)
	}
	if r.skipped != 0 {
		t.Fatalf("salteadas %d leyendo a la par", r.skipped)
	}
}

func TestEchoReference_PutPublishNoAllocs(t *testing.T) {
	ref := new(echoReference)
	at := int64(0)
	allocs := testing.AllocsPerRun(200, func() {
		at++
		ref.begin()
		for i := range 480 {
			ref.put(i, int16(i))
		}
		ref.publish(480, at)
	})
	if allocs != 0 {
		t.Fatalf("put/publish asigna %.1f veces por callback", allocs)
	}
}

// Escritor (callback) y lector (envío) en goroutines distintas: lo leído es continuo salvo lo
// salteado por atraso (con -race, además, sin carreras).
func TestEchoReference_Concurrent(t *testing.T) {
	ref := new(echoReference)
	var r echoReader
	r.reset(ref)
	const callbacks = 20000
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range callbacks {
			writeRef(ref, i*480, 480, int64(i+1))
		}
	}()
	read := uint64(0)
	check := func() {
		start := r.pos
		got := r.take(ref, 1<<62)
		for i, v := range got {
			if want := int16((r.pos - uint64(len(got)) + uint64(i)) % 32768); v != want {
				t.Fatalf("muestra %d = %d, quiero %d (desde %d)", start, v, want, start)
			}
		}
		read += uint64(len(got))
	}
	for {
		select {
		case <-done:
			check()
			if read+r.skipped != callbacks*480 {
				t.Fatalf("leídas %d + salteadas %d, quiero %d", read, r.skipped, callbacks*480)
			}
			return
		default:
			check()
		}
	}
}
