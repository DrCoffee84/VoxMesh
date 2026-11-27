//go:build cgo

package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/i18n"
	"voxmesh/internal/logging"
	"voxmesh/internal/winproc"
)

// newAutostartCheck arma "Iniciar con Windows" de Ajustes → Sistema (AGENDA F12) y su ayuda. No
// pasa por Guardar: escribe el registro en el momento, como el nivel del log, y lee de ahí cada
// vez que se abre Ajustes (también refleja si se apagó desde el Administrador de tareas). Si el
// registro no se deja escribir, el check vuelve a lo que dice Windows. Hilo de UI.
func (a *App) newAutostartCheck() (*widget.Check, *widget.Label) {
	check := widget.NewCheck(i18n.T("settings.autostart"), nil)
	check.SetChecked(winproc.Autostart())
	check.OnChanged = func(on bool) {
		if on == winproc.Autostart() {
			return // lo puso SetChecked de abajo, o ya estaba así
		}
		if err := winproc.SetAutostart(on); err != nil {
			logging.Errorf("inicio con Windows: %s", logging.FormatError(err))
			a.setStatus(i18n.M("settings.autostart_failed", errorMsg(err)))
			check.SetChecked(winproc.Autostart())
			return
		}
		logging.Infof("Inicio con Windows: %v", on)
	}
	help := widget.NewLabel(i18n.T("settings.autostart_help"))
	help.Wrapping = fyne.TextWrapWord
	help.Importance = widget.LowImportance
	return check, help
}

// refreshAutostart, al arrancar: si el inicio con Windows está prendido pero apunta a otra
// carpeta (el exe se movió), lo apunta a este exe.
func (a *App) refreshAutostart() {
	changed, err := winproc.RefreshAutostart()
	switch {
	case err != nil:
		logging.Errorf("actualizar inicio con Windows: %s", logging.FormatError(err))
	case changed:
		logging.Infof("Inicio con Windows: apuntaba a otra carpeta, ahora a este exe")
	}
}
