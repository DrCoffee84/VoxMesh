package audio

import (
	"fmt"
	"math"
	"sync"

	rnnoise "github.com/VoiceBlender/rnnoise-go"
	rnmodel "github.com/VoiceBlender/rnnoise-go/model"

	"voxmesh/internal/config"
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

// ProcessSamplesWithOptions pasa las muestras por RNNoise in-place (cada bloque de 480 se copia
// a float antes de escribir la salida en el mismo lugar), sin allocations.
func (r *RNNoise) ProcessSamplesWithOptions(samples []int16, gate NoiseGate, vadThreshold float32, vadEnabled, gateEnabled bool) (bool, float32, error) {
	if r == nil || r.denoiser == nil {
		return false, 0, fmt.Errorf("RNNoise no está inicializado")
	}
	if len(samples) == 0 {
		return false, 0, nil
	}

	maxVAD := float32(0)
	hasSpeech := !vadEnabled

	// Usamos la API float32 para controlar nosotros la vuelta a int16:
	// ProcessInt16 hace int16(v) sin clamp y un pico > 32767 da la vuelta (wrap-around).
	var inputFrame [RNNoiseFrameSamples]float32
	var outputFrame [RNNoiseFrameSamples]float32
	var resultFrame [RNNoiseFrameSamples]int16

	r.mu.Lock()
	defer r.mu.Unlock()
	for start := 0; start < len(samples); start += RNNoiseFrameSamples {
		end := start + RNNoiseFrameSamples
		if end > len(samples) {
			end = len(samples)
		}
		clear(inputFrame[:])
		for i, sample := range samples[start:end] {
			inputFrame[i] = float32(sample)
		}
		vad, err := r.denoiser.Process(outputFrame[:], inputFrame[:])
		if err != nil {
			return false, 0, fmt.Errorf("procesar frame RNNoise: %w", err)
		}
		for i, value := range outputFrame {
			resultFrame[i] = floatToInt16(value)
		}
		if vad > maxVAD {
			maxVAD = vad
		}
		gateOpen := true
		if gateEnabled {
			gateOpen = gate.Apply(resultFrame[:])
		}
		if (!vadEnabled || vad >= vadThreshold) && gateOpen {
			hasSpeech = true
		}
		copy(samples[start:end], resultFrame[:end-start])
	}
	return hasSpeech, maxVAD, nil
}

// floatToInt16 redondea una muestra float en escala S16 y la satura al rango int16.
func floatToInt16(value float32) int16 {
	if value != value { // NaN
		return 0
	}
	if value >= math.MaxInt16 {
		return math.MaxInt16
	}
	if value <= math.MinInt16 {
		return math.MinInt16
	}
	return int16(math.Round(float64(value)))
}

// PrepareOutgoingPCM procesa un bloque suelto (grabación de prueba) con los filtros por defecto.
func PrepareOutgoingPCM(data []byte, denoiser *RNNoise, gate NoiseGate, filterEnabled bool, vadThreshold float32) ([]byte, bool, error) {
	settings := DefaultFilterSettings()
	return PrepareOutgoingPCMWithFilters(data, denoiser, gate, filterEnabled, vadThreshold, settings, nil)
}

// PrepareOutgoingPCMWithFilters es PrepareOutgoingFrames en bytes S16LE: devuelve los frames que
// saldrían concatenados (pre-roll incluido, que es audio anterior al frame actual). Lo usan la
// escucha de prueba y la grabación; la sala usa PrepareOutgoingFrames directo.
func PrepareOutgoingPCMWithFilters(data []byte, denoiser *RNNoise, gate NoiseGate, filterEnabled bool, vadThreshold float32, settings FilterSettings, effects *Effects) ([]byte, bool, error) {
	if len(data) == 0 {
		return nil, false, nil
	}
	samples, err := DecodePCM(data)
	if err != nil {
		return nil, false, err
	}
	frames, _, err := PrepareOutgoingFrames(samples, denoiser, gate, filterEnabled, vadThreshold, settings, effects)
	if err != nil || len(frames) == 0 {
		return nil, false, err
	}
	if len(frames) == 1 {
		return EncodePCM(frames[0]), true, nil
	}
	total := 0
	for _, frame := range frames {
		total += len(frame)
	}
	joined := make([]int16, 0, total)
	for _, frame := range frames {
		joined = append(joined, frame...)
	}
	return EncodePCM(joined), true, nil
}

// PrepareOutgoingFrames corre la cadena de envío sobre un frame (in-place) y devuelve los frames
// a mandar, en orden: ninguno con la compuerta cerrada; al abrirse, primero el pre-roll. started
// indica que arrancó una frase. Los slices son del Effects (o el propio samples) y valen hasta la
// próxima llamada. Sin effects no hay estado entre frames: ni hold, ni pre-roll, ni envolvente.
//
// Orden: ganancia/filtros → RNNoise (siempre, sin bypass) → VAD/compuerta con hold → limitador
// con envolvente (un solo limitador real; el tanh de entrada solo actúa si InputGainDB > 0) →
// pre-roll / fade-in.
func PrepareOutgoingFrames(samples []int16, denoiser *RNNoise, gate NoiseGate, filterEnabled bool, vadThreshold float32, settings FilterSettings, effects *Effects) ([][]int16, bool, error) {
	if len(samples) == 0 {
		return nil, false, nil
	}
	ceiling := sendCeiling(settings, filterEnabled)

	if !filterEnabled {
		limitSend(samples, ceiling, effects)
		return [][]int16{samples}, false, nil
	}

	if effects != nil {
		// El tanh de entrada distorsiona lo que entra al denoiser: queda solo como protección
		// cuando el usuario sube la ganancia. El techo real lo pone el limitador del final.
		input := settings
		input.LimiterEnabled = settings.LimiterEnabled && settings.GainDB > 0
		effects.ApplySamples(samples, input)
	}

	if vadThreshold <= 0 {
		vadThreshold = 0.5
	}

	// Ya no hay bypass por grito: RNNoise tiene 20 ms de retardo interno (Delay() = 2*480)
	// y saltearlo frame a frame desordenaba y duplicaba audio. El "cirkci" original venía
	// del wrap-around de int16 en ProcessInt16, que ahora evita el clamp de floatToInt16.
	hasSpeech := true
	if settings.RNNoiseEnabled && denoiser != nil {
		speech, _, err := denoiser.ProcessSamplesWithOptions(samples, gate, vadThreshold, settings.VADEnabled, false)
		if err != nil {
			return nil, false, err
		}
		hasSpeech = speech
	}

	gateOpen := !settings.NoiseGateEnabled || gate.Open(samples)
	keepOpen := hasSpeech && gateOpen
	if effects != nil {
		keepOpen = effects.KeepVoiceOpen(hasSpeech, gateOpen, settings)
	}

	limitSend(samples, ceiling, effects)
	if effects == nil {
		if !keepOpen {
			return nil, false, nil
		}
		return [][]int16{samples}, false, nil
	}
	frames, started := effects.gateFrames(samples, keepOpen)
	return frames, started, nil
}

// sendCeiling es el techo del limitador de envío: AudioSendPeakCeiling, o el umbral del limiter
// del usuario si está activo y es más bajo.
func sendCeiling(settings FilterSettings, filterEnabled bool) int16 {
	ceiling := config.AudioSendPeakCeiling
	if filterEnabled && settings.LimiterEnabled {
		if user := math.Pow(10, float64(settings.LimiterThreshold)/20) * math.MaxInt16; user < float64(ceiling) {
			ceiling = int16(user)
		}
	}
	return ceiling
}

// limitSend usa la envolvente del Effects; sin Effects, un limitador nuevo (attack instantáneo:
// el techo se respeta igual, solo que la ganancia no se arrastra entre llamadas).
func limitSend(samples []int16, ceiling int16, effects *Effects) {
	if effects != nil {
		effects.limitSend(samples, ceiling)
		return
	}
	limiter := newPeakLimiter(float64(ceiling), config.AudioSendLimiterAttackMS, config.AudioSendLimiterReleaseMS)
	limiter.ProcessInt16(samples)
}
