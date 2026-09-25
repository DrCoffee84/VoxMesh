//go:build windows && cgo

package ui

import (
	"os"
	"os/exec"

	"voxmesh/internal/logging"
)

func openLogsDirectory() error {
	directory, err := logging.Directory()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	return exec.Command("explorer.exe", directory).Start()
}
