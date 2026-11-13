//go:build cgo

package ui

import (
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"voxmesh/internal/config"
)

// Al abrir la app se pide el nombre si es uno de fábrica (en cualquier idioma, sin importar
// mayúsculas y también con el sufijo que pone el host), salvo "No volver a preguntar".
func TestNeedsUsernamePrompt(t *testing.T) {
	cases := []struct {
		name string
		off  bool
		want bool
	}{
		{"", false, true},
		{"   ", false, true},
		{"Usuario", false, true},
		{"usuario", false, true},
		{"User", false, true},
		{" USER ", false, true},
		{"Usuario (2)", false, true},
		{"User (13)", false, true},
		{"Pepe", false, false},
		{"Pepe (2)", false, false},
		{"Usuario Pepe", false, false},
		{"Usuarios", false, false},
		{"Usuario", true, false},
		{"", true, false},
	}
	for _, c := range cases {
		cfg := config.Default()
		cfg.Username, cfg.UsernamePromptOff = c.name, c.off
		if got := needsUsernamePrompt(&cfg); got != c.want {
			t.Errorf("%q (no preguntar %v): %v, quería %v", c.name, c.off, got, c.want)
		}
	}
}

func newPromptApp(t *testing.T) *App {
	t.Helper()
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	setLanguage(t, "es")
	a.window = test.NewTempWindow(t, a.content())
	a.window.Resize(fyne.NewSize(900, 620))
	return a
}

// Guardar: el nombre recortado queda en la config y arriba; el diálogo se cierra una vez.
func TestUsernamePromptSaves(t *testing.T) {
	a := newPromptApp(t)
	closed := 0
	p := a.showUsernamePrompt(func() { closed++ })
	if !p.save.Disabled() {
		t.Fatal("Guardar habilitado con la entrada vacía")
	}
	if a.window.Canvas().Focused() != p.entry {
		t.Error("la entrada no tiene el foco al abrir")
	}
	test.Type(p.entry, "  Pepe  ")
	if p.save.Disabled() {
		t.Fatal("Guardar sigue deshabilitado con un nombre")
	}
	test.Tap(p.save)
	cfg := a.conf()
	if cfg.Username != "Pepe" || cfg.UsernamePromptOff || a.selfNameLabel.Text != "Pepe" || closed != 1 {
		t.Fatalf("nombre %q, no preguntar %v, arriba %q, cerrado %d", cfg.Username, cfg.UsernamePromptOff, a.selfNameLabel.Text, closed)
	}
}

// "Ahora no" y Esc cierran sin tocar el nombre; con el check marcado no vuelve a preguntar.
func TestUsernamePromptLater(t *testing.T) {
	for _, c := range []struct {
		name     string
		dontAsk  bool
		escape   bool
		askAgain bool
	}{
		{"ahora no", false, false, true},
		{"esc", false, true, true},
		{"ahora no + check", true, false, false},
		{"esc + check", true, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newPromptApp(t)
			before := a.conf().Username
			closed := 0
			p := a.showUsernamePrompt(func() { closed++ })
			test.Type(p.entry, "Pepe")
			p.dontAsk.SetChecked(c.dontAsk)
			if c.escape {
				p.entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
			} else {
				test.Tap(p.later)
			}
			cfg := a.conf()
			if cfg.Username != before || closed != 1 || needsUsernamePrompt(cfg) != c.askAgain {
				t.Fatalf("nombre %q (antes %q), cerrado %d, vuelve a preguntar %v", cfg.Username, before, closed, needsUsernamePrompt(cfg))
			}
		})
	}
}

// Sin diálogo (nombre propio) lo que sigue arranca enseguida.
func TestAskUsernameAtStartSkips(t *testing.T) {
	a := newPromptApp(t)
	a.updateConfig(func(c *config.Config) { c.Username = "Pepe" })
	ran := false
	a.askUsernameAtStart(func() { ran = true })
	if !ran || len(a.window.Canvas().Overlays().List()) != 0 {
		t.Fatalf("siguió %v, overlays %d", ran, len(a.window.Canvas().Overlays().List()))
	}
}
