//go:build !windows && cgo

package ui

import "errors"

func selectImageFile() (string, error) {
	return "", errors.New("selector nativo no disponible en este sistema")
}

func selectSoundFile() (string, error) {
	return "", errors.New("selector nativo no disponible en este sistema")
}
