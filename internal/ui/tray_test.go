//go:build cgo

package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"voxmesh/internal/config"
)

// TestTrayMenuEntries (C21): el orden (Abrir primero, Salir último), las tildes y "Colgar"
// deshabilitado fuera de una sala.
func TestTrayMenuEntries(t *testing.T) {
	order := []string{"tray.open", "tray.hang_up", "tray.mute", "tray.deafen", "tray.hotkeys", "", "tray.quit"}
	for _, s := range []trayState{
		{},
		{canHangUp: true, muted: true, deafened: true, hotkeysOn: true},
		{canHangUp: true, muted: true},
	} {
		entries := trayMenuEntries(s)
		if len(entries) != len(order) {
			t.Fatalf("%+v: %d renglones, quería %d", s, len(entries), len(order))
		}
		for i, entry := range entries {
			if entry.key != order[i] || (entry.key == "") != (entry.action == traySeparator) {
				t.Fatalf("%+v: renglón %d = %q, quería %q", s, i, entry.key, order[i])
			}
			checked, disabled := false, false
			switch entry.action {
			case trayHangUp:
				disabled = !s.canHangUp
			case trayMute:
				checked = s.muted
			case trayDeafen:
				checked = s.deafened
			case trayHotkeys:
				checked = s.hotkeysOn
			}
			if entry.checked != checked || entry.disabled != disabled {
				t.Errorf("%+v: %q tilde=%v deshabilitado=%v, quería %v y %v", s, entry.key, entry.checked, entry.disabled, checked, disabled)
			}
		}
		if last := entries[len(entries)-1]; last.action != trayQuit {
			t.Fatalf("%+v: el último no es Salir (Fyne agregaría su Quit)", s)
		}
	}

	// Los pedidos para entrar (F10): debajo de Abrir, uno por persona, y un separador.
	entries := trayMenuEntries(trayState{knocks: []string{"Juan", "Ana"}})
	if len(entries) != len(order)+3 || entries[0].action != trayOpen || entries[3].action != traySeparator {
		t.Fatalf("con pedidos: %+v", entries)
	}
	for i, name := range []string{"Juan", "Ana"} {
		if e := entries[i+1]; e.action != trayKnock || e.arg != name || e.index != i || e.key != "lan.knock" {
			t.Errorf("pedido %d: %+v", i, e)
		}
	}
	if last := entries[len(entries)-1]; last.action != trayQuit {
		t.Error("con pedidos Salir sigue último")
	}
}

// TestCloseActionFor (C21): la X oculta solo con la opción prendida y el ícono funcionando; si
// no, lo de siempre.
func TestCloseActionFor(t *testing.T) {
	cases := []struct {
		name                           string
		toTray, hintOff, ready, inRoom bool
		want                           closeAction
	}{
		{"opción + ícono, primera vez", true, false, true, false, closeHideWithHint},
		{"opción + ícono, primera vez en sala", true, false, true, true, closeHideWithHint},
		{"opción + ícono, aviso apagado, en sala", true, true, true, true, closeHide},
		{"opción + ícono, aviso apagado", true, true, true, false, closeHide},
		{"opción sin ícono, en sala", true, false, false, true, closeConfirmQuit},
		{"opción sin ícono", true, true, false, false, closeQuit},
		{"sin opción, en sala", false, false, true, true, closeConfirmQuit},
		{"sin opción", false, true, true, false, closeQuit},
	}
	for _, c := range cases {
		cfg := config.Default()
		cfg.CloseToTray, cfg.TrayHintOff = c.toTray, c.hintOff
		if got := closeActionFor(&cfg, c.ready, c.inRoom); got != c.want {
			t.Errorf("%s: %v, quería %v", c.name, got, c.want)
		}
	}
	if !config.Default().CloseToTray {
		t.Error("close_to_tray tiene que venir prendido de fábrica")
	}
}

// TestTrayMenuOpen (C21): el watchdog reconoce el menú del ícono abierto en el hilo de UI.
func TestTrayMenuOpen(t *testing.T) {
	stack := "goroutine 1 [syscall, locked to thread]:\nsyscall.SyscallN(...)\nfyne.io/systray.(*winTray).showMenu(0x1)\n\tD:/systray_windows.go:744 +0x1\nfyne.io/systray.(*winTray).wndProc(...)\n"
	if !trayMenuOpen(stack) {
		t.Fatal("no reconoció el menú abierto")
	}
	if trayMenuOpen("goroutine 1 [running]:\nvoxmesh/internal/ui.(*App).refreshChat()\n") {
		t.Fatal("tomó otro bloqueo como el menú")
	}
}

// fakeTray guarda el último menú que recibió la bandeja.
type fakeTray struct {
	menus int
	last  []fyne.MenuItem
}

func (f *fakeTray) SetSystemTrayMenu(menu *fyne.Menu) {
	f.menus++
	f.last = f.last[:0]
	for _, item := range menu.Items {
		f.last = append(f.last, *item)
	}
}

func (f *fakeTray) SetSystemTrayIcon(fyne.Resource) {}

// item busca un renglón por su texto en español.
func (f *fakeTray) item(t *testing.T, label string) fyne.MenuItem {
	t.Helper()
	for _, item := range f.last {
		if item.Label == label {
			return item
		}
	}
	t.Fatalf("el menú no tiene %q", label)
	return fyne.MenuItem{}
}

// newTrayApp arma una App con la ventana y una bandeja falsa (la config se guarda: carpeta propia).
func newTrayApp(t *testing.T) (*App, *fakeTray) {
	t.Helper()
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := newAppWithOwnConfig(t)
	setLanguage(t, "es")
	a.window = test.NewTempWindow(t, a.content())
	a.window.Resize(fyne.NewSize(900, 620))
	tray := &fakeTray{}
	a.trayDesk = tray
	a.trayMenu = fyne.NewMenu(appTitle)
	return a, tray
}

// TestTrayMenuFollowsState (C21): el menú se rehace cuando el mute, el ensordecer, los atajos o la
// sala cambian desde la ventana, y cada ítem hace lo mismo que su botón.
func TestTrayMenuFollowsState(t *testing.T) {
	a, tray := newTrayApp(t)

	test.Tap(a.muteButton)
	if !tray.item(t, "Silenciar micrófono").Checked {
		t.Fatal("muteado desde la ventana y el menú no tiene la tilde")
	}
	test.Tap(a.deafenButton)
	if !tray.item(t, "Ensordecer").Checked {
		t.Fatal("ensordecido desde la ventana y el menú no tiene la tilde")
	}
	tray.item(t, "Ensordecer").Action() // desde el menú: como el botón
	if a.isDeafened.Load() || tray.item(t, "Ensordecer").Checked {
		t.Fatal("Ensordecer del menú no volvió a escuchar")
	}

	if !tray.item(t, "Atajos de la botonera").Checked {
		t.Fatal("los atajos arrancan prendidos y el menú no tiene la tilde")
	}
	a.setSoundHotkeysOff(true) // lo mismo que hace su atajo
	if tray.item(t, "Atajos de la botonera").Checked {
		t.Fatal("atajos apagados y el menú sigue con la tilde")
	}

	if !tray.item(t, "Colgar").Disabled {
		t.Fatal("Colgar habilitado fuera de una sala")
	}
	a.setHangupEnabled(true)
	if tray.item(t, "Colgar").Disabled {
		t.Fatal("en una sala Colgar sigue deshabilitado")
	}
	if quit := tray.item(t, "Salir"); !quit.IsQuit || quit.Action == nil {
		t.Fatal("Salir sin IsQuit o sin acción propia: Fyne pondría su Quit, que no cierra la sala")
	}
}

// TestTrayHint (C21, C26): la primera X muestra el aviso; "Ocultar" oculta la ventana (sin foco en
// una entrada, para los atajos de una tecla) y, con el check, la próxima X oculta directo.
// "Abrir VoxMesh" la vuelve a mostrar.
func TestTrayHint(t *testing.T) {
	a, _ := newTrayApp(t)
	a.window.Canvas().Focus(a.chatInput)

	if got := closeActionFor(a.conf(), a.trayReady(), a.connected()); got != closeHideWithHint {
		t.Fatalf("la primera X: %v, quería el aviso", got)
	}
	h := a.showTrayHint()
	if again := a.showTrayHint(); again != nil {
		t.Fatal("la X con el aviso abierto abrió otro")
	}
	h.never.SetChecked(true)
	test.Tap(h.hide)
	if !a.hiddenToTray || !a.conf().TrayHintOff || a.window.Canvas().Focused() != nil {
		t.Fatalf("oculta %v, no mostrar %v, foco %v", a.hiddenToTray, a.conf().TrayHintOff, a.window.Canvas().Focused())
	}
	if a.trayHintOpen {
		t.Fatal("el aviso quedó marcado como abierto")
	}

	a.showMainWindow()
	if a.hiddenToTray {
		t.Fatal("Abrir VoxMesh no la mostró")
	}
	a.closeWindow()
	if !a.hiddenToTray || len(a.window.Canvas().Overlays().List()) != 0 {
		t.Fatal("con \"No volver a mostrar\" la X tiene que ocultar sin aviso")
	}
}
