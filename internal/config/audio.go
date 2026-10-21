package config

// Umbrales del pipeline de audio, en escala S16 (full scale = 32767).
const (
	// AudioSendPeakCeiling es el techo de pico del audio saliente (~ -1.7 dBFS). Lo aplica el
	// limitador con envolvente del final de la cadena de envío; si el usuario activa su
	// limitador con un umbral más bajo, manda ese umbral.
	AudioSendPeakCeiling int16 = 27000
	// AudioSendLimiterAttackMS es el attack del limitador de envío. 0 = instantáneo: sin
	// lookahead es la única forma de que ningún pico pase el techo; la ganancia vuelve de a
	// poco con el release, así que no hay escalones entre frames.
	AudioSendLimiterAttackMS = 0.0
	// AudioSendLimiterReleaseMS es lo que tarda el limitador de envío en recuperar ganancia.
	AudioSendLimiterReleaseMS = 60.0
	// AudioPreRollFrames son los frames ya procesados (con la compuerta cerrada) que se mandan
	// antes del primero que abre la compuerta, para no comerse el arranque de la primera sílaba.
	AudioPreRollFrames = 2
	// AudioFadeInSamples es el fade-in (~3 ms) del primer frame que sale al abrirse la compuerta.
	AudioFadeInSamples = 144

	// AudioSoftClipKnee es donde arranca la rodilla de softClip en el bus de mezcla (~ -0.8 dBFS).
	AudioSoftClipKnee int32 = 30000
	// AudioSoftClipRange es cuánto puede subir softClip por encima de la rodilla (asíntota en 32700).
	AudioSoftClipRange = 2700.0
	// AudioSoftClipCurve controla qué tan rápido la rodilla se acerca a la asíntota.
	AudioSoftClipCurve = 3000.0

	// AudioBusLimiterThreshold es el nivel al que el limitador del bus lleva los picos.
	// Queda por debajo de AudioSoftClipKnee para que softClip actúe solo como red de
	// seguridad durante el attack (~ -1.1 dBFS).
	AudioBusLimiterThreshold = 29000.0
	// AudioBusLimiterAttackMS es el tiempo de reacción del limitador del bus ante un pico.
	AudioBusLimiterAttackMS = 1.0
	// AudioBusLimiterReleaseMS es el tiempo que tarda el limitador del bus en recuperar ganancia.
	AudioBusLimiterReleaseMS = 80.0

	// PeerVolumeMax es el volumen máximo por usuario (300 %) del slider debajo de cada avatar.
	PeerVolumeMax = 3.0
)

// Codec Opus del audio de red (siempre Opus, 48 kHz mono, frames de 20 ms).
const (
	// OpusBitrate en bits/s. Voz a 40 kbps con FEC queda en ~100-130 bytes por paquete.
	OpusBitrate = 40000
	// OpusComplexity va de 0 a 10; 9 es casi la máxima calidad con un costo despreciable a 20 ms.
	OpusComplexity = 9
	// OpusPacketLossPerc es la pérdida esperada que el encoder usa para dimensionar el FEC in-band.
	OpusPacketLossPerc = 8
	// OpusMaxPacketBytes es el buffer de salida del encoder (un paquete Opus nunca pasa 1275 bytes).
	OpusMaxPacketBytes = 1275
)

// Parámetros del jitter buffer por emisor (frames de 20 ms).
const (
	// JitterSpliceSamples es el largo del crossfade (~2.5 ms) con el que se empalman frames no
	// contiguos: arranque de frase, descarte por deriva, reset. Lo que sale del decoder Opus
	// (real, FEC o PLC) es continuo y pasa sin crossfade.
	JitterSpliceSamples = 120
	// JitterPLCMaxFrames es cuántos frames perdidos seguidos se ocultan con el PLC de Opus
	// (100 ms); a partir de ahí, silencio.
	JitterPLCMaxFrames = 5
	// JitterFadeOutSamples es el fade a silencio (~10 ms) cuando el buffer se queda sin frames.
	JitterFadeOutSamples = 480
	// JitterTalkspurtGapMS es el silencio a partir del cual la próxima frase espera el colchón completo.
	// Por debajo, un underrun se considera un pico de lag y se reanuda apenas llega el frame demorado.
	JitterTalkspurtGapMS = 300
	// JitterDriftSlackFrames son los frames de más tolerados sobre el objetivo antes de descartar uno.
	JitterDriftSlackFrames = 2
	// JitterDriftCooldownFrames es la cantidad mínima de frames entre dos descartes por deriva.
	JitterDriftCooldownFrames = 5
	// JitterDriftQuietRMS es el RMS (escala S16, ~ -40 dBFS) por debajo del cual un frame se puede
	// descartar por deriva sin que se note: se posterga el descarte hasta encontrar uno así.
	JitterDriftQuietRMS = 330.0
	// JitterDriftForceFrames: si el buffer supera objetivo + slack + esto, se descarta aunque el
	// frame tenga voz (la latencia acumulada ya es peor que el corte).
	JitterDriftForceFrames = 4

	// JitterAdaptiveWindow es la cantidad de llegadas (~10 s de voz) sobre la que se mide la
	// variación de retardo para elegir el colchón.
	JitterAdaptiveWindow = 500
	// JitterAdaptiveMinSamples son las llegadas mínimas antes de confiar en la medición; hasta
	// entonces se usa el máximo (JitterBufferMS).
	JitterAdaptiveMinSamples = 50
	// JitterAdaptivePercentile es el percentil de la variación de retardo que el colchón cubre.
	JitterAdaptivePercentile = 0.95
	// JitterAdaptiveMarginFrames es el margen que se suma al percentil medido.
	JitterAdaptiveMarginFrames = 1
	// JitterHistogramBucketMS es la resolución del histograma de variación de retardo.
	JitterHistogramBucketMS = 5
)

// Micrófono del celular (servidor phonemic).
const (
	// PhoneMicDriftSlackFrames son los frames de más tolerados en la cola del celular antes de
	// descartar uno (corrige la deriva de reloj entre el celular y la PC).
	PhoneMicDriftSlackFrames = 2
	// PhoneMicDriftForceFrames: por encima de prebuffer + slack + esto se descarta aunque el frame
	// tenga voz.
	PhoneMicDriftForceFrames = 4
)
