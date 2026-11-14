package audio

import (
	"math"
	"slices"
	"testing"
)

// testDuck son los valores de fábrica de F8 (shout_duck_*: −10 dB desde −14 dBFS).
var testDuck = ShoutDuckSettings{Enabled: true, AmountDB: 10, ThresholdDB: -14}

func TestShoutDuckDefaultsMatchConfig(t *testing.T) {
	if got := defaultPlayback.ShoutDuck; got != testDuck {
		t.Fatalf("defaults de fábrica %+v, el test usa %+v", got, testDuck)
	}
}

// voiceAt lleva la voz sintética a S16 con el pico en peakDB dBFS.
func voiceAt(voice []float64, peakDB float64) []int16 {
	peak := 0.0
	for _, v := range voice {
		peak = math.Max(peak, math.Abs(v))
	}
	gain := dbGain(peakDB) * math.MaxInt16 / peak
	out := make([]int16, len(voice))
	for i, v := range voice {
		out[i] = int16(math.Round(v * gain))
	}
	return out
}

// shoutOf es un grito como llega por la red: la voz 15 dB más fuerte que en los tests (pico
// +4.5 dBFS), recortada por el conversor y pasada por el limitador de envío al techo de fábrica.
func shoutOf(voice []float64) []int16 {
	peak := 0.0
	for _, v := range voice {
		peak = math.Max(peak, math.Abs(v))
	}
	gain := dbGain(-10.5+15) * math.MaxInt16 / peak
	out := make([]int16, len(voice))
	for i, v := range voice {
		out[i] = int16(math.Max(math.Min(math.Round(v*gain), math.MaxInt16), -math.MaxInt16))
	}
	fx := &Effects{}
	ceiling := int16(dbToS16(-1.7))
	for start := 0; start+FrameSamples <= len(out); start += FrameSamples {
		fx.limitSend(out[start:start+FrameSamples], ceiling, 60)
	}
	return out
}

// duckBlocks pasa samples (una copia) por d en bloques de los tamaños dados, en ciclo, como los
// callbacks de malgo.
func duckBlocks(d *shoutDucker, samples []int16, settings ShoutDuckSettings, sizes ...int) []int16 {
	out := slices.Clone(samples)
	for start, k := 0, 0; start < len(out); k++ {
		end := min(start+sizes[k%len(sizes)], len(out))
		d.process(out[start:end], settings)
		start = end
	}
	return out
}

// Una voz normal (incluso un micrófono 2.5 dB más fuerte que el de los tests) sale bit a bit igual,
// con cualquier tamaño de callback.
func TestShoutDuck_NormalVoiceUntouched(t *testing.T) {
	for _, f0 := range []float64{120, 210} {
		voice := syntheticVoice(10, f0, int64(f0))
		for _, peak := range []float64{-16.5, -10.5, -8} {
			in := voiceAt(voice, peak)
			for _, size := range []int{441, 480, 512, 960} {
				var d shoutDucker
				if out := duckBlocks(&d, in, testDuck, size); !slices.Equal(out, in) {
					t.Fatalf("voz de %.0f Hz con pico %.1f dBFS (bloques de %d) cambió", f0, peak, size)
				}
			}
		}
	}
}

// Un grito real (limitado por el que lo manda) baja casi todo "Cuánto", nunca más.
func TestShoutDuck_ShoutGoesDown(t *testing.T) {
	for _, f0 := range []float64{120, 210} {
		in := shoutOf(syntheticVoice(10, f0, int64(f0)))
		var d shoutDucker
		out := duckBlocks(&d, in, testDuck, 480)
		var reductions []float64
		var inEnergy, outEnergy float64
		for start := 4800; start+480 <= len(in); start += 480 { // después de los primeros 100 ms
			block := in[start : start+480]
			if samplesRMSdB(block) < -45 {
				continue // pausa entre sílabas
			}
			reduction := samplesRMSdB(block) - samplesRMSdB(out[start:start+480])
			if reduction > testDuck.AmountDB+0.01 {
				t.Fatalf("%.0f Hz: un bloque bajó %.2f dB, más que Cuánto (%.0f)", f0, reduction, testDuck.AmountDB)
			}
			reductions = append(reductions, reduction)
			inEnergy += math.Pow(10, samplesRMSdB(block)/10)
			outEnergy += math.Pow(10, samplesRMSdB(out[start:start+480])/10)
		}
		slices.Sort(reductions)
		if median := reductions[len(reductions)/2]; median < 9 {
			t.Errorf("%.0f Hz: la mitad de los bloques del grito bajan menos de %.1f dB", f0, median)
		}
		if total := 10 * math.Log10(inEnergy/outEnergy); total < 8 {
			t.Errorf("%.0f Hz: el grito bajó %.1f dB en total, quería ~%.0f", f0, total, testDuck.AmountDB)
		}
	}
}

// sineSegments arma una senoidal de 440 Hz con un RMS (dBFS) por tramo: {segundos, dB}.
func sineSegments(segments ...[2]float64) []int16 {
	var out []int16
	for _, segment := range segments {
		amplitude := dbGain(segment[1]) * math.Sqrt2 * math.MaxInt16
		for range int(segment[0] * SampleRate) {
			out = append(out, int16(math.Round(amplitude*math.Sin(2*math.Pi*440*float64(len(out))/SampleRate))))
		}
	}
	return out
}

// La ganancia baja y vuelve en rampa (sin saltos de una muestra a la otra), se sostiene el hold y
// después vuelve a no tocar nada.
func TestShoutDuck_RampsHoldsAndReleases(t *testing.T) {
	const quiet, loud = 0.2, 0.5 // segundos
	in := sineSegments([2]float64{quiet, -30}, [2]float64{loud, -6}, [2]float64{1.5, -30})
	var d shoutDucker
	out := duckBlocks(&d, in, testDuck, 480)
	loudStart, loudEnd := int(quiet*SampleRate), int((quiet+loud)*SampleRate)
	if !slices.Equal(out[:loudStart], in[:loudStart]) {
		t.Fatal("antes del grito se tocó el audio")
	}
	gainAt := func(i int) float64 { return float64(out[i]) / float64(in[i]) }
	last := 1.0
	for i := range in {
		if math.Abs(float64(in[i])) < 1000 {
			continue // cerca del cruce por cero el redondeo domina
		}
		gain := gainAt(i)
		if math.Abs(gain-last) > 0.01 {
			t.Fatalf("salto de ganancia en la muestra %d: %.3f → %.3f", i, last, gain)
		}
		last = gain
	}
	for i := loudStart + SampleRate/20; i < loudEnd; i++ { // a los 50 ms ya baja todo
		if math.Abs(float64(in[i])) >= 1000 && math.Abs(gainAt(i)-dbGain(-10)) > 0.01 {
			t.Fatalf("muestra %d del grito con ganancia %.3f, quería %.3f (−10 dB)", i, gainAt(i), dbGain(-10))
		}
	}
	held := loudEnd + SampleRate/5 // 200 ms después: todavía en el hold
	for i := held; i < held+480; i++ {
		if math.Abs(float64(in[i])) >= 1000 && gainAt(i) > dbGain(-9.5) {
			t.Fatalf("a los 200 ms del grito ya volvió (ganancia %.3f): el hold no se sostuvo", gainAt(i))
		}
	}
	tail := len(in) - SampleRate*3/10
	if !slices.Equal(out[tail:], in[tail:]) {
		t.Fatal("1.2 s después del grito el audio sigue tocado")
	}
	if d.reduction != 0 || d.applied != 0 {
		t.Fatalf("estado después de volver: %+v", d)
	}
}

// Apagado (o Cuánto en 0) no toca nada; si se apaga en medio de un grito vuelve en un bloque, en
// rampa, y después no toca nada.
func TestShoutDuck_OffLeavesAudioAlone(t *testing.T) {
	shout := shoutOf(syntheticVoice(3, 120, 120))
	for _, settings := range []ShoutDuckSettings{{ThresholdDB: -14, AmountDB: 10}, {Enabled: true, ThresholdDB: -14}} {
		var d shoutDucker
		if out := duckBlocks(&d, shout, settings, 480); !slices.Equal(out, shout) {
			t.Fatalf("%+v tocó el audio", settings)
		}
	}

	in := sineSegments([2]float64{0.5, -6})
	var d shoutDucker
	duckBlocks(&d, in, testDuck, 480)
	if d.applied < 9.9 {
		t.Fatalf("el tono fuerte no bajó: %.2f dB", d.applied)
	}
	next := sineSegments([2]float64{0.5 + 0.02, -6})[int(0.5*SampleRate):] // sigue la misma onda
	off := testDuck
	off.Enabled = false
	out := duckBlocks(&d, next, off, 480)
	from := dbGain(-10)
	for i := range 480 { // rampa de −10 dB a 1 en el primer bloque
		if math.Abs(float64(next[i])) < 1000 {
			continue
		}
		want := from + (1-from)*float64(i+1)/480
		if got := float64(out[i]) / float64(next[i]); math.Abs(got-want) > 0.002 {
			t.Fatalf("al apagar, muestra %d con ganancia %.3f, quería %.3f", i, got, want)
		}
	}
	if !slices.Equal(out[480:], next[480:]) {
		t.Fatal("apagado sigue tocando el audio después de la rampa")
	}
	if d != (shoutDucker{}) {
		t.Fatalf("apagado quedó estado: %+v", d)
	}
}
