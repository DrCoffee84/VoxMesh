//go:build windows && cgo

package ui

import (
	"maps"
	"math"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/config"
	"voxmesh/internal/i18n"
)

// bindsByAction cuenta los atajos de la lista del poller por acción (y los sonidos por ID).
func bindsByAction(list []boundHotkey) (sounds map[string]bool, stop, stopAll, toggle int) {
	sounds = map[string]bool{}
	for _, bound := range list {
		switch bound.action {
		case hotkeyStop:
			stop++
		case hotkeyStopAll:
			stopAll++
		case hotkeyToggle:
			toggle++
		default:
			sounds[bound.soundID] = true
		}
	}
	return sounds, stop, stopAll, toggle
}

// TestHotkeyBinds (C20, C37): prendidos andan todos, también "Detener para todos"; apagados solo el
// que los prende, y la config conserva los demás para cuando vuelvan.
func TestHotkeyBinds(t *testing.T) {
	cfg := config.Default()
	cfg.SoundBinds = map[string]string{"a": "Ctrl + 1", "b": "F2", "roto": "Tecla rara"}
	cfg.StopSoundBind = "F3"
	cfg.StopAllSoundBind = "Ctrl + F3"
	cfg.ToggleHotkeysBind = "Shift + F4"
	saved := maps.Clone(cfg.SoundBinds)

	sounds, stop, stopAll, toggle := bindsByAction(hotkeyBinds(&cfg))
	if len(sounds) != 2 || !sounds["a"] || !sounds["b"] || stop != 1 || stopAll != 1 || toggle != 1 {
		t.Fatalf("prendidos: sonidos=%v detener=%d para todos=%d atajos=%d, quería a y b, 1, 1 y 1", sounds, stop, stopAll, toggle)
	}

	cfg.SoundHotkeysOff = true
	sounds, stop, stopAll, toggle = bindsByAction(hotkeyBinds(&cfg))
	if len(sounds) != 0 || stop != 0 || stopAll != 0 || toggle != 1 {
		t.Fatalf("apagados: sonidos=%v detener=%d para todos=%d atajos=%d, quería solo el de los atajos", sounds, stop, stopAll, toggle)
	}
	if !maps.Equal(cfg.SoundBinds, saved) || cfg.StopSoundBind != "F3" || cfg.StopAllSoundBind != "Ctrl + F3" {
		t.Fatal("apagar los atajos tocó los que están guardados")
	}

	// Sin atajo para prenderlos: apagados no queda ninguno (el botón sigue andando).
	cfg.ToggleHotkeysBind = ""
	if list := hotkeyBinds(&cfg); len(list) != 0 {
		t.Fatalf("apagados y sin atajo propio quedaron %d atajos", len(list))
	}
}

// TestBindConflict (C20, C37): una tecla que ya usa otro atajo se rechaza; la propia, un sonido de
// otra sala o una tecla libre, no.
func TestBindConflict(t *testing.T) {
	setLanguage(t, "es")
	cfg := config.Default()
	cfg.SoundBinds = map[string]string{"a": "Ctrl + 1", "b": "F2", "otra_sala": "F9"}
	cfg.StopSoundBind = "F3"
	cfg.StopAllSoundBind = "Ctrl + F3"
	cfg.ToggleHotkeysBind = "Shift + F4"
	names := map[string]string{"a": "Bocina", "b": "Risa"}
	soundA := bindTarget{action: hotkeySound, soundID: "a"}
	stop := bindTarget{action: hotkeyStop}
	stopAll := bindTarget{action: hotkeyStopAll}
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
		{"la de Para todos desde un sonido", "ctrl+f3", soundA, true, "Detener para todos"},
		{"la de Para todos desde Detener", "Ctrl + F3", stop, true, "Detener para todos"},
		{"la de Para todos desde los atajos", "Ctrl + F3", toggle, true, "Detener para todos"},
		{"la de Para todos para sí mismo", "Ctrl + F3", stopAll, false, ""},
		{"la de Detener desde Para todos", "F3", stopAll, true, i18n.T("sound.stop")},
		{"la de los atajos desde Para todos", "Shift + F4", stopAll, true, i18n.T("hotkey.toggle_name")},
		{"un sonido desde Para todos", "F2", stopAll, true, "Risa"},
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
	a := newAppWithOwnConfig(t) // cambiar el estado guarda voxmesh.json
	a.updateConfig(func(c *config.Config) {
		c.StopSoundBind = "F3"
		c.StopAllSoundBind = "F5"
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
	check("al abrir", false, 3)
	a.setSoundHotkeysOff(true)
	check("apagados", true, 1)
	a.setSoundHotkeysOff(false)
	check("prendidos de nuevo", false, 3)
}

// TestStopAllSoundRow (C37): "Para todos" va justo debajo de "Detener", del mismo ancho, con su
// botón de atajo al lado mostrando el guardado y la leyenda con el nombre entero.
func TestStopAllSoundRow(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := newAppWithOwnConfig(t)
	setLanguage(t, "es")
	a.updateConfig(func(c *config.Config) { c.StopAllSoundBind = "Ctrl + F3" })
	window := test.NewTempWindow(t, a.content())
	window.Resize(fyne.NewSize(960, 700))

	var stopAll *hintButton
	var stop *widget.Button
	walkObjects(a.soundPanel, func(o fyne.CanvasObject) {
		switch b := o.(type) {
		case *hintButton:
			if b.Text == "Para todos" {
				stopAll = b
			}
		case *widget.Button:
			if b.Text == "Detener" {
				stop = b
			}
		}
	})
	if stopAll == nil || stop == nil {
		t.Fatalf("botonera: Para todos %v, Detener %v", stopAll != nil, stop != nil)
	}
	if stopAll.hint != "Detener para todos: corta el sonido en toda la sala" || stopAll.Importance != widget.MediumImportance {
		t.Fatalf("Para todos: leyenda %q, importancia %v", stopAll.hint, stopAll.Importance)
	}
	if a.stopAllBindBtn.Text != "Ctrl + F3" {
		t.Fatalf("el atajo dice %q, quería el guardado", a.stopAllBindBtn.Text)
	}
	driver := fyne.CurrentApp().Driver()
	stopAt, allAt, bindAt := driver.AbsolutePositionForObject(stop), driver.AbsolutePositionForObject(stopAll), driver.AbsolutePositionForObject(a.stopAllBindBtn)
	if allAt.Y <= stopAt.Y || allAt.X != stopAt.X || stopAll.Size().Width != stop.Size().Width {
		t.Fatalf("Para todos en %v (%v de ancho), Detener en %v (%v): quería debajo y del mismo ancho", allAt, stopAll.Size().Width, stopAt, stop.Size().Width)
	}
	if math.Abs(float64(bindAt.Y-allAt.Y)) > 4 || bindAt.X < allAt.X+stopAll.Size().Width {
		t.Fatalf("el atajo en %v, Para todos en %v: quería al lado, en el mismo renglón", bindAt, allAt)
	}
}
