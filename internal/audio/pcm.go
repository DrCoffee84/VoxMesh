package audio

import (
	"encoding/binary"
	"fmt"
)

const (
	SampleRate   = 48000
	Channels     = 1
	FrameSamples = 960 // 20 ms at 48 kHz.
)

func EncodePCM(samples []int16) []byte {
	encoded := make([]byte, len(samples)*2)
	for index, sample := range samples {
		binary.LittleEndian.PutUint16(encoded[index*2:], uint16(sample))
	}
	return encoded
}

func DecodePCM(data []byte) ([]int16, error) {
	if len(data)%2 != 0 {
		return nil, fmt.Errorf("PCM payload has an odd byte count")
	}
	samples := make([]int16, len(data)/2)
	for index := range samples {
		samples[index] = int16(binary.LittleEndian.Uint16(data[index*2:]))
	}
	return samples, nil
}

// Source is implemented by a microphone backend. Keeping it here lets the
// UDP and gate code stay independent from platform-specific capture APIs.
type Source interface {
	ReadFrame() ([]int16, error)
	Close() error
}

func ProcessFrame(source Source, gate NoiseGate) ([]byte, bool, error) {
	return ProcessOutgoingFrame(source, gate, true)
}

func ProcessOutgoingFrame(source Source, gate NoiseGate, filterEnabled bool) ([]byte, bool, error) {
	samples, err := source.ReadFrame()
	if err != nil {
		return nil, false, err
	}
	open := true
	if filterEnabled {
		open = gate.Apply(samples)
	}
	if !open {
		return nil, false, nil
	}
	return EncodePCM(samples), true, nil
}

func ProcessOutgoingFrameWithRNNoise(source Source, denoiser *RNNoise, gate NoiseGate, filterEnabled bool, vadThreshold float32) ([]byte, bool, error) {
	samples, err := source.ReadFrame()
	if err != nil {
		return nil, false, err
	}
	return PrepareOutgoingPCM(EncodePCM(samples), denoiser, gate, filterEnabled, vadThreshold)
}

func PreparePCM(data []byte, gate NoiseGate, filterEnabled bool) ([]byte, error) {
	if !filterEnabled {
		return append([]byte(nil), data...), nil
	}
	samples, err := DecodePCM(data)
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(samples); start += FrameSamples {
		end := start + FrameSamples
		if end > len(samples) {
			end = len(samples)
		}
		gate.Apply(samples[start:end])
	}
	return EncodePCM(samples), nil
}
