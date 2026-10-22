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
		a.updateConfig(func(c *config.Config) { c.SoundboardMuted = checked })
		a.saveConfig()
	})
	cfg := a.conf()
	muteCheck.SetChecked(cfg.SoundboardMuted)

	volVal := cfg.SoundboardVolume
	if volVal <= 0.01 {
		volVal = 1.0
	}
	volLabel := widget.NewLabel(fmt.Sprintf("Volumen: %d%%", int(volVal*100)))
	volSlider := widget.NewSlider(0, 1)
	volSlider.Step = 0.05
	volSlider.Value = float64(volVal)
	volSlider.OnChanged = func(val float64) {
		a.updateConfig(func(c *config.Config) { c.SoundboardVolume = float32(val) })
		volLabel.SetText(fmt.Sprintf("Volumen: %d%%", int(val*100)))
		a.saveConfig()
	}

	stopButton := widget.NewButton("⏹ Detener", a.stopSoundboard)
	stopBindText := "⌨ Bind"
	if cfg.StopSoundBind != "" {
		stopBindText = "⌨ " + cfg.StopSoundBind
	}
	a.stopBindBtn = widget.NewButton(stopBindText, a.showStopSoundBindDialog)
	a.stopBindBtn.Importance = widget.LowImportance
	stopBindWrap := container.NewGridWrap(fyne.NewSize(96, 34), a.stopBindBtn)
	delSpacer := container.NewGridWrap(fyne.NewSize(34, 34), layout.NewSpacer())
	stopActions := container.NewHBox(stopBindWrap, delSpacer)
	stopRow := container.NewBorder(nil, nil, nil, stopActions, stopButton)

	searchEntry := widget.NewEntry()
	searchEntry.SetPlaceHolder("Buscar sonido...")
	clearSearch := widget.NewButtonWithIcon("", theme.CancelIcon(), func() { searchEntry.SetText("") })
	clearSearch.Importance = widget.LowImportance
	searchEntry.ActionItem = clearSearch
	searchEntry.OnChanged = func(text string) {
		filter := strings.ToLower(strings.TrimSpace(text))
		a.soundFilter.Store(&filter)
		a.rebuildSoundboard()
	}

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
		searchEntry,
		widget.NewSeparator(),
	)
	soundContent := container.NewBorder(soundHeader, addButton, nil, nil, soundScroll)
	a.soundPanel = uiCard(soundContent)
	a.rebuildSoundboard()
}

func (a *App) soundFilterText() string {
	if filter := a.soundFilter.Load(); filter != nil {
		return *filter
	}
	return ""
}

// rebuildSoundboard arma las filas (se puede llamar desde cualquier goroutine: los widgets nuevos
// todavía no están en el árbol) y las cambia dentro de fyne.Do.
func (a *App) rebuildSoundboard() {
	a.soundMu.RLock()
	sounds := append([]room.Sound(nil), a.sounds...)
	a.soundMu.RUnlock()
	binds := a.conf().SoundBinds // instantánea: el mapa publicado no se muta nunca
	filter := a.soundFilterText()
	objects := make([]fyne.CanvasObject, 0, len(sounds))
	for _, sound := range sounds {
		if filter != "" && !strings.Contains(strings.ToLower(sound.Name), filter) {
			continue
		}
		captured := sound
		displayName := captured.Name
		runes := []rune(displayName)
		if len(runes) > 13 {
			displayName = string(runes[:11]) + "…"
		}
		btnPlay := widget.NewButton(displayName, func() { a.triggerSound(captured) })
		btnEdit := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() { a.showEditSoundDialog(captured) })

		bindText := binds[captured.ID]
		bindLabel := "⌨ Bind"
		if bindText != "" {
			bindLabel = "⌨ " + bindText
		}
		btnBind := widget.NewButton(bindLabel, func() { a.showSoundBindDialog(captured) })
		btnBind.Importance = widget.LowImportance

		bindWrap := container.NewGridWrap(fyne.NewSize(96, 34), btnBind)
		editWrap := container.NewGridWrap(fyne.NewSize(34, 34), btnEdit)
		actions := container.NewHBox(bindWrap, editWrap)
		row := container.NewBorder(nil, nil, nil, actions, btnPlay)
		objects = append(objects, row)
	}
	if len(objects) == 0 && filter != "" && len(sounds) > 0 {
		objects = append(objects, widget.NewLabelWithStyle("Sin resultados", fyne.TextAlignCenter, fyne.TextStyle{Italic: true}))
	}
	fyne.Do(func() {
		if a.soundGrid == nil {
			return
		}
		// Una reconstrucción armada con un filtro viejo (desde otra goroutine) no pisa a la del
		// filtro actual, que ya está encolada detrás.
		if a.soundFilterText() != filter {
			return
		}
		a.soundGrid.Objects = objects
		a.soundGrid.Refresh()
	})
}

// showEditSoundDialog corre en el hilo de UI (callback del botón de la fila).
func (a *App) showEditSoundDialog(sound room.Sound) {
	nameEntry := widget.NewEntry()
	nameEntry.SetText(sound.Name)

	var editDialog *dialog.CustomDialog
	save := func() {
		name := strings.TrimSpace(nameEntry.Text)
		editDialog.Hide()
		if name != "" && name != sound.Name {
			go a.renameSound(sound.ID, name)
		}
	}
	nameEntry.OnSubmitted = func(string) { save() }

	replaceBtn := widget.NewButtonWithIcon("Cambiar audio", theme.FolderOpenIcon(), func() {
		name := strings.TrimSpace(nameEntry.Text)
		if name == "" {
			name = sound.Name
		}
		editDialog.Hide()
		a.pickSoundFile(func(_ string, pcm []byte, peaks []int16) {
			a.showSoundTrimDialog("Reemplazar audio", name, pcm, peaks, func(newName string, trimmed []byte) {
				a.replaceSoundAudio(sound, newName, trimmed)
			})
		})
	})
	if a.historyStore == nil || !a.connected() {
		replaceBtn.Disable()
	}

	deleteBtn := widget.NewButtonWithIcon("Eliminar", theme.DeleteIcon(), func() {
		confirm := dialog.NewConfirm("Eliminar \""+sound.Name+"\"", "¿Estás seguro de eliminar este sonido?", func(ok bool) {
			if !ok {
				return
			}
			editDialog.Hide()
			// removeSound borra el archivo y avisa a la sala: fuera del hilo de UI.
			go a.removeSound(sound.ID)
		}, a.window)
		confirm.SetConfirmText("Eliminar")
		confirm.SetDismissText("Cancelar")
		confirm.SetConfirmImportance(widget.DangerImportance)
		confirm.Show()
	})
	deleteBtn.Importance = widget.DangerImportance

	content := container.NewVBox(
		widget.NewLabel("Nombre:"),
		nameEntry,
		widget.NewSeparator(),
		container.NewGridWithColumns(2, replaceBtn, deleteBtn),
	)
	editDialog = dialog.NewCustom("Editar sonido", "Cancelar", content, a.window)
	cancelBtn := widget.NewButtonWithIcon("Cancelar", theme.CancelIcon(), editDialog.Hide)
	saveBtn := widget.NewButtonWithIcon("Guardar", theme.ConfirmIcon(), save)
	saveBtn.Importance = widget.HighImportance
	editDialog.SetButtons([]fyne.CanvasObject{cancelBtn, saveBtn})
	editDialog.Resize(fyne.NewSize(380, editDialog.MinSize().Height))
	editDialog.Show()
	a.window.Canvas().Focus(nameEntry)
}

// renameSound cambia solo el nombre: SoundBinds está indexado por ID, así que el atajo no se toca.
func (a *App) renameSound(soundID, name string) {
	a.soundMu.Lock()
	var renamed room.Sound
	found := false
	for index := range a.sounds {
		if a.sounds[index].ID == soundID {
			a.sounds[index].Name = name
			renamed = a.sounds[index]
			found = true
			break
		}
	}
	a.soundMu.Unlock()
	if !found {
		return
	}
	a.rebuildSoundboard()
	if a.historyStore != nil {
		// Sin el audio en disco (todavía no se tocó) no hay manifest que actualizar.
		if err := a.historyStore.UpdateSoundMeta(history.SoundMeta{ID: renamed.ID, Ext: renamed.Ext, Name: renamed.Name}); err != nil && !os.IsNotExist(err) {
			logging.Errorf("renombrar sonido %q: %s", renamed.ID, logging.FormatError(err))
		}
	}
	// sound_add con un ID existente reemplaza la entrada en addSound (también en versiones viejas).
	a.sendSoundEnvelope(room.SoundAddMessage, renamed)
	a.setStatus("Sonido renombrado: " + name)
}

// replaceSoundAudio guarda el audio nuevo con otro ID (los demás ya tienen cacheado el asset del
// ID viejo y no lo volverían a pedir), lo pone en el mismo lugar de la lista y le pasa el atajo.
func (a *App) replaceSoundAudio(old room.Sound, name string, pcm []byte) {
	if a.historyStore == nil || !a.connected() {
		dialog.ShowInformation("Cambiar audio", "Conecta a una sala antes de cambiar el audio.", a.window)
		return
	}
	a.setStatus("Reemplazando audio: " + name + "...")
	go func() {
		sound := room.Sound{ID: history.NewID(a.participantID), Ext: ".pcm", Name: name}
		if _, saveErr := a.historyStore.SaveSound(history.SoundMeta{ID: sound.ID, Ext: sound.Ext, Name: sound.Name}, pcm); saveErr != nil {
			a.showSoundError("No se pudo guardar el sonido: " + saveErr.Error())
			return
		}
		a.soundMu.Lock()
		replaced := false
		for index := range a.sounds {
			if a.sounds[index].ID == old.ID {
				a.sounds[index] = sound
				replaced = true
				break
			}
		}
		if !replaced { // lo borraron mientras se recortaba
			a.sounds = append(a.sounds, sound)
		}
		a.soundMu.Unlock()

		if _, bound := a.conf().SoundBinds[old.ID]; bound {
			// Sobre el clon de updateConfig: la closure puede correr dos veces, por eso relee binds.
			a.updateSoundBinds(func(binds map[string]string) {
				if key, ok := binds[old.ID]; ok {
					binds[sound.ID] = key
					delete(binds, old.ID)
				}
			})
		}
		a.rebuildSoundboard()

		if err := a.historyStore.DeleteSound(old.ID, old.Ext); err != nil && !os.IsNotExist(err) {
			logging.Errorf("borrar audio reemplazado %q: %s", old.ID, logging.FormatError(err))
		}
		a.sendSoundEnvelope(room.SoundAddMessage, sound)
		a.sendAsset(assets.KindSound, sound.ID, sound.Ext, pcm)
		a.sendSoundEnvelope(room.SoundRemoveMessage, old)
		a.setStatus("Audio reemplazado: " + name)
		go func() {
			runtime.GC()
			debug.FreeOSMemory()
		}()
	}()
}

func (a *App) sendSoundEnvelope(kind string, sound room.Sound) {
	if !a.connected() {
		return
	}
	payload, err := room.Encode(room.Envelope{Kind: kind, Sound: &sound})
	if err == nil {
		a.sendChatPayload(payload)
	}
}

// showSoundError se puede llamar desde cualquier goroutine. Va a un diálogo y no solo a setStatus:
// la barra de estado puede estar oculta (ShowStatusBar) y el error pasaba desapercibido.
func (a *App) showSoundError(text string) {
	logging.Errorf("soundboard: %s", text)
	a.setStatus(text)
	fyne.Do(func() {
		dialog.ShowInformation("Sonidos", text, a.window)
	})
}

// updateSoundBinds cambia cfg.SoundBinds sobre un clon de la configuración (updateConfig ya
// copia el mapa), publica los atajos y pide guardar. El mapa de una instantánea publicada se lee
// desde varias goroutines (hotkeys, rebuildSoundboard, config.Save) y removeSound llega también
// desde receiveLoop: mutarlo en sitio es un "concurrent map read and map write", que el runtime
// mata sin pasar por recover (y con -H windowsgui sin dejar rastro).
func (a *App) updateSoundBinds(mutate func(binds map[string]string)) {
	a.updateConfig(func(c *config.Config) {
		if c.SoundBinds == nil {
			c.SoundBinds = make(map[string]string)
		}
		mutate(c.SoundBinds)
	})
	a.publishHotkeys()
	a.saveConfig()
}

func (a *App) addSound(sound room.Sound) {
	a.soundMu.Lock()
	for index, existing := range a.sounds {
		if existing.ID == sound.ID {
			a.sounds[index] = sound
			a.soundMu.Unlock()
			if existing.Name != sound.Name && a.historyStore != nil {
				// Renombrado por otro peer (renameSound). Si el asset todavía no llegó, falla el Stat y
				// handleAssetChunk lo guarda después con el nombre nuevo de a.sounds.
				go func() {
					_ = a.historyStore.UpdateSoundMeta(history.SoundMeta{ID: sound.ID, Ext: sound.Ext, Name: sound.Name})
				}()
			}
			a.rebuildSoundboard()
			return
		}
	}
	a.sounds = append(a.sounds, sound)
	a.soundMu.Unlock()
	a.rebuildSoundboard()
}

// removeSound borra un sonido por pedido del usuario y avisa a la sala. Sin conexión queda la
// lápida (DeleteSound) y el próximo sync de la botonera lo borra en el resto.
func (a *App) removeSound(soundID string) {
	removed := a.dropSounds([]string{soundID})
	if len(removed) == 0 {
		return
	}
	a.sendSoundEnvelope(room.SoundRemoveMessage, removed[0])
	a.setStatus("Sonido eliminado: " + removed[0].Name)
}

// pickAndAddSound corre en el hilo de UI (callback de "Agregar sonido").
func (a *App) pickAndAddSound() {
	if a.historyStore == nil || !a.connected() {
		dialog.ShowInformation("Agregar sonido", "Conecta a una sala antes de agregar un sonido.", a.window)
		return
	}
	a.pickSoundFile(func(name string, pcm []byte, peaks []int16) {
		a.showSoundTrimDialog("Recortar sonido", name, pcm, peaks, a.saveAndBroadcastSound)
	})
}

// pickSoundFile se llama en el hilo de UI: el HWND se lee acá; el diálogo nativo, la lectura y el
// decode van en una goroutine. onLoaded corre de nuevo en el hilo de UI (dentro de fyne.Do) con el
// nombre del archivo, el PCM y los picos de la onda ya calculados.
func (a *App) pickSoundFile(onLoaded func(name string, pcm []byte, peaks []int16)) {
	// Entre el clic y que aparezca el diálogo modal se puede volver a clickear: un solo selector.
	if !a.soundPicking.CompareAndSwap(false, true) {
		return
	}
	owner := a.dialogOwner()
	go func() {
		defer a.soundPicking.Store(false)
		defer func() {
			if value := recover(); value != nil {
				logging.Panic(value)
				a.showSoundError("No se pudo abrir el selector de sonidos.")
			}
		}()
		path, err := selectSoundFile(owner)
		if err != nil {
			if err.Error() != "selección cancelada" {
				a.showSoundError("No se pudo abrir el selector de sonidos: " + err.Error())
			}
			return
		}
		a.setStatus("Cargando y procesando audio...")
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			a.showSoundError("No se pudo leer el sonido: " + readErr.Error())
			return
		}
		extension := strings.ToLower(filepath.Ext(path))
		pcm, decodeErr := audio.DecodeSoundFile(extension, raw)
		raw = nil // Liberar memoria del archivo original de inmediato
		if decodeErr != nil {
			a.showSoundError("No se pudo procesar el sonido: " + decodeErr.Error())
			go func() {
				runtime.GC()
				debug.FreeOSMemory()
			}()
			return
		}
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		// Recorre todo el PCM (millones de muestras en un MP3 largo): se calcula acá, no en el hilo de UI.
		peaks := computeWaveformPeaks(pcm, trimWaveWidth)
		a.setStatus("Audio cargado: " + name)
		fyne.Do(func() {
			onLoaded(name, pcm, peaks)
		})
	}()
}

const trimWaveWidth, trimWaveHeight = 420, 80

// maxSoundSec es lo más largo que se puede mandar a la sala: assets.Split rechaza más de
// assets.MaxBytes (8 MiB = ~87 s de PCM 48 kHz mono 16 bit). Antes el sonido se guardaba local,
// el envío fallaba y "Sonido agregado" tapaba el error.
const maxSoundSec = float64(assets.MaxBytes) / 96000.0

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

// showSoundTrimDialog corre en el hilo de UI; initialPeaks llega precalculado para todo el PCM.
// onSave recibe el nombre y el recorte (ya copiado) en el hilo de UI.
func (a *App) showSoundTrimDialog(title, defaultName string, pcm []byte, initialPeaks []int16, onSave func(name string, pcm []byte)) {
	totalSec := float64(len(pcm)) / 96000.0
	if totalSec <= 0.1 {
		onSave(defaultName, pcm)
		return
	}
	// Un archivo más largo que el máximo arranca con la selección ya recortada al límite.
	initialEnd := math.Min(totalSec, maxSoundSec)

	const imgW, imgH = trimWaveWidth, trimWaveHeight
	waveImg := image.NewRGBA(image.Rect(0, 0, imgW, imgH))

	viewStartByte := 0
	viewEndByte := len(pcm)

	type zoomLevel struct {
		startByte int
		endByte   int
	}
	var zoomHistory []zoomLevel

	activePeaks := initialPeaks
	drawWaveform(waveImg, activePeaks, 0, initialEnd/totalSec)

	nameEntry := widget.NewEntry()
	nameEntry.SetText(defaultName)

	durationLabel := widget.NewLabel(fmt.Sprintf("Duración visible: %.2f s", totalSec))

	waveformImg := canvas.NewImageFromImage(waveImg)
	waveformImg.SetMinSize(fyne.NewSize(imgW, imgH))
	waveformImg.FillMode = canvas.ImageFillStretch

	startSlider := widget.NewSlider(0, totalSec)
	startSlider.SetValue(0)

	endSlider := widget.NewSlider(0, totalSec)
	endSlider.SetValue(initialEnd)

	startEntry := widget.NewEntry()
	startEntry.SetText("0.00")
	startEntryBox := container.NewGridWrap(fyne.NewSize(75, 36), startEntry)

	endEntry := widget.NewEntry()
	endEntry.SetText(fmt.Sprintf("%.2f", initialEnd))
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
		widget.NewLabel(fmt.Sprintf("Máximo %.0f s por sonido.", math.Floor(maxSoundSec))),
		widget.NewLabel("Nombre del sonido:"),
		nameEntry,
		container.NewGridWithColumns(2, previewBtn, stopBtn),
	)

	var d *dialog.ConfirmDialog
	d = dialog.NewCustomConfirm(title, "Guardar sonido", "Cancelar", controls, func(confirmed bool) {
		a.stopSoundboard()
		if confirmed {
			soundName := strings.TrimSpace(nameEntry.Text)
			if soundName == "" {
				soundName = defaultName
			}
			trimmed := slicePCM()
			if len(trimmed) > assets.MaxBytes {
				// No se podría mandar a la sala: se vuelve al recorte (sin liberar el PCM).
				d.Show()
				dialog.ShowInformation("Recorte demasiado largo",
					fmt.Sprintf("La selección dura %.1f s y el máximo es %.0f s.\nAcorta el recorte.", float64(len(trimmed))/96000.0, math.Floor(maxSoundSec)),
					a.window)
				return
			}
			if len(trimmed) > 0 {
				onSave(soundName, trimmed)
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
	if a.historyStore == nil || !a.connected() {
		dialog.ShowInformation("Agregar sonido", "Conecta a una sala antes de agregar un sonido.", a.window)
		return
	}
	a.setStatus("Guardando sonido: " + name + "...")
	go func() {
		sound := room.Sound{ID: history.NewID(a.participantID), Ext: ".pcm", Name: name}
		if _, saveErr := a.historyStore.SaveSound(history.SoundMeta{ID: sound.ID, Ext: sound.Ext, Name: sound.Name}, pcm); saveErr != nil {
			a.showSoundError("No se pudo guardar el sonido: " + saveErr.Error())
			return
		}
		a.addSound(sound)
		a.sendSoundEnvelope(room.SoundAddMessage, sound)
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
		if a.playOrFetchSound(sound) {
			a.setStatus("Descargando sonido: " + sound.Name + "...")
		}
		payload, err := room.Encode(room.Envelope{Kind: room.SoundPlayMessage, Sound: &sound})
		if err == nil {
			a.sendChatPayload(payload)
		}
	}()
}
