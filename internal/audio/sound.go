// Soundboard clip decoding: converts uploaded .wav/.mp3 files into mono
// 16-bit PCM at the engine's SampleRate so they can be stored, transferred
// and played back with the same pipeline as the rest of the app.
package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/hajimehoshi/go-mp3"
)

func DecodeSoundFile(extension string, data []byte) ([]byte, error) {
	switch strings.ToLower(extension) {
	case ".mp3":
		return decodeMP3Sound(data)
	case ".wav":
		return decodeWAVSound(data)
	default:
		return nil, fmt.Errorf("formato no soportado (usá .wav o .mp3)")
	}
}

func decodeMP3Sound(data []byte) ([]byte, error) {
	decoder, err := mp3.NewDecoder(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("mp3 inválido: %w", err)
	}
	pcm, err := io.ReadAll(decoder)
	if err != nil {
		return nil, fmt.Errorf("decodificar mp3: %w", err)
	}
	return resamplePCM(pcm, decoder.SampleRate(), 2)
}

func decodeWAVSound(data []byte) ([]byte, error) {
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("wav inválido")
	}
	offset := 12
	var sampleRate, channels, bitsPerSample int
	var pcm []byte
	for offset+8 <= len(data) {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		body := offset + 8
		if chunkSize < 0 || body+chunkSize > len(data) {
			break
		}
		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return nil, fmt.Errorf("wav inválido: fmt corto")
			}
			channels = int(binary.LittleEndian.Uint16(data[body+2 : body+4]))
			sampleRate = int(binary.LittleEndian.Uint32(data[body+4 : body+8]))
			bitsPerSample = int(binary.LittleEndian.Uint16(data[body+14 : body+16]))
		case "data":
			pcm = data[body : body+chunkSize]
		}
		offset = body + chunkSize
		if chunkSize%2 == 1 {
			offset++
		}
	}
	if pcm == nil || sampleRate == 0 || channels == 0 {
		return nil, fmt.Errorf("wav inválido: faltan datos")
	}
	if bitsPerSample != 16 {
		return nil, fmt.Errorf("wav no soportado: usá PCM de 16 bits")
	}
	return resamplePCM(pcm, sampleRate, channels)
}

// resamplePCM downmixes interleaved 16-bit PCM to mono and resamples it to
// the engine's SampleRate using nearest-neighbor interpolation.
func resamplePCM(pcm []byte, sourceRate, channels int) ([]byte, error) {
	if channels <= 0 {
		return nil, fmt.Errorf("audio sin canales")
	}
	frameBytes := 2 * channels
	frames := len(pcm) / frameBytes
	mono := make([]int16, frames)
	for frame := 0; frame < frames; frame++ {
		var sum int32
		for channel := 0; channel < channels; channel++ {
			byteOffset := frame*frameBytes + channel*2
			sum += int32(int16(binary.LittleEndian.Uint16(pcm[byteOffset : byteOffset+2])))
		}
		mono[frame] = int16(sum / int32(channels))
	}
	if sourceRate == SampleRate || len(mono) == 0 {
		out := make([]byte, len(mono)*2)
		for index, sample := range mono {
			binary.LittleEndian.PutUint16(out[index*2:], uint16(sample))
		}
		return out, nil
	}
	ratio := float64(sourceRate) / float64(SampleRate)
	outFrames := int(float64(len(mono)) / ratio)
	out := make([]byte, outFrames*2)
	for index := 0; index < outFrames; index++ {
		source := int(float64(index) * ratio)
		if source >= len(mono) {
			source = len(mono) - 1
		}
		binary.LittleEndian.PutUint16(out[index*2:], uint16(mono[source]))
	}
	return out, nil
}
