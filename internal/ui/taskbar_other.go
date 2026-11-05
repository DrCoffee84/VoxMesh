//go:build !windows && cgo

package ui

// Fuera de Windows no hay punto en la barra de tareas: el aviso es solo el título
// (setSharingIndicator).
func (a *App) setTaskbarOverlay(bool) {}
