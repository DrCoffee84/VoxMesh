//go:build cgo

package ui

import (
	"testing"

	"voxmesh/internal/config"
)

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
