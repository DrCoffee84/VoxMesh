package audio

import (
	"math"
	"testing"
)

func TestPrepareOutgoingFramesDropsSilentFrameWithRNNoise(t *testing.T) {
	denoiser, err := NewRNNoise()
	if err != nil {
		t.Fatal(err)
	}
	frames, _, err := PrepareOutgoingFrames(make([]int16, RNNoiseFrameSamples), denoiser, NoiseGate{ThresholdDB: -55}, true, 0.5, DefaultFilterSettings(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 0 {
		t.Fatal("silent frame should not be sent")
	}
}

func TestPrepareOutgoingFramesKeepsRawWhenFilterDisabled(t *testing.T) {
	denoiser, err := NewRNNoise()
	if err != nil {
		t.Fatal(err)
	}
	frames, _, err := PrepareOutgoingFrames([]int16{100, -100}, denoiser, NoiseGate{ThresholdDB: 0}, false, 0.5, DefaultFilterSettings(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || len(frames[0]) != 2 || frames[0][0] != 100 || frames[0][1] != -100 {
		t.Fatalf("disabled filter must send unchanged PCM, got %v", frames)
	}
}

func TestPrepareOutgoingFrames_ShoutGoesThroughRNNoise(t *testing.T) {
	denoiser, err := NewRNNoise()
	if err != nil {
		t.Fatal(err)
	}

	settings := DefaultFilterSettings()
	settings.RNNoiseEnabled = true
	settings.VADEnabled = false
	settings.NoiseGateEnabled = false
	ceiling := sendCeiling(settings, true)

	// Varios frames seguidos de grito a full scale: ya no hay bypass, todo pasa por
	// RNNoise y la salida nunca debe dar la vuelta (wrap-around) ni pasar el techo.
	for frame := 0; frame < 10; frame++ {
		shoutSamples := make([]int16, FrameSamples)
		for i := range shoutSamples {
			n := frame*FrameSamples + i
			shoutSamples[i] = int16(32767 * math.Sin(2*math.Pi*300*float64(n)/SampleRate))
		}

		frames, _, err := PrepareOutgoingFrames(shoutSamples, denoiser, NoiseGate{}, true, 0.5, settings, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(frames) != 1 {
			t.Fatal("loud shout must be sent")
		}

		for _, s := range frames[0] {
			if s > ceiling || s < -ceiling {
				t.Fatalf("expected shout peak under %d, got %d", ceiling, s)
			}
		}
	}
}

func TestFloatToInt16Saturates(t *testing.T) {
	cases := map[float32]int16{
		0:        0,
		1234.4:   1234,
		-1234.6:  -1235,
		40000:    32767,
		-40000:   -32768,
		32767.9:  32767,
		-32768.9: -32768,
	}
	for in, want := range cases {
		if got := floatToInt16(in); got != want {
			t.Fatalf("floatToInt16(%v) = %d, want %d", in, got, want)
		}
	}
	if got := floatToInt16(float32(math.NaN())); got != 0 {
		t.Fatalf("floatToInt16(NaN) = %d, want 0", got)
	}
}

func TestPrepareOutgoingFrames_PeakCeiling(t *testing.T) {
	// Filter disabled: safety ceiling ~27000
	loudSamples := make([]int16, FrameSamples)
	for i := range loudSamples {
		loudSamples[i] = 32000
	}

	frames, _, err := PrepareOutgoingFrames(loudSamples, nil, NoiseGate{}, false, 0.5, DefaultFilterSettings(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 {
		t.Fatal("unfiltered frame must be sent")
	}

	for _, s := range frames[0] {
		if s > 27000 || s < -27000 {
			t.Fatalf("expected ceiling at 27000, got %d", s)
		}
	}
}

func TestPrepareOutgoingFrames_TanhLimiter(t *testing.T) {
	effects := &Effects{}
	settings := DefaultFilterSettings()
	settings.LimiterEnabled = true
	settings.LimiterThreshold = -6 // ~ -6 dBFS ≈ 16422
	settings.RNNoiseEnabled = false
	settings.NoiseGateEnabled = false

	// Input with high amplitude
	samples := make([]int16, FrameSamples)
	for i := range samples {
		samples[i] = 30000
	}

	frames, _, err := PrepareOutgoingFrames(samples, nil, NoiseGate{}, true, 0.5, settings, effects)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 {
		t.Fatal("frame must be sent")
	}

	// With -6dB threshold and tanh compression, peak should be well below 20000
	if frames[0][0] >= 20000 {
		t.Fatalf("expected tanh limiter to compress below 20000, got %d", frames[0][0])
	}
}

func TestPrepareOutgoingFrames_PreRollSendsPreviousFramesWithFadeIn(t *testing.T) {
	effects := &Effects{}
	settings := DefaultFilterSettings()
	settings.RNNoiseEnabled = false
	settings.NoiseGateEnabled = true
	settings.GateHoldMS = 0
	gate := NoiseGate{ThresholdDB: -30}
	constant := func(value int16) []int16 {
		frame := make([]int16, FrameSamples)
		for i := range frame {
			frame[i] = value
		}
		return frame
	}

	// Tres frames bajo la compuerta: no sale nada, quedan los 2 últimos como pre-roll.
	for _, level := range []int16{100, 200, 300} {
		frames, _, err := PrepareOutgoingFrames(constant(level), nil, gate, true, 0.5, settings, effects)
		if err != nil || len(frames) != 0 {
			t.Fatalf("closed gate sent %d frames (err %v)", len(frames), err)
		}
	}
	frames, started, err := PrepareOutgoingFrames(constant(10000), nil, gate, true, 0.5, settings, effects)
	if err != nil {
		t.Fatal(err)
	}
	if !started || len(frames) != 1+settings.PreRollFrames {
		t.Fatalf("expected pre-roll + current (%d frames), got %d (started %v)", 1+settings.PreRollFrames, len(frames), started)
	}
	if frames[0][FrameSamples-1] != 200 || frames[1][FrameSamples-1] != 300 || frames[2][0] != 10000 {
		t.Fatalf("pre-roll out of order: %d %d %d", frames[0][FrameSamples-1], frames[1][FrameSamples-1], frames[2][0])
	}
	// El fade-in va en el primer frame del pre-roll, no en el actual.
	if frames[0][0] != 0 || frames[2][0] != 10000 {
		t.Fatalf("fade-in not applied to the first pre-roll frame: %d / %d", frames[0][0], frames[2][0])
	}

	// Con la compuerta abierta sale solo el frame actual.
	frames, started, _ = PrepareOutgoingFrames(constant(10000), nil, gate, true, 0.5, settings, effects)
	if started || len(frames) != 1 {
		t.Fatalf("open gate should send just the current frame, got %d (started %v)", len(frames), started)
	}
}

func TestProcessRecording_MatchesSendChain(t *testing.T) {
	settings := DefaultFilterSettings()
	settings.RNNoiseEnabled = false
	settings.NoiseGateEnabled = true
	settings.GateHoldMS = 0
	gate := NoiseGate{ThresholdDB: -30}
	var input [][]int16
	for i := 0; i < 15; i++ {
		amplitude := 50.0
		if i >= 5 && i < 10 {
			amplitude = 16000
		}
		input = append(input, sineFrame(i, amplitude))
	}
	var recording []int16
	for _, frame := range input {
		recording = append(recording, frame...)
	}

	effects := &Effects{}
	var sent []int16
	first := -1
	for i, frame := range input {
		frames, _, err := PrepareOutgoingFrames(append([]int16(nil), frame...), nil, gate, true, 0.5, settings, effects)
		if err != nil {
			t.Fatal(err)
		}
		if len(frames) > 0 && first < 0 {
			first = i - (len(frames) - 1)
		}
		for _, f := range frames {
			sent = append(sent, f...)
		}
	}
	if first < 0 {
		t.Fatal("voice was never sent")
	}
	want := make([]int16, len(recording))
	copy(want[first*FrameSamples:], sent)

	got, err := ProcessRecording(EncodePCM(append(recording, 1, 2, 3)), nil, gate, true, 0.5, settings)
	if err != nil {
		t.Fatal(err)
	}
	gotSamples, err := DecodePCM(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSamples) != len(want) {
		t.Fatalf("recording length %d, want %d", len(gotSamples), len(want))
	}
	for i := range want {
		if gotSamples[i] != want[i] {
			t.Fatalf("sample %d: %d, want %d", i, gotSamples[i], want[i])
		}
	}
}

func TestEffectsReset_DropsStalePreRoll(t *testing.T) {
	settings := DefaultFilterSettings()
	settings.RNNoiseEnabled = false
	settings.NoiseGateEnabled = true
	settings.GateHoldMS = 0
	gate := NoiseGate{ThresholdDB: -30}
	effects := &Effects{}
	for i := 0; i < 3; i++ {
		if frames, _, err := PrepareOutgoingFrames(sineFrame(i, 50), nil, gate, true, 0.5, settings, effects); err != nil || len(frames) != 0 {
			t.Fatalf("closed gate sent %d frames (err %v)", len(frames), err)
		}
	}
	effects.Reset()
	frames, started, err := PrepareOutgoingFrames(sineFrame(3, 16000), nil, gate, true, 0.5, settings, effects)
	if err != nil {
		t.Fatal(err)
	}
	if !started || len(frames) != 1 {
		t.Fatalf("after Reset the first open frame must go alone, got %d frames (started %v)", len(frames), started)
	}
}

func TestCompressorUsesEnvelopeNotWaveshaping(t *testing.T) {
	settings := FilterSettings{
		CompressorEnabled:   true,
		CompressorThreshold: -18,
		CompressorRatio:     3,
		CompressorAttackMS:  5,
		CompressorReleaseMS: 100,
	}
	effects := &Effects{}
	var input, output []int16
	for frame := 0; frame < 10; frame++ {
		in := sineFrame(frame, 16400) // ~ -6 dBFS de pico
		out := append([]int16(nil), in...)
		effects.ApplySamples(out, settings)
		input, output = in, out
	}
	// Con la envolvente estable la ganancia es la misma en toda la onda: la salida es la misma
	// senoidal escalada (el compresor por muestra la deformaba).
	gain := float64(peakOf(output)) / float64(peakOf(input))
	for i := range input {
		if abs := math.Abs(float64(input[i])); abs > 2000 {
			if g := float64(output[i]) / float64(input[i]); math.Abs(g-gain) > 0.01 {
				t.Fatalf("sample %d: gain %.3f, steady gain %.3f (waveshaping)", i, g, gain)
			}
		}
	}
	// -6 dBFS con umbral -18 y ratio 3: sale en -14 dBFS → ganancia ≈ -8 dB.
	if want := math.Pow(10, -8.0/20); math.Abs(gain-want) > 0.03 {
		t.Fatalf("steady gain %.3f, want %.3f", gain, want)
	}
}

func TestHighPassKeepsUnityGainWithInputGain(t *testing.T) {
	// Con ganancia de entrada el pasa-altos guardaba la muestra cruda como x[n-1]: un tono en la
	// banda de paso salía con mucha más ganancia de la pedida.
	settings := FilterSettings{GainDB: 6, HighPassEnabled: true, HighPassHz: 80}
	effects := &Effects{}
	var input, output []int16
	for frame := 0; frame < 5; frame++ {
		in := sineFrame(frame, 4000) // 437 Hz: banda de paso
		out := append([]int16(nil), in...)
		effects.ApplySamples(out, settings)
		input, output = in, out
	}
	ratio := frameRMS(output) / frameRMS(input)
	if want := math.Pow(10, 6.0/20); math.Abs(ratio-want) > 0.1 {
		t.Fatalf("pass-band gain %.2f, want %.2f", ratio, want)
	}
}

func TestEffectsApplyOutput_SkipsNoOpChain(t *testing.T) {
	settings := FilterSettings{LimiterEnabled: true, LimiterThreshold: -1}
	frame := sineFrame(3, 20000)
	original := append([]int16(nil), frame...)
	(&Effects{}).ApplyOutput(frame, settings)
	for i := range frame {
		if frame[i] != original[i] {
			t.Fatalf("no-op output chain changed sample %d: %d -> %d", i, original[i], frame[i])
		}
	}
	// Por encima del umbral el limiter actúa.
	loud := sineFrame(3, 32000)
	(&Effects{}).ApplyOutput(loud, settings)
	if peakOf(loud) >= 32000 {
		t.Fatalf("limiter did not act on a loud frame: peak %d", peakOf(loud))
	}
}
