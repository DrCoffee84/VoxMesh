//go:build !windows && cgo

package ui

// hookTrayLeftClick: fuera de Windows el clic izquierdo queda como lo maneja systray.
func (a *App) hookTrayLeftClick() {}
