//go:build windows && cgo

package ui

import (
	"os"
	"os/exec"
	"path/filepath"

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

func openDataDirectory() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	directory := filepath.Join(configDir, "VoxMesh")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	return exec.Command("explorer.exe", directory).Start()
}
