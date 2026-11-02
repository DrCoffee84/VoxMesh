//go:build !windows && cgo

package ui

// Fuera de Windows solo se recuerda el tamaño: Fyne 2.6.3 no permite maximizar.
func (a *App) windowShowState() (maximized, minimized bool) { return false, false }

func (a *App) maximizeWindow() {}
