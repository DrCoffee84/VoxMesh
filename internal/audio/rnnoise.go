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

	output := make([]int16, len(samples))
	maxVAD := float32(0)
	hasSpeech := !vadEnabled
	r.mu.Lock()
	defer r.mu.Unlock()
	for start := 0; start < len(samples); start += RNNoiseFrameSamples {
		end := start + RNNoiseFrameSamples
		if end > len(samples) {
			end = len(samples)
		}
		inputFrame := make([]int16, RNNoiseFrameSamples)
		outputFrame := make([]int16, RNNoiseFrameSamples)
		copy(inputFrame, samples[start:end])
		vad, err := r.denoiser.ProcessInt16(outputFrame, inputFrame)
		if err != nil {
			return nil, false, 0, fmt.Errorf("procesar frame RNNoise: %w", err)
		}
		if vad > maxVAD {
			maxVAD = vad
		}
		gateOpen := true
		if gateEnabled {
			gateOpen = gate.Apply(outputFrame)
		}
		if (!vadEnabled || vad >= vadThreshold) && gateOpen {
			hasSpeech = true
		}
		copy(output[start:end], outputFrame[:end-start])
	}
	return EncodePCM(output), hasSpeech, maxVAD, nil
}

func PrepareOutgoingPCM(data []byte, denoiser *RNNoise, gate NoiseGate, filterEnabled bool, vadThreshold float32) ([]byte, bool, error) {
	settings := DefaultFilterSettings()
	return PrepareOutgoingPCMWithFilters(data, denoiser, gate, filterEnabled, vadThreshold, settings, nil)
}

func PrepareOutgoingPCMWithFilters(data []byte, denoiser *RNNoise, gate NoiseGate, filterEnabled bool, vadThreshold float32, settings FilterSettings, effects *Effects) ([]byte, bool, error) {
	if !filterEnabled {
		return append([]byte(nil), data...), true, nil
	}
	if effects != nil {
		var err error
		data, err = effects.Apply(data, settings)
		if err != nil {
			return nil, false, err
		}
	}
	if vadThreshold <= 0 {
		vadThreshold = 0.5
	}
	if !settings.RNNoiseEnabled {
		samples, err := DecodePCM(data)
		if err != nil {
			return nil, false, err
		}
		gateOpen := !settings.NoiseGateEnabled || gate.Open(samples)
		keepOpen := !settings.NoiseGateEnabled || gateOpen
		if effects != nil {
			keepOpen = effects.KeepVoiceOpen(true, gateOpen, settings)
		}
		if !keepOpen {
			return nil, false, nil
		}
		return EncodePCM(samples), true, nil
	}
	filtered, hasSpeech, _, err := denoiser.ProcessPCMWithOptions(data, gate, vadThreshold, settings.VADEnabled, false)
	if err != nil {
		return nil, false, err
	}
	samples, err := DecodePCM(filtered)
	if err != nil {
		return nil, false, err
	}
	gateOpen := !settings.NoiseGateEnabled || gate.Open(samples)
	if effects != nil && !effects.KeepVoiceOpen(hasSpeech, gateOpen, settings) {
		return nil, false, nil
	}
	if effects == nil && (!hasSpeech || !gateOpen) {
		return nil, false, nil
	}
	return filtered, true, nil
}
