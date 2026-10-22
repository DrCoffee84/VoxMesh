package audio

import (
	"math"
	"time"

	"voxmesh/internal/config"
)

// Traducción de la config (tabla config.Params) a lo que usa cada parte del pipeline. Son el
// único lugar que conoce los dos lados: corren por frame / por callback, así que solo copian
// campos y convierten unidades.

// JitterTuning son los parámetros del jitter buffer por emisor (perfil de salida).
type JitterTuning struct {
	MaxMS            int     // tope del colchón adaptativo
	MinMS            int     // piso del colchón adaptativo
	Percentile       float64 // 0–1: percentil de la variación de retardo que cubre el colchón
	MarginFrames     int
	PLCMaxFrames     int // frames perdidos seguidos que se ocultan; después, silencio
	TalkspurtGap     time.Duration
	DriftSlackFrames int
	DriftForceFrames int
}

// PlaybackSettings es todo lo que el callback de playback lee una vez por buffer.
type PlaybackSettings struct {
	Filter              FilterSettings // cadena de salida por emisor
	FilterEnabled       bool
	Jitter              JitterTuning
	BusLimiterThreshold float64 // escala S16
	BusLimiterReleaseMS float64
}

// OpusSettings es la configuración del encoder (solo del emisor: el receptor decodifica
// cualquier combinación, no rompe compatibilidad).
type OpusSettings struct {
	Music       bool // opus.AppAudio en vez de AppVoIP
	BitrateKbps int
	Complexity  int
	FEC         bool
	LossPerc    int
}

// InputSettings arma la cadena de envío (Ajustes → Entrada).
func InputSettings(cfg *config.Config) (NoiseGate, FilterSettings) {
	return NoiseGate{ThresholdDB: cfg.ThresholdDB}, FilterSettings{
		RNNoiseEnabled:      cfg.RNNoiseEnabled,
		VADEnabled:          cfg.VADEnabled,
		NoiseGateEnabled:    cfg.NoiseGateEnabled,
		GainDB:              cfg.InputGainDB,
		HighPassEnabled:     cfg.HighPassEnabled,
		HighPassHz:          cfg.HighPassHz,
		LowPassEnabled:      cfg.LowPassEnabled,
		LowPassHz:           cfg.LowPassHz,
		NotchEnabled:        cfg.NotchEnabled,
		NotchHz:             cfg.NotchHz,
		NotchQ:              cfg.NotchQ,
		CompressorEnabled:   cfg.CompressorEnabled,
		CompressorThreshold: cfg.CompressorThresholdDB,
		CompressorRatio:     cfg.CompressorRatio,
		CompressorAttackMS:  cfg.CompressorAttackMS,
		CompressorReleaseMS: cfg.CompressorReleaseMS,
		ExpanderEnabled:     cfg.ExpanderEnabled,
		ExpanderThreshold:   cfg.ExpanderThresholdDB,
		ExpanderRatio:       cfg.ExpanderRatio,
		ExpanderAttackMS:    cfg.ExpanderAttackMS,
		ExpanderReleaseMS:   cfg.ExpanderReleaseMS,
		LimiterEnabled:      cfg.LimiterEnabled,
		LimiterThreshold:    cfg.LimiterThresholdDB,
		VADHoldMS:           cfg.VADHoldMS,
		GateHoldMS:          cfg.GateHoldMS,
		RNNoiseDryMix:       1 - cfg.RNNoiseMix,
		ShoutEnabled:        cfg.ShoutEnabled,
		ShoutThresholdDB:    cfg.ShoutThresholdDB,
		ShoutDryMix:         cfg.ShoutDryMix,
		ShoutHoldMS:         cfg.ShoutHoldMS,
		SendCeilingDB:       cfg.SendCeilingDB,
		SendReleaseMS:       cfg.SendLimiterReleaseMS,
		PreRollFrames:       cfg.PreRollMS / 20,
		FadeInSamples:       int(cfg.FadeInMS * SampleRate / 1000),
	}
}

// DefaultFilterSettings es la cadena de envío con los valores de fábrica.
func DefaultFilterSettings() FilterSettings {
	cfg := config.Default()
	_, settings := InputSettings(&cfg)
	return settings
}

// OutputSettings arma lo que lee el callback de playback (Ajustes → Salida).
func OutputSettings(cfg *config.Config) PlaybackSettings {
	return PlaybackSettings{
		Filter: FilterSettings{
			GainDB:           cfg.OutputGainDB,
			HighPassEnabled:  cfg.OutputHighPassEnabled,
			HighPassHz:       cfg.OutputHighPassHz,
			LowPassEnabled:   cfg.OutputLowPassEnabled,
			LowPassHz:        cfg.OutputLowPassHz,
			LimiterEnabled:   cfg.OutputLimiterEnabled,
			LimiterThreshold: cfg.OutputLimiterDB,
		},
		FilterEnabled: cfg.OutputFilterEnabled,
		Jitter: JitterTuning{
			MaxMS:            cfg.JitterBufferMS,
			MinMS:            cfg.JitterMinMS,
			Percentile:       float64(cfg.JitterPercentile) / 100,
			MarginFrames:     cfg.JitterMarginMS / 20,
			PLCMaxFrames:     cfg.JitterPLCMaxMS / 20,
			TalkspurtGap:     time.Duration(cfg.JitterTalkspurtGapMS) * time.Millisecond,
			DriftSlackFrames: cfg.JitterDriftSlackMS / 20,
			DriftForceFrames: cfg.JitterDriftForceMS / 20,
		},
		BusLimiterThreshold: dbToS16(cfg.BusLimiterDB),
		BusLimiterReleaseMS: float64(cfg.BusLimiterReleaseMS),
	}
}

var defaultPlayback = func() PlaybackSettings {
	cfg := config.Default()
	return OutputSettings(&cfg)
}()

// DefaultJitterTuning son los parámetros de fábrica del jitter buffer.
func DefaultJitterTuning() JitterTuning {
	return defaultPlayback.Jitter
}

// EncoderSettings arma la configuración del encoder Opus.
func EncoderSettings(cfg *config.Config) OpusSettings {
	return OpusSettings{
		Music:       cfg.OpusMusicMode,
		BitrateKbps: cfg.OpusBitrateKbps,
		Complexity:  cfg.OpusComplexity,
		FEC:         cfg.OpusFEC,
		LossPerc:    cfg.OpusLossPerc,
	}
}

// dbToS16 convierte dBFS a amplitud en escala S16.
func dbToS16(db float32) float64 {
	return math.Pow(10, float64(db)/20) * math.MaxInt16
}
