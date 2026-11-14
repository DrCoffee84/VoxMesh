//go:build cgo

package ui

import (
	"math"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/i18n"
)

// findSoundVolume busca en el panel de la botonera el slider de volumen y el check de bloquear.
func findSoundVolume(root fyne.CanvasObject) (slider *widget.Slider, block *widget.Check) {
	var walk func(object fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		switch o := object.(type) {
		case *widget.Slider:
			slider = o
		case *widget.Check:
			if o.Text == i18n.T("sound.block_others") {
				block = o
			}
		case *fyne.Container:
			for _, child := range o.Objects {
				walk(child)
			}
		}
	}
	walk(root)
	return slider, block
}

// La botonera (C23): curva en dB; en 0 es silencio y prende "Bloquear sonidos ajenos", que se
// apaga al volver a subirlo; si lo tocó el usuario, queda como lo dejó.
func TestSoundboardVolumeSlider(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := newAppWithOwnConfig(t)
	test.NewTempWindow(t, a.content())
	slider, block := findSoundVolume(a.soundPanel)
	if slider == nil || block == nil {
		t.Fatal("no encontré el slider o el check de la botonera")
	}
	curve := newVolumeCurve(1)
	if curve.gain(slider.Value) != 1 || block.Checked {
		t.Fatalf("al arrancar: posición %v (quería el clic del 100 %%), bloqueado %v", slider.Value, block.Checked)
	}
	slider.SetValue(0.5)
	if got := a.conf().SoundboardVolume; math.Abs(float64(got)-curve.gain(0.5)) > 1e-6 || got >= 0.5 {
		t.Fatalf("a mitad de recorrido: %v (con la curva tiene que ser bastante menos que 50 %%)", got)
	}
	slider.SetValue(0)
	if cfg := a.conf(); cfg.SoundboardVolume != 0 || !cfg.SoundboardMuted || !block.Checked {
		t.Fatalf("en 0: volumen %v, bloqueados %v (check %v)", cfg.SoundboardVolume, cfg.SoundboardMuted, block.Checked)
	}
	slider.SetValue(0.3)
	if a.conf().SoundboardMuted || block.Checked {
		t.Fatal("al subirlo tenía que volver a dejar pasar los sonidos ajenos")
	}
	block.SetChecked(true) // lo bloquea el usuario
	slider.SetValue(0)
	slider.SetValue(0.6)
	if !a.conf().SoundboardMuted {
		t.Fatal("el bloqueo que puso el usuario no se toca")
	}
}

func TestVolumeCurve(t *testing.T) {
	for _, maxGain := range []float64{1, 3, 5} {
		c := newVolumeCurve(maxGain)
		// Extremos: 0 es silencio y el final es el máximo.
		if c.gain(0) != 0 || c.pos(0) != 0 || c.gain(1) != maxGain || (maxGain > 1 && c.pos(maxGain) != 1) {
			t.Fatalf("máx %v: extremos gain(0)=%v pos(0)=%v gain(1)=%v pos(máx)=%v", maxGain, c.gain(0), c.pos(0), c.gain(1), c.pos(maxGain))
		}
		// Apenas pasado el 0 suena (≈ −40 dB), no salta al 100 %.
		if g := c.gain(volumeSliderStep); g <= 0 || g > 0.02 {
			t.Fatalf("máx %v: primer paso %v", maxGain, g)
		}
		// El 100 % es un tramo ("clic") y su centro vuelve a 1.
		width := 2 * volumeDetent // en el medio; con máximo 100 % el clic es el final (medio tramo)
		if maxGain == 1 {
			width = volumeDetent
		}
		if c.gain(c.pos(1)) != 1 || c.gain(c.low) != 1 || c.gain(c.high) != 1 || c.high-c.low < width-1e-9 {
			t.Fatalf("máx %v: clic %v–%v, centro %v", maxGain, c.low, c.high, c.pos(1))
		}
		// Ida y vuelta: ganancias guardadas con el slider viejo (pasos de 0.05) vuelven iguales.
		for g := 0.05; g <= maxGain+1e-9; g += 0.05 {
			if got := c.gain(c.pos(g)); math.Abs(got-g) > 1e-9 {
				t.Fatalf("máx %v: ganancia %v → posición %v → %v", maxGain, g, c.pos(g), got)
			}
		}
		// Sube siempre (nunca baja al mover a la derecha).
		last := -1.0
		for p := 0.0; p <= 1+1e-9; p += 0.001 {
			if g := c.gain(p); g < last {
				t.Fatalf("máx %v: baja en %v (%v < %v)", maxGain, p, g, last)
			}
			last = c.gain(p)
		}
	}
	// Perceptual: los mismos dB por tramo a los dos lados del clic (con 300 %).
	c := newVolumeCurve(3)
	below := 20 * math.Log10(c.gain(c.low-0.1)/c.gain(c.low-0.2))
	above := 20 * math.Log10(c.gain(c.high+0.1)/c.gain(c.high))
	if math.Abs(below-above) > 0.01 {
		t.Fatalf("dB por 10 %% de recorrido: %.2f abajo, %.2f arriba", below, above)
	}
	// El 50 % (−6 dB) ya no está pegado al 100 %: queda a más de 10 % de recorrido.
	if c.pos(1)-c.pos(0.5) < 0.1 {
		t.Fatalf("50 %% en %v y 100 %% en %v", c.pos(0.5), c.pos(1))
	}
}
