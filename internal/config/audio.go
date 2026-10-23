package config

import "time"

// Constantes internas del pipeline de audio, en escala S16 (full scale = 32767). Lo ajustable
// por el usuario (techos, tiempos, Opus, jitter buffer, deriva) está en la tabla Params; esto
// queda fijo porque cambiarlo solo agrega formas de romper el audio.
const (
	// AudioSendLimiterAttackMS es el attack del limitador de envío. 0 = instantáneo: sin
	// lookahead es la única forma de que ningún pico pase el techo; la ganancia vuelve de a
	// poco con el release, así que no hay escalones entre frames.
	AudioSendLimiterAttackMS = 0.0
	// AudioMaxPreRollFrames es el tope del pre-roll (Params: preroll_ms, máximo 100 ms).
	AudioMaxPreRollFrames = 5
	// AudioCaptureQueueFrames son los frames del micrófono (500 ms) que pueden esperar a la
	// goroutine de envío si la PC se traba. Antes eran 8 (160 ms) y lo que no entraba se tiraba
	// sin avisar: los demás recibían menos audio que tiempo y se les cortaba.
	AudioCaptureQueueFrames = 25

	// AudioSoftClipKnee es donde arranca la rodilla de softClip en el bus de mezcla (~ -0.8 dBFS).
	// El techo del limitador del bus (bus_limiter_db) queda por debajo, así softClip actúa solo
	// como red de seguridad durante el attack.
	AudioSoftClipKnee int32 = 30000
	// AudioSoftClipRange es cuánto puede subir softClip por encima de la rodilla (asíntota en 32700).
	AudioSoftClipRange = 2700.0
	// AudioSoftClipCurve controla qué tan rápido la rodilla se acerca a la asíntota.
	AudioSoftClipCurve = 3000.0
	// AudioBusLimiterAttackMS es el tiempo de reacción del limitador del bus ante un pico.
	AudioBusLimiterAttackMS = 1.0
)

// Codec Opus del audio de red (siempre Opus, 48 kHz mono, frames de 20 ms).
const (
	// OpusMaxPacketBytes es el buffer de salida del encoder (un paquete Opus nunca pasa 1275 bytes).
	OpusMaxPacketBytes = 1275
)

// Parámetros internos del jitter buffer por emisor (frames de 20 ms).
const (
	// JitterSpliceSamples es el largo del crossfade (~2.5 ms) con el que se empalman frames no
	// contiguos: arranque de frase, descarte por deriva, reset. Lo que sale del decoder Opus
	// (real, FEC o PLC) es continuo y pasa sin crossfade.
	JitterSpliceSamples = 120
	// JitterFadeOutSamples es cuánto sigue el PLC de Opus, bajando hasta silencio, cuando el
	// buffer se queda sin frames (~60 ms): un pico de lag más corto suena como un estirón de la voz
	// en vez de un corte. Antes eran 10 ms y cualquier pico se oía como un hueco.
	JitterFadeOutSamples = 2880
	// JitterDriftCooldownFrames es la cantidad mínima de frames entre dos descartes por deriva.
	JitterDriftCooldownFrames = 5
	// JitterDriftQuietRMS es el RMS (escala S16, ~ -40 dBFS) por debajo del cual un frame se puede
	// descartar por deriva sin que se note: se posterga el descarte hasta encontrar uno así.
	JitterDriftQuietRMS = 330.0

	// JitterAdaptiveWindow es la cantidad de llegadas (~10 s de voz) sobre la que se mide la
	// variación de retardo para elegir el colchón.
	JitterAdaptiveWindow = 500
	// JitterAdaptiveMinSamples son las llegadas mínimas antes de confiar en la medición; hasta
	// entonces se usa el máximo (jitter_buffer_ms).
	JitterAdaptiveMinSamples = 50
	// JitterHistogramBucketMS es la resolución del histograma de variación de retardo.
	JitterHistogramBucketMS = 5
	// JitterPeakThresholdMS es la variación de retardo a partir de la cual una llegada es parte
	// de un pico (el enlace se trabó y entregó todo junto, típico del Wi-Fi).
	JitterPeakThresholdMS = 60
	// JitterPeakMinCount son los picos que tiene que haber en JitterPeakMemory para que el colchón
	// cubra el más alto: uno suelto no alcanza (no se sabe si se repite); dos ya son un patrón y
	// el percentil los deja afuera porque cada pico toca pocos paquetes.
	JitterPeakMinCount = 2
	// JitterPeakMemory son las llegadas (~20 s de voz) durante las que se recuerda un pico. Más
	// que la ventana del percentil: con picos de tamaño variable, recordar más evita que el
	// próximo supere al colchón (simulación de jitter_sim_test.go: 4.9 → 3.5 cortes/min en Wi-Fi).
	JitterPeakMemory = 1000

	// JitterStreamIdleEvict es cuánto sobrevive el stream de un emisor de red sin paquetes. Su
	// jitter buffer guarda lo medido de la red (colchón, picos): con 2 s se perdía en cada pausa y
	// cada frase arrancaba con el colchón máximo.
	JitterStreamIdleEvict = time.Minute
)
