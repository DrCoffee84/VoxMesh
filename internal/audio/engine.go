//go:build cgo

package audio

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/gen2brain/malgo"
)

const (
	streamFrameBytes = FrameSamples * 2 * Channels
	antiClickSamples = 64 // ~1.33 ms at 48 kHz
)

type audioStream struct {
	buffer     []byte
	buffering  bool
	lastSample int16
	lastActive time.Time
}

type Engine struct {
	context       *malgo.AllocatedContext
	capture       *malgo.Device
	playback      *malgo.Device
	captureFrames chan []byte
	captureBuffer []byte
	captureMu     sync.Mutex
	stopOnce      sync.Once

	streams    map[string]*audioStream
	mixBuffer  []int32
	playbackMu sync.Mutex
}

func applyFadeIn(pcm []byte, ramp int) {
	samples := len(pcm) / 2
	if samples > ramp {
		samples = ramp
	}
	for i := 0; i < samples; i++ {
		orig := int16(binary.LittleEndian.Uint16(pcm[i*2:]))
		factor := float64(i) / float64(samples)
		scaled := int16(float64(orig) * factor)
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(scaled))
	}
}

func softClip(sample int32) int16 {
	if sample > 30000 {
		excess := float64(sample - 30000)
		compressed := 30000.0 + 2700.0*(excess/(excess+3000.0))
		if compressed > 32767 {
			return 32767
		}
		return int16(compressed)
	}
	if sample < -30000 {
		excess := float64(-sample - 30000)
		compressed := -(30000.0 + 2700.0*(excess/(excess+3000.0)))
		if compressed < -32768 {
			return -32768
		}
		return int16(compressed)
	}
	return int16(sample)
}

func NewEngine(inputName, outputName string, onFrame func([]byte)) (*Engine, error) {
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("inicializar motor de audio: %w", err)
	}
	engine := &Engine{
		context:       context,
		captureFrames: make(chan []byte, 8),
		streams:       make(map[string]*audioStream),
	}
	inputID, err := findDeviceID(context, malgo.Capture, inputName)
	if err != nil {
		engine.Stop()
		return nil, err
	}
	outputID, err := findDeviceID(context, malgo.Playback, outputName)
	if err != nil {
		engine.Stop()
		return nil, err
	}

	captureConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	captureConfig.Capture.Format = malgo.FormatS16
	captureConfig.Capture.Channels = Channels
	captureConfig.SampleRate = SampleRate
	captureConfig.PerformanceProfile = malgo.LowLatency
	if inputID != nil {
		captureConfig.Capture.DeviceID = inputID.Pointer()
	}
	capture, err := malgo.InitDevice(context.Context, captureConfig, malgo.DeviceCallbacks{
		Data: func(_, input []byte, _ uint32) {
			engine.captureMu.Lock()
			engine.captureBuffer = append(engine.captureBuffer, input...)
			for len(engine.captureBuffer) >= streamFrameBytes {
				frame := make([]byte, streamFrameBytes)
				copy(frame, engine.captureBuffer[:streamFrameBytes])
				engine.captureBuffer = engine.captureBuffer[streamFrameBytes:]
				select {
				case engine.captureFrames <- frame:
				default:
				}
			}
			engine.captureMu.Unlock()
		},
	})
	if err != nil {
		engine.Stop()
		return nil, fmt.Errorf("abrir micrófono: %w", err)
	}
	engine.capture = capture

	playbackConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	playbackConfig.Playback.Format = malgo.FormatS16
	playbackConfig.Playback.Channels = Channels
	playbackConfig.SampleRate = SampleRate
	playbackConfig.PerformanceProfile = malgo.LowLatency
	if outputID != nil {
		playbackConfig.Playback.DeviceID = outputID.Pointer()
	}
	playback, err := malgo.InitDevice(context.Context, playbackConfig, malgo.DeviceCallbacks{
		Data: func(output, _ []byte, _ uint32) {
			engine.playbackMu.Lock()
			defer engine.playbackMu.Unlock()

			samples := len(output) / 2
			if samples == 0 {
				return
			}

			// Asegurar buffer de mezcla con capacidad suficiente sin nuevas asignaciones
			if len(engine.mixBuffer) < samples {
				engine.mixBuffer = make([]int32, samples)
			} else {
				for i := 0; i < samples; i++ {
					engine.mixBuffer[i] = 0
				}
			}

			now := time.Now()

			for id, stream := range engine.streams {
				if len(stream.buffer) == 0 {
					// Purgar streams inactivos tras 2 segundos sin paquetes de audio
					if now.Sub(stream.lastActive) > 2*time.Second {
						delete(engine.streams, id)
						continue
					}
					// Si sufrió underrun (se quedó sin audio en este ciclo)
					if !stream.buffering {
						stream.buffering = true
						if stream.lastSample != 0 {
							decay := stream.lastSample
							for i := 0; i < antiClickSamples && i < samples; i++ {
								decay = int16(float64(decay) * 0.8)
								engine.mixBuffer[i] += int32(decay)
							}
							stream.lastSample = 0
						}
					}
					continue
				}

				// Jitter buffer pre-cushion: esperar al menos 2 frames (~40ms) antes de iniciar playback de un burst
				if stream.buffering {
					if len(stream.buffer) < streamFrameBytes*2 {
						continue
					}
					stream.buffering = false
					if stream.lastSample == 0 {
						applyFadeIn(stream.buffer, antiClickSamples)
					}
				}

				streamSamples := len(stream.buffer) / 2
				samplesToRead := samples
				if streamSamples < samplesToRead {
					samplesToRead = streamSamples
				}

				for i := 0; i < samplesToRead; i++ {
					val := int16(binary.LittleEndian.Uint16(stream.buffer[i*2 : i*2+2]))
					engine.mixBuffer[i] += int32(val)
					stream.lastSample = val
				}

				if samplesToRead < samples {
					// Underrun al final de este bloque
					decay := stream.lastSample
					for i := samplesToRead; i < samplesToRead+antiClickSamples && i < samples; i++ {
						decay = int16(float64(decay) * 0.8)
						engine.mixBuffer[i] += int32(decay)
					}
					stream.lastSample = 0
					stream.buffering = true
					stream.buffer = stream.buffer[:0]
				} else {
					consumedBytes := samplesToRead * 2
					stream.buffer = stream.buffer[consumedBytes:]
				}
			}

			// Renderizar a output con limitador soft-knee anti-clipping
			for i := 0; i < samples; i++ {
				sum := engine.mixBuffer[i]
				clipped := softClip(sum)
				binary.LittleEndian.PutUint16(output[i*2:i*2+2], uint16(clipped))
			}
		},
	})
	if err != nil {
		engine.Stop()
		return nil, fmt.Errorf("abrir salida: %w", err)
	}
	engine.playback = playback
	if err := engine.capture.Start(); err != nil {
		engine.Stop()
		return nil, fmt.Errorf("iniciar micrófono: %w", err)
	}
	if err := engine.playback.Start(); err != nil {
		engine.Stop()
		return nil, fmt.Errorf("iniciar salida: %w", err)
	}
	go func() {
		for frame := range engine.captureFrames {
			onFrame(frame)
		}
	}()
	return engine, nil
}

func (e *Engine) PlayStream(senderID string, data []byte) {
	if e == nil || len(data) == 0 || len(data)%2 != 0 {
		return
	}
	if senderID == "" {
		senderID = "__default__"
	}

	e.playbackMu.Lock()
	defer e.playbackMu.Unlock()

	if e.streams == nil {
		return
	}

	stream, exists := e.streams[senderID]
	if !exists {
		stream = &audioStream{
			buffering:  true,
			lastActive: time.Now(),
		}
		e.streams[senderID] = stream
	}

	stream.buffer = append(stream.buffer, data...)
	stream.lastActive = time.Now()

	const maxStreamBufferBytes = streamFrameBytes * 6 // ~120 ms
	if len(stream.buffer) > maxStreamBufferBytes {
		keepBytes := streamFrameBytes * 3
		stream.buffer = stream.buffer[len(stream.buffer)-keepBytes:]
	}
}

func (e *Engine) Play(data []byte) {
	e.PlayStream("__sfx__", data)
}

func (e *Engine) Stop() {
	if e == nil {
		return
	}
	e.stopOnce.Do(func() {
		if e.capture != nil {
			_ = e.capture.Stop()
			e.capture.Uninit()
		}
		if e.playback != nil {
			_ = e.playback.Stop()
			e.playback.Uninit()
		}
		if e.context != nil {
			_ = e.context.Uninit()
			e.context.Free()
		}
		close(e.captureFrames)
		e.playbackMu.Lock()
		e.streams = nil
		e.playbackMu.Unlock()
	})
}
