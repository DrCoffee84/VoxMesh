//go:build !windows || !cgo

package ui

import "voxmesh/internal/room"

func (a *App) startHotkeyListener() {}

func (a *App) triggerSoundByID(id string) {}

func (a *App) showSoundBindDialog(sound room.Sound) {
	// Fallback when global hotkeys are not available
}

func (a *App) showStopSoundBindDialog() {
	// Fallback when global hotkeys are not available
}
