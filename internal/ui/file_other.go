//go:build !windows && cgo

package ui

import "errors"

func (a *App) dialogOwner() uintptr { return 0 }

func selectImageFile(uintptr) (string, error) {
	return "", errors.New("selector nativo no disponible en este sistema")
}

func selectSoundFile(uintptr) (string, error) {
	return "", errors.New("selector nativo no disponible en este sistema")
}
