package config

import (
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

func TestMigrateToProfiles(t *testing.T) {
	profiles := Profiles{Dir: filepath.Join(t.TempDir(), "perfiles")}
	settings := Default().LiveSettings
	settings.OutputGainDB = 2
	changed, err := profiles.MigrateToProfiles(&settings, true)
	if err != nil || !changed {
		t.Fatalf("primera migración: %v, %v", changed, err)
	}
	if settings.ActiveInputProfile != MigratedProfileName || settings.ActiveOutputProfile != MigratedProfileName {
		t.Fatal("la configuración migrada tiene que quedar activa")
	}
	loaded, _ := profiles.Values(OutputProfile, MigratedProfileName, Default().LiveSettings)
	if loaded.OutputGainDB != 2 {
		t.Fatal("la migración no guardó la configuración actual")
	}
	if changed, _ := profiles.MigrateToProfiles(&settings, true); changed {
		t.Fatal("la migración corre una sola vez")
	}

	fresh := Profiles{Dir: filepath.Join(t.TempDir(), "perfiles")}
	clean := Default().LiveSettings
	if changed, _ := fresh.MigrateToProfiles(&clean, false); changed || clean.ActiveInputProfile != "" {
		t.Fatal("instalación nueva: sin voxmesh.json no hay nada que migrar")
	}
}

func TestApplyDefaultProfilesOnlyTouchesDefault(t *testing.T) {
	settings := Default().LiveSettings
	settings.ShoutThresholdDB = -5 // valor viejo guardado estando en Default
	settings.OutputGainDB = 0
	settings.ActiveOutputProfile = "Auriculares" // la salida tiene perfil propio
	if !ApplyDefaultProfiles(&settings) {
		t.Fatal("tenía que pisar la entrada")
	}
	if settings.ShoutThresholdDB != Default().ShoutThresholdDB {
		t.Fatalf("la entrada en Default no recibió los valores de fábrica: %v", settings.ShoutThresholdDB)
	}
	if settings.OutputGainDB != 0 {
		t.Fatal("un perfil propio no se toca")
	}
	if ApplyDefaultProfiles(&settings) {
		t.Fatal("ya estaba en los valores de fábrica: no debería informar cambios")
	}
}

func TestNextCustomProfileName(t *testing.T) {
	if got := NextCustomProfileName(nil); got != "Personalizado" {
		t.Fatalf("primero: %q", got)
	}
	if got := NextCustomProfileName([]string{"personalizado", "Personalizado 2", "Mi configuración"}); got != "Personalizado 3" {
		t.Fatalf("siguiente libre: %q", got)
	}
}
