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
	var pcm []byte
	totalBytes := decoder.Length()
	if totalBytes > 0 && totalBytes < 500*1024*1024 {
		pcm = make([]byte, totalBytes)
		n, readErr := io.ReadFull(decoder, pcm)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("decodificar mp3: %w", readErr)
		}
		pcm = pcm[:n]
	} else {
		var readErr error
		pcm, readErr = io.ReadAll(decoder)
		if readErr != nil {
			return nil, fmt.Errorf("decodificar mp3: %w", readErr)
		}
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
// the engine's SampleRate in a single pass directly into the output slice.
func resamplePCM(pcm []byte, sourceRate, channels int) ([]byte, error) {
	if channels <= 0 {
		return nil, fmt.Errorf("audio sin canales")
	}
	frameBytes := 2 * channels
	totalFrames := len(pcm) / frameBytes
	if totalFrames == 0 {
		return nil, fmt.Errorf("audio vacío")
	}

	if sourceRate == SampleRate && channels == 1 {
		return append([]byte(nil), pcm...), nil
	}

	ratio := float64(sourceRate) / float64(SampleRate)
	outFrames := int(float64(totalFrames) / ratio)
	if outFrames <= 0 {
		return nil, fmt.Errorf("audio demasiado corto")
	}
	out := make([]byte, outFrames*2)

	for index := 0; index < outFrames; index++ {
		sourceFrame := int(float64(index) * ratio)
		if sourceFrame >= totalFrames {
			sourceFrame = totalFrames - 1
		}
		var sum int32
		offset := sourceFrame * frameBytes
		for c := 0; c < channels; c++ {
			sample := int16(binary.LittleEndian.Uint16(pcm[offset+c*2 : offset+c*2+2]))
			sum += int32(sample)
		}
		monoSample := int16(sum / int32(channels))
		binary.LittleEndian.PutUint16(out[index*2:index*2+2], uint16(monoSample))
	}
	return out, nil
}
