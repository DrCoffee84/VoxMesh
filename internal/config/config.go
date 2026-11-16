package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"voxmesh/internal/i18n"
)

type MicMode string

const (
	VoiceActivation MicMode = "voice"
	PushToTalk      MicMode = "ptt"
)

// Config es lo que se persiste en voxmesh.json. Los campos se dividen en dos grupos:
//   - LiveSettings: los que la vista de ajustes aplica en vivo y revierte al descartar.
//   - El resto: confirmados al tocar Guardar (Username, puertos) o escritos en segundo plano
//     (sala, token del celular, soundboard), que la vista de ajustes nunca revierte.
//
// Al agregar un campo, decidir en cuál de los dos va. LiveSettings está embebido sin tag, así
// que encoding/json aplana sus campos: el archivo es un solo objeto plano.
type Config struct {
	LiveSettings
	RoomName string `json:"room_name"`
	Username string `json:"username"`
	// Language es el idioma de la interfaz ("en" | "es"; vacío o desconocido = "en"). Lo confirma
	// Guardar y se aplica al arrancar (i18n.Set en ui.New).
	Language         string            `json:"language"`
	ListenAddress    string            `json:"listen_address"`
	PhoneMicToken    string            `json:"phone_mic_token"`
	PhoneMicPort     int               `json:"phone_mic_port"`
	SoundboardMuted  bool              `json:"soundboard_muted"`
	SoundboardVolume float32           `json:"soundboard_volume"`
	SoundBinds       map[string]string `json:"sound_binds,omitempty"`
	StopSoundBind    string            `json:"stop_sound_bind,omitempty"`
	// StopAllSoundBind es el atajo de "Detener para todos" (AGENDA C37; sin tecla por defecto). Se
	// apaga con los demás atajos de la botonera.
	StopAllSoundBind string `json:"stop_all_sound_bind,omitempty"`
	// ToggleHotkeysBind es el atajo que prende y apaga los atajos de la botonera (AGENDA C20; sin
	// tecla por defecto, para no chocar con un juego). Es el único que anda con los atajos
	// apagados. SoundHotkeysOff = apagados (SoundBinds, StopSoundBind, StopAllSoundBind y Detener
	// quedan guardados).
	// Los escribe la botonera (botón, su atajo, el menú de la bandeja); Ajustes no los toca.
	ToggleHotkeysBind string `json:"toggle_hotkeys_bind,omitempty"`
	SoundHotkeysOff   bool   `json:"sound_hotkeys_off,omitempty"`
	// PeerVolumes guarda el volumen por usuario (slider debajo de cada avatar) por nombre de
	// usuario: el ID de participante cambia con la dirección, el nombre no. 1.0 no se guarda.
	PeerVolumes map[string]float64 `json:"peer_volumes,omitempty"`
	// Estado de la ventana al cerrar. El tamaño es el último normal (en unidades de Fyne): si se
	// cerró maximizada se conserva el anterior, así al restaurar vuelve a ese tamaño.
	WindowWidth     float32 `json:"window_width"`
	WindowHeight    float32 `json:"window_height"`
	WindowMaximized bool    `json:"window_maximized"`
	// ScreenResolution es la última resolución elegida para compartir pantalla ("full" | "1080p" |
	// "900p" | "720p"). Se guarda tal cual: vacía o desconocida se lee como 1080p con
	// screen.ParseResolution. La escribe el diálogo de "Compartir"; Ajustes no la toca.
	ScreenResolution string `json:"screen_resolution,omitempty"`
	// UsernamePromptOff es el "No volver a preguntar" del diálogo que pide el nombre al abrir la
	// app cuando es uno de fábrica. Lo escribe ese diálogo; Ajustes no lo toca.
	UsernamePromptOff bool `json:"username_prompt_off,omitempty"`
	// ShowLagGraph muestra la tira de lag debajo de cada participante (AGENDA C18; oculta por
	// defecto). La escribe el ícono de la barra de participantes; Ajustes no la toca.
	ShowLagGraph bool `json:"show_lag_graph,omitempty"`
	// HidePeerSliders oculta los sliders de la barra de participantes (AGENDA C36): el volumen de
	// cada uno y el propio con su nivel en vivo. Apagado (de fábrica) se ven. La escribe el ícono de
	// la barra de participantes; Ajustes no la toca.
	HidePeerSliders bool `json:"hide_peer_sliders,omitempty"`
	// TrayHintOff es el "No volver a mostrar" del aviso que sale cuando la X oculta la app en el
	// área de notificaciones (AGENDA C21). Lo escribe ese aviso; Ajustes no lo toca.
	TrayHintOff bool `json:"tray_hint_off,omitempty"`
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
//
// Los numéricos y booleanos tienen su default, rango y texto en la tabla Params (params.go): un
// campo nuevo de ese tipo se agrega acá y en la tabla, en ningún otro lado.
type LiveSettings struct {
	MicMode      MicMode `json:"mic_mode"`
	InputDevice  string  `json:"input_device"`
	OutputDevice string  `json:"output_device"`
	// ID de Windows del dispositivo elegido (vacío = predeterminado o celular). Si al abrir el
	// audio ya no está, se vuelve al predeterminado y se olvida.
	InputDeviceID  string `json:"input_device_id,omitempty"`
	OutputDeviceID string `json:"output_device_id,omitempty"`
	// Perfil cargado ("" = Default). Ver profiles.go.
	ActiveInputProfile  string `json:"active_input_profile,omitempty"`
	ActiveOutputProfile string `json:"active_output_profile,omitempty"`
	// ActivePalette es la paleta de colores (AGENDA C15): "" = Default, una de fábrica por su clave
	// o una propia (<AppDir>/paletas). Se aplica al instante al elegirla en Ajustes.
	ActivePalette string `json:"active_palette,omitempty"`

	// Perfil de entrada.
	AudioFilterEnabled    bool    `json:"audio_filter_enabled"`
	ShoutEnabled          bool    `json:"shout_enabled"`
	ShoutThresholdDB      float32 `json:"shout_threshold_db"`
	ShoutOnClip           bool    `json:"shout_on_clip"`
	ShoutDryMix           float32 `json:"shout_dry_mix"`
	ShoutHoldMS           int     `json:"shout_hold_ms"`
	AECEnabled            bool    `json:"aec_enabled"`
	RNNoiseEnabled        bool    `json:"rnnoise_enabled"`
	RNNoiseMix            float32 `json:"rnnoise_mix"`
	VADEnabled            bool    `json:"vad_enabled"`
	VADThreshold          float32 `json:"vad_threshold"`
	VADHoldMS             int     `json:"vad_hold_ms"`
	NoiseGateEnabled      bool    `json:"noise_gate_enabled"`
	ThresholdDB           float32 `json:"threshold_db"`
	GateHoldMS            int     `json:"gate_hold_ms"`
	InputGainDB           float32 `json:"input_gain_db"`
	HighPassEnabled       bool    `json:"high_pass_enabled"`
	HighPassHz            float32 `json:"high_pass_hz"`
	LowPassEnabled        bool    `json:"low_pass_enabled"`
	LowPassHz             float32 `json:"low_pass_hz"`
	NotchEnabled          bool    `json:"notch_enabled"`
	NotchHz               float32 `json:"notch_hz"`
	NotchQ                float32 `json:"notch_q"`
	CompressorEnabled     bool    `json:"compressor_enabled"`
	CompressorThresholdDB float32 `json:"compressor_threshold_db"`
	CompressorRatio       float32 `json:"compressor_ratio"`
	CompressorAttackMS    float32 `json:"compressor_attack_ms"`
	CompressorReleaseMS   float32 `json:"compressor_release_ms"`
	ExpanderEnabled       bool    `json:"expander_enabled"`
	ExpanderThresholdDB   float32 `json:"expander_threshold_db"`
	ExpanderRatio         float32 `json:"expander_ratio"`
	ExpanderAttackMS      float32 `json:"expander_attack_ms"`
	ExpanderReleaseMS     float32 `json:"expander_release_ms"`
	LimiterEnabled        bool    `json:"limiter_enabled"`
	LimiterThresholdDB    float32 `json:"limiter_threshold_db"`
	SendCeilingDB         float32 `json:"send_ceiling_db"`
	SendLimiterReleaseMS  float32 `json:"send_limiter_release_ms"`
	PreRollMS             int     `json:"preroll_ms"`
	FadeInMS              float32 `json:"fade_in_ms"`
	OpusMusicMode         bool    `json:"opus_music_mode"`
	OpusBitrateKbps       int     `json:"opus_bitrate_kbps"`
	OpusComplexity        int     `json:"opus_complexity"`
	OpusFEC               bool    `json:"opus_fec"`
	OpusLossPerc          int     `json:"opus_loss_perc"`
	PhoneMicBufferEnabled bool    `json:"phone_mic_buffer_enabled"`
	PhoneMicBufferMS      int     `json:"phone_mic_buffer_ms"`
	PhoneDriftSlackMS     int     `json:"phone_drift_slack_ms"`
	PhoneDriftForceMS     int     `json:"phone_drift_force_ms"`

	// Perfil de salida.
	ShoutDuckEnabled      bool    `json:"shout_duck_enabled"`
	ShoutDuckDB           float32 `json:"shout_duck_db"`
	ShoutDuckThresholdDB  float32 `json:"shout_duck_threshold_db"`
	OutputFilterEnabled   bool    `json:"output_filter_enabled"`
	OutputGainDB          float32 `json:"output_gain_db"`
	OutputHighPassEnabled bool    `json:"output_high_pass_enabled"`
	OutputHighPassHz      float32 `json:"output_high_pass_hz"`
	OutputLowPassEnabled  bool    `json:"output_low_pass_enabled"`
	OutputLowPassHz       float32 `json:"output_low_pass_hz"`
	OutputLimiterEnabled  bool    `json:"output_limiter_enabled"`
	OutputLimiterDB       float32 `json:"output_limiter_db"`
	JitterBufferMS        int     `json:"jitter_buffer_ms"`
	JitterMinMS           int     `json:"jitter_min_ms"`
	JitterPercentile      float32 `json:"jitter_percentile"`
	JitterMarginMS        int     `json:"jitter_margin_ms"`
	JitterPLCMaxMS        int     `json:"jitter_plc_max_ms"`
	JitterTalkspurtGapMS  int     `json:"jitter_talkspurt_gap_ms"`
	JitterDriftSlackMS    int     `json:"jitter_drift_slack_ms"`
	JitterDriftForceMS    int     `json:"jitter_drift_force_ms"`
	BusLimiterDB          float32 `json:"bus_limiter_db"`
	BusLimiterReleaseMS   float32 `json:"bus_limiter_release_ms"`
	PeerVolumeMax         float32 `json:"peer_volume_max"`

	// Fuera de los perfiles.
	LiveMonitoring     bool `json:"live_monitoring"`
	ShowStatusBar      bool `json:"show_status_bar"`
	CloseToTray        bool `json:"close_to_tray"`    // la X oculta la ventana: sigue junto al reloj (AGENDA C21)
	ShowLANInvites     bool `json:"show_lan_invites"` // "Invitar" del host: también las direcciones LAN/VPN
	EventSoundsEnabled bool `json:"event_sounds_enabled"`
	AllowHostMigration bool `json:"allow_host_migration"`
	UPnPLeaseHours     int  `json:"upnp_lease_hours"` // cuánto dura el puerto que abre UPnP al iniciar la sala
	LogLevel           int  `json:"log_level"`
}

// Puertos por defecto de la sala (UDP) y del servidor del celular.
const (
	DefaultListenPort    = 47830
	DefaultListenAddress = ":47830"
	DefaultPhoneMicPort  = 47831
	DefaultDevice        = "Sistema predeterminado"
	DefaultUsername      = "Usuario"
)

func Default() Config {
	live := LiveSettings{
		MicMode:      VoiceActivation,
		InputDevice:  DefaultDevice,
		OutputDevice: DefaultDevice,
	}
	for _, param := range Params {
		param.Set(&live, param.Default)
	}
	return Config{
		LiveSettings:     live,
		RoomName:         "Mi sala",
		Username:         DefaultUsername,
		Language:         string(i18n.EN),
		ListenAddress:    DefaultListenAddress,
		PhoneMicPort:     DefaultPhoneMicPort,
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

// AppDir es la carpeta de datos de VoxMesh (%AppData%\VoxMesh en Windows): salas, perfiles,
// logs y el certificado del celular.
func AppDir() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "VoxMesh"), nil
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
		cfg.ListenAddress = DefaultListenAddress
	}
	if cfg.PhoneMicPort <= 0 {
		cfg.PhoneMicPort = DefaultPhoneMicPort
	}
	// 0 es silencio (el slider de la botonera en el 0); fuera de 0–1, editado a mano, al rango.
	cfg.SoundboardVolume = min(max(cfg.SoundboardVolume, 0), 1)
	cfg.Language = string(i18n.Normalize(cfg.Language))
	// Valores fuera de rango (archivo editado a mano o de otra versión) vuelven a su default.
	ResetOutOfRange(&cfg.LiveSettings)
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
