package audio

import (
	"encoding/binary"
	"fmt"
	"math"

	"voxmesh/internal/config"
)

const (
	SampleRate   = 48000
	Channels     = 1
	FrameSamples = 960 // 20 ms at 48 kHz.
)

// LevelFloorDB es el piso del medidor de nivel.
const LevelFloorDB = -60

// FrameLevel mide un bloque S16LE: nivel RMS en dBFS (entre LevelFloorDB y 0, la escala del
// medidor y del umbral del modo grito) y si algún pico llegó a la saturación (config.AudioClipPeak).
func FrameLevel(pcm []byte) (db float32, clipped bool) {
	count := len(pcm) / 2
	if count == 0 {
		return LevelFloorDB, false
	}
	var sum float64
	for index := 0; index < count; index++ {
		sample := int16(binary.LittleEndian.Uint16(pcm[index*2:]))
		if sample >= config.AudioClipPeak || sample <= -config.AudioClipPeak {
			clipped = true
		}
		normalized := float64(sample) / math.MaxInt16
		sum += normalized * normalized
	}
	level := 20 * math.Log10(math.Sqrt(sum/float64(count)))
	if math.IsInf(level, -1) || level < LevelFloorDB {
		level = LevelFloorDB
	}
	return float32(math.Min(level, 0)), clipped
}

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
