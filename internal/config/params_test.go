package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParamsMatchLiveSettings(t *testing.T) {
	seen := map[string]bool{}
	for _, param := range Params {
		if seen[param.Key] {
			t.Fatalf("clave repetida en la tabla: %q", param.Key)
		}
		seen[param.Key] = true
		if _, ok := paramFields[param.Key]; !ok {
			t.Fatalf("%q no es un campo de LiveSettings", param.Key)
		}
		if param.Label == "" {
			t.Fatalf("%q no tiene texto", param.Key)
		}
		if param.IsBool() {
			if param.Default != 0 && param.Default != 1 {
				t.Fatalf("%q: default de un check tiene que ser 0 o 1", param.Key)
			}
			continue
		}
		if param.Min >= param.Max || param.Step <= 0 {
			t.Fatalf("%q: rango/paso inválido (%v–%v paso %v)", param.Key, param.Min, param.Max, param.Step)
		}
		if !param.InRange(param.Default) {
			t.Fatalf("%q: default %v fuera de rango", param.Key, param.Default)
		}
	}
	// Todo campo numérico o booleano de LiveSettings tiene que estar en la tabla.
	typ := reflect.TypeOf(LiveSettings{})
	for i := 0; i < typ.NumField(); i++ {
		switch typ.Field(i).Type.Kind() {
		case reflect.Bool, reflect.Int, reflect.Float32:
			key := typ.Field(i).Tag.Get("json")
			if !seen[key] {
				t.Fatalf("el campo %s (%q) no está en Params", typ.Field(i).Name, key)
			}
		}
	}
}

func TestDefaultComesFromParams(t *testing.T) {
	cfg := Default()
	for _, param := range Params {
		if got := param.Get(&cfg.LiveSettings); !closeEnough(got, param.Default) {
			t.Fatalf("%q: Default() tiene %v, la tabla %v", param.Key, got, param.Default)
		}
	}
	// Defaults elegidos: solo filtro de voz + RNNoise + VAD en la entrada, salida apagada.
	if !cfg.AudioFilterEnabled || !cfg.RNNoiseEnabled || !cfg.VADEnabled || cfg.NoiseGateEnabled || cfg.LimiterEnabled {
		t.Fatalf("defaults de entrada inesperados: %+v", cfg.LiveSettings)
	}
	if cfg.OutputFilterEnabled || cfg.OutputGainDB != 5 || cfg.JitterBufferMS != 300 || cfg.ShoutDryMix != 0.2 {
		t.Fatalf("defaults de salida inesperados: %+v", cfg.LiveSettings)
	}
}

func closeEnough(a, b float64) bool {
	d := a - b
	return d < 1e-6 && d > -1e-6
}

func TestParamSetClampsAndRounds(t *testing.T) {
	var s LiveSettings
	ParamByKey("jitter_buffer_ms").Set(&s, 10_000)
	if s.JitterBufferMS != 500 {
		t.Fatalf("clamp: %d", s.JitterBufferMS)
	}
	ParamByKey("opus_complexity").Set(&s, 7.6)
	if s.OpusComplexity != 8 {
		t.Fatalf("redondeo: %d", s.OpusComplexity)
	}
	ParamByKey("opus_fec").Set(&s, 1)
	if !s.OpusFEC {
		t.Fatal("bool")
	}
}

func TestLoadResetsOutOfRangeToDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "voxmesh.json")
	if err := os.WriteFile(path, []byte(`{"jitter_buffer_ms":5,"log_level":9,"rnnoise_mix":3,"vad_threshold":0.8}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.JitterBufferMS != 300 || cfg.LogLevel != 0 || cfg.RNNoiseMix != 1 || cfg.VADThreshold != 0.8 {
		t.Fatalf("fuera de rango no volvió al default: jitter %d log %d mix %v vad %v", cfg.JitterBufferMS, cfg.LogLevel, cfg.RNNoiseMix, cfg.VADThreshold)
	}
}
