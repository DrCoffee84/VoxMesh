package audio

import (
	"fmt"
	"sync"

	rnnoise "github.com/VoiceBlender/rnnoise-go"
	rnmodel "github.com/VoiceBlender/rnnoise-go/model"
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

	var inputFrame [RNNoiseFrameSamples]int16
	var outputFrame [RNNoiseFrameSamples]int16

	r.mu.Lock()
	defer r.mu.Unlock()
	for start := 0; start < len(samples); start += RNNoiseFrameSamples {
		end := start + RNNoiseFrameSamples
		if end > len(samples) {
			end = len(samples)
		}
		clear(inputFrame[:])
		copy(inputFrame[:], samples[start:end])
		vad, err := r.denoiser.ProcessInt16(outputFrame[:], inputFrame[:])
		if err != nil {
			return nil, false, 0, fmt.Errorf("procesar frame RNNoise: %w", err)
		}
		if vad > maxVAD {
			maxVAD = vad
		}
		gateOpen := true
		if gateEnabled {
			gateOpen = gate.Apply(outputFrame[:])
		}
		if (!vadEnabled || vad >= vadThreshold) && gateOpen {
			hasSpeech = true
		}
		copy(output[start:end], outputFrame[:end-start])
	}
	return output, hasSpeech, maxVAD, nil
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
		ApplyPeakCeiling(samples, 27000)
		return EncodePCM(samples), true, nil
	}

	if effects != nil {
		effects.ApplySamples(samples, settings)
	}

	if vadThreshold <= 0 {
		vadThreshold = 0.5
	}

	// Detectar grito fuerte para no pasar por RNNoise (evita el "cirkci")
	peak := int16(0)
	for _, s := range samples {
		a := s
		if a < 0 {
			a = -a
		}
		if a > peak {
			peak = a
		}
	}
	isLoudShout := peak > 22000

	// Grito fuerte o RNNoise desactivado → camino sin RNNoise
	if !settings.RNNoiseEnabled || isLoudShout || denoiser == nil {
		if isLoudShout {
			ApplyPeakCeiling(samples, 24000)
		}
		gateOpen := !settings.NoiseGateEnabled || gate.Open(samples)
		keepOpen := !settings.NoiseGateEnabled || gateOpen
		if effects != nil {
			keepOpen = effects.KeepVoiceOpen(true, gateOpen, settings)
			effects.SmoothTransition(samples, keepOpen)
		}
		ApplyPeakCeiling(samples, 27000)
		if !keepOpen {
			return nil, false, nil
		}
		return EncodePCM(samples), true, nil
	}

	// Voz normal → RNNoise ON
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

	ApplyPeakCeiling(samples, 27000)
	if !keepOpen {
		return nil, false, nil
	}
	return EncodePCM(samples), true, nil
}
