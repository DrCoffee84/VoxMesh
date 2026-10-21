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
