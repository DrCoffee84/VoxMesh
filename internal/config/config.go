package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type MicMode string

const (
	VoiceActivation MicMode = "voice"
	PushToTalk      MicMode = "ptt"
)

// Config es lo que se persiste en voxmesh.json. Los campos se dividen en dos grupos:
//   - LiveSettings: los que la vista de ajustes aplica en vivo y revierte al descartar.
//   - El resto: confirmados al tocar Guardar (Username, puertos) o escritos en segundo plano
//     (LastPeer, token del celular, soundboard), que la vista de ajustes nunca revierte.
//
// Al agregar un campo, decidir en cuál de los dos va. LiveSettings está embebido sin tag, así
// que encoding/json aplana sus campos: el archivo queda igual que antes de la división.
type Config struct {
	LiveSettings
	RoomName         string            `json:"room_name"`
	Username         string            `json:"username"`
	ListenAddress    string            `json:"listen_address"`
	LastPeer         string            `json:"last_peer"`
	PhoneMicToken    string            `json:"phone_mic_token"`
	PhoneMicPort     int               `json:"phone_mic_port"`
	SoundboardMuted  bool              `json:"soundboard_muted"`
	SoundboardVolume float32           `json:"soundboard_volume"`
	SoundBinds       map[string]string `json:"sound_binds,omitempty"`
	StopSoundBind    string            `json:"stop_sound_bind,omitempty"`
	// PeerVolumes guarda el volumen por usuario (slider debajo de cada avatar) por nombre de
	// usuario: el ID de participante cambia con la dirección, el nombre no. 1.0 no se guarda.
	PeerVolumes map[string]float64 `json:"peer_volumes,omitempty"`
	// Estado de la ventana al cerrar. El tamaño es el último normal (en unidades de Fyne): si se
	// cerró maximizada se conserva el anterior, así al restaurar vuelve a ese tamaño.
	WindowWidth     float32 `json:"window_width"`
	WindowHeight    float32 `json:"window_height"`
	WindowMaximized bool    `json:"window_maximized"`
}

// Tamaño de ventana por defecto y mínimo aceptado al cargar (más chico = archivo roto).
const (
	DefaultWindowWidth  = 900
	DefaultWindowHeight = 620
	minWindowWidth      = 400
	minWindowHeight     = 300
)

// LiveSettings son los campos que la vista de ajustes publica en cuanto se tocan (para poder
// probar filtros escuchando el micrófono). Al abrir la vista se toma una copia como base; si se
// descartan los cambios se restaura esa base. Solo tipos comparables: se compara con ==.
type LiveSettings struct {
	MicMode               MicMode `json:"mic_mode"`
	AudioFilterEnabled    bool    `json:"audio_filter_enabled"`
	VADThreshold          float32 `json:"vad_threshold"`
	ThresholdDB           float32 `json:"threshold_db"`
	RNNoiseEnabled        bool    `json:"rnnoise_enabled"`
	VADEnabled            bool    `json:"vad_enabled"`
	NoiseGateEnabled      bool    `json:"noise_gate_enabled"`
	InputGainDB           float32 `json:"input_gain_db"`
	HighPassEnabled       bool    `json:"high_pass_enabled"`
	HighPassHz            float32 `json:"high_pass_hz"`
	LowPassEnabled        bool    `json:"low_pass_enabled"`
	LowPassHz             float32 `json:"low_pass_hz"`
	NotchEnabled          bool    `json:"notch_enabled"`
	NotchHz               float32 `json:"notch_hz"`
	CompressorEnabled     bool    `json:"compressor_enabled"`
	CompressorThresholdDB float32 `json:"compressor_threshold_db"`
	CompressorRatio       float32 `json:"compressor_ratio"`
	ExpanderEnabled       bool    `json:"expander_enabled"`
	ExpanderThresholdDB   float32 `json:"expander_threshold_db"`
	ExpanderRatio         float32 `json:"expander_ratio"`
	LimiterEnabled        bool    `json:"limiter_enabled"`
	LimiterThresholdDB    float32 `json:"limiter_threshold_db"`
	OutputFilterEnabled   bool    `json:"output_filter_enabled"`
	OutputGainDB          float32 `json:"output_gain_db"`
	OutputHighPassEnabled bool    `json:"output_high_pass_enabled"`
	OutputHighPassHz      float32 `json:"output_high_pass_hz"`
	OutputLowPassEnabled  bool    `json:"output_low_pass_enabled"`
	OutputLowPassHz       float32 `json:"output_low_pass_hz"`
	OutputLimiterEnabled  bool    `json:"output_limiter_enabled"`
	OutputLimiterDB       float32 `json:"output_limiter_db"`
	VADHoldMS             int     `json:"vad_hold_ms"`
	GateHoldMS            int     `json:"gate_hold_ms"`
	LiveMonitoring        bool    `json:"live_monitoring"`
	InputDevice           string  `json:"input_device"`
	OutputDevice          string  `json:"output_device"`
	PhoneMicBufferEnabled bool    `json:"phone_mic_buffer_enabled"`
	PhoneMicBufferMS      int     `json:"phone_mic_buffer_ms"`
	ShowStatusBar         bool    `json:"show_status_bar"`
	JitterBufferMS        int     `json:"jitter_buffer_ms"`
	EventSoundsEnabled    bool    `json:"event_sounds_enabled"`
	AllowHostMigration    bool    `json:"allow_host_migration"`
	LogLevel              int     `json:"log_level"`
}

func Default() Config {
	return Config{
		LiveSettings: LiveSettings{
			MicMode:               VoiceActivation,
			AudioFilterEnabled:    true,
			VADThreshold:          0.5,
			ThresholdDB:           -42,
			RNNoiseEnabled:        true,
			VADEnabled:            true,
			NoiseGateEnabled:      true,
			HighPassHz:            80,
			LowPassHz:             12000,
			NotchHz:               50,
			CompressorThresholdDB: -18,
			CompressorRatio:       3,
			ExpanderThresholdDB:   -50,
			ExpanderRatio:         2,
			LimiterEnabled:        true,
			LimiterThresholdDB:    -1,
			OutputFilterEnabled:   true,
			OutputHighPassHz:      80,
			OutputLowPassHz:       12000,
			OutputLimiterEnabled:  true,
			OutputLimiterDB:       -1,
			VADHoldMS:             250,
			GateHoldMS:            250,
			LiveMonitoring:        false,
			InputDevice:           "Sistema predeterminado",
			OutputDevice:          "Sistema predeterminado",
			PhoneMicBufferEnabled: true,
			PhoneMicBufferMS:      40,
			ShowStatusBar:         true,
			JitterBufferMS:        120,
			EventSoundsEnabled:    true,
			AllowHostMigration:    true,
			LogLevel:              0,
		},
		RoomName:         "Mi sala",
		Username:         "Usuario",
		ListenAddress:    ":47830",
		PhoneMicPort:     47831,
		SoundboardVolume: 1.0,
		SoundBinds:       make(map[string]string),
		WindowWidth:      DefaultWindowWidth,
		WindowHeight:     DefaultWindowHeight,
	}
}

// Clone devuelve una copia independiente: el struct se copia por valor y los mapas (SoundBinds,
// PeerVolumes: los únicos campos por referencia) se duplican, así el clon se puede modificar sin
// tocar al original.
func (c Config) Clone() Config {
	if c.SoundBinds != nil {
		binds := make(map[string]string, len(c.SoundBinds))
		for id, bind := range c.SoundBinds {
			binds[id] = bind
		}
		c.SoundBinds = binds
	}
	if c.PeerVolumes != nil {
		volumes := make(map[string]float64, len(c.PeerVolumes))
		for name, volume := range c.PeerVolumes {
			volumes[name] = volume
		}
		c.PeerVolumes = volumes
	}
	return c
}

func Path(executable string) string {
	return filepath.Join(filepath.Dir(executable), "voxmesh.json")
}

func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default(), err
	}
	if cfg.SoundBinds == nil {
		cfg.SoundBinds = make(map[string]string)
	}
	if cfg.ListenAddress == "" || cfg.ListenAddress == ":0" {
		cfg.ListenAddress = ":47830"
	}
	if cfg.PhoneMicPort <= 0 {
		cfg.PhoneMicPort = 47831
	}
	if cfg.SoundboardVolume <= 0 {
		cfg.SoundboardVolume = 1.0
	}
	if cfg.JitterBufferMS < 40 || cfg.JitterBufferMS > 500 {
		cfg.JitterBufferMS = 120
	}
	if cfg.LogLevel < 0 || cfg.LogLevel > 2 {
		cfg.LogLevel = 0
	}
	if cfg.WindowWidth < minWindowWidth || cfg.WindowHeight < minWindowHeight {
		cfg.WindowWidth, cfg.WindowHeight = DefaultWindowWidth, DefaultWindowHeight
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}
