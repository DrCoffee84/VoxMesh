//go:build cgo

package audio

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/gen2brain/malgo"

	"voxmesh/internal/config"
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
	volume     float64

	// jitter reordena y oculta pérdidas para los emisores de red con secuencia de
	// audio propia (PlayStreamSeq). Es nil para SFX, monitor local y emisores legacy,
	// que siguen usando buffer.
	jitter *jitterBuffer
}

type streamWorkItem struct {
	chunk       []byte
	samples     int
	lastSample  int16
	applyFadeIn bool
	hasGap      bool
	volume      float64
}

type Engine struct {
	context       *malgo.AllocatedContext
	capture       *malgo.Device
	playback      *malgo.Device
	captureFrames chan []byte
	captureBuffer []byte
	captureMu     sync.Mutex
	stopOnce      sync.Once

	streams        map[string]*audioStream
	mixBuffer      []int32
	playbackMu     sync.Mutex
	jitterBufferMS int

	workItems     []streamWorkItem
	scratchBuffer []byte

	// Limitador con envolvente del bus de mezcla; solo lo toca el callback de playback.
	mixLimiter busLimiter
}

func softClip(sample int32) int16 {
	knee := config.AudioSoftClipKnee
	if sample > knee {
		excess := float64(sample - knee)
		compressed := float64(knee) + config.AudioSoftClipRange*(excess/(excess+config.AudioSoftClipCurve))
		if compressed > 32767 {
			return 32767
		}
		return int16(compressed)
	}
	if sample < -knee {
		excess := float64(-sample - knee)
		compressed := -(float64(knee) + config.AudioSoftClipRange*(excess/(excess+config.AudioSoftClipCurve)))
		if compressed < -32768 {
			return -32768
		}
		return int16(compressed)
	}
	return int16(sample)
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

// limitFrame ajusta el volumen de un frame de audio si su pico supera un umbral dado.
// Ya no se usa en el callback de playback: aplicaba una ganancia fija por buffer y
// generaba escalones entre buffers. Lo reemplaza busLimiter sobre el bus int32.
func limitFrame(pcm []byte, threshold int16) {
	if len(pcm) < 2 {
		return
	}

	// 1. Encontrar el pico absoluto del frame
	var peak int16
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int16(binary.LittleEndian.Uint16(pcm[i : i+2]))
		if sample < 0 {
			sample = -sample
		}
		if sample > peak {
			peak = sample
		}
	}

	if peak <= threshold {
		return // no hace falta tocar nada
	}

	// 2. Calcular cuánto hay que bajar el volumen
	scale := float64(threshold) / float64(peak)

	// 3. Aplicar la atenuación a todo el frame
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int16(binary.LittleEndian.Uint16(pcm[i : i+2]))
		scaled := int16(float64(sample) * scale)
		binary.LittleEndian.PutUint16(pcm[i:i+2], uint16(scaled))
	}
}

func (e *Engine) SetJitterBufferMS(ms int) {
	if ms < 40 {
		ms = 40
	} else if ms > 500 {
		ms = 500
	}
	e.playbackMu.Lock()
	e.jitterBufferMS = ms
	for _, stream := range e.streams {
		if stream.jitter != nil {
			stream.jitter.setTargetMS(ms)
		}
	}
	e.playbackMu.Unlock()
}

func NewEngine(inputName, outputName string, onFrame func([]byte)) (*Engine, error) {
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("inicializar motor de audio: %w", err)
	}
	engine := &Engine{
		context:        context,
		captureFrames:  make(chan []byte, 8),
		streams:        make(map[string]*audioStream),
		jitterBufferMS: 120,
		mixBuffer:      make([]int32, 4096),
		workItems:      make([]streamWorkItem, 0, 16),
		scratchBuffer:  make([]byte, 4096*2),
		mixLimiter: newBusLimiter(
			config.AudioBusLimiterThreshold,
			config.AudioBusLimiterAttackMS,
			config.AudioBusLimiterReleaseMS,
		),
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
				copy(engine.captureBuffer, engine.captureBuffer[streamFrameBytes:])
				engine.captureBuffer = engine.captureBuffer[:len(engine.captureBuffer)-streamFrameBytes]
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
			samples := len(output) / 2
			if samples == 0 {
				return
			}

			// --- Lock corto: inspección de streams y extracción de chunks ---
			engine.playbackMu.Lock()
			if engine.streams == nil {
				engine.playbackMu.Unlock()
				return
			}

			now := time.Now()
			targetBytes := (engine.jitterBufferMS / 20) * streamFrameBytes
			if targetBytes < streamFrameBytes {
				targetBytes = streamFrameBytes
			}

			numStreams := len(engine.streams)
			neededScratch := numStreams * samples * 2
			if len(engine.scratchBuffer) < neededScratch {
				engine.scratchBuffer = make([]byte, neededScratch)
			}
			scratchOffset := 0

			engine.workItems = engine.workItems[:0]

			for id, stream := range engine.streams {
				// Emisores de red con jitter buffer: él ya resuelve reorden, PLC,
				// fades y deriva, así que solo copiamos su salida al bus.
				if stream.jitter != nil {
					chunk := engine.scratchBuffer[scratchOffset : scratchOffset+samples*2]
					if !stream.jitter.Read(chunk, now) {
						// Purgar streams inactivos tras 2 segundos sin paquetes de audio
						if now.Sub(stream.lastActive) > 2*time.Second {
							delete(engine.streams, id)
						}
						continue
					}
					scratchOffset += samples * 2
					engine.workItems = append(engine.workItems, streamWorkItem{
						chunk:   chunk,
						samples: samples,
						volume:  stream.volume,
					})
					continue
				}

				if len(stream.buffer) == 0 {
					// Purgar streams inactivos tras 2 segundos sin paquetes de audio
					if now.Sub(stream.lastActive) > 2*time.Second {
						delete(engine.streams, id)
						continue
					}
					// Silencio real prolongado (> 300 ms)
					if now.Sub(stream.lastActive) > 300*time.Millisecond {
						stream.buffering = true
					}
					if stream.lastSample != 0 {
						engine.workItems = append(engine.workItems, streamWorkItem{
							samples:    0,
							lastSample: stream.lastSample,
							hasGap:     true,
							volume:     stream.volume,
						})
						stream.lastSample = 0
					}
					continue
				}

				// Pre-buffering al arrancar una frase
				applyFade := false
				if stream.buffering && id != "__sfx__" && id != "__local__" {
					if len(stream.buffer) < targetBytes {
						continue
					}
					stream.buffering = false
					if stream.lastSample == 0 {
						applyFade = true
					}
				}

				streamSamples := len(stream.buffer) / 2
				samplesToRead := samples
				if streamSamples < samplesToRead {
					samplesToRead = streamSamples
				}

				consumedBytes := samplesToRead * 2
				var chunk []byte
				if consumedBytes > 0 {
					chunk = engine.scratchBuffer[scratchOffset : scratchOffset+consumedBytes]
					copy(chunk, stream.buffer[:consumedBytes])
					scratchOffset += consumedBytes

					// Movemos el buffer sin realloc innecesario
					copy(stream.buffer, stream.buffer[consumedBytes:])
					stream.buffer = stream.buffer[:len(stream.buffer)-consumedBytes]
				}

				hadGap := samplesToRead < samples
				lastSampleForDecay := int16(0)
				if samplesToRead > 0 {
					lastVal := int16(binary.LittleEndian.Uint16(chunk[consumedBytes-2 : consumedBytes]))
					if hadGap {
						lastSampleForDecay = lastVal
						stream.lastSample = 0
					} else {
						stream.lastSample = lastVal
					}
				} else if stream.lastSample != 0 {
					lastSampleForDecay = stream.lastSample
					stream.lastSample = 0
				}

				engine.workItems = append(engine.workItems, streamWorkItem{
					chunk:       chunk,
					samples:     samplesToRead,
					lastSample:  lastSampleForDecay,
					applyFadeIn: applyFade,
					hasGap:      hadGap,
					volume:      stream.volume,
				})
			}
			engine.playbackMu.Unlock()
			// --- Fin del lock corto ---

			// Mezclado fuera del lock (DSP / suma algebraica)
			if len(engine.mixBuffer) < samples {
				engine.mixBuffer = make([]int32, samples)
			} else {
				clear(engine.mixBuffer[:samples])
			}
			for _, item := range engine.workItems {
				if item.applyFadeIn && len(item.chunk) > 0 {
					applyFadeIn(item.chunk, antiClickSamples)
				}

				for i := 0; i < item.samples; i++ {
					val := int16(binary.LittleEndian.Uint16(item.chunk[i*2 : i*2+2]))
					if item.volume != 1.0 {
						engine.mixBuffer[i] += int32(float64(val) * item.volume)
					} else {
						engine.mixBuffer[i] += int32(val)
					}
				}

				if item.hasGap && item.lastSample != 0 {
					// Micro-bache: suavizar hacia cero sin frenar el flujo futuro
					decay := item.lastSample
					for i := item.samples; i < item.samples+antiClickSamples && i < samples; i++ {
						decay = int16(float64(decay) * 0.8)
						if item.volume != 1.0 {
							engine.mixBuffer[i] += int32(float64(decay) * item.volume)
						} else {
							engine.mixBuffer[i] += int32(decay)
						}
					}
				}
			}

			// Limitador con envolvente sobre el bus int32: absorbe la suma de varios
			// streams fuertes con ganancia continua, sin escalones entre buffers.
			engine.mixLimiter.Process(engine.mixBuffer[:samples])

			// Renderizar a output con limitador soft-knee anti-clipping
			// (red de seguridad para los transientes que pasan durante el attack)
			for i := 0; i < samples; i++ {
				clipped := softClip(engine.mixBuffer[i])
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

	now := time.Now()
	stream, exists := e.streams[senderID]
	if !exists {
		// Pre-allocamos capacidad razonable para evitar reallocs frecuentes
		stream = &audioStream{
			buffer:     make([]byte, 0, streamFrameBytes*16),
			buffering:  true,
			lastActive: now,
			volume:     1.0,
		}
		e.streams[senderID] = stream
	} else if now.Sub(stream.lastActive) > 300*time.Millisecond {
		stream.buffering = true
	}

	// Si el emisor pasó a mandar audio sin secuencia propia (versión anterior),
	// volvemos al buffer FIFO.
	if stream.jitter != nil {
		stream.jitter = nil
		stream.buffering = true
	}

	// SFX y monitor local sin buffer
	if senderID == "__sfx__" || senderID == "__local__" {
		stream.buffering = false
	}

	// Append eficiente
	stream.buffer = append(stream.buffer, data...)
	stream.lastActive = now

	// Control de latencia (delay drift)
	targetBytes := (e.jitterBufferMS / 20) * streamFrameBytes
	if targetBytes < streamFrameBytes {
		targetBytes = streamFrameBytes
	}
	maxBytes := targetBytes * 2

	if len(stream.buffer) > maxBytes {
		// Descartamos lo más viejo de forma eficiente
		keep := targetBytes
		copy(stream.buffer, stream.buffer[len(stream.buffer)-keep:])
		stream.buffer = stream.buffer[:keep]
	}
}

// PlayStreamSeq encola un frame de audio de red junto con la secuencia de audio
// propia del emisor. Los frames pasan por un jitter buffer por emisor que los
// reordena, descarta los que llegan tarde y oculta las pérdidas (PLC).
func (e *Engine) PlayStreamSeq(senderID string, seq uint32, data []byte) {
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

	now := time.Now()
	stream, exists := e.streams[senderID]
	if !exists {
		stream = &audioStream{
			lastActive: now,
			volume:     1.0,
		}
		e.streams[senderID] = stream
	}
	if stream.jitter == nil {
		stream.jitter = newJitterBuffer(e.jitterBufferMS)
		stream.buffer = stream.buffer[:0]
	}
	stream.jitter.Push(seq, data, now)
	stream.lastActive = now
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
		e.workItems = nil
		e.scratchBuffer = nil
		e.playbackMu.Unlock()
	})
}

// SetStreamVolume ajusta el volumen individual de un usuario (1.0 = 100%, 0.5 = 50%, 2.0 = 200%)
func (e *Engine) SetStreamVolume(senderID string, volume float64) {
	if e == nil {
		return
	}
	e.playbackMu.Lock()
	defer e.playbackMu.Unlock()

	if e.streams == nil {
		return
	}

	if stream, exists := e.streams[senderID]; exists {
		stream.volume = volume
	} else {
		e.streams[senderID] = &audioStream{
			buffer:     make([]byte, 0, streamFrameBytes*16),
			buffering:  true,
			lastActive: time.Now(),
			volume:     volume,
		}
	}
}
