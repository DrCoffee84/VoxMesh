//go:build cgo

package audio

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"

	"voxmesh/internal/config"
)

const (
	streamFrameBytes = FrameSamples * 2 * Channels
	antiClickSamples = 64 // ~1.33 ms at 48 kHz
)

type audioStream struct {
	buffer      []byte
	buffering   bool
	lastSample  int16
	lastActive  time.Time // último paquete recibido
	lastAudible time.Time // último buffer del callback con audio de este stream (indicador "hablando")
	volume      float64

	// jitter reordena, decodifica (Opus) y oculta pérdidas para los emisores de red
	// (PlayStreamSeq). Es nil para SFX y monitor local, que usan buffer (PCM FIFO).
	jitter *jitterBuffer
	// fx es la cadena de salida (Ajustes → Salida) propia del emisor: corre sobre el audio ya
	// decodificado y en orden de secuencia, así los filtros con estado no mezclan emisores.
	fx *Effects
}

type streamWorkItem struct {
	chunk       []int16
	samples     int
	lastSample  int16
	applyFadeIn bool
	hasGap      bool
	volume      float64
	fx          *Effects // nil = sin cadena de salida (SFX, monitor local)
}

// PlaybackProvider devuelve la configuración de salida (filtro por emisor, jitter buffer, limitador
// de la mezcla). El callback de playback la llama una vez por buffer: tiene que ser rápida y sin
// locks (a.conf()).
type PlaybackProvider func() PlaybackSettings

type Engine struct {
	context       *malgo.AllocatedContext
	capture       *malgo.Device
	playback      *malgo.Device
	captureFrames chan []byte
	captureBuffer []byte
	captureMu     sync.Mutex
	stopOnce      sync.Once

	streams    map[string]*audioStream
	volumes    map[string]float64 // volumen por emisor; sobrevive a la purga del stream
	mixBuffer  []int32
	playbackMu sync.Mutex
	// jitter son los parámetros vigentes del jitter buffer (los copia mix bajo playbackMu); los
	// usan PlayStreamSeq al crear un stream y el FIFO de PlayStream.
	jitter JitterTuning

	workItems     []streamWorkItem
	scratchBuffer []int16

	playbackSettings atomic.Pointer[PlaybackProvider]

	// Limitador con envolvente del bus de mezcla; solo lo toca el callback de playback.
	mixLimiter        peakLimiter
	mixLimiterRelease float64
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

func newEngineState() *Engine {
	return &Engine{
		captureFrames: make(chan []byte, 8),
		streams:       make(map[string]*audioStream),
		volumes:       make(map[string]float64),
		jitter:        defaultPlayback.Jitter,
		mixBuffer:     make([]int32, 4096),
		workItems:     make([]streamWorkItem, 0, 16),
		scratchBuffer: make([]int16, 4096),
		mixLimiter: newPeakLimiter(
			defaultPlayback.BusLimiterThreshold,
			config.AudioBusLimiterAttackMS,
			defaultPlayback.BusLimiterReleaseMS,
		),
		mixLimiterRelease: defaultPlayback.BusLimiterReleaseMS,
	}
}

func NewEngine(inputName, outputName string, onFrame func([]byte)) (*Engine, error) {
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("inicializar motor de audio: %w", err)
	}
	engine := newEngineState()
	engine.context = context
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
			engine.mix(output)
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

// mix es el callback de playback: arma output (S16LE) con la suma de todos los streams.
func (e *Engine) mix(output []byte) {
	samples := len(output) / 2
	if samples == 0 {
		return
	}

	settings := e.currentPlaybackSettings()

	// --- Lock corto: inspección de streams y extracción de chunks ---
	e.playbackMu.Lock()
	if e.streams == nil {
		e.playbackMu.Unlock()
		return
	}

	now := time.Now()
	e.jitter = settings.Jitter
	targetBytes := e.fifoTargetBytes()

	neededScratch := len(e.streams) * samples
	if len(e.scratchBuffer) < neededScratch {
		e.scratchBuffer = make([]int16, neededScratch)
	}
	scratchOffset := 0

	e.workItems = e.workItems[:0]

	for id, stream := range e.streams {
		// Emisores de red con jitter buffer: él ya resuelve reorden, decodificación, PLC,
		// fades y deriva, así que solo copiamos su salida al bus.
		if stream.jitter != nil {
			chunk := e.scratchBuffer[scratchOffset : scratchOffset+samples]
			stream.jitter.setTuning(settings.Jitter)
			if !stream.jitter.Read(chunk, now) {
				// Purgar streams inactivos tras 2 segundos sin paquetes de audio
				if now.Sub(stream.lastActive) > 2*time.Second {
					delete(e.streams, id)
				}
				continue
			}
			stream.lastAudible = now
			scratchOffset += samples
			e.workItems = append(e.workItems, streamWorkItem{
				chunk:   chunk,
				samples: samples,
				volume:  stream.volume,
				fx:      stream.fx,
			})
			continue
		}

		if len(stream.buffer) == 0 {
			// Purgar streams inactivos tras 2 segundos sin paquetes de audio
			if now.Sub(stream.lastActive) > 2*time.Second {
				delete(e.streams, id)
				continue
			}
			// Silencio real prolongado (> 300 ms)
			if now.Sub(stream.lastActive) > 300*time.Millisecond {
				stream.buffering = true
			}
			if stream.lastSample != 0 {
				e.workItems = append(e.workItems, streamWorkItem{
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

		samplesToRead := len(stream.buffer) / 2
		if samplesToRead > samples {
			samplesToRead = samples
		}

		consumedBytes := samplesToRead * 2
		var chunk []int16
		if samplesToRead > 0 {
			chunk = e.scratchBuffer[scratchOffset : scratchOffset+samplesToRead]
			for i := range chunk {
				chunk[i] = int16(binary.LittleEndian.Uint16(stream.buffer[i*2:]))
			}
			scratchOffset += samplesToRead

			// Movemos el buffer sin realloc innecesario
			copy(stream.buffer, stream.buffer[consumedBytes:])
			stream.buffer = stream.buffer[:len(stream.buffer)-consumedBytes]
		}

		hadGap := samplesToRead < samples
		lastSampleForDecay := int16(0)
		if samplesToRead > 0 {
			lastVal := chunk[samplesToRead-1]
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

		e.workItems = append(e.workItems, streamWorkItem{
			chunk:       chunk,
			samples:     samplesToRead,
			lastSample:  lastSampleForDecay,
			applyFadeIn: applyFade,
			hasGap:      hadGap,
			volume:      stream.volume,
		})
	}
	e.playbackMu.Unlock()
	// --- Fin del lock corto ---

	// Cadena de salida por emisor y mezclado fuera del lock (DSP / suma algebraica). Cada fx
	// solo lo toca este callback.
	filterSettings, filterOn := settings.Filter, settings.FilterEnabled
	if len(e.mixBuffer) < samples {
		e.mixBuffer = make([]int32, samples)
	} else {
		clear(e.mixBuffer[:samples])
	}
	for _, item := range e.workItems {
		chunk := item.chunk[:item.samples]
		if item.applyFadeIn && len(chunk) > 0 {
			applyFadeInInt16(chunk, antiClickSamples)
		}
		if filterOn && item.fx != nil {
			item.fx.ApplyOutput(chunk, filterSettings)
		}

		for i, val := range chunk {
			if item.volume != 1.0 {
				e.mixBuffer[i] += int32(float64(val) * item.volume)
			} else {
				e.mixBuffer[i] += int32(val)
			}
		}

		if item.hasGap && item.lastSample != 0 {
			// Micro-bache: suavizar hacia cero sin frenar el flujo futuro
			decay := item.lastSample
			for i := item.samples; i < item.samples+antiClickSamples && i < samples; i++ {
				decay = int16(float64(decay) * 0.8)
				if item.volume != 1.0 {
					e.mixBuffer[i] += int32(float64(decay) * item.volume)
				} else {
					e.mixBuffer[i] += int32(decay)
				}
			}
		}
	}

	// Limitador con envolvente sobre el bus int32: absorbe la suma de varios
	// streams fuertes con ganancia continua, sin escalones entre buffers.
	e.mixLimiter.threshold = settings.BusLimiterThreshold
	if settings.BusLimiterReleaseMS != e.mixLimiterRelease {
		e.mixLimiter.releaseCoeff = smoothingCoeff(settings.BusLimiterReleaseMS)
		e.mixLimiterRelease = settings.BusLimiterReleaseMS
	}
	e.mixLimiter.Process(e.mixBuffer[:samples])

	// Renderizar a output con limitador soft-knee anti-clipping
	// (red de seguridad para los transientes que pasan durante el attack)
	for i := 0; i < samples; i++ {
		clipped := softClip(e.mixBuffer[i])
		binary.LittleEndian.PutUint16(output[i*2:i*2+2], uint16(clipped))
	}
}

// PlayStream encola PCM S16LE en un stream FIFO sin jitter buffer: solo para "__sfx__"
// (sonidos de eventos) y "__local__" (monitoreo de la voz propia). El audio de red usa
// PlayStreamSeq.
func (e *Engine) PlayStream(senderID string, data []byte) {
	if e == nil || len(data) == 0 || len(data)%2 != 0 {
		return
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
			lastActive: now,
			volume:     1.0,
		}
		e.streams[senderID] = stream
	}

	// Append eficiente
	stream.buffer = append(stream.buffer, data...)
	stream.lastActive = now

	// Control de latencia (delay drift)
	targetBytes := e.fifoTargetBytes()
	maxBytes := targetBytes * 2

	if len(stream.buffer) > maxBytes {
		// Descartamos lo más viejo de forma eficiente
		keep := targetBytes
		copy(stream.buffer, stream.buffer[len(stream.buffer)-keep:])
		stream.buffer = stream.buffer[:keep]
	}
}

// PlayStreamSeq encola un paquete Opus de red junto con la secuencia de audio propia del
// emisor y su número de frase (talkspurt). Pasa por un jitter buffer por emisor que reordena,
// descarta los tardíos, decodifica al reproducir y oculta las pérdidas (FEC / PLC de Opus).
func (e *Engine) PlayStreamSeq(senderID string, seq uint32, talkspurt uint8, packet []byte) {
	if e == nil || len(packet) == 0 {
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
		volume, ok := e.volumes[senderID]
		if !ok {
			volume = 1.0
		}
		stream = &audioStream{lastActive: now, volume: volume}
		e.streams[senderID] = stream
	}
	if stream.jitter == nil {
		decoder, err := newOpusDecoder()
		if err != nil {
			return
		}
		stream.jitter = newJitterBufferTuned(e.jitter, decoder)
		stream.fx = &Effects{}
		stream.buffer = nil
	}
	stream.jitter.Push(seq, talkspurt, packet, now)
	stream.lastActive = now
}

// SetPlaybackSettings registra de dónde sale la configuración de salida. Sin proveedor se usan
// los valores de fábrica.
func (e *Engine) SetPlaybackSettings(provider PlaybackProvider) {
	if e == nil {
		return
	}
	if provider == nil {
		e.playbackSettings.Store(nil)
		return
	}
	e.playbackSettings.Store(&provider)
}

func (e *Engine) currentPlaybackSettings() PlaybackSettings {
	if provider := e.playbackSettings.Load(); provider != nil {
		return (*provider)()
	}
	return defaultPlayback
}

// fifoTargetBytes es el colchón del FIFO de PlayStream (SFX / monitor local): el tope del jitter
// buffer, como mínimo un frame. Se llama con playbackMu tomado.
func (e *Engine) fifoTargetBytes() int {
	return max(e.jitter.MaxMS/20, 1) * streamFrameBytes
}

// JitterReport devuelve una copia de las estadísticas del jitter buffer de cada emisor de red.
func (e *Engine) JitterReport() map[string]JitterStats {
	if e == nil {
		return nil
	}
	e.playbackMu.Lock()
	defer e.playbackMu.Unlock()
	report := make(map[string]JitterStats, len(e.streams))
	for id, stream := range e.streams {
		if stream.jitter != nil {
			report[id] = stream.jitter.Stats()
		}
	}
	return report
}

// AudibleStreams agrega a dst los emisores de red que sonaron en la ventana within: lo que el
// callback efectivamente reprodujo, no lo que llegó (que suena un colchón después). Es la
// fuente del indicador "hablando".
func (e *Engine) AudibleStreams(dst []string, within time.Duration) []string {
	if e == nil {
		return dst
	}
	now := time.Now()
	e.playbackMu.Lock()
	defer e.playbackMu.Unlock()
	for id, stream := range e.streams {
		if stream.jitter != nil && !stream.lastAudible.IsZero() && now.Sub(stream.lastAudible) <= within {
			dst = append(dst, id)
		}
	}
	return dst
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

// SetStreamVolume ajusta el volumen individual de un emisor (1.0 = 100 %, 0.5 = 50 %, 3.0 = 300 %).
// Queda guardado aunque el stream todavía no exista o se purgue por inactividad.
func (e *Engine) SetStreamVolume(senderID string, volume float64) {
	if e == nil {
		return
	}
	volume = math.Min(math.Max(volume, 0), config.ParamByKey("peer_volume_max").Max)
	e.playbackMu.Lock()
	defer e.playbackMu.Unlock()

	if e.streams == nil {
		return
	}
	if volume == 1.0 {
		delete(e.volumes, senderID)
	} else {
		e.volumes[senderID] = volume
	}
	if stream, exists := e.streams[senderID]; exists {
		stream.volume = volume
	}
}
