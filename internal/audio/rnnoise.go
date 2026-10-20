package audio

import (
	"fmt"
	"math"
	"sync"

	rnnoise "github.com/VoiceBlender/rnnoise-go"
	rnmodel "github.com/VoiceBlender/rnnoise-go/model"

	"voxmesh/internal/config"
)

const RNNoiseFrameSamples = 480

type RNNoise struct {
	mu       sync.Mutex
	denoiser *rnnoise.Denoiser
}

func NewRNNoise() (*RNNoise, error) {
	denoiser, err := rnnoise.New(rnnoise.Options{
		SampleRate: SampleRate,
		Model:      rnmodel.MustLoad(),
	})
	if err != nil {
		return nil, fmt.Errorf("inicializar RNNoise: %w", err)
	}
	return &RNNoise{denoiser: denoiser}, nil
}

func (r *RNNoise) ProcessPCM(data []byte, gate NoiseGate, vadThreshold float32) ([]byte, bool, float32, error) {
	return r.ProcessPCMWithOptions(data, gate, vadThreshold, true, true)
}

func (r *RNNoise) ProcessPCMWithOptions(data []byte, gate NoiseGate, vadThreshold float32, vadEnabled, gateEnabled bool) ([]byte, bool, float32, error) {
	if r == nil || r.denoiser == nil {
		return nil, false, 0, fmt.Errorf("RNNoise no está inicializado")
	}
	samples, err := DecodePCM(data)
	if err != nil {
		return nil, false, 0, err
	}
	if len(samples) == 0 {
		return nil, false, 0, nil
	}
	outSamples, hasSpeech, maxVAD, err := r.ProcessSamplesWithOptions(samples, gate, vadThreshold, vadEnabled, gateEnabled)
	if err != nil {
		return nil, false, 0, err
	}
	return EncodePCM(outSamples), hasSpeech, maxVAD, nil
}

// ProcessSamplesWithOptions procesa directamente muestras int16 sin allocations redundantes de codificación/decodificación.
func (r *RNNoise) ProcessSamplesWithOptions(samples []int16, gate NoiseGate, vadThreshold float32, vadEnabled, gateEnabled bool) ([]int16, bool, float32, error) {
	if r == nil || r.denoiser == nil {
		return nil, false, 0, fmt.Errorf("RNNoise no está inicializado")
	}
	if len(samples) == 0 {
		return nil, false, 0, nil
	}

	output := make([]int16, len(samples))
	maxVAD := float32(0)
	hasSpeech := !vadEnabled

	// Usamos la API float32 para controlar nosotros la vuelta a int16:
	// ProcessInt16 hace int16(v) sin clamp y un pico > 32767 da la vuelta (wrap-around).
	var inputFrame [RNNoiseFrameSamples]float32
	var outputFrame [RNNoiseFrameSamples]float32
	var resultFrame [RNNoiseFrameSamples]int16

	r.mu.Lock()
	defer r.mu.Unlock()
	for start := 0; start < len(samples); start += RNNoiseFrameSamples {
		end := start + RNNoiseFrameSamples
		if end > len(samples) {
			end = len(samples)
		}
		clear(inputFrame[:])
		for i, sample := range samples[start:end] {
			inputFrame[i] = float32(sample)
		}
		vad, err := r.denoiser.Process(outputFrame[:], inputFrame[:])
		if err != nil {
			return nil, false, 0, fmt.Errorf("procesar frame RNNoise: %w", err)
		}
		for i, value := range outputFrame {
			resultFrame[i] = floatToInt16(value)
		}
		if vad > maxVAD {
			maxVAD = vad
		}
		gateOpen := true
		if gateEnabled {
			gateOpen = gate.Apply(resultFrame[:])
		}
		if (!vadEnabled || vad >= vadThreshold) && gateOpen {
			hasSpeech = true
		}
		copy(output[start:end], resultFrame[:end-start])
	}
	return output, hasSpeech, maxVAD, nil
}

// floatToInt16 redondea una muestra float en escala S16 y la satura al rango int16.
func floatToInt16(value float32) int16 {
	if value != value { // NaN
		return 0
	}
	if value >= math.MaxInt16 {
		return math.MaxInt16
	}
	if value <= math.MinInt16 {
		return math.MinInt16
	}
	return int16(math.Round(float64(value)))
}

func PrepareOutgoingPCM(data []byte, denoiser *RNNoise, gate NoiseGate, filterEnabled bool, vadThreshold float32) ([]byte, bool, error) {
	settings := DefaultFilterSettings()
	return PrepareOutgoingPCMWithFilters(data, denoiser, gate, filterEnabled, vadThreshold, settings, nil)
}

func PrepareOutgoingPCMWithFilters(data []byte, denoiser *RNNoise, gate NoiseGate, filterEnabled bool, vadThreshold float32, settings FilterSettings, effects *Effects) ([]byte, bool, error) {
	if len(data) == 0 {
		return nil, false, nil
	}
	samples, err := DecodePCM(data)
	if err != nil {
		return nil, false, err
	}

	if !filterEnabled {
		ApplyPeakCeiling(samples, config.AudioSendPeakCeiling)
		return EncodePCM(samples), true, nil
	}

	if effects != nil {
		effects.ApplySamples(samples, settings)
	}

	if vadThreshold <= 0 {
		vadThreshold = 0.5
	}

	// Ya no hay bypass por grito: RNNoise tiene 20 ms de retardo interno (Delay() = 2*480)
	// y saltearlo frame a frame desordenaba y duplicaba audio. El "cirkci" original venía
	// del wrap-around de int16 en ProcessInt16, que ahora evita el clamp de floatToInt16.

	// RNNoise desactivado → camino sin RNNoise
	if !settings.RNNoiseEnabled || denoiser == nil {
		gateOpen := !settings.NoiseGateEnabled || gate.Open(samples)
		keepOpen := !settings.NoiseGateEnabled || gateOpen
		if effects != nil {
			keepOpen = effects.KeepVoiceOpen(true, gateOpen, settings)
			effects.SmoothTransition(samples, keepOpen)
		}
		ApplyPeakCeiling(samples, config.AudioSendPeakCeiling)
		if !keepOpen {
			return nil, false, nil
		}
		return EncodePCM(samples), true, nil
	}

	// Voz (incluidos gritos) → RNNoise ON, siempre, para mantener su estado continuo
	filteredSamples, hasSpeech, _, err := denoiser.ProcessSamplesWithOptions(samples, gate, vadThreshold, settings.VADEnabled, false)
	if err != nil {
		return nil, false, err
	}
	samples = filteredSamples

	gateOpen := !settings.NoiseGateEnabled || gate.Open(samples)
	keepOpen := false
	if effects != nil {
		keepOpen = effects.KeepVoiceOpen(hasSpeech, gateOpen, settings)
		effects.SmoothTransition(samples, keepOpen)
	} else {
		keepOpen = hasSpeech && gateOpen
	}

	ApplyPeakCeiling(samples, config.AudioSendPeakCeiling)
	if !keepOpen {
		return nil, false, nil
	}
	return EncodePCM(samples), true, nil
}
