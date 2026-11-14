//go:build windows && cgo

package ui

import (
	"maps"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"voxmesh/internal/config"
	"voxmesh/internal/i18n"
)

// bindsByAction cuenta los atajos de la lista del poller por acción (y los sonidos por ID).
func bindsByAction(list []boundHotkey) (sounds map[string]bool, stop, toggle int) {
	sounds = map[string]bool{}
	for _, bound := range list {
		switch bound.action {
		case hotkeyStop:
			stop++
		case hotkeyToggle:
			toggle++
		default:
			sounds[bound.soundID] = true
		}
	}
	return sounds, stop, toggle
}

// TestHotkeyBinds (C20): prendidos andan todos; apagados solo el que los prende, y la config
// conserva los demás para cuando vuelvan.
func TestHotkeyBinds(t *testing.T) {
	cfg := config.Default()
	cfg.SoundBinds = map[string]string{"a": "Ctrl + 1", "b": "F2", "roto": "Tecla rara"}
	cfg.StopSoundBind = "F3"
	cfg.ToggleHotkeysBind = "Shift + F4"
	saved := maps.Clone(cfg.SoundBinds)

	sounds, stop, toggle := bindsByAction(hotkeyBinds(&cfg))
	if len(sounds) != 2 || !sounds["a"] || !sounds["b"] || stop != 1 || toggle != 1 {
		t.Fatalf("prendidos: sonidos=%v detener=%d atajos=%d, quería a y b, 1 y 1", sounds, stop, toggle)
	}

	cfg.SoundHotkeysOff = true
	sounds, stop, toggle = bindsByAction(hotkeyBinds(&cfg))
	if len(sounds) != 0 || stop != 0 || toggle != 1 {
		t.Fatalf("apagados: sonidos=%v detener=%d atajos=%d, quería solo el de los atajos", sounds, stop, toggle)
	}
	if !maps.Equal(cfg.SoundBinds, saved) || cfg.StopSoundBind != "F3" {
		t.Fatal("apagar los atajos tocó los que están guardados")
	}

	// Sin atajo para prenderlos: apagados no queda ninguno (el botón sigue andando).
	cfg.ToggleHotkeysBind = ""
	if list := hotkeyBinds(&cfg); len(list) != 0 {
		t.Fatalf("apagados y sin atajo propio quedaron %d atajos", len(list))
	}
}

// TestBindConflict (C20): una tecla que ya usa otro atajo se rechaza; la propia, un sonido de otra
// sala o una tecla libre, no.
func TestBindConflict(t *testing.T) {
	cfg := config.Default()
	cfg.SoundBinds = map[string]string{"a": "Ctrl + 1", "b": "F2", "otra_sala": "F9"}
	cfg.StopSoundBind = "F3"
	cfg.ToggleHotkeysBind = "Shift + F4"
	names := map[string]string{"a": "Bocina", "b": "Risa"}
	soundA := bindTarget{action: hotkeySound, soundID: "a"}
	stop := bindTarget{action: hotkeyStop}
	toggle := bindTarget{action: hotkeyToggle}

	cases := []struct {
		name   string
		combo  string
		self   bindTarget
		taken  bool
		holder string
	}{
		{"la de otro sonido, escrita distinto", "ctrl+1", bindTarget{action: hotkeySound, soundID: "b"}, true, "Bocina"},
		{"la propia", "Ctrl + 1", soundA, false, ""},
		{"la de Detener", "F3", soundA, true, i18n.T("sound.stop")},
		{"la de los atajos", "Shift + F4", stop, true, i18n.T("hotkey.toggle_name")},
		{"la de los atajos para sí mismo", "Shift + F4", toggle, false, ""},
		{"un sonido desde el de los atajos", "F2", toggle, true, "Risa"},
		{"mismos modificadores no alcanzan", "F4", toggle, false, ""},
		{"un sonido que no está en la botonera", "F9", stop, false, ""},
		{"una tecla libre", "F10", soundA, false, ""},
	}
	for _, c := range cases {
		holder, taken := bindConflict(&cfg, names, c.combo, c.self)
		if taken != c.taken || holder != c.holder {
			t.Errorf("%s: %q → (%q, %v), quería (%q, %v)", c.name, c.combo, holder, taken, c.holder, c.taken)
		}
	}
}

// TestToggleSoundHotkeys (C20): prender y apagar lo guarda en la config, publica la lista del
// poller y cambia el texto y el ícono del botón. (El toque del botón además toca el aviso, que
// abriría la salida de audio: acá se prueba el cambio de estado.)
func TestToggleSoundHotkeys(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	// Cambiar el estado guarda voxmesh.json desde configSaver (otra goroutine): en t.TempDir() esa
	// escritura se puede cruzar con el borrado de la carpeta (como en T3). Una carpeta propia, que
	// se borra sin hacer fallar el test.
	dir, err := os.MkdirTemp("", "voxmesh-hotkeys")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	a := New(filepath.Join(dir, "voxmesh.exe"))
	a.updateConfig(func(c *config.Config) {
		c.StopSoundBind = "F3"
		c.ToggleHotkeysBind = "F4"
	})
	test.NewTempWindow(t, a.content())
	a.publishHotkeys()

	check := func(step string, off bool, published int) {
		t.Helper()
		if a.conf().SoundHotkeysOff != off {
			t.Fatalf("%s: sound_hotkeys_off=%v, quería %v", step, a.conf().SoundHotkeysOff, off)
		}
		if list := activeHotkeys.Load(); list == nil || len(*list) != published {
			t.Fatalf("%s: el poller ve %v atajos, quería %d", step, list, published)
		}
		var icon fyne.Resource = keyboardIcon
		text := i18n.T("sound.hotkeys_on")
		if off {
			text, icon = i18n.T("sound.hotkeys_off"), keyboardOffIcon
		}
		if a.hotkeysButton.Text != text || a.hotkeysButton.Icon != icon {
			t.Fatalf("%s: el botón dice %q, quería %q", step, a.hotkeysButton.Text, text)
		}
	}
	check("al abrir", false, 2)
	a.setSoundHotkeysOff(true)
	check("apagados", true, 1)
	a.setSoundHotkeysOff(false)
	check("prendidos de nuevo", false, 2)
}
