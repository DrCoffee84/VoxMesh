package audio

import (
	"math"
	"testing"

	"voxmesh/internal/config"
)

func TestPrepareOutgoingPCMDropsSilentFrameWithRNNoise(t *testing.T) {
	denoiser, err := NewRNNoise()
	if err != nil {
		t.Fatal(err)
	}
	raw := EncodePCM(make([]int16, RNNoiseFrameSamples))
	output, shouldSend, err := PrepareOutgoingPCM(raw, denoiser, NoiseGate{ThresholdDB: -55}, true, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if shouldSend {
		t.Fatal("silent frame should not be sent")
	}
	if output != nil {
		t.Fatal("silent frame should not produce a packet payload")
	}
}

func TestPrepareOutgoingPCMKeepsRawWhenFilterDisabled(t *testing.T) {
	denoiser, err := NewRNNoise()
	if err != nil {
		t.Fatal(err)
	}
	raw := EncodePCM([]int16{100, -100})
	output, shouldSend, err := PrepareOutgoingPCM(raw, denoiser, NoiseGate{ThresholdDB: 0}, false, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if !shouldSend || string(output) != string(raw) {
		t.Fatal("disabled filter must send unchanged PCM")
	}
}

func TestPrepareOutgoingPCMWithFilters_ShoutGoesThroughRNNoise(t *testing.T) {
	denoiser, err := NewRNNoise()
	if err != nil {
		t.Fatal(err)
	}

	settings := DefaultFilterSettings()
	settings.RNNoiseEnabled = true
	settings.VADEnabled = false
	settings.NoiseGateEnabled = false

	// Varios frames seguidos de grito a full scale: ya no hay bypass, todo pasa por
	// RNNoise y la salida nunca debe dar la vuelta (wrap-around) ni pasar el techo.
	for frame := 0; frame < 10; frame++ {
		shoutSamples := make([]int16, FrameSamples)
		for i := range shoutSamples {
			n := frame*FrameSamples + i
			shoutSamples[i] = int16(32767 * math.Sin(2*math.Pi*300*float64(n)/SampleRate))
		}
		raw := EncodePCM(shoutSamples)

		output, shouldSend, err := PrepareOutgoingPCMWithFilters(raw, denoiser, NoiseGate{}, true, 0.5, settings, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !shouldSend {
			t.Fatal("loud shout must be sent")
		}

		outSamples, err := DecodePCM(output)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range outSamples {
			if s > config.AudioSendPeakCeiling || s < -config.AudioSendPeakCeiling {
				t.Fatalf("expected shout peak under %d, got %d", config.AudioSendPeakCeiling, s)
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

func TestPrepareOutgoingPCMWithFilters_PeakCeiling(t *testing.T) {
	// Filter disabled: safety ceiling ~27000
	loudSamples := make([]int16, FrameSamples)
	for i := range loudSamples {
		loudSamples[i] = 32000
	}
	raw := EncodePCM(loudSamples)

	output, shouldSend, err := PrepareOutgoingPCMWithFilters(raw, nil, NoiseGate{}, false, 0.5, DefaultFilterSettings(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !shouldSend {
		t.Fatal("unfiltered frame must be sent")
	}

	outSamples, err := DecodePCM(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range outSamples {
		if s > 27000 || s < -27000 {
			t.Fatalf("expected ceiling at 27000, got %d", s)
		}
	}
}

func TestPrepareOutgoingPCMWithFilters_TanhLimiter(t *testing.T) {
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
	raw := EncodePCM(samples)

	output, shouldSend, err := PrepareOutgoingPCMWithFilters(raw, nil, NoiseGate{}, true, 0.5, settings, effects)
	if err != nil {
		t.Fatal(err)
	}
	if !shouldSend {
		t.Fatal("frame must be sent")
	}

	outSamples, err := DecodePCM(output)
	if err != nil {
		t.Fatal(err)
	}
	// With -6dB threshold and tanh compression, peak should be well below 20000
	if outSamples[0] >= 20000 {
		t.Fatalf("expected tanh limiter to compress below 20000, got %d", outSamples[0])
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
	if !started || len(frames) != 1+config.AudioPreRollFrames {
		t.Fatalf("expected pre-roll + current (%d frames), got %d (started %v)", 1+config.AudioPreRollFrames, len(frames), started)
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

// applyDynamicsReference es applyDynamicsFast antes de calcular Log10 solo cuando hace falta.
func applyDynamicsReference(value float64, settings FilterSettings, limit float64) float64 {
	level := math.Abs(value)
	if level == 0 {
		return 0
	}
	db := 20 * math.Log10(level)
	if settings.CompressorEnabled && db > float64(settings.CompressorThreshold) {
		ratio := math.Max(float64(settings.CompressorRatio), 1)
		value *= math.Pow(10, (float64(settings.CompressorThreshold)+(db-float64(settings.CompressorThreshold))/ratio-db)/20)
	}
	if settings.ExpanderEnabled && db < float64(settings.ExpanderThreshold) {
		ratio := math.Max(float64(settings.ExpanderRatio), 1)
		value *= math.Pow(10, (float64(settings.ExpanderThreshold)+(db-float64(settings.ExpanderThreshold))*ratio-db)/20)
	}
	if settings.LimiterEnabled && limit > 0 {
		absVal := math.Abs(value)
		if absVal > limit {
			sign := 1.0
			if value < 0 {
				sign = -1.0
			}
			value = sign * (limit + math.Tanh((absVal-limit)*1.5)*0.1*limit)
		}
	}
	return value
}

func TestApplyDynamicsFast_BitExactWithoutLog10(t *testing.T) {
	limiterOnly := DefaultFilterSettings()
	withCompressor := DefaultFilterSettings()
	withCompressor.CompressorEnabled = true
	withCompressor.ExpanderEnabled = true
	limit := math.Pow(10, -1.0/20)
	for _, settings := range []FilterSettings{limiterOnly, withCompressor} {
		for i := -40000; i <= 40000; i += 37 {
			value := float64(i) / math.MaxInt16
			if got, want := applyDynamicsFast(value, settings, limit), applyDynamicsReference(value, settings, limit); got != want {
				t.Fatalf("value %v: got %v, want %v", value, got, want)
			}
		}
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
