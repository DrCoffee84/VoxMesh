package config

// Umbrales del pipeline de audio, en escala S16 (full scale = 32767).
const (
	// AudioSendPeakCeiling es el techo de pico del frame saliente (~ -1.7 dBFS).
	AudioSendPeakCeiling int16 = 27000

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
)

// Parámetros del jitter buffer por emisor (frames de 20 ms).
const (
	// JitterSpliceSamples es el largo del crossfade (~2.5 ms) con el que se empalman
	// frames no contiguos: PLC, recuperación tras una pérdida, descarte por deriva y arranque.
	JitterSpliceSamples = 120
	// JitterPLCMaxFrames es cuántos frames perdidos seguidos se ocultan repitiendo el último (60 ms).
	JitterPLCMaxFrames = 3
	// JitterPLCAttenuation es la ganancia que se suma en cada frame ocultado sucesivo.
	JitterPLCAttenuation = 0.5
	// JitterFadeOutSamples es el fade a silencio (~10 ms) cuando el buffer se queda sin frames.
	JitterFadeOutSamples = 480
	// JitterTalkspurtGapMS es el silencio a partir del cual la próxima frase espera el colchón completo.
	// Por debajo, un underrun se considera un pico de lag y se reanuda apenas llega el frame demorado.
	JitterTalkspurtGapMS = 300
	// JitterDriftSlackFrames son los frames de más tolerados sobre el objetivo antes de descartar uno.
	JitterDriftSlackFrames = 2
	// JitterDriftCooldownFrames es la cantidad mínima de frames entre dos descartes por deriva.
	JitterDriftCooldownFrames = 5
)
