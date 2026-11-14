package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileSaveLoadOnlyItsKind(t *testing.T) {
	profiles := Profiles{Dir: t.TempDir()}
	settings := Default().LiveSettings
	settings.ShoutDryMix = 0.35
	settings.VADEnabled = false
	settings.OpusBitrateKbps = 64
	settings.OutputGainDB = -3
	settings.InputDevice = "Mic USB"
	if err := profiles.Save(InputProfile, "Mic nuevo", settings); err != nil {
		t.Fatal(err)
	}

	loaded, err := profiles.Values(InputProfile, "Mic nuevo", Default().LiveSettings)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ShoutDryMix != 0.35 || loaded.VADEnabled || loaded.OpusBitrateKbps != 64 {
		t.Fatalf("valores de entrada mal cargados: %+v", loaded)
	}
	if loaded.OutputGainDB != Default().OutputGainDB {
		t.Fatal("un perfil de entrada no debe tocar la salida")
	}
	if loaded.InputDevice != Default().InputDevice {
		t.Fatal("los perfiles no guardan dispositivos")
	}
	if !SameProfileValues(InputProfile, loaded, settings) {
		t.Fatal("lo cargado no coincide con lo guardado")
	}

	names, err := profiles.List(InputProfile)
	if err != nil || len(names) != 1 || names[0] != "Mic nuevo" {
		t.Fatalf("List = %v, %v", names, err)
	}
	if names, _ := profiles.List(OutputProfile); len(names) != 0 {
		t.Fatalf("perfiles de salida inesperados: %v", names)
	}
}

func TestProfileMissingKeysUseDefaults(t *testing.T) {
	profiles := Profiles{Dir: t.TempDir()}
	dir := filepath.Join(profiles.Dir, string(InputProfile))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// Perfil "viejo": no conoce el modo grito y trae un valor fuera de rango.
	content := `{"version":1,"name":"Viejo","kind":"entrada","vad_enabled":false,"vad_threshold":7}`
	if err := os.WriteFile(filepath.Join(dir, "viejo.json"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	current := Default().LiveSettings
	current.ShoutDryMix = 0.9
	loaded, err := profiles.Values(InputProfile, "Viejo", current)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.VADEnabled || loaded.VADThreshold != 0.5 || loaded.ShoutDryMix != 0.2 {
		t.Fatalf("faltantes/fuera de rango deberían ir al default: vad %v umbral %v grito %v", loaded.VADEnabled, loaded.VADThreshold, loaded.ShoutDryMix)
	}
}

func TestProfileRenameDeleteAndDefault(t *testing.T) {
	profiles := Profiles{Dir: t.TempDir()}
	settings := Default().LiveSettings
	settings.OutputGainDB = 7
	if err := profiles.Save(OutputProfile, "Auriculares", settings); err != nil {
		t.Fatal(err)
	}
	if err := profiles.Rename(OutputProfile, "Auriculares", "Parlantes"); err != nil {
		t.Fatal(err)
	}
	if profiles.Exists(OutputProfile, "Auriculares") || !profiles.Exists(OutputProfile, "Parlantes") {
		t.Fatal("rename no movió el archivo")
	}
	loaded, _ := profiles.Values(OutputProfile, "Parlantes", Default().LiveSettings)
	if loaded.OutputGainDB != 7 {
		t.Fatal("rename perdió valores")
	}
	if err := profiles.Delete(OutputProfile, "Parlantes"); err != nil {
		t.Fatal(err)
	}
	if err := profiles.Delete(OutputProfile, DefaultProfileName); err == nil {
		t.Fatal("el perfil de fábrica no se puede borrar")
	}
	if err := profiles.Save(OutputProfile, "default", settings); err == nil {
		t.Fatal("no se puede pisar el perfil de fábrica")
	}
	defaults, err := profiles.Values(OutputProfile, DefaultProfileName, settings)
	if err != nil || defaults.OutputGainDB != Default().OutputGainDB {
		t.Fatalf("Default: %v, %v", defaults.OutputGainDB, err)
	}
}

func TestProfileFileIsReadable(t *testing.T) {
	profiles := Profiles{Dir: t.TempDir()}
	settings := Default().LiveSettings
	if err := profiles.Save(InputProfile, "Lectura", settings); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(profiles.Dir, "entrada", "lectura.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`"name":"Lectura"`, `"shout_dry_mix": 0.2,`, `"vad_enabled": true`} {
		if !strings.Contains(text, want) {
			t.Fatalf("falta %s en:\n%s", want, text)
		}
	}
}

func TestApplyBuiltinProfilesOnlyTouchesBuiltins(t *testing.T) {
	settings := Default().LiveSettings
	settings.ShoutThresholdDB = -5 // valor viejo guardado estando en Default
	settings.OutputGainDB = 0
	settings.ActiveOutputProfile = "Auriculares" // la salida tiene perfil propio
	if !ApplyBuiltinProfiles(&settings) {
		t.Fatal("tenía que pisar la entrada")
	}
	if settings.ShoutThresholdDB != Default().ShoutThresholdDB {
		t.Fatalf("la entrada en Default no recibió los valores de fábrica: %v", settings.ShoutThresholdDB)
	}
	if settings.OutputGainDB != 0 {
		t.Fatal("un perfil propio no se toca")
	}
	if ApplyBuiltinProfiles(&settings) {
		t.Fatal("ya estaba en los valores de fábrica: no debería informar cambios")
	}
}

// "Sin filtros" (C19): cada filtro apagado, el check general prendido y todo lo demás igual que
// Default.
func TestNoFiltersProfileValues(t *testing.T) {
	defaults := Default().LiveSettings
	for _, kind := range []ProfileKind{InputProfile, OutputProfile} {
		values := BuiltinValues(kind, NoFiltersProfileName, defaults)
		for _, param := range ProfileParams(kind) {
			got, want := param.Get(&values), param.Default
			switch {
			case param.Key == "audio_filter_enabled" || param.Key == "output_filter_enabled":
				want = 1
			case strings.HasSuffix(param.Key, "_enabled") && !strings.HasPrefix(param.Key, "phone_"):
				want = 0
			case strings.HasSuffix(param.Key, "gain_db"):
				want = 0
			}
			if math.Abs(got-want) > 1e-4 { // float32 guardado vs float64 de la tabla
				t.Errorf("Sin filtros: %s = %v, quería %v", param.Key, got, want)
			}
		}
	}
	// Los switches de Opus y del celular no son filtros: quedan como en Default.
	values := BuiltinValues(InputProfile, NoFiltersProfileName, defaults)
	if values.OpusFEC != defaults.OpusFEC || values.PhoneMicBufferEnabled != defaults.PhoneMicBufferEnabled || values.OpusMusicMode != defaults.OpusMusicMode {
		t.Fatal("Opus o el buffer del celular cambiaron")
	}
}

// La tabla de "Sin filtros" solo nombra parámetros que existen (un typo sería un filtro prendido).
func TestNoFiltersValuesAreParams(t *testing.T) {
	for key := range noFiltersValues {
		if _, ok := paramIndex[key]; !ok {
			t.Errorf("%q no es un parámetro", key)
		}
	}
}

func TestBuiltinProfileNames(t *testing.T) {
	for name, want := range map[string]string{
		"":            DefaultProfileName,
		"default":     DefaultProfileName,
		"Sin filtros": NoFiltersProfileName,
		"sin FILTROS": NoFiltersProfileName,
		"No filters":  NoFiltersProfileName, // traducción del catálogo en inglés
		"Mi perfil":   "Mi perfil",
	} {
		if got := CanonicalProfileName(name); got != want {
			t.Errorf("CanonicalProfileName(%q) = %q, quería %q", name, got, want)
		}
	}
	for _, name := range []string{"Default", "sin filtros", "NO FILTERS", " No filters "} {
		if _, err := ValidateProfileName(name); err != ErrProfileNameReserved {
			t.Errorf("%q: %v, quería ErrProfileNameReserved", name, err)
		}
	}
	profiles := Profiles{Dir: t.TempDir()}
	if err := profiles.Delete(InputProfile, NoFiltersProfileName); err != ErrDefaultProfileFixed {
		t.Fatalf("borrar Sin filtros: %v", err)
	}
	values, err := profiles.Values(InputProfile, "No filters", Default().LiveSettings)
	if err != nil || values.RNNoiseEnabled {
		t.Fatalf("Sin filtros por su nombre en inglés: %v, RNNoise %v", err, values.RNNoiseEnabled)
	}
}

// Un perfil guardado antes con el nombre de uno de fábrica se ignora: no se lista y, si estaba
// activo, queda activo el de fábrica (con sus valores).
func TestSavedProfileWithBuiltinNameIsIgnored(t *testing.T) {
	profiles := Profiles{Dir: t.TempDir()}
	dir := filepath.Join(profiles.Dir, string(InputProfile))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	content := `{"version":1,"name":"No filters","kind":"entrada","rnnoise_enabled":true}`
	if err := os.WriteFile(filepath.Join(dir, "no_filters.json"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if names, err := profiles.List(InputProfile); err != nil || len(names) != 0 {
		t.Fatalf("List = %v, %v", names, err)
	}
	settings := Default().LiveSettings
	settings.ActiveInputProfile = "No filters"
	if !ApplyBuiltinProfiles(&settings) {
		t.Fatal("tenía que aplicar Sin filtros")
	}
	if settings.ActiveInputProfile != NoFiltersProfileName || settings.RNNoiseEnabled || settings.VADEnabled {
		t.Fatalf("activo %q, RNNoise %v, VAD %v", settings.ActiveInputProfile, settings.RNNoiseEnabled, settings.VADEnabled)
	}
}

// Los cambios sobre un perfil de fábrica van a "Personalizado"; sin cambios o con un perfil propio,
// no se crea nada.
func TestProfileForBuiltinChanges(t *testing.T) {
	settings := BuiltinValues(InputProfile, NoFiltersProfileName, Default().LiveSettings)
	settings.ActiveInputProfile = NoFiltersProfileName
	if _, ok := ProfileForBuiltinChanges(InputProfile, settings, nil, "Personalizado"); ok {
		t.Fatal("Sin filtros sin cambios no crea un perfil")
	}
	settings.HighPassEnabled = true // prende un filtro
	if name, ok := ProfileForBuiltinChanges(InputProfile, settings, []string{"Personalizado"}, "Personalizado"); !ok || name != "Personalizado 2" {
		t.Fatalf("cambio sobre Sin filtros: %q, %v", name, ok)
	}
	if _, ok := ProfileForBuiltinChanges(OutputProfile, settings, nil, "Personalizado"); ok {
		t.Fatal("la salida (Default, sin cambios) no crea nada")
	}
	settings.ActiveInputProfile = "Mío"
	if _, ok := ProfileForBuiltinChanges(InputProfile, settings, nil, "Personalizado"); ok {
		t.Fatal("con un perfil propio activo se guarda ese, no se crea otro")
	}
}

func TestNextCustomProfileName(t *testing.T) {
	if got := NextCustomProfileName("Personalizado", nil); got != "Personalizado" {
		t.Fatalf("primero: %q", got)
	}
	if got := NextCustomProfileName("Personalizado", []string{"personalizado", "Personalizado 2", "Mi configuración"}); got != "Personalizado 3" {
		t.Fatalf("siguiente libre: %q", got)
	}
}
