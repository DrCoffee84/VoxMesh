//go:build cgo && windows

package audio

import (
	"encoding/binary"
	"math"
	"math/rand"
	"slices"
	"testing"
	"time"
)

// syntheticVoice arma voz sintética: pulsos glotales con vibrato y jitter, filtrados por tres
// formantes que cambian por sílaba (vocales), con sílabas de ~250 ms y pausas. Pico ~ -10 dBFS.
func syntheticVoice(seconds, f0 float64, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	n := int(seconds * SampleRate)
	vowels := [][3]float64{{730, 1090, 2440}, {270, 2290, 3010}, {530, 1840, 2480}, {570, 840, 2410}, {300, 870, 2240}}
	out := make([]float64, n)
	var res [3][2]float64 // estado de los resonadores
	phase := 0.0
	syllableEnd, gain := 0, 0.0
	formants := vowels[0]
	pause := false
	for i := range n {
		if i >= syllableEnd {
			pause = rng.Float64() < 0.25
			syllableEnd = i + int((0.15+0.2*rng.Float64())*SampleRate)
			formants = vowels[rng.Intn(len(vowels))]
		}
		target := 1.0
		if pause {
			target = 0
		}
		gain += (target - gain) * 0.002 // ~10 ms de ataque/caída
		freq := f0 * (1 + 0.03*math.Sin(2*math.Pi*5*float64(i)/SampleRate))
		phase += freq / SampleRate
		source := 0.02 * rng.NormFloat64() // aspiración
		if phase >= 1 {
			phase -= 1 + 0.01*rng.NormFloat64()
			source += 1
		}
		sample := 0.0
		for k, fc := range formants {
			bw := 80.0 + 40*float64(k)
			r := math.Exp(-math.Pi * bw / SampleRate)
			a1, a2 := 2*r*math.Cos(2*math.Pi*fc/SampleRate), -r*r
			y := (1-r)*source + a1*res[k][0] + a2*res[k][1]
			res[k][1], res[k][0] = res[k][0], y
			sample += y / float64(k+1)
		}
		out[i] = sample * gain
	}
	peak := 0.0
	for _, v := range out {
		peak = math.Max(peak, math.Abs(v))
	}
	for i := range out {
		out[i] *= 0.3 * 32767 / peak
	}
	return out
}

// echoScenario simula una PC con parlantes: la voz lejana sale por Engine.mix (callbacks de
// tamaños irregulares, la referencia pasa por el ring de verdad), vuelve al micrófono con
// retardo, atenuación, reverberación corta y deriva de reloj entre salida y micrófono, y se
// suma a la voz cercana. Cada frame capturado pasa por CancelEcho con la hora en que lo
// entregaría la captura.
type echoScenario struct {
	far, near   []float64
	delay       time.Duration // del callback de salida al micrófono
	gain        float64       // atenuación del camino directo
	drift       float64       // ppm: el micrófono corre más rápido que la salida
	captureLate time.Duration // de capturado a entregado (At)
	settings    EchoSettings
	// dropAt: frames en los que la entrega se saltea uno (el servidor del celular tira un frame
	// por deriva y la latencia baja 20 ms de golpe).
	dropAt []int
}

// echoRun es el resultado por muestra: micrófono antes del AEC, salida y voz cercana sola
// (los tres en la escala del micrófono, alineados).
type echoRun struct {
	mic, out, near []float64
}

func (s echoScenario) run(t *testing.T) echoRun {
	t.Helper()
	e := newEngineState()
	defer e.Stop()
	base := time.Unix(1_000_000, 0)
	at := func(sample float64) time.Time {
		return base.Add(time.Duration(sample * float64(time.Second) / SampleRate))
	}

	// Salida: callbacks de 441/480/512 muestras; el de la muestra s corre a la hora de s.
	played := make([]float64, 0, len(s.far)+1024)
	sizes := []int{441, 480, 512, 480}
	buf := make([]byte, 2*512)
	callback := 0
	playUntil := func(t time.Time) {
		for {
			n := sizes[callback%len(sizes)]
			start := len(played)
			if start+n > len(s.far) || at(float64(start)).After(t) {
				return
			}
			chunk := make([]byte, 2*n)
			for i := range n {
				binary.LittleEndian.PutUint16(chunk[2*i:], uint16(int16(s.far[start+i])))
			}
			e.PlayStream("__sfx__", chunk)
			e.mix(buf[:2*n], at(float64(start)).UnixNano())
			for i := range n {
				played = append(played, float64(int16(binary.LittleEndian.Uint16(buf[2*i:]))))
			}
			callback++
		}
	}

	// Camino de eco: directo + reflexiones (~30 ms) con signo alternado.
	taps := []struct {
		ms, g float64
	}{{0, 1}, {3, -0.5}, {7, 0.35}, {13, -0.25}, {21, 0.15}, {29, -0.08}}
	delaySamples := s.delay.Seconds() * SampleRate
	echoAt := func(m float64) float64 {
		pos := m/(1+s.drift*1e-6) - delaySamples // índice de la salida que llega al micrófono
		sum := 0.0
		for _, tap := range taps {
			p := pos - tap.ms*SampleRate/1000
			i := int(math.Floor(p))
			if i < 0 || i+1 >= len(played) {
				continue
			}
			frac := p - float64(i)
			sum += tap.g * (played[i]*(1-frac) + played[i+1]*frac)
		}
		return s.gain * sum
	}

	var r echoRun
	pcm := make([]byte, streamFrameBytes)
	frames := len(s.near)/FrameSamples - 1
	micPos := 0 // primera muestra del micrófono del próximo frame
	for k := range frames {
		if slices.Contains(s.dropAt, k) {
			micPos += FrameSamples // el frame capturado se tiró: la latencia baja 20 ms
		}
		if micPos+FrameSamples > len(s.near) {
			break
		}
		// Entregado a la hora del frame k (ritmo fijo, como el reparto del celular o la captura).
		delivered := at(float64((k + 1) * FrameSamples)).Add(s.captureLate)
		// La salida sigue sonando hasta que el frame llega a quien cancela el eco.
		playUntil(delivered)
		mic := make([]float64, FrameSamples)
		for i := range FrameSamples {
			m := micPos + i
			mic[i] = s.near[m] + echoAt(float64(m))
			binary.LittleEndian.PutUint16(pcm[2*i:], uint16(int16(math.Max(-32768, math.Min(32767, mic[i])))))
		}
		if err := e.CancelEcho(pcm, delivered, s.settings); err != nil {
			t.Fatal(err)
		}
		for i := range FrameSamples {
			r.mic = append(r.mic, mic[i])
			r.out = append(r.out, float64(int16(binary.LittleEndian.Uint16(pcm[2*i:]))))
			r.near = append(r.near, s.near[micPos+i])
		}
		micPos += FrameSamples
	}
	return r
}

func energy(x []float64) float64 {
	sum := 0.0
	for _, v := range x {
		sum += v * v
	}
	return sum
}

func window(x []float64, from, to float64) []float64 {
	a, b := int(from*SampleRate), int(to*SampleRate)
	return x[min(a, len(x)):min(b, len(x))]
}

// erle es cuánto eco sacó el AEC en un tramo sin voz cercana (dB).
func erle(r echoRun, from, to float64) float64 {
	return 10 * math.Log10(energy(window(r.mic, from, to))/(energy(window(r.out, from, to))+1))
}

// nearKept compara la salida con la de la misma voz cercana pasada por el AEC sin eco (base):
// nivel (dB) y correlación. Así no cuentan lo que el AEC le hace siempre a la voz (≈ 9 ms de
// latencia por los bloques y el banco de filtros, y la fase del pasa-altos), solo lo que se
// come por el eco.
func nearKept(r, base echoRun, from, to float64) (levelDB, corr float64) {
	out, ref := window(r.out, from, to), window(base.out, from, to)
	var xy float64
	for i := range out {
		xy += out[i] * ref[i]
	}
	return 10 * math.Log10(energy(out)/energy(ref)), xy / math.Sqrt(energy(out)*energy(ref))
}

// talks arma una voz que habla solo en los tramos dados (segundos) y calla el resto.
func talks(voice []float64, spans ...[2]float64) []float64 {
	out := make([]float64, len(voice))
	for _, span := range spans {
		copy(out[int(span[0]*SampleRate):int(span[1]*SampleRate)], voice[int(span[0]*SampleRate):int(span[1]*SampleRate)])
	}
	return out
}

// checkEcho corre la charla de prueba: habla solo el otro (0–10 s), los dos (10–16 s) y solo yo
// (16–20 s). Pide ERLE ≥ 20 dB con el AEC ya convergido (por defecto 4–10 s; si no, en los tramos
// dados), que con los dos hablando la voz cercana no pierda más de 3 dB ni la forma (correlación
// ≥ 0.85: el supresor de residuo de AEC3 atenúa las bandas donde domina el eco) y que sola no
// cambie.
func checkEcho(t *testing.T, s echoScenario, erleSpans ...[2]float64) {
	t.Helper()
	s.far = talks(syntheticVoice(20, 120, 1), [2]float64{0, 16})
	s.near = talks(syntheticVoice(20, 210, 2), [2]float64{10, 20})
	r := s.run(t)
	quiet := s
	quiet.far = make([]float64, len(s.far))
	base := quiet.run(t)

	if len(erleSpans) == 0 {
		erleSpans = [][2]float64{{4, 10}}
	}
	for _, span := range erleSpans {
		got := erle(r, span[0], span[1])
		t.Logf("ERLE hablando solo el otro (%g–%g s): %.1f dB", span[0], span[1], got)
		if got < 20 {
			t.Errorf("ERLE = %.1f dB, quiero ≥ 20", got)
		}
	}
	level, corr := nearKept(r, base, 11, 16)
	t.Logf("hablando los dos: voz cercana %.1f dB, correlación %.3f (en el micrófono el eco está a %.1f dB de la voz)",
		level, corr, 10*math.Log10((energy(window(r.mic, 11, 16))-energy(window(r.near, 11, 16)))/energy(window(r.near, 11, 16))))
	if level < -3 || corr < 0.85 {
		t.Errorf("hablando los dos la voz cercana no se mantiene: %.1f dB, correlación %.3f", level, corr)
	}
	level, corr = nearKept(r, base, 16.5, 19.5)
	t.Logf("hablando solo yo: %.2f dB, correlación %.4f", level, corr)
	if level < -1 || level > 1 || corr < 0.98 {
		t.Errorf("hablando solo yo la voz cambia: %.1f dB, correlación %.3f", level, corr)
	}
}

// Parlantes de la PC: eco a 120 ms del callback de salida, −6 dB con reverberación de ~30 ms y
// 100 ppm de deriva entre los relojes de la salida y del micrófono.
func TestCancelEcho_Speakers(t *testing.T) {
	checkEcho(t, echoScenario{
		delay: 120 * time.Millisecond, gain: 0.5, drift: 100, captureLate: 10 * time.Millisecond,
		settings: EchoSettings{Enabled: true, DelayMS: 60},
	})
}

// Tele por HDMI: 150 ms más de retardo y el eco más fuerte que la voz cercana.
func TestCancelEcho_TV(t *testing.T) {
	checkEcho(t, echoScenario{
		delay: 270 * time.Millisecond, gain: 0.7, drift: 100, captureLate: 10 * time.Millisecond,
		settings: EchoSettings{Enabled: true, DelayMS: 60},
	})
}

// Micrófono del celular: el frame se entrega 160 ms después de capturado (100 ms de buffer + Wi-Fi
// y la captura del teléfono). A los 6 s el reparto tira un frame por deriva: la latencia baja
// 20 ms de golpe y AEC3 tiene que volver a encontrar el eco (≤ 2.5 s). La referencia se corre el
// buffer (ReferenceOffset).
func TestCancelEcho_Phone(t *testing.T) {
	checkEcho(t, echoScenario{
		delay: 60 * time.Millisecond, gain: 0.5, drift: 100, captureLate: 160 * time.Millisecond, dropAt: []int{300},
		settings: EchoSettings{Enabled: true, Phone: true, ReferenceOffset: 100 * time.Millisecond, DelayMS: 60},
	}, [2]float64{3.5, 6}, [2]float64{8.5, 10})
}

// El callback de playback copia la referencia sin asignar, y la goroutine de envío no asigna
// memoria de Go por frame.
func TestCancelEcho_NoAllocs(t *testing.T) {
	e := newEngineState()
	defer e.Stop()
	settings := EchoSettings{Enabled: true, DelayMS: 60}
	pcm := make([]byte, streamFrameBytes)
	buf := make([]byte, streamFrameBytes)
	now := time.Unix(1_000_000, 0)
	step := func() {
		now = now.Add(20 * time.Millisecond)
		e.mix(buf, now.UnixNano())
		if err := e.CancelEcho(pcm, now, settings); err != nil {
			t.Fatal(err)
		}
	}
	step() // crea el AEC y prende la referencia
	if !e.echoActive.Load() {
		t.Fatal("la referencia no se prendió")
	}
	if allocs := testing.AllocsPerRun(100, func() {
		now = now.Add(20 * time.Millisecond)
		e.mix(buf, now.UnixNano())
	}); allocs != 0 {
		t.Errorf("mix con la referencia asigna %.1f veces por callback", allocs)
	}
	if allocs := testing.AllocsPerRun(100, step); allocs != 0 {
		t.Errorf("CancelEcho asigna %.1f veces por frame", allocs)
	}
	e.CancelEcho(pcm, now, EchoSettings{})
	if e.echoActive.Load() || e.echo != nil {
		t.Error("apagado sigue copiando la referencia o con el AEC vivo")
	}
}
