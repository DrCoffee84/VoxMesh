//go:build windows && cgo

package ui

import (
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/config"
	"voxmesh/internal/room"
)

var (
	hotkeyUser32         = syscall.NewLazyDLL("user32.dll")
	procGetAsyncKeyState = hotkeyUser32.NewProc("GetAsyncKeyState")
)

func getAsyncKey(vk int) bool {
	ret, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return int16(ret) < 0
}

const (
	vkShift   = 0x10
	vkControl = 0x11
	vkMenu    = 0x12 // Alt
)

type keyInfo struct {
	vk   int
	name string
}

var candidateKeys = []keyInfo{
	// Numpad
	{0x60, "Numpad 0"},
	{0x61, "Numpad 1"},
	{0x62, "Numpad 2"},
	{0x63, "Numpad 3"},
	{0x64, "Numpad 4"},
	{0x65, "Numpad 5"},
	{0x66, "Numpad 6"},
	{0x67, "Numpad 7"},
	{0x68, "Numpad 8"},
	{0x69, "Numpad 9"},
	{0x6A, "Numpad *"},
	{0x6B, "Numpad +"},
	{0x6D, "Numpad -"},
	{0x6E, "Numpad ."},
	{0x6F, "Numpad /"},

	// Regular numbers 0-9
	{0x30, "0"},
	{0x31, "1"},
	{0x32, "2"},
	{0x33, "3"},
	{0x34, "4"},
	{0x35, "5"},
	{0x36, "6"},
	{0x37, "7"},
	{0x38, "8"},
	{0x39, "9"},

	// Function keys F1-F12
	{0x70, "F1"},
	{0x71, "F2"},
	{0x72, "F3"},
	{0x73, "F4"},
	{0x74, "F5"},
	{0x75, "F6"},
	{0x76, "F7"},
	{0x77, "F8"},
	{0x78, "F9"},
	{0x79, "F10"},
	{0x7A, "F11"},
	{0x7B, "F12"},

	// Letters A-Z
	{0x41, "A"}, {0x42, "B"}, {0x43, "C"}, {0x44, "D"}, {0x45, "E"},
	{0x46, "F"}, {0x47, "G"}, {0x48, "H"}, {0x49, "I"}, {0x4A, "J"},
	{0x4B, "K"}, {0x4C, "L"}, {0x4D, "M"}, {0x4E, "N"}, {0x4F, "O"},
	{0x50, "P"}, {0x51, "Q"}, {0x52, "R"}, {0x53, "S"}, {0x54, "T"},
	{0x55, "U"}, {0x56, "V"}, {0x57, "W"}, {0x58, "X"}, {0x59, "Y"},
	{0x5A, "Z"},

	// Navigation
	{0x2D, "Insert"},
	{0x2E, "Delete"},
	{0x24, "Home"},
	{0x23, "End"},
	{0x21, "Page Up"},
	{0x22, "Page Down"},
}

type parsedHotkey struct {
	shift bool
	ctrl  bool
	alt   bool
	vk    int
}

func parseHotkeyString(s string) (parsedHotkey, bool) {
	parts := strings.Split(s, "+")
	var res parsedHotkey
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if strings.EqualFold(trimmed, "Shift") {
			res.shift = true
		} else if strings.EqualFold(trimmed, "Ctrl") || strings.EqualFold(trimmed, "Control") {
			res.ctrl = true
		} else if strings.EqualFold(trimmed, "Alt") {
			res.alt = true
		} else {
			for _, k := range candidateKeys {
				if strings.EqualFold(k.name, trimmed) {
					res.vk = k.vk
					break
				}
			}
		}
	}
	return res, res.vk != 0
}

func isHotkeyActive(hk parsedHotkey) bool {
	if hk.shift != getAsyncKey(vkShift) {
		return false
	}
	if hk.ctrl != getAsyncKey(vkControl) {
		return false
	}
	if hk.alt != getAsyncKey(vkMenu) {
		return false
	}
	return getAsyncKey(hk.vk)
}

func isFunctionOrNumpad(vk int) bool {
	if vk >= 0x60 && vk <= 0x6F { // Numpad
		return true
	}
	if vk >= 0x70 && vk <= 0x7B { // F1-F12
		return true
	}
	if vk >= 0x21 && vk <= 0x2E { // Navigation
		return true
	}
	return false
}

func (a *App) triggerSoundByID(id string) {
	a.soundMu.RLock()
	var targetSound *room.Sound
	for i := range a.sounds {
		if a.sounds[i].ID == id {
			s := a.sounds[i]
			targetSound = &s
			break
		}
	}
	a.soundMu.RUnlock()

	if targetSound != nil {
		a.triggerSound(*targetSound)
	}
}

func (a *App) startHotkeyListener() {
	go func() {
		wasDown := make(map[string]bool)
		for {
			time.Sleep(25 * time.Millisecond)

			// Check stop sound bind
			if a.cfg.StopSoundBind != "" {
				hk, ok := parseHotkeyString(a.cfg.StopSoundBind)
				if ok {
					canTrigger := true
					if a.window != nil && a.window.Canvas() != nil && a.window.Canvas().Focused() != nil {
						if !hk.shift && !hk.ctrl && !hk.alt && !isFunctionOrNumpad(hk.vk) {
							wasDown["__stop__"] = false
							canTrigger = false
						}
					}
					if canTrigger {
						active := isHotkeyActive(hk)
						if active && !wasDown["__stop__"] {
							a.stopSoundboard()
						}
						wasDown["__stop__"] = active
					}
				}
			}

			if a.cfg.SoundBinds == nil || len(a.cfg.SoundBinds) == 0 {
				continue
			}

			for soundID, bindStr := range a.cfg.SoundBinds {
				if bindStr == "" {
					continue
				}
				hk, ok := parseHotkeyString(bindStr)
				if !ok {
					continue
				}

				if a.window != nil && a.window.Canvas() != nil && a.window.Canvas().Focused() != nil {
					if !hk.shift && !hk.ctrl && !hk.alt && !isFunctionOrNumpad(hk.vk) {
						wasDown[soundID] = false
						continue
					}
				}

				active := isHotkeyActive(hk)
				if active && !wasDown[soundID] {
					a.triggerSoundByID(soundID)
				}
				wasDown[soundID] = active
			}
		}
	}()
}

func (a *App) showSoundBindDialog(sound room.Sound) {
	currentBind := ""
	if a.cfg.SoundBinds != nil {
		currentBind = a.cfg.SoundBinds[sound.ID]
	}

	detectedCombo := currentBind

	titleLabel := widget.NewLabelWithStyle(fmt.Sprintf("Sonido: %s", sound.Name), fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	statusText := "[ Presiona las teclas deseadas ]"
	if currentBind != "" {
		statusText = "Atajo actual: " + currentBind
	}
	comboLabel := widget.NewLabelWithStyle(statusText, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	helpLabel := widget.NewLabel("Presiona teclas como 1..9, Numpad 1..9, F1..F12, o con Shift/Ctrl/Alt.")
	helpLabel.Alignment = fyne.TextAlignCenter

	stopScan := make(chan struct{})
	var closeOnce sync.Once
	cleanup := func() {
		closeOnce.Do(func() {
			close(stopScan)
		})
	}

	go func() {
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopScan:
				return
			case <-ticker.C:
				shift := getAsyncKey(vkShift)
				ctrl := getAsyncKey(vkControl)
				alt := getAsyncKey(vkMenu)

				for _, k := range candidateKeys {
					if getAsyncKey(k.vk) {
						var parts []string
						if ctrl {
							parts = append(parts, "Ctrl")
						}
						if alt {
							parts = append(parts, "Alt")
						}
						if shift {
							parts = append(parts, "Shift")
						}
						parts = append(parts, k.name)
						comboStr := strings.Join(parts, " + ")
						detectedCombo = comboStr
						fyne.Do(func() {
							comboLabel.SetText("Detectado: " + comboStr)
						})
						break
					}
				}
			}
		}
	}()

	var d dialog.Dialog

	btnAccept := widget.NewButton("Aceptar", func() {
		cleanup()
		if detectedCombo != "" {
			if a.cfg.SoundBinds == nil {
				a.cfg.SoundBinds = make(map[string]string)
			}
			a.cfg.SoundBinds[sound.ID] = detectedCombo
			_ = config.Save(a.cfgPath, a.cfg)
			a.rebuildSoundboard()
			a.setStatus(fmt.Sprintf("Atajo '%s' asignado a '%s'", detectedCombo, sound.Name))
		}
		d.Hide()
	})
	btnAccept.Importance = widget.HighImportance

	btnUnbind := widget.NewButton("Desbindear", func() {
		cleanup()
		if a.cfg.SoundBinds != nil {
			delete(a.cfg.SoundBinds, sound.ID)
			_ = config.Save(a.cfgPath, a.cfg)
		}
		a.rebuildSoundboard()
		a.setStatus(fmt.Sprintf("Atajo removido de '%s'", sound.Name))
		d.Hide()
	})
	btnUnbind.Importance = widget.DangerImportance

	btnCancel := widget.NewButton("Cancelar", func() {
		cleanup()
		d.Hide()
	})

	buttons := container.NewGridWithColumns(3, btnAccept, btnUnbind, btnCancel)
	content := container.NewVBox(
		titleLabel,
		widget.NewSeparator(),
		comboLabel,
		helpLabel,
		widget.NewSeparator(),
		buttons,
	)

	d = dialog.NewCustomWithoutButtons("Configurar atajo de teclado", content, a.window)
	d.Resize(fyne.NewSize(380, 180))
	d.SetOnClosed(cleanup)
	d.Show()
}

func (a *App) showStopSoundBindDialog() {
	currentBind := a.cfg.StopSoundBind
	detectedCombo := currentBind

	titleLabel := widget.NewLabelWithStyle("Detener sonido", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	statusText := "[ Presiona las teclas deseadas ]"
	if currentBind != "" {
		statusText = "Atajo actual: " + currentBind
	}
	comboLabel := widget.NewLabelWithStyle(statusText, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	helpLabel := widget.NewLabel("Presiona teclas como 1..9, Numpad 1..9, F1..F12, o con Shift/Ctrl/Alt.")
	helpLabel.Alignment = fyne.TextAlignCenter

	stopScan := make(chan struct{})
	var closeOnce sync.Once
	cleanup := func() {
		closeOnce.Do(func() {
			close(stopScan)
		})
	}

	go func() {
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopScan:
				return
			case <-ticker.C:
				shift := getAsyncKey(vkShift)
				ctrl := getAsyncKey(vkControl)
				alt := getAsyncKey(vkMenu)

				for _, k := range candidateKeys {
					if getAsyncKey(k.vk) {
						var parts []string
						if ctrl {
							parts = append(parts, "Ctrl")
						}
						if alt {
							parts = append(parts, "Alt")
						}
						if shift {
							parts = append(parts, "Shift")
						}
						parts = append(parts, k.name)
						comboStr := strings.Join(parts, " + ")
						detectedCombo = comboStr
						fyne.Do(func() {
							comboLabel.SetText("Detectado: " + comboStr)
						})
						break
					}
				}
			}
		}
	}()

	var d dialog.Dialog

	btnAccept := widget.NewButton("Aceptar", func() {
		cleanup()
		if detectedCombo != "" {
			a.cfg.StopSoundBind = detectedCombo
			_ = config.Save(a.cfgPath, a.cfg)
			if a.stopBindBtn != nil {
				a.stopBindBtn.SetText("⌨ " + detectedCombo)
			}
			a.setStatus(fmt.Sprintf("Atajo '%s' asignado para detener sonido", detectedCombo))
		}
		d.Hide()
	})
	btnAccept.Importance = widget.HighImportance

	btnUnbind := widget.NewButton("Desbindear", func() {
		cleanup()
		a.cfg.StopSoundBind = ""
		_ = config.Save(a.cfgPath, a.cfg)
		if a.stopBindBtn != nil {
			a.stopBindBtn.SetText("⌨ Bind")
		}
		a.setStatus("Atajo removido para detener sonido")
		d.Hide()
	})
	btnUnbind.Importance = widget.DangerImportance

	btnCancel := widget.NewButton("Cancelar", func() {
		cleanup()
		d.Hide()
	})

	buttons := container.NewGridWithColumns(3, btnAccept, btnUnbind, btnCancel)
	content := container.NewVBox(
		titleLabel,
		widget.NewSeparator(),
		comboLabel,
		helpLabel,
		widget.NewSeparator(),
		buttons,
	)

	d = dialog.NewCustomWithoutButtons("Configurar atajo para detener sonido", content, a.window)
	d.Resize(fyne.NewSize(380, 180))
	d.SetOnClosed(cleanup)
	d.Show()
}
