//go:build cgo

package ui

import (
	"strings"

	"voxmesh/internal/config"
)

// trayState es lo que muestra el menú del ícono del área de notificaciones (AGENDA C21).
type trayState struct {
	canHangUp bool // el botón de colgar de arriba está habilitado (en una sala o entrando a una)
	muted     bool
	deafened  bool
	hotkeysOn bool     // atajos de la botonera prendidos (C20)
	knocks    []string // los que piden entrar a la sala, pendientes (F10): un submenú Aceptar / Rechazar por cada uno
}

// trayAction es lo que hace un renglón del menú.
type trayAction int

const (
	trayOpen trayAction = iota
	trayHangUp
	trayMute
	trayDeafen
	trayHotkeys
	traySeparator
	trayQuit
	trayKnock // "Juan quiere entrar" ▸ Aceptar / Rechazar (index = cuál de trayState.knocks)
)

// trayEntry es un renglón del menú: la clave de su texto (con arg, si lleva), tilde y deshabilitado.
type trayEntry struct {
	action   trayAction
	key      string
	arg      string
	index    int
	checked  bool
	disabled bool
}

// trayMenuEntries arma el menú del ícono. "Abrir la app de Coffee" va primero: en Windows el clic
// izquierdo abre este mismo menú, no la ventana (Fyne 2.6.3). "Salir" va último: si no, Fyne
// agrega su propio "Quit", que cierra sin mandar el Leave ni borrar la regla UPnP.
//
// Los pedidos para entrar que esperan (F10) van debajo de "Abrir la app de Coffee", cada uno con su
// submenú: así se contestan sin abrir la ventana ni sacar el juego.
func trayMenuEntries(s trayState) []trayEntry {
	entries := []trayEntry{{action: trayOpen, key: "tray.open"}}
	for i, name := range s.knocks {
		entries = append(entries, trayEntry{action: trayKnock, key: "lan.knock", arg: name, index: i})
	}
	if len(s.knocks) > 0 {
		entries = append(entries, trayEntry{action: traySeparator})
	}
	return append(entries, []trayEntry{
		{action: trayHangUp, key: "tray.hang_up", disabled: !s.canHangUp},
		{action: trayMute, key: "tray.mute", checked: s.muted},
		{action: trayDeafen, key: "tray.deafen", checked: s.deafened},
		{action: trayHotkeys, key: "tray.hotkeys", checked: s.hotkeysOn},
		{action: traySeparator},
		{action: trayQuit, key: "tray.quit"},
	}...)
}

// closeAction es lo que hace la X de la ventana principal.
type closeAction int

const (
	closeQuit         closeAction = iota // sale (fuera de una sala)
	closeConfirmQuit                     // pregunta y sale cerrando la sala
	closeHide                            // oculta: sigue junto al reloj, y en la sala si estaba
	closeHideWithHint                    // el aviso de que sigue abierta, y después oculta
)

// closeActionFor decide la X: con close_to_tray y el ícono funcionando oculta la ventana (con el
// aviso hasta que se marque "No volver a mostrar"); si no, lo de siempre. Sin ícono (el driver de
// test, o si Fyne no lo pudo crear) nunca oculta: no habría cómo volver.
func closeActionFor(cfg *config.Config, trayReady, connected bool) closeAction {
	switch {
	case cfg.CloseToTray && trayReady && cfg.TrayHintOff:
		return closeHide
	case cfg.CloseToTray && trayReady:
		return closeHideWithHint
	case connected:
		return closeConfirmQuit
	}
	return closeQuit
}

// trayMenuOpen dice si el stack del hilo de UI está dentro del menú del ícono. Ese menú
// (TrackPopupMenu de systray) es modal y corre en el hilo de UI: mientras está abierto la ventana
// no se repinta, y no es un bloqueo para el watchdog.
func trayMenuOpen(mainStack string) bool {
	return strings.Contains(mainStack, "systray.(*winTray).showMenu")
}
