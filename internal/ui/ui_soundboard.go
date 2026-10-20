//go:build cgo

package ui

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/assets"
	"voxmesh/internal/audio"
	"voxmesh/internal/config"
	"voxmesh/internal/history"
	"voxmesh/internal/logging"
	"voxmesh/internal/room"
)

func (a *App) buildSoundPanel() {
	title := widget.NewLabelWithStyle("Sonidos", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	muteCheck := widget.NewCheck("Bloquear sonidos ajenos", func(checked bool) {
		a.cfg.SoundboardMuted = checked
		_ = config.Save(a.cfgPath, a.cfg)
	})
	muteCheck.SetChecked(a.cfg.SoundboardMuted)

	volVal := a.cfg.SoundboardVolume
	if volVal <= 0.01 {
		volVal = 1.0
	}
	volLabel := widget.NewLabel(fmt.Sprintf("Volumen: %d%%", int(volVal*100)))
	volSlider := widget.NewSlider(0, 1)
	volSlider.Step = 0.05
	volSlider.Value = float64(volVal)
	volSlider.OnChanged = func(val float64) {
		a.cfg.SoundboardVolume = float32(val)
		volLabel.SetText(fmt.Sprintf("Volumen: %d%%", int(val*100)))
		_ = config.Save(a.cfgPath, a.cfg)
	}

	stopButton := widget.NewButton("⏹ Detener", a.stopSoundboard)
	stopBindText := "⌨ Bind"
	if a.cfg.StopSoundBind != "" {
		stopBindText = "⌨ " + a.cfg.StopSoundBind
	}
	a.stopBindBtn = widget.NewButton(stopBindText, a.showStopSoundBindDialog)
	a.stopBindBtn.Importance = widget.LowImportance
	stopBindWrap := container.NewGridWrap(fyne.NewSize(96, 34), a.stopBindBtn)
	delSpacer := container.NewGridWrap(fyne.NewSize(34, 34), layout.NewSpacer())
	stopActions := container.NewHBox(stopBindWrap, delSpacer)
	stopRow := container.NewBorder(nil, nil, nil, stopActions, stopButton)

	addButton := widget.NewButton("➕ Agregar sonido", a.pickAndAddSound)
	a.soundGrid = container.NewVBox()
	soundScroll := container.NewVScroll(a.soundGrid)
	soundScroll.SetMinSize(fyne.NewSize(220, 160))
	soundHeader := container.NewVBox(
		title,
		muteCheck,
		volLabel,
		volSlider,
		stopRow,
		widget.NewSeparator(),
	)
	soundContent := container.NewBorder(soundHeader, addButton, nil, nil, soundScroll)
	a.soundPanel = uiCard(soundContent)
	a.rebuildSoundboard()
}

func (a *App) rebuildSoundboard() {
	a.soundMu.RLock()
	sounds := append([]room.Sound(nil), a.sounds...)
	a.soundMu.RUnlock()
	objects := make([]fyne.CanvasObject, 0, len(sounds))
	for _, sound := range sounds {
		captured := sound
		displayName := captured.Name
		runes := []rune(displayName)
		if len(runes) > 13 {
			displayName = string(runes[:11]) + "…"
		}
		btnPlay := widget.NewButton(displayName, func() { a.triggerSound(captured) })
		btnDelete := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() { a.removeSound(captured.ID) })

		bindText := ""
		if a.cfg.SoundBinds != nil {
			bindText = a.cfg.SoundBinds[captured.ID]
		}
		bindLabel := "⌨ Bind"
		if bindText != "" {
			bindLabel = "⌨ " + bindText
		}
		btnBind := widget.NewButton(bindLabel, func() { a.showSoundBindDialog(captured) })
		btnBind.Importance = widget.LowImportance

		bindWrap := container.NewGridWrap(fyne.NewSize(96, 34), btnBind)
		delWrap := container.NewGridWrap(fyne.NewSize(34, 34), btnDelete)
		actions := container.NewHBox(bindWrap, delWrap)
		row := container.NewBorder(nil, nil, nil, actions, btnPlay)
		objects = append(objects, row)
	}
	fyne.Do(func() {
		if a.soundGrid == nil {
			return
		}
		a.soundGrid.Objects = objects
		a.soundGrid.Refresh()
	})
}

func (a *App) addSound(sound room.Sound) {
	a.soundMu.Lock()
	for index, existing := range a.sounds {
		if existing.ID == sound.ID {
			a.sounds[index] = sound
			a.soundMu.Unlock()
			a.rebuildSoundboard()
			return
		}
	}
	a.sounds = append(a.sounds, sound)
	a.soundMu.Unlock()
	a.rebuildSoundboard()
}

func (a *App) removeSound(soundID string) {
	a.soundMu.Lock()
	soundIndex := -1
	var deletedSound room.Sound
	for index, existing := range a.sounds {
		if existing.ID == soundID {
			soundIndex = index
			deletedSound = existing
			break
		}
	}

	if soundIndex == -1 {
		a.soundMu.Unlock()
		return
	}

	a.sounds = append(a.sounds[:soundIndex], a.sounds[soundIndex+1:]...)
	a.soundMu.Unlock()

	if a.cfg.SoundBinds != nil {
		delete(a.cfg.SoundBinds, soundID)
		_ = config.Save(a.cfgPath, a.cfg)
	}

	a.rebuildSoundboard()

	if a.historyStore != nil {
		if err := a.historyStore.DeleteSound(deletedSound.ID, deletedSound.Ext); err != nil && !os.IsNotExist(err) {
			logging.Errorf("no se pudo eliminar el archivo del sonido local: %s", logging.FormatError(err))
		}
	}

	if a.transport != nil {
		payload, encodeErr := room.Encode(room.Envelope{
			Kind:  room.SoundRemoveMessage,
			Sound: &deletedSound,
		})
		if encodeErr == nil {
			a.sendChatPayload(payload)
		}
	}

	a.setStatus("Sonido eliminado: " + deletedSound.Name)
	go func() {
		runtime.GC()
		debug.FreeOSMemory()
	}()
}

func (a *App) pickAndAddSound() {
	defer func() {
		if value := recover(); value != nil {
			logging.Panic(value)
			a.setStatus("No se pudo abrir el selector de sonidos.")
		}
	}()
	if a.historyStore == nil || a.transport == nil {
		a.setStatus("Conecta a una sala antes de agregar un sonido.")
		return
	}
	path, err := selectSoundFile()
	if err != nil {
		if err.Error() != "selección cancelada" {
			logging.Errorf("selector de sonidos: %s", logging.FormatError(err))
		}
		return
	}
	a.setStatus("Cargando y procesando audio...")
	go func() {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			a.setStatus("No se pudo leer el sonido: " + readErr.Error())
			return
		}
		extension := strings.ToLower(filepath.Ext(path))
		pcm, decodeErr := audio.DecodeSoundFile(extension, raw)
		raw = nil // Liberar memoria del archivo original de inmediato
		if decodeErr != nil {
			a.setStatus("No se pudo procesar el sonido: " + decodeErr.Error())
			go func() {
				runtime.GC()
				debug.FreeOSMemory()
			}()
			return
		}
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		fyne.Do(func() {
			a.showSoundTrimDialog(name, pcm)
		})
	}()
}

// computeWaveformPeaks calcula los picos de amplitud absoluta directamente de los bytes PCM S16LE
// sin asignar arreglos gigantes intermedios ni decodificar todo a []int16.
func computeWaveformPeaks(pcm []byte, width int) []int16 {
	peaks := make([]int16, width)
	totalSamples := len(pcm) / 2
	if totalSamples == 0 || width <= 0 {
		return peaks
	}
	for x := 0; x < width; x++ {
		sStart := (x * totalSamples) / width
		sEnd := ((x + 1) * totalSamples) / width
		if sEnd > totalSamples {
			sEnd = totalSamples
		}
		var maxVal int16
		for i := sStart; i < sEnd; i++ {
			byteIdx := i * 2
			sample := int16(binary.LittleEndian.Uint16(pcm[byteIdx : byteIdx+2]))
			if sample < 0 {
				sample = -sample
			}
			if sample > maxVal {
				maxVal = sample
			}
		}
		peaks[x] = maxVal
	}
	return peaks
}

// drawWaveform dibuja la onda directamente sobre el buffer de imagen existente sin ninguna asignación de memoria.
func drawWaveform(img *image.RGBA, peaks []int16, startFrac, endFrac float64) {
	width := img.Bounds().Dx()
	height := img.Bounds().Dy()
	midY := height / 2

	bgColor := color.RGBA{R: 24, G: 28, B: 34, A: 255}
	selectedColor := color.RGBA{R: 46, G: 204, B: 113, A: 255}
	unselectedColor := color.RGBA{R: 70, G: 80, B: 95, A: 255}

	startX := int(startFrac * float64(width))
	endX := int(endFrac * float64(width))
	if startX < 0 {
		startX = 0
	}
	if endX > width {
		endX = width
	}

	for x := 0; x < width; x++ {
		maxVal := int16(0)
		if x < len(peaks) {
			maxVal = peaks[x]
		}
		barHeight := int(float64(maxVal) / 32767.0 * float64(midY-2))
		if barHeight < 1 {
			barHeight = 1
		}

		isSelected := x >= startX && x <= endX
		colColor := unselectedColor
		if isSelected {
			colColor = selectedColor
		}

		for y := 0; y < height; y++ {
			if y >= midY-barHeight && y <= midY+barHeight {
				img.SetRGBA(x, y, colColor)
			} else {
				img.SetRGBA(x, y, bgColor)
			}
		}
	}
}

func (a *App) showSoundTrimDialog(defaultName string, pcm []byte) {
	totalSec := float64(len(pcm)) / 96000.0
	if totalSec <= 0.1 {
		a.saveAndBroadcastSound(defaultName, pcm)
		return
	}

	const imgW, imgH = 420, 80
	waveImg := image.NewRGBA(image.Rect(0, 0, imgW, imgH))

	viewStartByte := 0
	viewEndByte := len(pcm)

	type zoomLevel struct {
		startByte int
		endByte   int
	}
	var zoomHistory []zoomLevel

	activePeaks := computeWaveformPeaks(pcm[viewStartByte:viewEndByte], imgW)
	drawWaveform(waveImg, activePeaks, 0, 1)

	nameEntry := widget.NewEntry()
	nameEntry.SetText(defaultName)

	durationLabel := widget.NewLabel(fmt.Sprintf("Duración visible: %.2f s", totalSec))

	waveformImg := canvas.NewImageFromImage(waveImg)
	waveformImg.SetMinSize(fyne.NewSize(imgW, imgH))
	waveformImg.FillMode = canvas.ImageFillStretch

	startSlider := widget.NewSlider(0, totalSec)
	startSlider.SetValue(0)

	endSlider := widget.NewSlider(0, totalSec)
	endSlider.SetValue(totalSec)

	startEntry := widget.NewEntry()
	startEntry.SetText("0.00")
	startEntryBox := container.NewGridWrap(fyne.NewSize(75, 36), startEntry)

	endEntry := widget.NewEntry()
	endEntry.SetText(fmt.Sprintf("%.2f", totalSec))
	endEntryBox := container.NewGridWrap(fyne.NewSize(75, 36), endEntry)

	var isUpdating bool

	updateWaveform := func() {
		viewLenSec := float64(viewEndByte-viewStartByte) / 96000.0
		sFrac := 0.0
		eFrac := 1.0
		if viewLenSec > 0 {
			sFrac = startSlider.Value / viewLenSec
			eFrac = endSlider.Value / viewLenSec
		}
		drawWaveform(waveImg, activePeaks, sFrac, eFrac)
		waveformImg.Refresh()
	}

	applyStart := func(v float64) {
		if isUpdating {
			return
		}
		isUpdating = true
		curTotal := float64(viewEndByte-viewStartByte) / 96000.0
		if v < 0 {
			v = 0
		}
		if v >= endSlider.Value {
			v = math.Max(0, endSlider.Value-0.05)
		}
		if v > curTotal {
			v = curTotal
		}
		startSlider.SetValue(v)
		startEntry.SetText(fmt.Sprintf("%.2f", v))
		isUpdating = false
		updateWaveform()
	}

	applyEnd := func(v float64) {
		if isUpdating {
			return
		}
		isUpdating = true
		curTotal := float64(viewEndByte-viewStartByte) / 96000.0
		if v > curTotal {
			v = curTotal
		}
		if v <= startSlider.Value {
			v = math.Min(curTotal, startSlider.Value+0.05)
		}
		endSlider.SetValue(v)
		endEntry.SetText(fmt.Sprintf("%.2f", v))
		isUpdating = false
		updateWaveform()
	}

	startSlider.OnChanged = func(v float64) {
		if isUpdating {
			return
		}
		isUpdating = true
		if v >= endSlider.Value {
			v = math.Max(0, endSlider.Value-0.05)
			startSlider.SetValue(v)
		}
		startEntry.SetText(fmt.Sprintf("%.2f", v))
		isUpdating = false
		updateWaveform()
	}

	endSlider.OnChanged = func(v float64) {
		if isUpdating {
			return
		}
		isUpdating = true
		curTotal := float64(viewEndByte-viewStartByte) / 96000.0
		if v <= startSlider.Value {
			v = math.Min(curTotal, startSlider.Value+0.05)
			endSlider.SetValue(v)
		}
		endEntry.SetText(fmt.Sprintf("%.2f", v))
		isUpdating = false
		updateWaveform()
	}

	startEntry.OnSubmitted = func(txt string) {
		if val, err := strconv.ParseFloat(strings.TrimSpace(txt), 64); err == nil {
			applyStart(val)
		}
	}
	startEntry.OnChanged = func(txt string) {
		if isUpdating {
			return
		}
		if val, err := strconv.ParseFloat(strings.TrimSpace(txt), 64); err == nil {
			if val >= 0 && val < endSlider.Value {
				isUpdating = true
				startSlider.SetValue(val)
				isUpdating = false
				updateWaveform()
			}
		}
	}

	endEntry.OnSubmitted = func(txt string) {
		if val, err := strconv.ParseFloat(strings.TrimSpace(txt), 64); err == nil {
			applyEnd(val)
		}
	}
	endEntry.OnChanged = func(txt string) {
		if isUpdating {
			return
		}
		curTotal := float64(viewEndByte-viewStartByte) / 96000.0
		if val, err := strconv.ParseFloat(strings.TrimSpace(txt), 64); err == nil {
			if val > startSlider.Value && val <= curTotal {
				isUpdating = true
				endSlider.SetValue(val)
				isUpdating = false
				updateWaveform()
			}
		}
	}

	slicePCM := func() []byte {
		curTotalBytes := viewEndByte - viewStartByte
		s := int(startSlider.Value*96000) &^ 1
		e := int(endSlider.Value*96000) &^ 1
		if s < 0 {
			s = 0
		}
		if e > curTotalBytes {
			e = curTotalBytes
		}
		if e <= s {
			e = curTotalBytes
		}
		absStart := viewStartByte + s
		absEnd := viewStartByte + e
		if absEnd > len(pcm) {
			absEnd = len(pcm)
		}
		if absStart >= absEnd {
			return nil
		}
		return append([]byte(nil), pcm[absStart:absEnd]...)
	}

	var zoomOutBtn *widget.Button

	zoomInBtn := widget.NewButtonWithIcon("Zoom a selección", theme.ZoomInIcon(), func() {
		s := int(startSlider.Value*96000) &^ 1
		e := int(endSlider.Value*96000) &^ 1
		curTotalBytes := viewEndByte - viewStartByte
		if s < 0 {
			s = 0
		}
		if e > curTotalBytes {
			e = curTotalBytes
		}
		if e-s < 4800 { // al menos 50 ms
			return
		}
		zoomHistory = append(zoomHistory, zoomLevel{startByte: viewStartByte, endByte: viewEndByte})
		viewStartByte += s
		viewEndByte = viewStartByte + (e - s)
		newTotal := float64(viewEndByte-viewStartByte) / 96000.0
		durationLabel.SetText(fmt.Sprintf("Duración visible: %.2f s", newTotal))

		activePeaks = computeWaveformPeaks(pcm[viewStartByte:viewEndByte], imgW)

		isUpdating = true
		startSlider.Max = newTotal
		startSlider.SetValue(0)
		endSlider.Max = newTotal
		endSlider.SetValue(newTotal)
		startEntry.SetText("0.00")
		endEntry.SetText(fmt.Sprintf("%.2f", newTotal))
		isUpdating = false
		if zoomOutBtn != nil {
			zoomOutBtn.Enable()
		}
		updateWaveform()
	})

	zoomOutBtn = widget.NewButtonWithIcon("Alejar zoom", theme.ZoomOutIcon(), func() {
		if len(zoomHistory) == 0 {
			return
		}
		prev := zoomHistory[len(zoomHistory)-1]
		zoomHistory = zoomHistory[:len(zoomHistory)-1]
		viewStartByte = prev.startByte
		viewEndByte = prev.endByte
		prevTotal := float64(viewEndByte-viewStartByte) / 96000.0
		durationLabel.SetText(fmt.Sprintf("Duración visible: %.2f s", prevTotal))

		activePeaks = computeWaveformPeaks(pcm[viewStartByte:viewEndByte], imgW)

		isUpdating = true
		startSlider.Max = prevTotal
		startSlider.SetValue(0)
		endSlider.Max = prevTotal
		endSlider.SetValue(prevTotal)
		startEntry.SetText("0.00")
		endEntry.SetText(fmt.Sprintf("%.2f", prevTotal))
		isUpdating = false
		if len(zoomHistory) == 0 {
			zoomOutBtn.Disable()
		}
		updateWaveform()
	})
	zoomOutBtn.Disable()

	previewBtn := widget.NewButton("▶ Probar recorte", func() {
		trimmed := slicePCM()
		if len(trimmed) > 0 {
			a.playSoundPCM(trimmed)
		}
	})

	stopBtn := widget.NewButton("⏹ Detener", func() {
		a.stopSoundboard()
	})

	startRow := container.NewBorder(nil, nil, widget.NewLabel("Inicio:"), container.NewHBox(startEntryBox, widget.NewLabel("s")), startSlider)
	endRow := container.NewBorder(nil, nil, widget.NewLabel("Fin:   "), container.NewHBox(endEntryBox, widget.NewLabel("s")), endSlider)
	zoomToolbar := container.NewHBox(durationLabel, layout.NewSpacer(), zoomInBtn, zoomOutBtn)

	controls := container.NewVBox(
		widget.NewLabelWithStyle("Onda de audio (verde = área seleccionada):", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		waveformImg,
		zoomToolbar,
		widget.NewSeparator(),
		startRow,
		endRow,
		widget.NewSeparator(),
		widget.NewLabel("Nombre del sonido:"),
		nameEntry,
		container.NewGridWithColumns(2, previewBtn, stopBtn),
	)

	d := dialog.NewCustomConfirm("Recortar sonido", "Guardar sonido", "Cancelar", controls, func(confirmed bool) {
		a.stopSoundboard()
		if confirmed {
			soundName := strings.TrimSpace(nameEntry.Text)
			if soundName == "" {
				soundName = defaultName
			}
			trimmed := slicePCM()
			if len(trimmed) > 0 {
				a.saveAndBroadcastSound(soundName, trimmed)
			}
		}
		// Limpieza explícita de referencias grandes y liberación forzada de RAM al SO
		pcm = nil
		activePeaks = nil
		waveImg = nil
		zoomHistory = nil
		go func() {
			runtime.GC()
			debug.FreeOSMemory()
		}()
	}, a.window)
	d.Resize(fyne.NewSize(520, 480))
	d.Show()
}

func (a *App) saveAndBroadcastSound(name string, pcm []byte) {
	if a.historyStore == nil || a.transport == nil {
		return
	}
	a.setStatus("Guardando sonido: " + name + "...")
	go func() {
		sound := room.Sound{ID: history.NewID(a.participantID), Ext: ".pcm", Name: name}
		if _, saveErr := a.historyStore.SaveSound(history.SoundMeta{ID: sound.ID, Ext: sound.Ext, Name: sound.Name}, pcm); saveErr != nil {
			a.setStatus("No se pudo guardar el sonido: " + saveErr.Error())
			return
		}
		a.addSound(sound)
		payload, encodeErr := room.Encode(room.Envelope{Kind: room.SoundAddMessage, Sound: &sound})
		if encodeErr == nil {
			a.sendChatPayload(payload)
		}
		a.sendAsset(assets.KindSound, sound.ID, sound.Ext, pcm)
		a.setStatus("Sonido agregado: " + name)
		go func() {
			runtime.GC()
			debug.FreeOSMemory()
		}()
	}()
}

func (a *App) triggerSound(sound room.Sound) {
	go func() {
		if a.historyStore != nil {
			if data, readErr := a.historyStore.ReadSound(sound.ID, sound.Ext); readErr == nil {
				a.playSoundPCM(data)
			}
		}
		payload, err := room.Encode(room.Envelope{Kind: room.SoundPlayMessage, Sound: &sound})
		if err == nil {
			a.sendChatPayload(payload)
		}
	}()
}
