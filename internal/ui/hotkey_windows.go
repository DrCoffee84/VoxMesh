//go:build windows && cgo

package ui

import (
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"golang.org/x/sys/windows"

	"voxmesh/internal/config"
	"voxmesh/internal/i18n"
	"voxmesh/internal/logging"
	"voxmesh/internal/room"
)

var (
	hotkeyUser32         = windows.NewLazySystemDLL("user32.dll")
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

// hotkeyAction es lo que dispara un atajo de la botonera.
type hotkeyAction int

const (
	hotkeySound   hotkeyAction = iota // un sonido (boundHotkey.soundID)
	hotkeyStop                        // "Detener"
	hotkeyToggle                      // prender/apagar los atajos de la botonera (AGENDA C20)
	hotkeyStopAll                     // "Detener para todos" (AGENDA C37)
)

// boundHotkey es un atajo ya parseado; soundID solo con hotkeySound.
type boundHotkey struct {
	action  hotkeyAction
	soundID string
	hk      parsedHotkey
}

// pollKey identifica el atajo en el estado de teclas apretadas del poller.
func (b boundHotkey) pollKey() string {
	switch b.action {
	case hotkeyStop:
		return "__stop__"
	case hotkeyToggle:
		return "__toggle__"
	case hotkeyStopAll:
		return "__stop_all__"
	}
	return b.soundID
}

// activeHotkeys es lo único que lee el poller. publishHotkeys lo reemplaza entero cada vez que
// cambia un atajo, así el poller no itera cfg.SoundBinds ni re-parsea strings cada 25 ms.
var activeHotkeys atomic.Pointer[[]boundHotkey]

// hotkeyBinds arma la lista del poller. Con los atajos de la botonera apagados
// (SoundHotkeysOff) queda solo el que los prende: los demás siguen en la config y vuelven al
// prenderlos.
func hotkeyBinds(cfg *config.Config) []boundHotkey {
	var list []boundHotkey
	if hk, ok := parseHotkeyString(cfg.ToggleHotkeysBind); ok {
		list = append(list, boundHotkey{action: hotkeyToggle, hk: hk})
	}
	if cfg.SoundHotkeysOff {
		return list
	}
	if hk, ok := parseHotkeyString(cfg.StopSoundBind); ok {
		list = append(list, boundHotkey{action: hotkeyStop, hk: hk})
	}
	if hk, ok := parseHotkeyString(cfg.StopAllSoundBind); ok {
		list = append(list, boundHotkey{action: hotkeyStopAll, hk: hk})
	}
	for soundID, bind := range cfg.SoundBinds {
		if hk, ok := parseHotkeyString(bind); ok {
			list = append(list, boundHotkey{action: hotkeySound, soundID: soundID, hk: hk})
		}
	}
	return list
}

func (a *App) publishHotkeys() {
	list := hotkeyBinds(a.conf())
	activeHotkeys.Store(&list)
}

// isTypingInApp consulta el foco en el hilo de UI. Antes se leía Canvas().Focused() desde la
// goroutine del poller 40 veces por segundo, sin sincronizar con el hilo de Fyne.
func (a *App) isTypingInApp() bool {
	if a.window == nil {
		return false
	}
	focused := false
	fyne.DoAndWait(func() {
		focused = a.window.Canvas().Focused() != nil
	})
	return focused
}

func (a *App) startHotkeyListener() {
	a.publishHotkeys()
	go func() {
		// Un panic queda en el log y el poller vuelve a arrancar: los atajos siguen andando.
		for !logging.Guard("atajos de teclado", a.pollHotkeys) {
			time.Sleep(time.Second)
		}
	}()
}

// pollHotkeys lee el teclado cada 25 ms y dispara los sonidos de las teclas recién apretadas.
func (a *App) pollHotkeys() {
	wasDown := make(map[string]bool)
	for {
		time.Sleep(25 * time.Millisecond)
		list := activeHotkeys.Load()
		if list == nil {
			continue
		}
		for _, bound := range *list {
			key := bound.pollKey()
			active := isHotkeyActive(bound.hk)
			pressed := active && !wasDown[key]
			wasDown[key] = active
			if !pressed {
				continue
			}
			// Una tecla suelta (letra/número) no se dispara mientras se escribe en VoxMesh.
			// El foco solo se consulta en ese caso, que es raro (una vez por pulsación).
			hk := bound.hk
			if !hk.shift && !hk.ctrl && !hk.alt && !isFunctionOrNumpad(hk.vk) && a.isTypingInApp() {
				continue
			}
			switch bound.action {
			case hotkeyStop:
				a.stopSoundboard()
			case hotkeyStopAll:
				a.stopSoundsForAll() // corta acá y manda sound_stop en su propia goroutine
			case hotkeyToggle:
				fyne.Do(a.toggleSoundHotkeys)
			default:
				a.triggerSoundByID(bound.soundID)
			}
		}
	}
}

// bindTarget es el dueño de un atajo: un sonido (por ID), "Detener", "Detener para todos" o el que
// prende y apaga los atajos.
type bindTarget struct {
	action  hotkeyAction
	soundID string
}

// bindConflict dice si combo ya lo usa otro atajo que no sea self y, si es así, su nombre en la
// botonera. Compara la tecla parseada ("Ctrl + K" y "ctrl+k" son la misma). Un atajo de un sonido
// que no está en soundNames (de otra sala o borrado) no cuenta: hoy no suena.
func bindConflict(cfg *config.Config, soundNames map[string]string, combo string, self bindTarget) (string, bool) {
	want, ok := parseHotkeyString(combo)
	if !ok {
		return "", false
	}
	same := func(bind string) bool {
		hk, ok := parseHotkeyString(bind)
		return ok && hk == want
	}
	if self.action != hotkeyToggle && same(cfg.ToggleHotkeysBind) {
		return i18n.T("hotkey.toggle_name"), true
	}
	if self.action != hotkeyStop && same(cfg.StopSoundBind) {
		return i18n.T("sound.stop"), true
	}
	if self.action != hotkeyStopAll && same(cfg.StopAllSoundBind) {
		return i18n.T("hotkey.stop_all_title"), true
	}
	for _, id := range slices.Sorted(maps.Keys(cfg.SoundBinds)) {
		if self.action == hotkeySound && id == self.soundID {
			continue
		}
		if name, present := soundNames[id]; present && same(cfg.SoundBinds[id]) {
			return name, true
		}
	}
	return "", false
}

// soundNames es el nombre de cada sonido de la botonera por ID (bindConflict).
func (a *App) soundNames() map[string]string {
	a.soundMu.RLock()
	defer a.soundMu.RUnlock()
	names := make(map[string]string, len(a.sounds))
	for _, sound := range a.sounds {
		names[sound.ID] = sound.Name
	}
	return names
}

// bindDialog es el diálogo que elige un atajo: el de un sonido, el de "Detener", el de "Detener para
// todos" y el que prende y apaga los atajos son iguales salvo los textos y qué se guarda.
type bindDialog struct {
	dialogTitle string
	title       string
	current     string
	target      bindTarget
	accept      func(combo string) // hilo de UI
	unbind      func()             // hilo de UI
}

// showBindDialog lee el teclado mientras está abierto. Una tecla que ya usa otro atajo no se
// acepta: lo dice y el diálogo queda abierto para elegir otra.
func (a *App) showBindDialog(spec bindDialog) {
	detectedCombo := spec.current

	titleLabel := widget.NewLabelWithStyle(spec.title, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	statusText := i18n.T("hotkey.press_keys")
	if spec.current != "" {
		statusText = i18n.T("hotkey.current", spec.current)
	}
	comboLabel := widget.NewLabelWithStyle(statusText, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	comboLabel.Wrapping = fyne.TextWrapWord

	helpLabel := widget.NewLabel(i18n.T("hotkey.help"))
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
		lastCombo := ""
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
						if comboStr != lastCombo {
							lastCombo = comboStr
							// detectedCombo lo lee el botón Aceptar: se escribe en el hilo de UI.
							fyne.Do(func() {
								detectedCombo = comboStr
								comboLabel.SetText(i18n.T("hotkey.detected", comboStr))
							})
						}
						break
					}
				}
			}
		}
	}()

	var d dialog.Dialog

	btnAccept := widget.NewButton(i18n.T("common.accept"), func() {
		if detectedCombo != "" {
			if name, taken := bindConflict(a.conf(), a.soundNames(), detectedCombo, spec.target); taken {
				comboLabel.SetText(i18n.T("hotkey.in_use", detectedCombo, name))
				return
			}
			spec.accept(detectedCombo)
		}
		cleanup()
		d.Hide()
	})
	btnAccept.Importance = widget.HighImportance

	btnUnbind := widget.NewButton(i18n.T("hotkey.unbind"), func() {
		cleanup()
		spec.unbind()
		d.Hide()
	})
	btnUnbind.Importance = widget.DangerImportance

	btnCancel := widget.NewButton(i18n.T("common.cancel"), func() {
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

	d = dialog.NewCustomWithoutButtons(spec.dialogTitle, content, a.window)
	d.Resize(fyne.NewSize(380, 180))
	d.SetOnClosed(cleanup)
	d.Show()
}

func (a *App) showSoundBindDialog(sound room.Sound) {
	a.showBindDialog(bindDialog{
		dialogTitle: i18n.T("hotkey.dialog_title"),
		title:       i18n.T("hotkey.sound_title", sound.Name),
		current:     a.conf().SoundBinds[sound.ID],
		target:      bindTarget{action: hotkeySound, soundID: sound.ID},
		accept: func(combo string) {
			a.updateSoundBinds(func(binds map[string]string) { binds[sound.ID] = combo })
			a.rebuildSoundboard()
			a.setStatus(i18n.M("hotkey.assigned", combo, sound.Name))
		},
		unbind: func() {
			a.updateSoundBinds(func(binds map[string]string) { delete(binds, sound.ID) })
			a.rebuildSoundboard()
			a.setStatus(i18n.M("hotkey.removed", sound.Name))
		},
	})
}

func (a *App) showStopSoundBindDialog() {
	set := func(combo string) {
		a.updateConfig(func(c *config.Config) { c.StopSoundBind = combo })
		a.publishHotkeys()
		a.saveConfig()
		if a.stopBindBtn != nil {
			a.stopBindBtn.SetCombo(combo)
		}
	}
	a.showBindDialog(bindDialog{
		dialogTitle: i18n.T("hotkey.stop_dialog_title"),
		title:       i18n.T("hotkey.stop_title"),
		current:     a.conf().StopSoundBind,
		target:      bindTarget{action: hotkeyStop},
		accept: func(combo string) {
			set(combo)
			a.setStatus(i18n.M("hotkey.stop_assigned", combo))
		},
		unbind: func() {
			set("")
			a.setStatus(i18n.M("hotkey.stop_removed"))
		},
	})
}

// showStopAllSoundBindDialog elige el atajo de "Detener para todos" (C37).
func (a *App) showStopAllSoundBindDialog() {
	set := func(combo string) {
		a.updateConfig(func(c *config.Config) { c.StopAllSoundBind = combo })
		a.publishHotkeys()
		a.saveConfig()
		if a.stopAllBindBtn != nil {
			a.stopAllBindBtn.SetCombo(combo)
		}
	}
	a.showBindDialog(bindDialog{
		dialogTitle: i18n.T("hotkey.stop_all_dialog_title"),
		title:       i18n.T("hotkey.stop_all_title"),
		current:     a.conf().StopAllSoundBind,
		target:      bindTarget{action: hotkeyStopAll},
		accept: func(combo string) {
			set(combo)
			a.setStatus(i18n.M("hotkey.stop_all_assigned", combo))
		},
		unbind: func() {
			set("")
			a.setStatus(i18n.M("hotkey.stop_all_removed"))
		},
	})
}

// showToggleHotkeysBindDialog elige el atajo que prende y apaga los atajos de la botonera (C20).
func (a *App) showToggleHotkeysBindDialog() {
	set := func(combo string) {
		a.updateConfig(func(c *config.Config) { c.ToggleHotkeysBind = combo })
		a.publishHotkeys()
		a.saveConfig()
		if a.toggleBindBtn != nil {
			a.toggleBindBtn.SetCombo(combo)
		}
	}
	a.showBindDialog(bindDialog{
		dialogTitle: i18n.T("hotkey.toggle_dialog_title"),
		title:       i18n.T("hotkey.toggle_name"),
		current:     a.conf().ToggleHotkeysBind,
		target:      bindTarget{action: hotkeyToggle},
		accept: func(combo string) {
			set(combo)
			a.setStatus(i18n.M("hotkey.toggle_assigned", combo))
		},
		unbind: func() {
			set("")
			a.setStatus(i18n.M("hotkey.toggle_removed"))
		},
	})
}
