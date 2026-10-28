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
	context  *malgo.AllocatedContext
	capture  *malgo.Device
	playback *malgo.Device
	// Lo que se abrió para cada elección (Devices); fijo desde NewEngine.
	inputDevice, outputDevice Resolved
	// Captura → goroutine de envío: frames de buffers preasignados (captureFree), así el callback
	// de malgo (hilo de tiempo real) no pide memoria. Ver captureInput.
	captureFrames chan CapturedFrame
	captureFree   chan *[streamFrameBytes]byte
	captureBuffer []byte
	captureDrops  int // frames tirados desde el último encolado (bajo captureMu)
	captureMu     sync.Mutex
	sendThread    atomic.Pointer[string] // prioridad que consiguió la goroutine de envío
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

	// Medición del lado que envía (TakeCaptureStats): la escriben el callback de captura y la
	// goroutine de envío.
	captureCount   atomic.Uint64
	captureDropped atomic.Uint64
	sendQueueMax   atomic.Int64
	sendProcMax    atomic.Int64 // ns

	// arrivals graba la llegada de cada paquete de red (nil = apagado).
	arrivals atomic.Pointer[ArrivalRecorder]

	// Cancelación de eco (CancelEcho). mix copia lo que sale por los parlantes a echoRef solo
	// mientras echoActive; el resto es de quien procesa la captura, bajo echoMu (micrófono y
	// celular pueden coincidir un instante al cambiar de entrada).
	echoRef    *echoReference
	echoActive atomic.Bool
	echoMu     sync.Mutex
	echo       *echoCanceller
	echoReader echoReader
	echoPhone  bool  // entrada para la que se creó echo
	echoFailed error // falló (al crearlo o al procesar): se avisa una vez y no se reintenta
	echoPCM    [FrameSamples]int16
	echoCount  int // frames desde la última lectura de medidas
	// Medidas para el log (TakeCaptureStats): frames procesados, ERLE en centésimas de dB
	// (suma y cantidad) y el último retardo encontrado (-1 = ninguno).
	echoFrames    atomic.Uint64
	echoERLESum   atomic.Int64
	echoERLECount atomic.Int64
	echoDelayMS   atomic.Int64

	// Salud de los dispositivos (Health): cuándo se abrió y el último callback de cada uno
	// (UnixNano), qué dispositivo avisó que el sistema lo detuvo, y si el Stop fue nuestro.
	started      atomic.Int64
	lastCapture  atomic.Int64
	lastPlayback atomic.Int64
	lost         atomic.Pointer[string]
	stopping     atomic.Bool
}

// CapturedFrame es un frame de 20 ms del micrófono que el Engine le entrega a la goroutine de
// envío (o que arma el servidor del celular).
type CapturedFrame struct {
	PCM []byte // S16LE; el buffer se reutiliza: vale solo durante el callback
	// At es cuándo se capturó: un atraso al procesar no es una pausa del que habla.
	At time.Time
	// Dropped son los frames que la captura tuvo que tirar justo antes de este (la goroutine de
	// envío estaba atrasada más que la cola): audio que no existe y que el que manda tiene que
	// marcar como pérdida (avanzando la secuencia) para que los demás no se queden cortos.
	Dropped int
	// Phone: el frame viene del micrófono del celular (cancelación de eco, EchoSettings.Phone).
	Phone bool

	buf *[streamFrameBytes]byte // buffer de captureFree a devolver (nil si no es del Engine)
}

// CaptureStats es lo que mide el Engine del lado que envía: si la captura entrega frames a
// ritmo real (50 por segundo) y si la goroutine de envío los procesa a tiempo.
type CaptureStats struct {
	Frames   uint64        // frames de 20 ms que armó el callback de captura (acumulado)
	Dropped  uint64        // frames tirados porque la cola de envío estaba llena (acumulado)
	MaxQueue int           // cola máxima desde la lectura anterior (frames esperando)
	MaxProc  time.Duration // procesamiento más lento de un frame desde la lectura anterior
	// Cancelación de eco: frames procesados (acumulado), ERLE medido desde la lectura anterior
	// (suma en dB y cantidad de medidas) y el último retardo de eco encontrado (-1 = ninguno).
	EchoFrames    uint64
	EchoERLESum   float64
	EchoERLECount int
	EchoDelayMS   int
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
	// Tantos buffers como lugares en la cola: con un buffer libre en la mano siempre hay lugar
	// para encolarlo, así el callback nunca se bloquea.
	free := make(chan *[streamFrameBytes]byte, config.AudioCaptureQueueFrames)
	for range config.AudioCaptureQueueFrames {
		free <- new([streamFrameBytes]byte)
	}
	e := &Engine{
		captureFrames: make(chan CapturedFrame, config.AudioCaptureQueueFrames),
		captureFree:   free,
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
		echoRef:           new(echoReference),
	}
	e.echoDelayMS.Store(-1)
	return e
}

// NewEngine abre micrófono y salida. onFrame recibe cada frame capturado en una goroutine propia
// con prioridad de audio (LockAudioThread); el PCM del frame vale solo durante la llamada. Si un
// dispositivo elegido ya no está abre el predeterminado (Devices dice cuál se abrió).
func NewEngine(input, output Device, onFrame func(CapturedFrame)) (*Engine, error) {
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("inicializar motor de audio: %w", err)
	}
	engine := newEngineState()
	engine.context = context
	inputID, inputResolved, _ := resolveDevice(context, malgo.Capture, input)
	outputID, outputResolved, _ := resolveDevice(context, malgo.Playback, output)
	engine.inputDevice, engine.outputDevice = inputResolved, outputResolved

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
			now := time.Now()
			engine.lastCapture.Store(now.UnixNano())
			engine.captureInput(input, now)
		},
		Stop: func() { engine.deviceStopped("micrófono") },
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
			now := time.Now().UnixNano()
			engine.lastPlayback.Store(now)
			engine.mix(output, now)
		},
		Stop: func() { engine.deviceStopped("salida") },
	})
	if err != nil {
		engine.Stop()
		return nil, fmt.Errorf("abrir salida: %w", err)
	}
	engine.playback = playback

	engine.started.Store(time.Now().UnixNano())
	if err := engine.capture.Start(); err != nil {
		engine.Stop()
		return nil, fmt.Errorf("iniciar micrófono: %w", err)
	}
	if err := engine.playback.Start(); err != nil {
		engine.Stop()
		return nil, fmt.Errorf("iniciar salida: %w", err)
	}

	go func() {
		priority := LockAudioThread()
		engine.sendThread.Store(&priority)
		for frame := range engine.captureFrames {
			storeMax(&engine.sendQueueMax, int64(len(engine.captureFrames)))
			start := time.Now()
			onFrame(frame)
			storeMax(&engine.sendProcMax, int64(time.Since(start)))
			engine.captureFree <- frame.buf // hay lugar para todos los buffers: no bloquea
		}
	}()
	return engine, nil
}

// captureInput arma frames de 20 ms con lo que entrega el micrófono y los encola para la
// goroutine de envío. Corre en el hilo de tiempo real de malgo: no pide memoria (buffers de
// captureFree) y nunca espera. Sin buffer libre, la goroutine de envío está atrasada más que toda
// la cola (AudioCaptureQueueFrames): el frame se tira y el siguiente lo avisa (Dropped).
func (e *Engine) captureInput(input []byte, now time.Time) {
	e.captureMu.Lock()
	defer e.captureMu.Unlock()
	e.captureBuffer = append(e.captureBuffer, input...)
	for len(e.captureBuffer) >= streamFrameBytes {
		e.captureCount.Add(1)
		select {
		case buf := <-e.captureFree:
			copy(buf[:], e.captureBuffer[:streamFrameBytes])
			e.captureFrames <- CapturedFrame{PCM: buf[:], At: now, Dropped: e.captureDrops, buf: buf}
			e.captureDrops = 0
		default:
			e.captureDrops++
			e.captureDropped.Add(1)
		}
		copy(e.captureBuffer, e.captureBuffer[streamFrameBytes:])
		e.captureBuffer = e.captureBuffer[:len(e.captureBuffer)-streamFrameBytes]
	}
}

// deviceStopped es el callback Stop de malgo: el sistema detuvo un dispositivo (desconectado,
// invalidado al volver de una suspensión). Los Stop de Engine.Stop no cuentan.
func (e *Engine) deviceStopped(device string) {
	if !e.stopping.Load() {
		e.lost.CompareAndSwap(nil, &device)
	}
}

// Health dice si los dos dispositivos siguen vivos: ninguno detenido por el sistema y los dos
// con callbacks en los últimos DeviceSilenceLimit. Si no, reason dice qué pasó (para el log y la
// barra de estado) y hay que reabrir el audio.
func (e *Engine) Health(now time.Time) (ok bool, reason string) {
	if e == nil || e.stopping.Load() {
		return true, ""
	}
	lost := ""
	if device := e.lost.Load(); device != nil {
		lost = *device
	}
	return deviceHealth(now, e.started.Load(), e.lastCapture.Load(), e.lastPlayback.Load(), lost)
}

// SendThreadPriority dice qué prioridad consiguió la goroutine de envío ("" si todavía no arrancó).
func (e *Engine) SendThreadPriority() string {
	if e == nil {
		return ""
	}
	if priority := e.sendThread.Load(); priority != nil {
		return *priority
	}
	return ""
}

// storeMax sube v hasta x si es mayor (lo reinicia el lector con Swap).
func storeMax(v *atomic.Int64, x int64) {
	for {
		old := v.Load()
		if x <= old || v.CompareAndSwap(old, x) {
			return
		}
	}
}

// Devices dice qué micrófono y qué salida se abrieron, y si alguno elegido ya no estaba.
func (e *Engine) Devices() (input, output Resolved) {
	return e.inputDevice, e.outputDevice
}

// TakeCaptureStats devuelve la medición de la captura y reinicia los máximos.
func (e *Engine) TakeCaptureStats() CaptureStats {
	if e == nil {
		return CaptureStats{}
	}
	return CaptureStats{
		Frames:        e.captureCount.Load(),
		Dropped:       e.captureDropped.Load(),
		MaxQueue:      int(e.sendQueueMax.Swap(0)),
		MaxProc:       time.Duration(e.sendProcMax.Swap(0)),
		EchoFrames:    e.echoFrames.Load(),
		EchoERLESum:   float64(e.echoERLESum.Swap(0)) / 100,
		EchoERLECount: int(e.echoERLECount.Swap(0)),
		EchoDelayMS:   int(e.echoDelayMS.Load()),
	}
}

// SetArrivalRecorder empieza a grabar la llegada de cada paquete de red; nil lo apaga.
func (e *Engine) SetArrivalRecorder(rec *ArrivalRecorder) {
	if e != nil {
		e.arrivals.Store(rec)
	}
}

// mix es el callback de playback: arma output (S16LE) con la suma de todos los streams. at
// (UnixNano) es la hora del callback: con la cancelación de eco activa, la salida se copia a
// echoRef con esa hora.
func (e *Engine) mix(output []byte, at int64) {
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
				// Sobrevive a las pausas de la charla: el jitter buffer conserva lo medido de la red.
				if now.Sub(stream.lastActive) > config.JitterStreamIdleEvict {
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
	// (red de seguridad para los transientes que pasan durante el attack). Lo que sale es la
	// referencia de la cancelación de eco: ring preasignado, sin locks.
	echo := e.echoActive.Load()
	if echo {
		e.echoRef.begin()
	}
	for i := 0; i < samples; i++ {
		clipped := softClip(e.mixBuffer[i])
		binary.LittleEndian.PutUint16(output[i*2:i*2+2], uint16(clipped))
		if echo {
			e.echoRef.put(i, clipped)
		}
	}
	if echo {
		e.echoRef.publish(samples, at)
	}
}

// CancelEcho saca del frame capturado (S16LE, 20 ms, en el lugar) lo que sonó por la salida de
// este Engine (WebRTC AEC3): antes le pasa la referencia que ya había sonado cuando se capturó
// (at − ReferenceOffset). Va primero de todo en la cadena de envío. Lo llaman la goroutine de
// envío y la del celular, nunca un callback de malgo. Con Enabled en false deja de copiar la
// referencia y libera el AEC. Si el AEC falla (al crearlo o al procesar) devuelve el error una
// sola vez y queda apagado en este Engine: el frame sigue sin cancelar.
func (e *Engine) CancelEcho(pcm []byte, at time.Time, settings EchoSettings) error {
	if e == nil || len(pcm) != streamFrameBytes {
		return nil
	}
	e.echoMu.Lock()
	defer e.echoMu.Unlock()
	if !settings.Enabled || e.stopping.Load() {
		e.closeEcho()
		return nil
	}
	if e.echo == nil || settings.Phone != e.echoPhone {
		if e.echoFailed != nil {
			return nil
		}
		e.closeEcho()
		echo, err := newEchoCanceller()
		if err != nil {
			e.echoFailed = err
			return err
		}
		e.echo, e.echoPhone = echo, settings.Phone
		e.echoReader.reset(e.echoRef)
		e.echoActive.Store(true)
	}

	for i := range e.echoPCM {
		e.echoPCM[i] = int16(binary.LittleEndian.Uint16(pcm[i*2:]))
	}
	render := e.echoReader.take(e.echoRef, at.Add(-settings.ReferenceOffset).UnixNano())
	if err := e.echo.process(render, e.echoPCM[:], settings.DelayMS); err != nil {
		e.closeEcho()
		e.echoFailed = err
		return err
	}
	for i, sample := range e.echoPCM {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(sample))
	}

	e.echoFrames.Add(1)
	if e.echoCount++; e.echoCount >= config.AudioEchoStatsFrames {
		e.echoCount = 0
		erle, hasERLE, delay, hasDelay := e.echo.stats()
		if hasERLE {
			e.echoERLESum.Add(int64(math.Round(erle * 100)))
			e.echoERLECount.Add(1)
		}
		if hasDelay {
			e.echoDelayMS.Store(int64(delay))
		}
	}
	return nil
}

// closeEcho apaga la referencia y libera el AEC (bajo echoMu).
func (e *Engine) closeEcho() {
	e.echoActive.Store(false)
	if e.echo != nil {
		e.echo.close()
		e.echo = nil
	}
	e.echoCount = 0
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
	if rec := e.arrivals.Load(); rec != nil {
		rec.Record(senderID, seq, talkspurt, now)
	}
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
		e.stopping.Store(true)
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
		e.echoMu.Lock()
		e.closeEcho()
		e.echoMu.Unlock()
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
