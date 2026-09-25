//go:build !windows && cgo

package ui

import "errors"

func openLogsDirectory() error {
	return errors.New("apertura de carpeta no disponible en este sistema")
}

func openDataDirectory() error {
	return errors.New("apertura de carpeta no disponible en este sistema")
}
