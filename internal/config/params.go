package config

import (
	"fmt"
	"math"
	"reflect"
	"strings"
)

// ProfileKind es el tipo de perfil al que pertenece un parámetro ("" = a ninguno).
type ProfileKind string

const (
	InputProfile  ProfileKind = "entrada"
	OutputProfile ProfileKind = "salida"
)

// Param describe un ajuste numérico o booleano de LiveSettings. Es la única fuente de su
// default, su rango y cómo se muestra: de acá salen Default(), el control de rango de Load, los
// perfiles y los controles de Ajustes. Los booleanos usan Default 0/1 y no llevan rango. El texto
// está en los catálogos de i18n: "param.<Key>.label" y, opcional, "param.<Key>.help".
type Param struct {
	Key     string // clave json (voxmesh.json y perfiles); tiene que existir en LiveSettings
	Profile ProfileKind
	Default float64
	Min     float64
	Max     float64
	Step    float64
	Unit    string  // "dB", "dBFS", "ms", "Hz", "%", "kbps"...
	Scale   float64 // valor mostrado = guardado × Scale (0 = 1): mezclas 0–1 que se muestran en %
}

// Params es la tabla de todos los ajustes numéricos y booleanos.
var Params = []Param{
	// Entrada: cadena de envío.
	{Key: "audio_filter_enabled", Profile: InputProfile, Default: 1},

	{Key: "shout_enabled", Profile: InputProfile, Default: 1},
	{Key: "shout_threshold_db", Profile: InputProfile, Default: -5.5, Min: -40, Max: 0, Step: 0.5, Unit: "dBFS"},
	{Key: "shout_on_clip", Profile: InputProfile, Default: 1},
	{Key: "shout_dry_mix", Profile: InputProfile, Default: 0.2, Min: 0, Max: 1, Step: 0.01, Unit: "%", Scale: 100},
	{Key: "shout_hold_ms", Profile: InputProfile, Default: 300, Min: 0, Max: 1000, Step: 10, Unit: "ms"},

	{Key: "aec_enabled", Profile: InputProfile, Default: 0},
	{Key: "rnnoise_enabled", Profile: InputProfile, Default: 1},
	{Key: "rnnoise_mix", Profile: InputProfile, Default: 1, Min: 0, Max: 1, Step: 0.01, Unit: "%", Scale: 100},
	{Key: "vad_enabled", Profile: InputProfile, Default: 1},
	{Key: "vad_threshold", Profile: InputProfile, Default: 0.5, Min: 0, Max: 1, Step: 0.05},
	{Key: "vad_hold_ms", Profile: InputProfile, Default: 250, Min: 0, Max: 500, Step: 10, Unit: "ms"},
	{Key: "noise_gate_enabled", Profile: InputProfile, Default: 1},
	{Key: "threshold_db", Profile: InputProfile, Default: -42, Min: -60, Max: -5, Step: 0.5, Unit: "dBFS"},
	{Key: "gate_hold_ms", Profile: InputProfile, Default: 250, Min: 0, Max: 500, Step: 10, Unit: "ms"},

	{Key: "high_pass_enabled", Profile: InputProfile, Default: 0},
	{Key: "high_pass_hz", Profile: InputProfile, Default: 80, Min: 20, Max: 500, Step: 1, Unit: "Hz"},
	{Key: "low_pass_enabled", Profile: InputProfile, Default: 0},
	{Key: "low_pass_hz", Profile: InputProfile, Default: 12000, Min: 1000, Max: 18000, Step: 100, Unit: "Hz"},
	{Key: "notch_enabled", Profile: InputProfile, Default: 0},
	{Key: "notch_hz", Profile: InputProfile, Default: 50, Min: 40, Max: 1000, Step: 1, Unit: "Hz"},
	{Key: "notch_q", Profile: InputProfile, Default: 10, Min: 1, Max: 30, Step: 0.5},

	{Key: "input_gain_db", Profile: InputProfile, Default: 0, Min: -24, Max: 24, Step: 0.5, Unit: "dB"},
	{Key: "compressor_enabled", Profile: InputProfile, Default: 0},
	{Key: "compressor_threshold_db", Profile: InputProfile, Default: -18, Min: -50, Max: 0, Step: 0.5, Unit: "dB"},
	{Key: "compressor_ratio", Profile: InputProfile, Default: 3, Min: 1, Max: 12, Step: 0.1},
	{Key: "compressor_attack_ms", Profile: InputProfile, Default: 5, Min: 0, Max: 100, Step: 1, Unit: "ms"},
	{Key: "compressor_release_ms", Profile: InputProfile, Default: 100, Min: 10, Max: 1000, Step: 10, Unit: "ms"},
	{Key: "expander_enabled", Profile: InputProfile, Default: 0},
	{Key: "expander_threshold_db", Profile: InputProfile, Default: -50, Min: -70, Max: -10, Step: 0.5, Unit: "dB"},
	{Key: "expander_ratio", Profile: InputProfile, Default: 2, Min: 1, Max: 12, Step: 0.1},
	{Key: "expander_attack_ms", Profile: InputProfile, Default: 5, Min: 0, Max: 100, Step: 1, Unit: "ms"},
	{Key: "expander_release_ms", Profile: InputProfile, Default: 100, Min: 10, Max: 1000, Step: 10, Unit: "ms"},
	{Key: "limiter_enabled", Profile: InputProfile, Default: 0},
	{Key: "limiter_threshold_db", Profile: InputProfile, Default: -1, Min: -12, Max: 0, Step: 0.1, Unit: "dB"},

	{Key: "send_ceiling_db", Profile: InputProfile, Default: -1.7, Min: -12, Max: 0, Step: 0.1, Unit: "dBFS"},
	{Key: "send_limiter_release_ms", Profile: InputProfile, Default: 60, Min: 10, Max: 500, Step: 5, Unit: "ms"},
	{Key: "preroll_ms", Profile: InputProfile, Default: 40, Min: 0, Max: 100, Step: 20, Unit: "ms"},
	{Key: "fade_in_ms", Profile: InputProfile, Default: 3, Min: 0, Max: 20, Step: 0.5, Unit: "ms"},

	{Key: "opus_music_mode", Profile: InputProfile, Default: 0},
	{Key: "opus_bitrate_kbps", Profile: InputProfile, Default: 40, Min: 16, Max: 128, Step: 1, Unit: "kbps"},
	{Key: "opus_complexity", Profile: InputProfile, Default: 9, Min: 0, Max: 10, Step: 1},
	{Key: "opus_fec", Profile: InputProfile, Default: 1},
	{Key: "opus_loss_perc", Profile: InputProfile, Default: 8, Min: 0, Max: 30, Step: 1, Unit: "%"},

	{Key: "phone_mic_buffer_enabled", Profile: InputProfile, Default: 1},
	{Key: "phone_mic_buffer_ms", Profile: InputProfile, Default: 100, Min: 40, Max: 1000, Step: 20, Unit: "ms"},
	{Key: "phone_drift_slack_ms", Profile: InputProfile, Default: 40, Min: 20, Max: 200, Step: 20, Unit: "ms"},
	{Key: "phone_drift_force_ms", Profile: InputProfile, Default: 80, Min: 20, Max: 200, Step: 20, Unit: "ms"},

	// Salida: recepción y mezcla.
	{Key: "output_filter_enabled", Profile: OutputProfile, Default: 0},
	{Key: "output_gain_db", Profile: OutputProfile, Default: 5, Min: -24, Max: 24, Step: 0.5, Unit: "dB"},
	{Key: "output_high_pass_enabled", Profile: OutputProfile, Default: 0},
	{Key: "output_high_pass_hz", Profile: OutputProfile, Default: 80, Min: 20, Max: 500, Step: 1, Unit: "Hz"},
	{Key: "output_low_pass_enabled", Profile: OutputProfile, Default: 0},
	{Key: "output_low_pass_hz", Profile: OutputProfile, Default: 12000, Min: 1000, Max: 18000, Step: 100, Unit: "Hz"},
	{Key: "output_limiter_enabled", Profile: OutputProfile, Default: 1},
	{Key: "output_limiter_db", Profile: OutputProfile, Default: -1, Min: -12, Max: 0, Step: 0.1, Unit: "dB"},

	{Key: "jitter_buffer_ms", Profile: OutputProfile, Default: 300, Min: 40, Max: 500, Step: 10, Unit: "ms"},
	{Key: "jitter_min_ms", Profile: OutputProfile, Default: 40, Min: 20, Max: 200, Step: 20, Unit: "ms"},
	{Key: "jitter_percentile", Profile: OutputProfile, Default: 95, Min: 80, Max: 99, Step: 1, Unit: "%"},
	{Key: "jitter_margin_ms", Profile: OutputProfile, Default: 20, Min: 0, Max: 100, Step: 20, Unit: "ms"},
	{Key: "jitter_plc_max_ms", Profile: OutputProfile, Default: 100, Min: 0, Max: 200, Step: 20, Unit: "ms"},
	{Key: "jitter_talkspurt_gap_ms", Profile: OutputProfile, Default: 300, Min: 100, Max: 1000, Step: 10, Unit: "ms"},
	{Key: "jitter_drift_slack_ms", Profile: OutputProfile, Default: 40, Min: 20, Max: 200, Step: 20, Unit: "ms"},
	{Key: "jitter_drift_force_ms", Profile: OutputProfile, Default: 80, Min: 20, Max: 200, Step: 20, Unit: "ms"},

	{Key: "bus_limiter_db", Profile: OutputProfile, Default: -1.1, Min: -12, Max: -0.8, Step: 0.1, Unit: "dBFS"},
	{Key: "bus_limiter_release_ms", Profile: OutputProfile, Default: 80, Min: 10, Max: 500, Step: 5, Unit: "ms"},
	{Key: "peer_volume_max", Profile: OutputProfile, Default: 3, Min: 1, Max: 5, Step: 0.1, Unit: "%", Scale: 100},

	// Fuera de los perfiles.
	{Key: "live_monitoring", Default: 0},
	{Key: "show_status_bar", Default: 1},
	{Key: "show_lan_invites", Default: 0},
	{Key: "event_sounds_enabled", Default: 1},
	{Key: "allow_host_migration", Default: 1},
	// Sin nivel 0: un 0 guardado por una versión anterior queda fuera de rango y pasa a 1 al cargar.
	{Key: "log_level", Default: 1, Min: 1, Max: 2, Step: 1},
}

// paramFields es el índice de cada clave json en LiveSettings.
var paramFields = func() map[string]int {
	fields := map[string]int{}
	t := reflect.TypeOf(LiveSettings{})
	for i := 0; i < t.NumField(); i++ {
		key, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		fields[key] = i
	}
	return fields
}()

var paramIndex = func() map[string]int {
	index := make(map[string]int, len(Params))
	for i, param := range Params {
		index[param.Key] = i
	}
	return index
}()

// ParamByKey devuelve el parámetro de esa clave. Una clave que no está en la tabla es un error
// de programación: entra en pánico (lo cubren los tests).
func ParamByKey(key string) Param {
	i, ok := paramIndex[key]
	if !ok {
		panic(fmt.Sprintf("config: parámetro desconocido %q", key))
	}
	return Params[i]
}

func (p Param) field(s *LiveSettings) reflect.Value {
	i, ok := paramFields[p.Key]
	if !ok {
		panic(fmt.Sprintf("config: %q no es un campo de LiveSettings", p.Key))
	}
	return reflect.ValueOf(s).Elem().Field(i)
}

// IsBool indica si el parámetro es un check.
func (p Param) IsBool() bool {
	i, ok := paramFields[p.Key]
	return ok && reflect.TypeOf(LiveSettings{}).Field(i).Type.Kind() == reflect.Bool
}

// Get devuelve el valor guardado (bool: 0/1).
func (p Param) Get(s *LiveSettings) float64 {
	field := p.field(s)
	switch field.Kind() {
	case reflect.Bool:
		if field.Bool() {
			return 1
		}
		return 0
	case reflect.Int:
		return float64(field.Int())
	default:
		return field.Float()
	}
}

// Set guarda value llevado al rango del parámetro (bool: ≥ 0.5 es true; int: redondeado).
func (p Param) Set(s *LiveSettings, value float64) {
	field := p.field(s)
	switch field.Kind() {
	case reflect.Bool:
		field.SetBool(value >= 0.5)
	case reflect.Int:
		field.SetInt(int64(math.Round(p.Clamp(value))))
	default:
		field.SetFloat(p.Clamp(value))
	}
}

// Clamp lleva value al rango del parámetro (sin efecto en booleanos).
func (p Param) Clamp(value float64) float64 {
	if p.Min == 0 && p.Max == 0 {
		return value
	}
	return math.Min(math.Max(value, p.Min), p.Max)
}

// InRange indica si value está dentro del rango (con tolerancia para float32).
func (p Param) InRange(value float64) bool {
	if p.Min == 0 && p.Max == 0 {
		return true
	}
	const eps = 1e-6
	return value >= p.Min-eps && value <= p.Max+eps && !math.IsNaN(value)
}

// DisplayScale es el factor entre el valor guardado y el que se muestra.
func (p Param) DisplayScale() float64 {
	if p.Scale == 0 {
		return 1
	}
	return p.Scale
}

// ResetOutOfRange vuelve a su default los parámetros fuera de rango.
func ResetOutOfRange(s *LiveSettings) {
	for _, param := range Params {
		if !param.IsBool() && !param.InRange(param.Get(s)) {
			param.Set(s, param.Default)
		}
	}
}

// ProfileParams devuelve los parámetros de un tipo de perfil, en el orden de la tabla.
func ProfileParams(kind ProfileKind) []Param {
	var params []Param
	for _, param := range Params {
		if param.Profile == kind {
			params = append(params, param)
		}
	}
	return params
}
