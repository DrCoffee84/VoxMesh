//go:build cgo

package ui

import (
	"slices"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/config"
)

// waitFor espera (hasta 2 s) a que cond se cumpla: los perfiles se leen y escriben en goroutines.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("no pasó: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// C19: "Sin filtros" sale traducido en el selector, sin Renombrar ni Borrar y con su ayuda; cargarlo
// apaga los filtros; Guardar sobre él pide un nombre (no lo pisa) y el Guardar de Ajustes con
// cambios crea "Custom" / "Personalizado".
func TestProfileBarNoFilters(t *testing.T) {
	test.NewTempApp(t)
	a := newAppWithOwnConfig(t)
	a.profiles.Dir = t.TempDir()
	setLanguage(t, "en")
	a.window = test.NewTempWindow(t, widget.NewLabel(""))
	bar, row := a.newProfileBar(config.InputProfile, &paramControls{app: a}, profileState{loaded: a.conf().LiveSettings})
	a.window.SetContent(row)
	if got := bar.selector.Options; !slices.Equal(got, []string{"Default", "No filters"}) {
		t.Fatalf("opciones %v", got)
	}

	if profileFromLabel("No filters") != config.NoFiltersProfileName || profileFromLabel("Default") != config.DefaultProfileName {
		t.Fatal("el selector no traduce la etiqueta a la clave")
	}
	values, err := a.profiles.Values(config.InputProfile, config.NoFiltersProfileName, a.conf().LiveSettings)
	bar.applyLoaded(config.NoFiltersProfileName, values, err) // lo que hace elegirlo, sin la goroutine
	cfg := a.conf()
	if cfg.ActiveInputProfile != config.NoFiltersProfileName || cfg.RNNoiseEnabled || cfg.VADEnabled || cfg.NoiseGateEnabled || !cfg.AudioFilterEnabled {
		t.Fatalf("Sin filtros cargado: activo %q, RNNoise %v, VAD %v, puerta %v, filtro %v",
			cfg.ActiveInputProfile, cfg.RNNoiseEnabled, cfg.VADEnabled, cfg.NoiseGateEnabled, cfg.AudioFilterEnabled)
	}
	if !bar.rename.Disabled() || !bar.remove.Disabled() || !bar.help.Visible() {
		t.Fatalf("Renombrar deshabilitado %v, Borrar deshabilitado %v, ayuda visible %v", bar.rename.Disabled(), bar.remove.Disabled(), bar.help.Visible())
	}

	bar.save()
	if a.window.Canvas().Overlays().Top() == nil {
		t.Fatal("Guardar sobre Sin filtros tenía que pedir un nombre")
	}
	a.window.Canvas().Overlays().Top().Hide()

	if _, ok := bar.saveBuiltinChangesAsProfile(); ok {
		t.Fatal("sin cambios no se crea nada")
	}
	a.updateConfig(func(c *config.Config) { c.HighPassEnabled = true })
	if name, ok := bar.saveBuiltinChangesAsProfile(); !ok || name != "Custom" {
		t.Fatalf("con un filtro prendido: %q, %v", name, ok)
	}
	waitFor(t, "guardar Custom", func() bool { return a.profiles.Exists(config.InputProfile, "Custom") })
	if active := a.conf().ActiveInputProfile; active != "Custom" {
		t.Fatalf("activo %q, quería Custom", active)
	}
	values, err = a.profiles.Values(config.InputProfile, "Custom", config.Default().LiveSettings)
	if err != nil || !values.HighPassEnabled || values.RNNoiseEnabled {
		t.Fatalf("Custom guardado: %v, pasa-altos %v, RNNoise %v", err, values.HighPassEnabled, values.RNNoiseEnabled)
	}
}

// Cada parámetro de un perfil aparece una sola vez en su acordeón (salvo el check principal, que
// va fuera), y nada de otro tipo se cuela.
func TestParamSectionsCoverProfiles(t *testing.T) {
	cases := []struct {
		kind     config.ProfileKind
		sections []paramSection
		master   string
	}{
		{config.InputProfile, inputSections, "audio_filter_enabled"},
		{config.OutputProfile, outputSections, "output_filter_enabled"},
	}
	for _, c := range cases {
		seen := map[string]bool{c.master: true}
		for _, section := range c.sections {
			for _, key := range section.keys {
				if seen[key] {
					t.Fatalf("%s: %q aparece dos veces", c.kind, key)
				}
				seen[key] = true
				if config.ParamByKey(key).Profile != c.kind {
					t.Fatalf("%s: %q es de otro perfil", c.kind, key)
				}
			}
		}
		for _, param := range config.ProfileParams(c.kind) {
			if !seen[param.Key] {
				t.Fatalf("%s: %q no está en ninguna sección", c.kind, param.Key)
			}
		}
	}
}

func TestFormatParamValue(t *testing.T) {
	cases := map[string]struct {
		display float64
		want    string
	}{
		"shout_dry_mix":        {20, "20 %"},
		"shout_threshold_db":   {-12.5, "-12.5 dBFS"},
		"vad_threshold":        {0.55, "0.55"},
		"opus_bitrate_kbps":    {40, "40 kbps"},
		"limiter_threshold_db": {-1, "-1.0 dB"},
	}
	for key, c := range cases {
		if got := formatParamValue(config.ParamByKey(key), c.display); got != c.want {
			t.Fatalf("%s: %q, want %q", key, got, c.want)
		}
	}
}

func TestMeterPos(t *testing.T) {
	for db, want := range map[float32]float64{-60: 0, -30: 0.5, 0: 1, -18: 0.7} {
		if got := meterPos(db); got < want-1e-6 || got > want+1e-6 {
			t.Fatalf("meterPos(%v) = %v, want %v", db, got, want)
		}
	}
}
