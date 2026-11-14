//go:build cgo

package ui

import (
	"math"
	"testing"

	"fyne.io/fyne/v2/test"

	"voxmesh/internal/audio"
	"voxmesh/internal/config"
)

// C22: el slider del chip propio mueve threshold_db (rango y paso de Params), prende la puerta y,
// con un perfil de fábrica activo, guarda el valor en "Personalizado".
func TestSelfGateSlider(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := newAppWithOwnConfig(t)
	a.profiles.Dir = t.TempDir()
	setLanguage(t, "es")
	test.NewTempWindow(t, a.content())
	a.updateConfig(func(c *config.Config) { c.NoiseGateEnabled = false }) // p. ej. con "Sin filtros"
	a.peerNames = []peerView{{id: "aaaaaaaaaaaaaaaa", name: "Yo", isSelf: true}, {id: "bbbbbbbbbbbbbbbb", name: "Pepe"}}
	a.refreshPeerAvatars()
	g := a.selfGate
	if g == nil || a.peerChips[0].gate != g || a.peerChips[1].gate != nil || a.selfGateMeter.Load() == nil {
		t.Fatalf("slider propio %v, oyente %v: quería el slider solo en el chip propio", g, a.selfGateMeter.Load())
	}
	param := config.ParamByKey("threshold_db")
	if g.slider.Min != param.Min || g.slider.Max != param.Max || g.slider.Step != param.Step {
		t.Fatalf("slider %v..%v paso %v, quería el de Params %v..%v paso %v", g.slider.Min, g.slider.Max, g.slider.Step, param.Min, param.Max, param.Step)
	}
	if g.slider.Value != -42 || g.value.Text != "-42 dB" || g.slider.Disabled() {
		t.Fatalf("al arrancar: %v, %q, deshabilitado %v", g.slider.Value, g.value.Text, g.slider.Disabled())
	}

	g.slider.SetValue(-30.3) // Fyne lo lleva al paso (0.5) y llama a OnChanged y OnChangeEnded
	cfg := a.conf()
	if g.slider.Value != -30.5 || cfg.ThresholdDB != -30.5 || !cfg.NoiseGateEnabled || g.value.Text != "-30.5 dB" {
		t.Fatalf("después de moverlo: slider %v, config %v, puerta %v, texto %q", g.slider.Value, cfg.ThresholdDB, cfg.NoiseGateEnabled, g.value.Text)
	}
	waitFor(t, "crear Personalizado", func() bool { return a.conf().ActiveInputProfile == "Personalizado" })
	a.selfGateSaveMu.Lock() // saveSelfGate terminó (lo tiene tomado hasta el final)
	a.selfGateSaveMu.Unlock()
	values, err := a.profiles.Values(config.InputProfile, "Personalizado", config.Default().LiveSettings)
	if err != nil || values.ThresholdDB != -30.5 || !values.NoiseGateEnabled {
		t.Fatalf("Personalizado: %v, umbral %v, puerta %v", err, values.ThresholdDB, values.NoiseGateEnabled)
	}

	// Ajustes cambió el umbral (o lo descartó): al cerrarlo el slider toma la config sin guardar.
	a.updateConfig(func(c *config.Config) { c.ThresholdDB = -50 })
	g.refresh(a.conf())
	if g.slider.Value != -50 || g.value.Text != "-50 dB" {
		t.Fatalf("sincronizado: %v, %q", g.slider.Value, g.value.Text)
	}
	a.updateConfig(func(c *config.Config) { c.AudioFilterEnabled = false })
	g.refresh(a.conf())
	if !g.slider.Disabled() {
		t.Fatal("sin filtro de voz la puerta no corre: el slider va deshabilitado")
	}

	a.peerNames = nil // salió de la sala
	a.refreshPeerAvatars()
	if a.selfGate != nil || a.selfGateMeter.Load() != nil {
		t.Fatal("sin chip propio quedó el slider o el oyente")
	}
}

// reportMeter le da el mismo frame a los dos medidores (Ajustes y la barrita del slider propio).
func TestReportMeterFansOut(t *testing.T) {
	a := newAppWithOwnConfig(t)
	var settings, gate []inputMeter
	toSettings := func(m inputMeter) { settings = append(settings, m) }
	toGate := func(m inputMeter) { gate = append(gate, m) }
	a.levelListener.Store(&toSettings)
	a.selfGateMeter.Store(&toGate)
	frame := make([]int16, audio.FrameSamples)
	for i := range frame {
		frame[i] = int16(8000 * math.Sin(float64(i)/10))
	}
	a.reportMeter(audio.EncodePCM(frame), true, -20)
	if len(settings) != 1 || len(gate) != 1 || settings[0] != gate[0] || !gate[0].sent || gate[0].postDB != -20 {
		t.Fatalf("Ajustes %+v, slider %+v", settings, gate)
	}
	a.levelListener.Store(nil) // Ajustes cerrado: el slider sigue recibiendo
	a.reportMeter(audio.EncodePCM(frame), false, -40)
	if len(settings) != 1 || len(gate) != 2 || gate[1].sent {
		t.Fatalf("con Ajustes cerrado: Ajustes %d, slider %+v", len(settings), gate)
	}
}

// La barrita se pinta en la escala del slider, verde si se manda, y a lo sumo cada 50 ms.
func TestSelfGateMeterPaint(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := newAppWithOwnConfig(t)
	test.NewTempWindow(t, a.content())
	a.peerNames = []peerView{{id: "aaaaaaaaaaaaaaaa", name: "Yo", isSelf: true}}
	a.refreshPeerAvatars()
	a.onSelfGateMeter(inputMeter{postDB: -42, sent: true})
	bar := a.selfGate.bar
	if math.Abs(bar.value-selfGatePos(-42)) > 1e-9 || bar.fill != levelFillColor {
		t.Fatalf("barra %v en %v, quería %v en verde", bar.fill, bar.value, selfGatePos(-42))
	}
	a.onSelfGateMeter(inputMeter{postDB: -10, sent: false}) // enseguida: no se repinta
	if math.Abs(bar.value-selfGatePos(-42)) > 1e-9 {
		t.Fatal("se repintó antes de 50 ms")
	}
	if selfGatePos(float32(config.ParamByKey("threshold_db").Min)) != 0 || selfGatePos(float32(config.ParamByKey("threshold_db").Max)) != 1 {
		t.Fatal("la barra no cubre el recorrido del slider")
	}
}
