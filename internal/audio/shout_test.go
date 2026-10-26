package audio

import (
	"math"
	"testing"

	"voxmesh/internal/config"
)

// shoutTestSettings: solo RNNoise, sin VAD/compuerta/limitador del usuario, para medir la mezcla.
func shoutTestSettings() FilterSettings {
	settings := DefaultFilterSettings()
	settings.VADEnabled = false
	settings.NoiseGateEnabled = false
	settings.LimiterEnabled = false
	settings.ShoutEnabled = false
	return settings
}

func TestShoutMix_DryIsAlignedWithRNNoiseDelay(t *testing.T) {
	denoiser, err := NewRNNoise()
	if err != nil {
		t.Fatal(err)
	}
	if denoiser.Delay() != FrameSamples {
		t.Fatalf("RNNoise delay %d, the dry delay line assumes any value but this test assumes one frame (%d)", denoiser.Delay(), FrameSamples)
	}
	effects := &Effects{}
	settings := shoutTestSettings()
	settings.RNNoiseDryMix = 1 // intensidad 0 %: sale solo la señal original, retrasada como RNNoise

	var previous []int16
	for frame := 0; frame < 6; frame++ {
		input := sineFrame(frame, 8000)
		frames, _, err := PrepareOutgoingFrames(append([]int16(nil), input...), denoiser, NoiseGate{}, true, 0.5, settings, effects)
		if err != nil {
			t.Fatal(err)
		}
		if len(frames) != 1 {
			t.Fatalf("frame %d: expected 1 frame, got %d", frame, len(frames))
		}
		// El primer frame rampea la mezcla desde 0; desde el segundo la salida es la entrada
		// del frame anterior, muestra por muestra.
		if frame >= 2 {
			for i, sample := range frames[0] {
				if sample != previous[i] {
					t.Fatalf("frame %d sample %d: got %d, want delayed input %d", frame, i, sample, previous[i])
				}
			}
		}
		previous = input
	}
}

func TestShout_ForcesVoiceAndSendsOriginalAligned(t *testing.T) {
	denoiser, err := NewRNNoise()
	if err != nil {
		t.Fatal(err)
	}
	effects := &Effects{}
	settings := shoutTestSettings()
	settings.VADEnabled = true
	settings.VADHoldMS = 0
	settings.ShoutEnabled = true
	settings.ShoutThresholdDB = -12
	settings.ShoutDryMix = 1
	settings.ShoutHoldMS = 0
	const vadThreshold = 0.99 // un seno no es voz: sin modo grito no saldría nada

	for frame := 0; frame < 3; frame++ {
		frames, _, err := PrepareOutgoingFrames(sineFrame(frame, 100), denoiser, NoiseGate{}, true, vadThreshold, settings, effects)
		if err != nil {
			t.Fatal(err)
		}
		if len(frames) != 0 {
			t.Fatalf("quiet frame %d was sent", frame)
		}
	}

	// Grito (RMS ≈ -7 dBFS): sale aunque el VAD no lo vea como voz, a partir del frame fuerte
	// número AudioShoutEnterFrames (antes podría ser un golpe).
	frame := 3
	var loud []int16
	for n := 1; n <= config.AudioShoutEnterFrames; n++ {
		loud = sineFrame(frame, 20000)
		frames, started, err := PrepareOutgoingFrames(append([]int16(nil), loud...), denoiser, NoiseGate{}, true, vadThreshold, settings, effects)
		if err != nil {
			t.Fatal(err)
		}
		if n < config.AudioShoutEnterFrames && len(frames) != 0 {
			t.Fatalf("loud frame %d of %d already opened the gate", n, config.AudioShoutEnterFrames)
		}
		if n == config.AudioShoutEnterFrames && (!started || len(frames) == 0) {
			t.Fatal("a sustained shout must open the gate")
		}
		frame++
	}

	// El frame siguiente trae el audio del último (retardo de RNNoise): 100 % original, exacto.
	frames, _, err := PrepareOutgoingFrames(sineFrame(frame, 20000), denoiser, NoiseGate{}, true, vadThreshold, settings, effects)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 {
		t.Fatalf("expected the shout to keep sending, got %d frames", len(frames))
	}
	for i, sample := range frames[0] {
		if sample != loud[i] {
			t.Fatalf("sample %d: got %d, want original shout %d", i, sample, loud[i])
		}
	}
}

// knockSignal es un golpe en la mesa: silencio y, desde offset, ruido de banda ancha que
// arranca saturado (amplitud 40000, recortada a int16) y decae con constante tauSamples.
// Determinístico (xorshift). Un tono que decae no sirve: el VAD de RNNoise lo toma como voz.
func knockSignal(frames, offset int, tauSamples float64) [][]int16 {
	state := uint32(2463534242)
	out := make([][]int16, frames)
	for n := range out {
		out[n] = make([]int16, FrameSamples)
		for i := range out[n] {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			k := n*FrameSamples + i - offset
			if k < 0 {
				continue
			}
			noise := float64(state)/math.MaxUint32*2 - 1
			v := 40000 * math.Exp(-float64(k)/tauSamples) * noise
			out[n][i] = int16(math.Max(math.Min(v, math.MaxInt16), math.MinInt16))
		}
	}
	return out
}

func TestShoutBegin_IgnoresKnock(t *testing.T) {
	settings := shoutTestSettings()
	settings.ShoutEnabled = true
	settings.ShoutOnClip = true
	// Seco (1 ms) o con resonancia (5 ms), en medio de un frame o justo en el borde de dos.
	for _, tau := range []float64{48, 240} {
		for _, offset := range []int{FrameSamples + 100, FrameSamples + 500, 2*FrameSamples - 60, 2*FrameSamples - 10} {
			effects := &Effects{}
			for n, frame := range knockSignal(6, offset, tau) {
				if effects.shoutBegin(frame, FrameSamples, settings) {
					t.Fatalf("knock (tau %v, offset %d) activated shout mode on frame %d", tau, offset, n)
				}
			}
		}
	}
}

func TestShout_KnockIsNotSent(t *testing.T) {
	denoiser, err := NewRNNoise()
	if err != nil {
		t.Fatal(err)
	}
	effects := &Effects{}
	settings := shoutTestSettings()
	settings.VADEnabled = true
	settings.VADHoldMS = 0
	settings.ShoutEnabled = true // resto del modo grito con los valores de fábrica
	const vadThreshold = 0.99
	for n, frame := range knockSignal(10, 3*FrameSamples-60, 240) {
		frames, _, err := PrepareOutgoingFrames(frame, denoiser, NoiseGate{}, true, vadThreshold, settings, effects)
		if err != nil {
			t.Fatal(err)
		}
		if len(frames) != 0 || effects.Shouting() {
			t.Fatalf("frame %d: the knock was sent (%d frames, shouting %v, vad %.2f)", n, len(frames), effects.Shouting(), effects.VAD())
		}
	}
}

func TestShoutBegin_NeedsSustainedLoudness(t *testing.T) {
	effects := &Effects{}
	settings := shoutTestSettings()
	settings.ShoutEnabled = true
	settings.ShoutThresholdDB = -12
	settings.ShoutHoldMS = 0
	quiet, loud := sineFrame(0, 100), sineFrame(0, 20000)
	// Ráfagas más cortas que AudioShoutEnterFrames, separadas por silencio: nunca entra.
	for burst := 1; burst < config.AudioShoutEnterFrames; burst++ {
		for i := 0; i < burst; i++ {
			if effects.shoutBegin(loud, FrameSamples, settings) {
				t.Fatalf("a burst of %d loud frames activated shout mode", burst)
			}
		}
		effects.shoutBegin(quiet, FrameSamples, settings)
	}
	for i := 1; i <= config.AudioShoutEnterFrames; i++ {
		if got := effects.shoutBegin(loud, FrameSamples, settings); got != (i == config.AudioShoutEnterFrames) {
			t.Fatalf("loud frame %d: shout mode %v", i, got)
		}
	}
}

func TestShoutMix_DefaultLeavesFilteredOutputUntouched(t *testing.T) {
	effects := &Effects{}
	settings := shoutTestSettings() // intensidad 100 %, modo grito apagado
	effects.shoutBegin(sineFrame(0, 20000), FrameSamples, settings)
	filtered := sineFrame(1, 5000)
	want := append([]int16(nil), filtered...)
	effects.shoutMix(filtered, false, settings)
	for i := range filtered {
		if filtered[i] != want[i] {
			t.Fatalf("sample %d changed: %d → %d", i, want[i], filtered[i])
		}
	}
}

func TestShoutBegin_HoldCountsFromTheDelayedFrame(t *testing.T) {
	effects := &Effects{}
	settings := shoutTestSettings()
	settings.ShoutEnabled = true
	settings.ShoutThresholdDB = -12
	settings.ShoutHoldMS = 40 // 2 frames

	quiet, loud := sineFrame(0, 100), sineFrame(0, 20000)
	if effects.shoutBegin(quiet, FrameSamples, settings) {
		t.Fatal("quiet frame activated shout mode")
	}
	// Grito: entra en el último frame fuerte (rampa), sigue en el siguiente (trae ese audio) y 2
	// de hold.
	var got, want []bool
	for i := 1; i <= config.AudioShoutEnterFrames; i++ {
		got = append(got, effects.shoutBegin(loud, FrameSamples, settings))
		want = append(want, i == config.AudioShoutEnterFrames)
	}
	for i := 0; i < 4; i++ {
		got = append(got, effects.shoutBegin(quiet, FrameSamples, settings))
	}
	want = append(want, true, true, true, false)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("shout mode per frame = %v, want %v", got, want)
		}
	}
}

func TestShoutBegin_ClipTriggersBelowThreshold(t *testing.T) {
	settings := shoutTestSettings()
	settings.ShoutEnabled = true
	settings.ShoutThresholdDB = -3 // una voz que satura de a ratos no llega a este RMS
	settings.ShoutHoldMS = 0
	// Voz fuerte (RMS ≈ -12 dBFS) que toca el tope en cada ciclo.
	clippy := func(n int) []int16 {
		frame := sineFrame(n, 12000)
		for i := 0; i < len(frame); i += 200 {
			frame[i] = 32767
		}
		return frame
	}

	run := func(settings FilterSettings) bool {
		effects := &Effects{}
		shout := false
		for n := 0; n < config.AudioShoutEnterFrames; n++ {
			shout = effects.shoutBegin(clippy(n), FrameSamples, settings)
		}
		return shout
	}
	off := settings
	off.ShoutOnClip = false
	if run(off) {
		t.Fatal("sin ShoutOnClip la saturación no activa el modo grito")
	}
	settings.ShoutOnClip = true
	if !run(settings) {
		t.Fatal("con ShoutOnClip la voz que satura tiene que activar el modo grito")
	}
}

func TestFrameLevel(t *testing.T) {
	if db, clipped := FrameLevel(EncodePCM(make([]int16, FrameSamples))); db != LevelFloorDB || clipped {
		t.Fatalf("silencio: %v dB, saturado %v", db, clipped)
	}
	frame := sineFrame(0, 16384) // pico -6 dBFS, RMS ≈ -9 dBFS
	if db, clipped := FrameLevel(EncodePCM(frame)); db < -10 || db > -8 || clipped {
		t.Fatalf("seno a -6 dBFS: %v dB, saturado %v", db, clipped)
	}
	frame[10] = -32768
	if _, clipped := FrameLevel(EncodePCM(frame)); !clipped {
		t.Fatal("un pico al tope tiene que marcar saturación")
	}
}

func TestShouting_ReportsLastFrame(t *testing.T) {
	denoiser, err := NewRNNoise()
	if err != nil {
		t.Fatal(err)
	}
	effects := &Effects{}
	settings := shoutTestSettings()
	settings.ShoutEnabled = true
	settings.ShoutThresholdDB = -12
	settings.ShoutHoldMS = 0
	if _, _, err := PrepareOutgoingFrames(sineFrame(0, 100), denoiser, NoiseGate{}, true, 0.5, settings, effects); err != nil || effects.Shouting() {
		t.Fatalf("quiet frame: shouting=%v err=%v", effects.Shouting(), err)
	}
	frame := 1
	for ; frame <= config.AudioShoutEnterFrames; frame++ {
		if _, _, err := PrepareOutgoingFrames(sineFrame(frame, 20000), denoiser, NoiseGate{}, true, 0.5, settings, effects); err != nil {
			t.Fatal(err)
		}
	}
	if !effects.Shouting() {
		t.Fatal("sustained loud frames: not shouting")
	}
	settings.RNNoiseEnabled = false // sin RNNoise no hay modo grito
	if _, _, err := PrepareOutgoingFrames(sineFrame(frame, 20000), denoiser, NoiseGate{}, true, 0.5, settings, effects); err != nil || effects.Shouting() {
		t.Fatalf("without RNNoise: shouting=%v err=%v", effects.Shouting(), err)
	}
}

func TestSamplesLevelMatchesFrameLevel(t *testing.T) {
	frame := sineFrame(0, 12000)
	want, _ := FrameLevel(EncodePCM(frame))
	if got := SamplesLevel(frame); got != want {
		t.Fatalf("SamplesLevel %v, FrameLevel %v", got, want)
	}
}

func TestEffectsVADReportsLastFrame(t *testing.T) {
	denoiser, err := NewRNNoise()
	if err != nil {
		t.Fatal(err)
	}
	effects := &Effects{}
	settings := shoutTestSettings()
	if _, _, err := PrepareOutgoingFrames(sineFrame(0, 8000), denoiser, NoiseGate{}, true, 0.5, settings, effects); err != nil {
		t.Fatal(err)
	}
	if vad := effects.VAD(); vad < 0 || vad > 1 {
		t.Fatalf("VAD with RNNoise: %v", vad)
	}
	if _, _, err := PrepareOutgoingFrames(sineFrame(1, 8000), denoiser, NoiseGate{}, false, 0.5, settings, effects); err != nil || effects.VAD() != -1 {
		t.Fatalf("VAD without the filter chain: %v (err %v)", effects.VAD(), err)
	}
}
