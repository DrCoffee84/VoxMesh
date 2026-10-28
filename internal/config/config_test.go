package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// LiveSettings va embebido sin tag: el JSON es plano, con las mismas claves que la tabla Params.
func TestLiveSettingsJSONIsFlat(t *testing.T) {
	data, err := json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}
	if _, nested := keys["LiveSettings"]; nested {
		t.Fatal("LiveSettings aparece anidado en el JSON")
	}
	for _, key := range []string{"vad_threshold", "input_device", "log_level", "username", "last_peer"} {
		if _, ok := keys[key]; !ok {
			t.Fatalf("falta la clave %q en el nivel superior del JSON", key)
		}
	}
}

func TestLoadFlatFileFillsLiveSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "voxmesh.json")
	if err := os.WriteFile(path, []byte(`{"username":"Coffee","vad_threshold":0.8,"input_device":"Mic USB","jitter_buffer_ms":200}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Username != "Coffee" || cfg.VADThreshold != 0.8 || cfg.InputDevice != "Mic USB" || cfg.JitterBufferMS != 200 {
		t.Fatalf("campos mal cargados: %+v", cfg)
	}
	if cfg.OutputDevice != Default().OutputDevice {
		t.Fatalf("un campo ausente debería quedar en su valor por defecto, quedó %q", cfg.OutputDevice)
	}
}

func TestLoadWindowState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "voxmesh.json")
	write := func(content string) Config {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	cfg := write(`{"window_width":1280,"window_height":800,"window_maximized":true}`)
	if cfg.WindowWidth != 1280 || cfg.WindowHeight != 800 || !cfg.WindowMaximized {
		t.Fatalf("estado de ventana mal cargado: %v x %v max=%v", cfg.WindowWidth, cfg.WindowHeight, cfg.WindowMaximized)
	}
	for _, content := range []string{`{}`, `{"window_width":50,"window_height":800}`, `{"window_width":1280,"window_height":0}`} {
		cfg = write(content)
		if cfg.WindowWidth != DefaultWindowWidth || cfg.WindowHeight != DefaultWindowHeight {
			t.Fatalf("%s: tamaño inválido debería volver al default, quedó %v x %v", content, cfg.WindowWidth, cfg.WindowHeight)
		}
	}
}

// Restaurar la base de ajustes no debe tocar los campos que cambian en segundo plano.
func TestRestoreLiveSettingsKeepsBackgroundFields(t *testing.T) {
	cfg := Default()
	baseline := cfg.LiveSettings
	cfg.VADThreshold = 0.9
	cfg.LastPeer = "10.0.0.2:47830"
	cfg.PhoneMicToken = "nuevo"
	if cfg.LiveSettings == baseline {
		t.Fatal("un cambio de ajuste en vivo debería verse como pendiente")
	}
	cfg.LiveSettings = baseline
	if cfg.VADThreshold != Default().VADThreshold {
		t.Fatal("no se restauró el ajuste en vivo")
	}
	if cfg.LastPeer != "10.0.0.2:47830" || cfg.PhoneMicToken != "nuevo" {
		t.Fatal("restaurar ajustes pisó campos de segundo plano")
	}
}

func TestCloneIsIndependent(t *testing.T) {
	original := Default()
	original.SoundBinds["a"] = "F1"
	clone := original.Clone()
	clone.SoundBinds["a"] = "F2"
	clone.SoundBinds["b"] = "F3"
	clone.Username = "otro"
	if original.SoundBinds["a"] != "F1" || len(original.SoundBinds) != 1 {
		t.Fatalf("el clon comparte SoundBinds con el original: %v", original.SoundBinds)
	}
	if original.Username == "otro" {
		t.Fatal("el clon comparte campos con el original")
	}
	if (Config{}).Clone().SoundBinds != nil {
		t.Fatal("Clone de un mapa nil no debería crear uno")
	}

	withVolumes := Default()
	withVolumes.PeerVolumes = map[string]float64{"Pepe": 1.5}
	volumesClone := withVolumes.Clone()
	volumesClone.PeerVolumes["Pepe"] = 0.2
	if withVolumes.PeerVolumes["Pepe"] != 1.5 {
		t.Fatalf("el clon comparte PeerVolumes con el original: %v", withVolumes.PeerVolumes)
	}
}
