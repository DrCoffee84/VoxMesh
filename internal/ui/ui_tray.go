//go:build cgo

package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"fyne.io/systray"

	"voxmesh/internal/config"
	"voxmesh/internal/i18n"
	"voxmesh/internal/logging"
)

// systrayMonitorTitle es el título de la ventana oculta que Fyne 2.6.3 crea con el ícono
// (runSystray): su cierre llama a Quit (taskkill sin /F, instaladores, WM_QUIT).
const systrayMonitorTitle = "SystrayMonitor"

// setupTray crea el ícono del área de notificaciones (AGENDA C21), siempre que la app está
// abierta. Va en la goroutine principal y antes de application.Run: Fyne crea ahí su ventana
// oculta sin pasar al hilo de UI y solo arranca la bandeja si ya existía al empezar el loop. Sin
// bandeja (el driver de test) no hace nada y la X sale como antes.
func (a *App) setupTray(application fyne.App) {
	desk, ok := application.(desktop.App)
	if !ok {
		return
	}
	// Siempre el mismo *fyne.Menu: cuando la bandeja termina de arrancar, Fyne vuelve a cargar el
	// menú que recibió la primera vez; con el mismo objeto, carga los ítems de ese momento.
	a.trayMenu = fyne.NewMenu(appTitle)
	a.trayMenu.Items = a.trayMenuItems()
	desk.SetSystemTrayMenu(a.trayMenu)
	a.trayDesk = desk
	a.hookTrayLeftClick()        // clic izquierdo = abrir (C27); el derecho sigue abriendo el menú
	systray.SetTooltip(appTitle) // Fyne no le pone leyenda en Windows
	// El cierre de esa ventana oculta llamaba al Quit de Fyne, que no manda el Leave ni borra la
	// regla UPnP: ahora es "Salir".
	for _, w := range application.Driver().AllWindows() {
		if w.Title() == systrayMonitorTitle {
			w.SetCloseIntercept(a.quitApp)
		}
	}
	logging.Infof("Ícono en el área de notificaciones listo")
}

// trayReady dice si el ícono existe: sin él, la X nunca oculta la ventana.
func (a *App) trayReady() bool {
	return a.trayDesk != nil
}

// trayMenuItems arma los ítems con el estado de ahora (trayMenuEntries). Hilo de UI.
func (a *App) trayMenuItems() []*fyne.MenuItem {
	state := trayState{
		canHangUp: a.topHangupBtn != nil && !a.topHangupBtn.Disabled(),
		muted:     a.isMuted.Load(),
		deafened:  a.isDeafened.Load(),
		hotkeysOn: !a.conf().SoundHotkeysOff,
	}
	entries := trayMenuEntries(state)
	items := make([]*fyne.MenuItem, 0, len(entries))
	for _, entry := range entries {
		if entry.action == traySeparator {
			items = append(items, fyne.NewMenuItemSeparator())
			continue
		}
		item := fyne.NewMenuItem(i18n.T(entry.key), a.trayActionFunc(entry.action))
		item.Checked, item.Disabled = entry.checked, entry.disabled
		item.IsQuit = entry.action == trayQuit
		items = append(items, item)
	}
	return items
}

// trayActionFunc es lo que hace cada ítem: lo mismo que su botón de la ventana. Fyne los llama en
// el hilo de UI.
func (a *App) trayActionFunc(action trayAction) func() {
	switch action {
	case trayOpen:
		return a.showMainWindow
	case trayHangUp:
		return a.stopConnection
	case trayMute:
		return a.toggleMute
	case trayDeafen:
		return a.toggleDeafen
	case trayHotkeys:
		return a.toggleSoundHotkeys
	case trayQuit:
		return a.quitApp
	}
	return nil
}

// refreshTrayMenu rehace el menú del ícono cuando cambia algo que muestra (la sala, el mute, el
// ensordecer, los atajos), desde la ventana, un atajo o el mismo menú. Hilo de UI.
func (a *App) refreshTrayMenu() {
	if a.trayDesk == nil || a.quitting.Load() {
		return
	}
	a.trayMenu.Items = a.trayMenuItems()
	a.trayDesk.SetSystemTrayMenu(a.trayMenu)
}

// closeWindow es la X de la ventana principal (closeActionFor). Hilo de UI.
func (a *App) closeWindow() {
	if a.quitting.Load() {
		return
	}
	switch closeActionFor(a.conf(), a.trayReady(), a.connected()) {
	case closeHide:
		a.hideToTray()
	case closeHideWithHint:
		a.showTrayHint()
	case closeConfirmQuit:
		dialog.ShowConfirm(i18n.T("main.quit.title"), i18n.T("main.quit.message"), func(confirmed bool) {
			if confirmed {
				a.quit(a.stopConnectionAsync(), nil)
			}
		}, a.window)
	default:
		a.quit(nil, nil)
	}
}

// hideToTray oculta la ventana principal: VoxMesh sigue junto al reloj, y en la sala si estaba.
// Hilo de UI.
func (a *App) hideToTray() {
	if a.hiddenToTray || a.quitting.Load() {
		return
	}
	a.rememberWindowState() // a la vista: oculta, el estado de la ventana ya no es el que vio el usuario
	a.saveConfig()
	// Sin foco en una entrada: si no, isTypingInApp seguiría salteando los atajos de una tecla.
	a.window.Canvas().Unfocus()
	a.window.Hide()
	a.hiddenToTray = true
	logging.Infof("Ventana oculta: VoxMesh sigue en el área de notificaciones")
}

// showMainWindow muestra la ventana principal y la pasa adelante: "Abrir VoxMesh" del menú y
// otra instancia que se abrió (main → OnShowRequest). Hilo de UI.
func (a *App) showMainWindow() {
	if a.window == nil || a.quitting.Load() {
		return
	}
	a.hiddenToTray = false
	a.window.Show()
	restoreWindow(a.window) // Show no la saca de minimizada
	a.window.RequestFocus()
}

// quitApp sale del todo sin preguntar: "Salir" del menú del ícono, el aviso de la primera vez y
// el cierre de la ventana oculta de Fyne. En una sala la cierra como la X (Leave, regla UPnP).
// Hilo de UI.
func (a *App) quitApp() {
	var cleanup <-chan struct{}
	if a.connected() {
		cleanup = a.stopConnectionAsync()
	}
	a.quit(cleanup, nil)
}

// trayHint son los controles del aviso de la primera vez (para los tests).
type trayHint struct {
	never  *widget.Check
	quit   *widget.Button
	hide   *widget.Button // "Ocultar" (C26; antes "Entendido")
	dialog dialog.Dialog
}

// showTrayHint avisa, cuando la X oculta la app y hasta que se marque "No volver a mostrar", que
// VoxMesh sigue abierto y cómo salir: para un usuario común, si no, parece que se cerró.
// "Entendido" la oculta; "Salir de VoxMesh" sale del todo. Hilo de UI.
func (a *App) showTrayHint() *trayHint {
	if a.trayHintOpen {
		return nil // la X otra vez con el aviso abierto
	}
	a.trayHintOpen = true
	message := widget.NewLabel(i18n.T("tray.hint_message"))
	message.Wrapping = fyne.TextWrapWord
	h := &trayHint{never: widget.NewCheck(i18n.T("tray.hint_never"), nil)}
	finish := func(then func()) {
		if h.never.Checked {
			a.updateConfig(func(c *config.Config) { c.TrayHintOff = true })
			a.saveConfig()
		}
		h.dialog.Hide()
		then()
	}
	h.quit = widget.NewButton(i18n.T("tray.hint_quit"), func() { finish(a.quitApp) })
	h.hide = widget.NewButton(i18n.T("tray.hint_hide"), func() { finish(a.hideToTray) })
	h.hide.Importance = widget.HighImportance
	content := container.NewVBox(message, h.never, container.NewGridWithColumns(2, h.quit, h.hide))
	h.dialog = dialog.NewCustomWithoutButtons(i18n.T("tray.hint_title"), content, a.window)
	h.dialog.SetOnClosed(func() { a.trayHintOpen = false })
	h.dialog.Resize(fyne.NewSize(440, 0))
	h.dialog.Show()
	return h
}
