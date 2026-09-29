//go:build cgo

package ui

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/atotto/clipboard"

	"voxmesh/internal/assets"
	"voxmesh/internal/audio"
	"voxmesh/internal/config"
	"voxmesh/internal/history"
	"voxmesh/internal/logging"
	"voxmesh/internal/netinfo"
	"voxmesh/internal/phonemic"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
	"voxmesh/internal/version"
)

type App struct {
	window          fyne.Window
	cfg             config.Config
	cfgPath         string
	transport       *transport.UDP
	sequence        atomic.Uint32
	endpoint        string
	status          *widget.Label
	peerHBox        *fyne.Container
	hostPanel       *fyne.Container
	createButton    *widget.Button
	stopButton      *widget.Button
	connectButton   *widget.Button
	leaveButton     *widget.Button
	topHangupBtn    *widget.Button
	endpointEntry   *widget.Entry
	peerNames       []peerView
	peerMu          sync.RWMutex
	recordingMu     sync.RWMutex
	recordedInput   []byte
	pingStop        chan struct{}
	voiceProcessor  *audio.RNNoise
	effects         *audio.Effects
	outputEffects   *audio.Effects
	audioEngine     *audio.Engine
	hostMode        atomic.Bool
	lastHostSeen    atomic.Int64
	monitorStop     chan struct{}
	roomStore       *room.Store
	historyStore    *history.Store
	roomState       room.State
	participantID   string
	chatScroll      *container.Scroll
	chatBox         *fyne.Container
	chatInput       *widget.Entry
	chatSearch      *widget.Entry
	chatSearchCount *widget.Label
	chatMessages    []history.Message
	chatMu          sync.RWMutex
	selfNameLabel   *widget.Label
	voiceIndicator  *canvas.Circle
	voiceTimerMu    sync.Mutex
	voiceTimer      *time.Timer
	voiceActive     atomic.Bool
	audioSeqMu      sync.Mutex
	peerAudioSeq    map[string]uint32
	peerAudioTime   map[string]time.Time
	phoneMicMu      sync.RWMutex
	phoneMic        *phonemic.Server
	previewMu       sync.RWMutex
	previewPlayer   *audio.Player
	previewEngine   *audio.Engine
	mainView        fyne.CanvasObject
	settingsStatus  *widget.Label
	assetAssembler  *assets.Assembler
	clientPanel     *fyne.Container
	connectionPanel *fyne.Container
	bottomBar       *fyne.Container
	roomSelect      *widget.Select
	activeRoomName  string
	viewingRoomName string
	activityBox     *fyne.Container
	activityScroll  *container.Scroll
	stagedBar       *fyne.Container
	stagedLabel     *widget.Label
	stagedExt       string
	stagedData      []byte
	stagedName      string
	soundPanel      *fyne.Container
	soundGrid       *fyne.Container
	soundPlayer     *audio.Player
	stopBindBtn     *widget.Button
	soundMu         sync.RWMutex
	sounds          []room.Sound
	pendingPlays    map[string]struct{}
}

type peerView struct {
	id        string
	name      string
	address   string
	lag       time.Duration
	speaking  bool
	speakStop time.Time
	isSelf    bool
}

const phoneMicInput = "Micrófono del celular"

func New(executable string) *App {
	cfgPath := config.Path(executable)
	cfg, _ := config.Load(cfgPath)
	voiceProcessor, _ := audio.NewRNNoise()
	return &App{
		cfg:            cfg,
		cfgPath:        cfgPath,
		voiceProcessor: voiceProcessor,
		effects:        &audio.Effects{},
		outputEffects:  &audio.Effects{},
		assetAssembler: assets.NewAssembler(),
		peerAudioSeq:   make(map[string]uint32),
		peerAudioTime:  make(map[string]time.Time),
	}
}

func (a *App) setStatus(text string) {
	logging.Infof("%s", text)
	if a.status == nil && a.settingsStatus == nil {
		return
	}
	fyne.Do(func() {
		if a.status != nil {
			a.status.SetText(text)
		}
		if a.settingsStatus != nil {
			a.settingsStatus.SetText(text)
		}
	})
}

func (a *App) flashVoiceIndicator() {
	wasActive := a.voiceActive.Swap(true)
	if !wasActive && a.voiceIndicator != nil {
		fyne.Do(func() {
			a.voiceIndicator.FillColor = color.NRGBA{R: 46, G: 204, B: 113, A: 255}
			a.voiceIndicator.Refresh()
		})
	}
	a.markPeerSpeaking(a.participantID, "")
	a.voiceTimerMu.Lock()
	if a.voiceTimer != nil {
		a.voiceTimer.Stop()
	}
	a.voiceTimer = time.AfterFunc(350*time.Millisecond, func() {
		a.voiceTimerMu.Lock()
		a.voiceActive.Store(false)
		a.voiceTimerMu.Unlock()
		if a.voiceIndicator != nil {
			fyne.Do(func() {
				a.voiceIndicator.FillColor = color.NRGBA{R: 100, G: 110, B: 120, A: 255}
				a.voiceIndicator.Refresh()
			})
		}
		a.sweepSpeakingPeers()
	})
	a.voiceTimerMu.Unlock()
}

func (a *App) isAudioSequenceValid(senderID string, seq uint32) bool {
	a.audioSeqMu.Lock()
	defer a.audioSeqMu.Unlock()

	lastSeq, exists := a.peerAudioSeq[senderID]
	lastTime := a.peerAudioTime[senderID]
	now := time.Now()

	if !exists || now.Sub(lastTime) > 800*time.Millisecond {
		a.peerAudioSeq[senderID] = seq
		a.peerAudioTime[senderID] = now
		return true
	}

	diff := int32(seq - lastSeq)
	if diff <= 0 {
		return false // Discard duplicate or out-of-order late arrival
	}

	a.peerAudioSeq[senderID] = seq
	a.peerAudioTime[senderID] = now
	return true
}

func (a *App) Run() {
	_ = logging.Start()
	logging.Infof("VoxMesh %s iniciado", version.Value)
	defer func() {
		a.stopPhoneMic()
		a.stopPreviewAudio()
		if value := recover(); value != nil {
			logging.Panic(value)
		}
		logging.Close()
	}()
	application := app.NewWithID("com.voxmesh.desktop")
	a.window = application.NewWindow("VoxMesh")
	a.window.Resize(fyne.NewSize(900, 620))
	a.window.SetCloseIntercept(func() {
		if a.transport != nil {
			dialog.ShowConfirm("Salir de VoxMesh", "Estás en una sala activa. ¿Deseas salir y cerrar VoxMesh?", func(confirmed bool) {
				if confirmed {
					a.stopConnection()
					a.window.Close()
				}
			}, a.window)
		} else {
			a.window.Close()
		}
	})
	a.window.SetContent(a.content())
	a.refreshRoomList("")
	if a.cfg.InputDevice == phoneMicInput {
		if _, err := a.ensurePhoneMicServer(); err != nil {
			logging.Errorf("iniciar micrófono de celular: %s", logging.FormatError(err))
			a.setStatus("No se pudo iniciar el servidor del celular: " + err.Error())
		}
	}
	a.startHotkeyListener()
	a.window.ShowAndRun()
}

func newScrollableEntry() *widget.Entry {
	e := widget.NewEntry()
	e.Wrapping = fyne.TextWrapOff
	e.Scroll = fyne.ScrollNone
	return e
}

type chatInputEntry struct {
	widget.Entry
	app *App
}

func newChatInputEntry(app *App) *chatInputEntry {
	e := &chatInputEntry{app: app}
	e.Wrapping = fyne.TextWrapOff
	e.Scroll = fyne.ScrollNone
	e.ExtendBaseWidget(e)
	return e
}

func (e *chatInputEntry) TypedShortcut(s fyne.Shortcut) {
	if _, ok := s.(*fyne.ShortcutPaste); ok {
		if e.app != nil && e.app.tryPasteImage() {
			return
		}
	}
	e.Entry.TypedShortcut(s)
}

var urlRegex = regexp.MustCompile(`https?://[^\s<>"'()]+`)

func (a *App) content() fyne.CanvasObject {
	a.status = widget.NewLabel("Listo. Crea una sala o conecta con un host.")
	buildVersion := widget.NewLabel(version.Value)
	openLogs := widget.NewButton("📄", func() {
		if err := openLogsDirectory(); err != nil {
			a.setStatus("No se pudo abrir la carpeta de logs: " + err.Error())
		}
	})
	endpoint := newScrollableEntry()
	endpoint.SetPlaceHolder("IP del host (puerto por defecto 47830)")
	if a.cfg.LastPeer != "" {
		endpoint.SetText(a.cfg.LastPeer)
	}
	endpoint.OnSubmitted = func(text string) { a.connectClient(text) }
	a.endpointEntry = endpoint
	a.connectButton = widget.NewButton("Conectar", func() { a.connectClient(endpoint.Text) })
	a.leaveButton = widget.NewButton("Salir de la sala", a.stopConnection)
	a.leaveButton.Disable()
	a.createButton = widget.NewButton("Iniciar sala", a.createHost)
	a.stopButton = widget.NewButton("Parar sala", a.stopConnection)
	a.stopButton.Disable()
	copy := widget.NewButton("Copiar dirección", func() {
		if a.endpoint == "" {
			a.setStatus("Primero crea una sala.")
			return
		}
		if err := clipboard.WriteAll(a.endpoint); err != nil {
			a.setStatus("No se pudo copiar: " + err.Error())
			return
		}
		a.setStatus("Dirección copiada: " + a.endpoint)
	})

	chatInput := newChatInputEntry(a)
	chatInput.SetPlaceHolder("Escribe un mensaje...")
	chatInput.OnSubmitted = func(text string) { a.submitChat() }
	a.chatInput = &chatInput.Entry

	sendChat := widget.NewButton("Enviar", a.submitChat)
	attachImage := widget.NewButton("📎", a.selectChatImage)

	a.stagedLabel = widget.NewLabel("")
	clearStagedBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), a.clearStagedImage)
	clearStagedBtn.Importance = widget.LowImportance
	a.stagedBar = container.NewBorder(nil, nil, a.stagedLabel, clearStagedBtn)
	a.stagedBar.Hide()

	composerInput := container.NewVBox(a.stagedBar, chatInput)
	chatComposer := container.NewBorder(nil, nil, attachImage, sendChat, composerInput)

	a.chatBox = container.NewVBox()
	a.chatScroll = container.NewVScroll(a.chatBox)
	a.roomSelect = widget.NewSelect(nil, func(selected string) { a.viewRoom(selected) })
	a.roomSelect.PlaceHolder = "Ver otra sala..."

	hostContent := container.NewVBox(
		widget.NewLabelWithStyle("Host", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		a.createButton,
		a.stopButton,
		copy,
	)
	a.hostPanel = uiCard(hostContent)

	clientContent := container.NewVBox(
		widget.NewLabelWithStyle("Cliente", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		endpoint,
		a.connectButton,
		a.leaveButton,
	)
	a.clientPanel = uiCard(clientContent)

	topControls := container.NewVBox(a.hostPanel, a.clientPanel)

	a.activityBox = container.NewVBox()
	a.activityScroll = container.NewVScroll(a.activityBox)
	a.activityScroll.SetMinSize(fyne.NewSize(180, 120))
	clearLogsBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
		fyne.Do(func() {
			if a.activityBox != nil {
				a.activityBox.Objects = nil
				a.activityBox.Refresh()
			}
		})
	})
	clearLogsBtn.Importance = widget.LowImportance
	activityHeader := container.NewBorder(nil, nil, widget.NewLabelWithStyle("Registro de actividad", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), clearLogsBtn)
	activityCard := uiCard(container.NewBorder(activityHeader, nil, nil, nil, a.activityScroll))

	a.connectionPanel = container.NewBorder(topControls, nil, nil, nil, activityCard)

	a.peerHBox = container.NewHBox()
	a.refreshPeerAvatars()
	peerLabel := widget.NewLabelWithStyle("En sala:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	peerScroll := container.NewHScroll(a.peerHBox)
	peerScroll.SetMinSize(fyne.NewSize(120, 32))
	peersRow := container.NewBorder(nil, nil, peerLabel, nil, peerScroll)
	statusCard := uiCard(peersRow)

	a.chatSearch = newScrollableEntry()
	a.chatSearch.SetPlaceHolder("🔍 Buscar en el chat...")
	a.chatSearch.OnChanged = func(string) { a.refreshChat() }
	a.chatSearchCount = widget.NewLabel("")
	clearSearch := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		a.chatSearch.SetText("")
	})
	searchBar := container.NewBorder(nil, nil, nil, container.NewHBox(a.chatSearchCount, clearSearch), a.chatSearch)
	chatTop := container.NewBorder(nil, nil, widget.NewLabelWithStyle("Chat de la sala", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), nil, a.roomSelect)
	chatHeader := container.NewVBox(chatTop, searchBar)

	chatCard := uiCard(container.NewBorder(chatHeader, chatComposer, nil, nil, a.chatScroll))

	a.buildSoundPanel()
	toggleRoomsBtn := widget.NewButtonWithIcon("", theme.MenuIcon(), a.toggleConnectionPanels)
	toggleRoomsBtn.Importance = widget.LowImportance

	hangupRedIcon := fyne.NewStaticResource("call_end.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24"><path fill="#e74c3c" d="M12 9c-2.3 0-4.47.58-6.37 1.62-.43.23-.71.69-.71 1.18 0 .42.19.82.52 1.09l2.4 1.95c.34.28.81.33 1.19.13 1.11-.58 2.34-.97 3.65-.97s2.54.39 3.65.97c.38.2.85.15 1.19-.13l2.4-1.95c.33-.27.52-.67.52-1.09 0-.49-.28-.95-.71-1.18C16.47 9.58 14.3 9 12 9z"/></svg>`))
	a.topHangupBtn = widget.NewButtonWithIcon("", hangupRedIcon, a.stopConnection)
	a.topHangupBtn.Importance = widget.LowImportance
	a.topHangupBtn.Disable()

	title := widget.NewLabelWithStyle("VoxMesh", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	settingsBtn := widget.NewButtonWithIcon("", theme.SettingsIcon(), a.showSettings)
	settingsBtn.Importance = widget.LowImportance

	leftTop := container.NewHBox(toggleRoomsBtn, a.topHangupBtn, title)
	topBar := container.NewBorder(nil, nil, leftTop, settingsBtn, nil)
	centerPanel := container.NewBorder(statusCard, nil, nil, nil, chatCard)
	main := container.NewBorder(nil, nil, a.connectionPanel, a.soundPanel, centerPanel)
	a.bottomBar = container.NewBorder(nil, nil, a.status, container.NewHBox(buildVersion, openLogs), nil)
	if !a.cfg.ShowStatusBar {
		a.bottomBar.Hide()
	}
	a.mainView = container.NewBorder(topBar, a.bottomBar, nil, nil, main)
	return a.mainView
}

func uiCard(content fyne.CanvasObject) *fyne.Container {
	background := canvas.NewRectangle(color.NRGBA{R: 32, G: 36, B: 43, A: 255})
	background.CornerRadius = 6
	card := container.NewStack(background, container.NewPadded(content))
	return container.NewPadded(card)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (a *App) toggleHostPanel() {
	a.toggleConnectionPanels()
}

func (a *App) hideConnectionPanels() {
	if a.hostPanel != nil {
		a.hostPanel.Hide()
	}
	if a.clientPanel != nil {
		a.clientPanel.Hide()
	}
	if a.connectionPanel != nil {
		a.connectionPanel.Hide()
	}
}

func (a *App) showConnectionPanels() {
	if a.hostPanel != nil {
		a.hostPanel.Show()
	}
	if a.clientPanel != nil {
		a.clientPanel.Show()
	}
	if a.connectionPanel != nil {
		a.connectionPanel.Show()
	}
}

func (a *App) toggleConnectionPanels() {
	if (a.hostPanel != nil && a.hostPanel.Visible()) || (a.clientPanel != nil && a.clientPanel.Visible()) || (a.connectionPanel != nil && a.connectionPanel.Visible()) {
		a.hideConnectionPanels()
	} else {
		a.showConnectionPanels()
	}
}

func (a *App) stageImage(ext string, data []byte, name string) {
	if len(data) > assets.MaxBytes {
		a.setStatus(fmt.Sprintf("La imagen supera el límite de %d MiB.", assets.MaxBytes/1024/1024))
		return
	}
	a.stagedExt = ext
	a.stagedData = data
	a.stagedName = name
	kb := len(data) / 1024
	fyne.Do(func() {
		if a.stagedLabel != nil {
			a.stagedLabel.SetText(fmt.Sprintf("📷 Imagen lista para enviar: %s (%d KB)", name, kb))
		}
		if a.stagedBar != nil {
			a.stagedBar.Show()
		}
	})
	a.setStatus(fmt.Sprintf("Imagen adjuntada (%s, %d KB). Presiona Enviar para transmitirla.", name, kb))
}

func (a *App) clearStagedImage() {
	a.stagedExt = ""
	a.stagedData = nil
	a.stagedName = ""
	fyne.Do(func() {
		if a.stagedBar != nil {
			a.stagedBar.Hide()
		}
	})
}

func (a *App) tryPasteImage() bool {
	ext, data, name, err := readClipboardImage()
	if err != nil || len(data) == 0 {
		return false
	}
	a.stageImage(ext, data, name)
	return true
}

func (a *App) submitChat() {
	var txt string
	if a.chatInput != nil {
		txt = a.chatInput.Text
	}
	hasText := strings.TrimSpace(txt) != ""
	hasImage := len(a.stagedData) > 0

	if !hasText && !hasImage {
		return
	}

	if a.transport == nil {
		a.setStatus("Conecta a una sala antes de enviar un mensaje.")
		return
	}

	if a.chatInput != nil {
		a.chatInput.SetText("")
	}

	if hasImage {
		ext := a.stagedExt
		data := a.stagedData
		a.clearStagedImage()
		go a.sendChatImageBytes(ext, data, txt)
		return
	}

	if hasText {
		a.sendChatMessage(txt)
	}
}

func (a *App) sendChatImageBytes(ext string, data []byte, caption string) {
	if a.historyStore == nil || a.transport == nil {
		return
	}
	imageID := history.NewID(a.participantID)
	if ext == "" {
		ext = ".png"
	}
	filePath, saveErr := a.historyStore.SaveImage(imageID, ext, data)
	if saveErr != nil {
		logging.Errorf("guardar imagen %q: %s", imageID, logging.FormatError(saveErr))
		a.setStatus("No se pudo guardar la imagen: " + saveErr.Error())
		return
	}
	message := history.Message{
		ID:        history.NewID(a.participantID),
		Timestamp: time.Now().UTC(),
		SenderID:  a.participantID,
		Username:  a.cfg.Username,
		Kind:      "image",
		ImageID:   imageID,
		ImageExt:  ext,
		ImagePath: filePath,
		Text:      caption,
	}
	a.addChatMessage(message)
	a.playEventSound(audio.SFXMessage)
	payload, encodeErr := room.Encode(room.Envelope{Kind: room.ChatMessage, Message: &message})
	if encodeErr == nil {
		a.sendChatPayload(payload)
	}
	a.sendAsset(assets.KindImage, imageID, ext, data)
}

func (a *App) addActivity(text string) {
	stamp := time.Now().Format("15:04")
	fyne.Do(func() {
		if a.activityBox == nil {
			return
		}
		lbl := widget.NewLabel(fmt.Sprintf("%s %s", stamp, text))
		lbl.Wrapping = fyne.TextWrapWord
		a.activityBox.Add(lbl)
		if a.activityScroll != nil {
			a.activityScroll.ScrollToBottom()
		}
	})
}

func (a *App) showSettings() {
	a.setStatus("Cargando opciones y dispositivos de audio...")
	go func() {
		devices, err := audio.ListDevices()
		if err != nil {
			fyne.Do(func() {
				a.setStatus("No se pudieron enumerar los dispositivos: " + err.Error())
			})
			return
		}
		fyne.Do(func() {
			a.renderSettingsView(devices)
		})
	}()
}

func (a *App) renderSettingsView(devices audio.DeviceLists) {
	inputOptions := append([]string{"Sistema predeterminado", phoneMicInput}, devices.Inputs...)
	outputOptions := append([]string{"Sistema predeterminado"}, devices.Outputs...)
	username := widget.NewEntry()
	username.SetText(a.cfg.Username)
	input := widget.NewSelect(inputOptions, nil)
	input.SetSelected(a.cfg.InputDevice)
	output := widget.NewSelect(outputOptions, nil)
	output.SetSelected(a.cfg.OutputDevice)
	sensitivity := widget.NewSlider(0, 1)
	sensitivity.Step = 0.05
	sensitivity.Value = float64(a.cfg.VADThreshold)
	thresholdLabel := widget.NewLabel(fmt.Sprintf("Sensibilidad VAD: %.2f", a.cfg.VADThreshold))
	sensitivity.OnChanged = func(value float64) {
		a.cfg.VADThreshold = float32(value)
		thresholdLabel.SetText(fmt.Sprintf("Sensibilidad VAD: %.2f", value))
	}
	mode := widget.NewRadioGroup([]string{"Activación por voz", "Push-to-talk"}, func(selected string) {
		if selected == "Push-to-talk" {
			a.cfg.MicMode = config.PushToTalk
		} else {
			a.cfg.MicMode = config.VoiceActivation
		}
	})
	if a.cfg.MicMode == config.PushToTalk {
		mode.SetSelected("Push-to-talk")
	} else {
		mode.SetSelected("Activación por voz")
	}
	filterCheck := widget.NewCheck("Aplicar filtro de voz", func(checked bool) { a.cfg.AudioFilterEnabled = checked })
	filterCheck.SetChecked(a.cfg.AudioFilterEnabled)
	rnnoiseCheck := widget.NewCheck("Supresión de ruido (RNNoise)", func(checked bool) { a.cfg.RNNoiseEnabled = checked })
	rnnoiseCheck.SetChecked(a.cfg.RNNoiseEnabled)
	vadCheck := widget.NewCheck("Detección de voz (VAD)", func(checked bool) { a.cfg.VADEnabled = checked })
	vadCheck.SetChecked(a.cfg.VADEnabled)
	gateCheck := widget.NewCheck("Puerta de ruido", func(checked bool) { a.cfg.NoiseGateEnabled = checked })
	gateCheck.SetChecked(a.cfg.NoiseGateEnabled)
	highPassCheck := widget.NewCheck("Pasa-altos", func(checked bool) { a.cfg.HighPassEnabled = checked })
	highPassCheck.SetChecked(a.cfg.HighPassEnabled)
	lowPassCheck := widget.NewCheck("Pasa-bajos", func(checked bool) { a.cfg.LowPassEnabled = checked })
	lowPassCheck.SetChecked(a.cfg.LowPassEnabled)
	notchCheck := widget.NewCheck("Notch de zumbido", func(checked bool) { a.cfg.NotchEnabled = checked })
	notchCheck.SetChecked(a.cfg.NotchEnabled)
	compressorCheck := widget.NewCheck("Compresor", func(checked bool) { a.cfg.CompressorEnabled = checked })
	compressorCheck.SetChecked(a.cfg.CompressorEnabled)
	expanderCheck := widget.NewCheck("Expansor", func(checked bool) { a.cfg.ExpanderEnabled = checked })
	expanderCheck.SetChecked(a.cfg.ExpanderEnabled)
	limiterCheck := widget.NewCheck("Limitador", func(checked bool) { a.cfg.LimiterEnabled = checked })
	limiterCheck.SetChecked(a.cfg.LimiterEnabled)
	newSlider := func(label string, minimum, maximum, value float64, onChanged func(float64)) (*widget.Slider, *widget.Label) {
		valueLabel := widget.NewLabel("")
		slider := widget.NewSlider(minimum, maximum)
		slider.Step = 0.1
		slider.Value = value
		slider.OnChanged = func(current float64) {
			onChanged(current)
			valueLabel.SetText(fmt.Sprintf("%s: %.1f", label, current))
		}
		valueLabel.SetText(fmt.Sprintf("%s: %.1f", label, value))
		return slider, valueLabel
	}
	gain, gainLabel := newSlider("Ganancia (dB)", -24, 24, float64(a.cfg.InputGainDB), func(value float64) { a.cfg.InputGainDB = float32(value) })
	highPass, highPassLabel := newSlider("Frecuencia pasa-altos (Hz)", 20, 500, float64(a.cfg.HighPassHz), func(value float64) { a.cfg.HighPassHz = float32(value) })
	lowPass, lowPassLabel := newSlider("Frecuencia pasa-bajos (Hz)", 1000, 18000, float64(a.cfg.LowPassHz), func(value float64) { a.cfg.LowPassHz = float32(value) })
	notch, notchLabel := newSlider("Frecuencia notch (Hz)", 40, 1000, float64(a.cfg.NotchHz), func(value float64) { a.cfg.NotchHz = float32(value) })
	compressorThreshold, compressorThresholdLabel := newSlider("Umbral compresor (dB)", -50, 0, float64(a.cfg.CompressorThresholdDB), func(value float64) { a.cfg.CompressorThresholdDB = float32(value) })
	compressorRatio, compressorRatioLabel := newSlider("Ratio compresor", 1, 12, float64(a.cfg.CompressorRatio), func(value float64) { a.cfg.CompressorRatio = float32(value) })
	expanderThreshold, expanderThresholdLabel := newSlider("Umbral expansor (dB)", -70, -10, float64(a.cfg.ExpanderThresholdDB), func(value float64) { a.cfg.ExpanderThresholdDB = float32(value) })
	expanderRatio, expanderRatioLabel := newSlider("Ratio expansor", 1, 12, float64(a.cfg.ExpanderRatio), func(value float64) { a.cfg.ExpanderRatio = float32(value) })
	limiterThreshold, limiterThresholdLabel := newSlider("Techo limitador (dB)", -12, 0, float64(a.cfg.LimiterThresholdDB), func(value float64) { a.cfg.LimiterThresholdDB = float32(value) })
	gateThreshold, gateThresholdLabel := newSlider("Umbral de puerta (dBFS)", -60, -5, float64(a.cfg.ThresholdDB), func(value float64) { a.cfg.ThresholdDB = float32(value) })
	vadHold, vadHoldLabel := newSlider("Mantener VAD abierto (ms)", 0, 500, float64(a.cfg.VADHoldMS), func(value float64) { a.cfg.VADHoldMS = int(value) })
	gateHold, gateHoldLabel := newSlider("Mantener puerta abierta (ms)", 0, 500, float64(a.cfg.GateHoldMS), func(value float64) { a.cfg.GateHoldMS = int(value) })
	filterCard := func(title, detail string, controls ...fyne.CanvasObject) fyne.CanvasObject {
		titleLabel := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		detailLabel := canvas.NewText(detail, color.NRGBA{R: 145, G: 155, B: 165, A: 255})
		detailLabel.TextSize = theme.TextSize() - 2
		content := container.NewVBox(append([]fyne.CanvasObject{titleLabel, detailLabel}, controls...)...)
		background := canvas.NewRectangle(color.NRGBA{R: 32, G: 36, B: 43, A: 255})
		background.CornerRadius = 6
		return container.NewStack(background, container.NewPadded(content))
	}
	advancedFilters := container.NewVBox(
		filterCard("Supresión de ruido", "Reduce ruido constante y de fondo antes de transmitir.", rnnoiseCheck),
		filterCard("Detección de voz (VAD)", "Evita enviar cuando no detecta voz.", vadCheck, thresholdLabel, sensitivity, vadHoldLabel, vadHold),
		filterCard("Puerta de ruido", "Silencia señales por debajo del umbral elegido y evita cortes bruscos.", gateCheck, gateThresholdLabel, gateThreshold, gateHoldLabel, gateHold),
		filterCard("Ganancia", "Ajusta el volumen de entrada antes de los demás filtros.", gainLabel, gain),
		filterCard("Pasa-altos", "Quita golpes, vibración y graves no deseados.", highPassCheck, highPassLabel, highPass),
		filterCard("Pasa-bajos", "Reduce hiss y agudos excesivos.", lowPassCheck, lowPassLabel, lowPass),
		filterCard("Notch de zumbido", "Atenúa una frecuencia puntual de zumbido.", notchCheck, notchLabel, notch),
		filterCard("Compresor", "Acerca los sonidos fuertes y suaves.", compressorCheck, compressorThresholdLabel, compressorThreshold, compressorRatioLabel, compressorRatio),
		filterCard("Expansor", "Baja el ruido de fondo de forma gradual.", expanderCheck, expanderThresholdLabel, expanderThreshold, expanderRatioLabel, expanderRatio),
		filterCard("Limitador", "Evita saturación al hablar muy fuerte.", limiterCheck, limiterThresholdLabel, limiterThreshold),
	)
	advancedFilters.Hide()
	advancedButton := widget.NewButton("Configuración avanzada", func() {
		if advancedFilters.Visible() {
			advancedFilters.Hide()
		} else {
			advancedFilters.Show()
		}
	})
	resetFilters := widget.NewButton("↺", func() {
		defaults := config.Default()
		a.cfg.AudioFilterEnabled = defaults.AudioFilterEnabled
		a.cfg.RNNoiseEnabled = defaults.RNNoiseEnabled
		a.cfg.VADEnabled = defaults.VADEnabled
		a.cfg.NoiseGateEnabled = defaults.NoiseGateEnabled
		a.cfg.ThresholdDB = defaults.ThresholdDB
		a.cfg.VADThreshold = defaults.VADThreshold
		a.cfg.InputGainDB = defaults.InputGainDB
		a.cfg.HighPassEnabled, a.cfg.HighPassHz = defaults.HighPassEnabled, defaults.HighPassHz
		a.cfg.LowPassEnabled, a.cfg.LowPassHz = defaults.LowPassEnabled, defaults.LowPassHz
		a.cfg.NotchEnabled, a.cfg.NotchHz = defaults.NotchEnabled, defaults.NotchHz
		a.cfg.CompressorEnabled, a.cfg.CompressorThresholdDB, a.cfg.CompressorRatio = defaults.CompressorEnabled, defaults.CompressorThresholdDB, defaults.CompressorRatio
		a.cfg.ExpanderEnabled, a.cfg.ExpanderThresholdDB, a.cfg.ExpanderRatio = defaults.ExpanderEnabled, defaults.ExpanderThresholdDB, defaults.ExpanderRatio
		a.cfg.LimiterEnabled, a.cfg.LimiterThresholdDB = defaults.LimiterEnabled, defaults.LimiterThresholdDB
		a.cfg.VADHoldMS, a.cfg.GateHoldMS = defaults.VADHoldMS, defaults.GateHoldMS
		filterCheck.SetChecked(a.cfg.AudioFilterEnabled)
		rnnoiseCheck.SetChecked(a.cfg.RNNoiseEnabled)
		vadCheck.SetChecked(a.cfg.VADEnabled)
		gateCheck.SetChecked(a.cfg.NoiseGateEnabled)
		highPassCheck.SetChecked(a.cfg.HighPassEnabled)
		lowPassCheck.SetChecked(a.cfg.LowPassEnabled)
		notchCheck.SetChecked(a.cfg.NotchEnabled)
		compressorCheck.SetChecked(a.cfg.CompressorEnabled)
		expanderCheck.SetChecked(a.cfg.ExpanderEnabled)
		limiterCheck.SetChecked(a.cfg.LimiterEnabled)
		gain.SetValue(float64(a.cfg.InputGainDB))
		highPass.SetValue(float64(a.cfg.HighPassHz))
		lowPass.SetValue(float64(a.cfg.LowPassHz))
		notch.SetValue(float64(a.cfg.NotchHz))
		compressorThreshold.SetValue(float64(a.cfg.CompressorThresholdDB))
		compressorRatio.SetValue(float64(a.cfg.CompressorRatio))
		expanderThreshold.SetValue(float64(a.cfg.ExpanderThresholdDB))
		expanderRatio.SetValue(float64(a.cfg.ExpanderRatio))
		limiterThreshold.SetValue(float64(a.cfg.LimiterThresholdDB))
		gateThreshold.SetValue(float64(a.cfg.ThresholdDB))
		sensitivity.SetValue(float64(a.cfg.VADThreshold))
		vadHold.SetValue(float64(a.cfg.VADHoldMS))
		gateHold.SetValue(float64(a.cfg.GateHoldMS))
		a.setStatus("Filtros restaurados a valores de fábrica.")
	})
	liveMonitoring := widget.NewCheck("Escuchar mi voz en vivo (en la sala)", func(enabled bool) { a.cfg.LiveMonitoring = enabled })
	liveMonitoring.SetChecked(a.cfg.LiveMonitoring)
	phoneBufferCheck := widget.NewCheck("Activar buffer", func(enabled bool) { a.cfg.PhoneMicBufferEnabled = enabled })
	phoneBufferCheck.SetChecked(a.cfg.PhoneMicBufferEnabled)
	phoneBufferMS := widget.NewEntry()
	phoneBufferMS.SetText(strconv.Itoa(a.cfg.PhoneMicBufferMS))
	level := newLevelBar()
	level.SetValue(0)
	levelText := widget.NewLabel("Nivel: -60 dBFS")
	var monitor *audio.Monitor
	var previewButton *widget.Button
	previewActive := false
	startMonitor := func(name string) {
		if monitor != nil {
			monitor.Stop()
			monitor = nil
		}
		if a.transport != nil {
			fyne.Do(func() {
				level.SetValue(0)
				levelText.SetText("Nivel: micrófono activo en la sala")
			})
			return
		}
		if name == phoneMicInput {
			fyne.Do(func() {
				level.SetValue(0)
				levelText.SetText("Nivel: controlado desde el celular")
			})
			return
		}
		monitor, _ = audio.StartMonitor(name, func(db float32) {
			fyne.Do(func() {
				level.SetValue(float64((db + 60) / 60))
				levelText.SetText(fmt.Sprintf("Nivel: %.0f dBFS", db))
			})
		})
	}
	startMonitor(input.Selected)
	stopPreview := func() {
		if !previewActive {
			return
		}
		a.stopPreviewAudio()
		previewActive = false
		if previewButton != nil {
			previewButton.SetText("Escuchar micrófono")
		}
		startMonitor(input.Selected)
	}
	a.settingsStatus = widget.NewLabel("")
	phoneStatusStop := make(chan struct{})
	var closeOnce sync.Once
	var recordingTest atomic.Bool
	var refreshPhoneStatus func()
	input.OnChanged = func(selected string) {
		stopPreview()
		a.cfg.InputDevice = selected
		startMonitor(selected)
		if selected == phoneMicInput {
			a.showPhoneMic()
		} else {
			a.stopPhoneMic()
		}
		refreshPhoneStatus()
	}
	output.OnChanged = func(selected string) {
		stopPreview()
		a.cfg.OutputDevice = selected
	}
	testInput := widget.NewButton("Grabar prueba", nil)
	playRecording := widget.NewButton("Reproducir prueba", func() { a.playRecording(output.Selected) })
	testOutput := widget.NewButton("Reproducir tono", func() { a.testOutput(output.Selected) })
	showQR := widget.NewButton("Mostrar QR", func() {
		a.showPhoneMic()
		refreshPhoneStatus()
	})
	stopPhone := widget.NewButton("Detener celular", func() {
		a.phoneMicMu.RLock()
		server := a.phoneMic
		a.phoneMicMu.RUnlock()
		if server != nil {
			a.stopPhoneMic()
			a.cfg.InputDevice = phoneMicInput
			a.setStatus("Servidor de micrófono de celular detenido.")
		}
		refreshPhoneStatus()
	})
	bufferSave := widget.NewButton("✓", nil)
	serverLED := canvas.NewCircle(color.NRGBA{R: 210, G: 85, B: 85, A: 255})
	connectionLED := canvas.NewCircle(color.NRGBA{R: 210, G: 85, B: 85, A: 255})
	serverLabel := widget.NewLabel("Servidor detenido")
	connectionLabel := widget.NewLabel("Celular no conectado")
	previewButton = widget.NewButton("Escuchar micrófono", func() {
		if previewActive {
			stopPreview()
			return
		}
		if a.transport != nil {
			a.setStatus("La prueba de micrófono solo está disponible fuera de una sala.")
			return
		}
		if monitor != nil {
			monitor.Stop()
			monitor = nil
		}
		if input.Selected == phoneMicInput {
			if !a.phoneMicEnabled() {
				a.setStatus("Primero iniciá el servidor y conectá el celular.")
				startMonitor(input.Selected)
				return
			}
			player, err := audio.NewPlayer(output.Selected)
			if err != nil {
				a.setStatus("No se pudo iniciar la escucha: " + err.Error())
				startMonitor(input.Selected)
				return
			}
			a.setPreviewPlayer(player)
		} else {
			var engine *audio.Engine
			var err error
			engine, err = audio.NewEngine(input.Selected, output.Selected, func(frame []byte) {
				payload, shouldPlay := a.prepareOutgoingPCM(frame)
				if shouldPlay && engine != nil {
					engine.Play(payload)
				}
			})
			if err != nil {
				a.setStatus("No se pudo iniciar la escucha: " + err.Error())
				startMonitor(input.Selected)
				return
			}
			a.previewMu.Lock()
			a.previewEngine = engine
			a.previewMu.Unlock()
		}
		previewActive = true
		previewButton.SetText("Dejar de escuchar")
		a.setStatus("Escuchando el micrófono. Ajustá filtro y VAD para probar.")
	})
	applyBuffer := func() bool {
		bufferMS, parseErr := strconv.Atoi(strings.TrimSpace(phoneBufferMS.Text))
		if parseErr != nil || bufferMS < 40 || bufferMS > 1000 {
			a.setStatus("El buffer del celular debe estar entre 40 y 1000 ms.")
			return false
		}
		a.cfg.PhoneMicBufferMS = bufferMS
		return true
	}
	bufferSave.OnTapped = func() {
		if !applyBuffer() {
			return
		}
		if err := config.Save(a.cfgPath, a.cfg); err != nil {
			logging.Errorf("guardar buffer de celular: %s", logging.FormatError(err))
			a.setStatus("No se pudo guardar el buffer del celular.")
			return
		}
		a.setStatus("Buffer guardado. Reiniciá el servidor del celular para aplicarlo.")
	}
	testInput.OnTapped = func() {
		if a.transport != nil {
			a.setStatus("No se puede grabar prueba mientras estás en una sala.")
			return
		}
		if !recordingTest.CompareAndSwap(false, true) {
			return
		}
		testInput.Disable()
		a.testInput(input.Selected)
		duration := 2200 * time.Millisecond
		if input.Selected == phoneMicInput {
			duration = 5 * time.Second
		}
		time.AfterFunc(duration, func() {
			recordingTest.Store(false)
			refreshPhoneStatus()
		})
	}
	refreshPhoneStatus = func() {
		phoneSelected := input.Selected == phoneMicInput
		a.phoneMicMu.RLock()
		server := a.phoneMic
		a.phoneMicMu.RUnlock()
		listening := server != nil
		connected := listening && server.Connected()
		fyne.Do(func() {
			serverLED.FillColor = color.NRGBA{R: 210, G: 85, B: 85, A: 255}
			serverLabel.SetText("Servidor detenido")
			connectionLED.FillColor = color.NRGBA{R: 210, G: 85, B: 85, A: 255}
			connectionLabel.SetText("Celular no conectado")
			if listening {
				serverLED.FillColor = color.NRGBA{R: 90, G: 205, B: 120, A: 255}
				serverLabel.SetText("Servidor escuchando")
			}
			if connected {
				connectionLED.FillColor = color.NRGBA{R: 90, G: 205, B: 120, A: 255}
				connectionLabel.SetText("Celular conectado")
			}
			serverLED.Refresh()
			connectionLED.Refresh()
			if phoneSelected {
				phoneBufferCheck.Enable()
				phoneBufferMS.Enable()
				bufferSave.Enable()
				showQR.Enable()
				if listening {
					stopPhone.Enable()
				} else {
					stopPhone.Disable()
				}
				if !connected || recordingTest.Load() {
					testInput.Disable()
				} else {
					testInput.Enable()
				}
			} else {
				phoneBufferCheck.Disable()
				phoneBufferMS.Disable()
				bufferSave.Disable()
				showQR.Disable()
				stopPhone.Disable()
				if recordingTest.Load() {
					testInput.Disable()
				} else {
					testInput.Enable()
				}
			}
		})
	}
	refreshPhoneStatus()
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-phoneStatusStop:
				return
			case <-ticker.C:
				refreshPhoneStatus()
			}
		}
	}()
	closeSettings := func() {
		closeOnce.Do(func() {
			close(phoneStatusStop)
			go func() {
				stopPreview()
				if monitor != nil {
					monitor.Stop()
				}
			}()
			a.settingsStatus = nil
			a.window.SetContent(a.mainView)
		})
	}
	roomPortEntry := widget.NewEntry()
	cleanPort := strings.TrimPrefix(a.cfg.ListenAddress, ":")
	if cleanPort == "" || cleanPort == "0" {
		cleanPort = "47830"
	}
	roomPortEntry.SetText(cleanPort)

	phonePortEntry := widget.NewEntry()
	phonePortVal := a.cfg.PhoneMicPort
	if phonePortVal <= 0 {
		phonePortVal = 47831
	}
	phonePortEntry.SetText(strconv.Itoa(phonePortVal))

	saveSettings := func() {
		if !applyBuffer() {
			return
		}
		oldInput := a.cfg.InputDevice
		oldOutput := a.cfg.OutputDevice
		a.cfg.Username = strings.TrimSpace(username.Text)
		if a.cfg.Username == "" {
			a.cfg.Username = "Usuario"
		}
		if a.selfNameLabel != nil {
			a.selfNameLabel.SetText(a.cfg.Username)
		}
		for i := range a.roomState.Participants {
			if a.roomState.Participants[i].ID == a.participantID {
				a.roomState.Participants[i].CanBeHost = a.cfg.AllowHostMigration
				break
			}
		}
		portStr := strings.TrimSpace(roomPortEntry.Text)
		if portStr != "" {
			if !strings.HasPrefix(portStr, ":") && !strings.Contains(portStr, ":") {
				a.cfg.ListenAddress = ":" + portStr
			} else {
				a.cfg.ListenAddress = portStr
			}
		} else {
			a.cfg.ListenAddress = ":47830"
		}
		if p, err := strconv.Atoi(strings.TrimSpace(phonePortEntry.Text)); err == nil && p > 0 && p < 65536 {
			a.cfg.PhoneMicPort = p
		}
		if a.bottomBar != nil {
			if a.cfg.ShowStatusBar {
				a.bottomBar.Show()
			} else {
				a.bottomBar.Hide()
			}
		}
		a.setStatus("Configuración guardada.")
		closeSettings()

		go func() {
			if err := config.Save(a.cfgPath, a.cfg); err != nil {
				logging.Errorf("guardar configuración: %s", logging.FormatError(err))
				fyne.Do(func() {
					a.setStatus("No se pudo guardar la configuración.")
				})
				return
			}
			if a.transport != nil && (oldInput != a.cfg.InputDevice || oldOutput != a.cfg.OutputDevice) {
				a.restartAudioInRoom()
			}
		}()
	}
	outputFilterCheck := widget.NewCheck("Aplicar filtro de salida", func(enabled bool) { a.cfg.OutputFilterEnabled = enabled })
	outputFilterCheck.SetChecked(a.cfg.OutputFilterEnabled)
	outputHighPassCheck := widget.NewCheck("Pasa-altos", func(enabled bool) { a.cfg.OutputHighPassEnabled = enabled })
	outputHighPassCheck.SetChecked(a.cfg.OutputHighPassEnabled)
	outputLowPassCheck := widget.NewCheck("Pasa-bajos", func(enabled bool) { a.cfg.OutputLowPassEnabled = enabled })
	outputLowPassCheck.SetChecked(a.cfg.OutputLowPassEnabled)
	outputLimiterCheck := widget.NewCheck("Limitador", func(enabled bool) { a.cfg.OutputLimiterEnabled = enabled })
	outputLimiterCheck.SetChecked(a.cfg.OutputLimiterEnabled)
	outputGain, outputGainLabel := newSlider("Ganancia salida (dB)", -24, 24, float64(a.cfg.OutputGainDB), func(value float64) { a.cfg.OutputGainDB = float32(value) })
	outputHighPass, outputHighPassLabel := newSlider("Frecuencia pasa-altos (Hz)", 20, 500, float64(a.cfg.OutputHighPassHz), func(value float64) { a.cfg.OutputHighPassHz = float32(value) })
	outputLowPass, outputLowPassLabel := newSlider("Frecuencia pasa-bajos (Hz)", 1000, 18000, float64(a.cfg.OutputLowPassHz), func(value float64) { a.cfg.OutputLowPassHz = float32(value) })
	outputLimiter, outputLimiterLabel := newSlider("Techo limitador (dB)", -12, 0, float64(a.cfg.OutputLimiterDB), func(value float64) { a.cfg.OutputLimiterDB = float32(value) })
	outputAdvanced := container.NewVBox(
		filterCard("Ganancia", "Ajusta el volumen de las voces recibidas.", outputGainLabel, outputGain),
		filterCard("Pasa-altos", "Reduce graves, golpes y vibración en las voces recibidas.", outputHighPassCheck, outputHighPassLabel, outputHighPass),
		filterCard("Pasa-bajos", "Reduce hiss y agudos molestos en las voces recibidas.", outputLowPassCheck, outputLowPassLabel, outputLowPass),
		filterCard("Limitador", "Evita saturación y picos molestos en la salida.", outputLimiterCheck, outputLimiterLabel, outputLimiter),
	)
	outputAdvanced.Hide()
	outputAdvancedButton := widget.NewButton("Configuración avanzada", func() {
		if outputAdvanced.Visible() {
			outputAdvanced.Hide()
		} else {
			outputAdvanced.Show()
		}
	})
	resetOutputFilters := widget.NewButton("↺", func() {
		defaults := config.Default()
		a.cfg.OutputFilterEnabled, a.cfg.OutputGainDB = defaults.OutputFilterEnabled, defaults.OutputGainDB
		a.cfg.OutputHighPassEnabled, a.cfg.OutputHighPassHz = defaults.OutputHighPassEnabled, defaults.OutputHighPassHz
		a.cfg.OutputLowPassEnabled, a.cfg.OutputLowPassHz = defaults.OutputLowPassEnabled, defaults.OutputLowPassHz
		a.cfg.OutputLimiterEnabled, a.cfg.OutputLimiterDB = defaults.OutputLimiterEnabled, defaults.OutputLimiterDB
		outputFilterCheck.SetChecked(a.cfg.OutputFilterEnabled)
		outputHighPassCheck.SetChecked(a.cfg.OutputHighPassEnabled)
		outputLowPassCheck.SetChecked(a.cfg.OutputLowPassEnabled)
		outputLimiterCheck.SetChecked(a.cfg.OutputLimiterEnabled)
		outputGain.SetValue(float64(a.cfg.OutputGainDB))
		outputHighPass.SetValue(float64(a.cfg.OutputHighPassHz))
		outputLowPass.SetValue(float64(a.cfg.OutputLowPassHz))
		outputLimiter.SetValue(float64(a.cfg.OutputLimiterDB))
		a.setStatus("Filtros de salida restaurados a valores de fábrica.")
	})
	showStatusBarCheck := widget.NewCheck("Mostrar barra de estado inferior", func(checked bool) { a.cfg.ShowStatusBar = checked })
	showStatusBarCheck.SetChecked(a.cfg.ShowStatusBar)
	profileCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle("👤 Perfil de Usuario", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Nombre de usuario"),
		username,
	))
	inputCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle("🎙️ Entrada de Audio", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Fuente de voz"),
		input,
		levelText,
		level,
		mode,
		container.NewHBox(filterCheck, advancedButton, resetFilters),
		advancedFilters,
		liveMonitoring,
		previewButton,
		testInput,
	))
	outputCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle("🔊 Salida de Audio", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Dispositivo de reproducción"),
		output,
		container.NewGridWithColumns(2, testOutput, playRecording),
		container.NewHBox(outputFilterCheck, outputAdvancedButton, resetOutputFilters),
		outputAdvanced,
	))
	phoneCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle("📱 Micrófono del Celular", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewBorder(nil, nil, container.NewGridWrap(fyne.NewSize(12, 12), serverLED), nil, serverLabel),
		container.NewBorder(nil, nil, container.NewGridWrap(fyne.NewSize(12, 12), connectionLED), nil, connectionLabel),
		container.NewHBox(phoneBufferCheck, container.NewGridWrap(fyne.NewSize(76, 36), phoneBufferMS), container.NewGridWrap(fyne.NewSize(36, 36), bufferSave)),
		container.NewGridWithColumns(2, showQR, stopPhone),
	))
	eventSoundsCheck := widget.NewCheck("Sonidos de eventos (conexión, desconexión, chat)", func(checked bool) { a.cfg.EventSoundsEnabled = checked })
	eventSoundsCheck.SetChecked(a.cfg.EventSoundsEnabled)
	allowHostCheck := widget.NewCheck("Permitir ser Host de respaldo si el Host se desconecta", func(checked bool) { a.cfg.AllowHostMigration = checked })
	allowHostCheck.SetChecked(a.cfg.AllowHostMigration)
	networkCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle("🌐 Red y Conectividad", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Puerto UDP de la sala (predeterminado 47830):"),
		roomPortEntry,
		widget.NewLabel("Puerto UDP para el celular (predeterminado 47831):"),
		phonePortEntry,
		allowHostCheck,
	))
	openDataDirButton := widget.NewButtonWithIcon("Abrir carpeta de datos y grabaciones", theme.FolderOpenIcon(), func() {
		if err := openDataDirectory(); err != nil {
			a.setStatus("No se pudo abrir la carpeta: " + err.Error())
		}
	})
	openLogsDirButton := widget.NewButtonWithIcon("Abrir carpeta de registros (logs)", theme.DocumentIcon(), func() {
		if err := openLogsDirectory(); err != nil {
			a.setStatus("No se pudo abrir la carpeta de logs: " + err.Error())
		}
	})
	interfaceCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle("⚙️ Interfaz y Sistema", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		showStatusBarCheck,
		eventSoundsCheck,
		container.NewGridWithColumns(2, openDataDirButton, openLogsDirButton),
	))
	settingsBody := container.NewVScroll(container.NewVBox(
		profileCard,
		container.NewGridWithColumns(2, inputCard, outputCard),
		phoneCard,
		networkCard,
		interfaceCard,
	))
	back := widget.NewButtonWithIcon("Volver", theme.NavigateBackIcon(), closeSettings)
	header := container.NewBorder(nil, nil, back, nil, widget.NewLabelWithStyle("Configuración", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	footer := container.NewBorder(nil, nil, a.settingsStatus, container.NewHBox(widget.NewButton("Cancelar", closeSettings), widget.NewButton("Guardar", saveSettings)), nil)
	a.window.SetContent(container.NewBorder(header, footer, nil, nil, settingsBody))
}

func (a *App) showPhoneMic() {
	a.cfg.InputDevice = phoneMicInput
	server, err := a.ensurePhoneMicServer()
	if err != nil {
		a.setStatus("No se pudo iniciar el micrófono de celular: " + err.Error())
		return
	}
	png, err := server.QRCode()
	if err != nil {
		a.setStatus("No se pudo crear el QR: " + err.Error())
		return
	}
	qr := canvas.NewImageFromResource(fyne.NewStaticResource("voxmesh-phone-mic.png", png))
	qr.FillMode = canvas.ImageFillContain
	qr.Resize(fyne.NewSize(280, 280))
	address := widget.NewMultiLineEntry()
	address.SetText(server.URL())
	address.Disable()
	content := container.NewVBox(
		widget.NewLabel("Escaneá el código QR desde la app VoxMesh Mic (misma Wi-Fi):"),
		container.NewCenter(container.NewGridWrap(fyne.NewSize(280, 280), qr)),
		address,
	)
	dialog.ShowCustom("Usar celular como micrófono", "Cerrar", content, a.window)
}

func (a *App) ensurePhoneMicServer() (*phonemic.Server, error) {
	a.phoneMicMu.RLock()
	server := a.phoneMic
	a.phoneMicMu.RUnlock()
	if server != nil {
		return server, nil
	}
	server, err := phonemic.Start(a.cfg.PhoneMicToken, a.cfg.PhoneMicPort, a.cfg.PhoneMicBufferEnabled, a.cfg.PhoneMicBufferMS, a.cfg.Username, a.receivePhonePCM, a.setStatus)
	if err != nil {
		return nil, err
	}
	a.phoneMicMu.Lock()
	if a.phoneMic == nil {
		a.phoneMic = server
	} else {
		server.Stop()
		server = a.phoneMic
	}
	a.phoneMicMu.Unlock()
	a.cfg.PhoneMicToken = server.Token()
	a.cfg.PhoneMicPort = server.Port()
	if err := config.Save(a.cfgPath, a.cfg); err != nil {
		logging.Errorf("guardar enlace de micrófono de celular: %s", logging.FormatError(err))
	}
	if a.cfg.PhoneMicBufferEnabled {
		a.setStatus("Micrófono de celular listo con " + strconv.Itoa(a.cfg.PhoneMicBufferMS) + " ms de buffer. Escaneá el QR desde la misma Wi-Fi.")
	} else {
		a.setStatus("Micrófono de celular listo sin buffer. Escaneá el QR desde la misma Wi-Fi.")
	}
	return server, nil
}

func (a *App) testPhoneMic() {
	a.phoneMicMu.RLock()
	server := a.phoneMic
	a.phoneMicMu.RUnlock()
	if server == nil {
		a.setStatus("Primero abrí 'Usar tu celular como micrófono' y conectá el teléfono.")
		return
	}
	if !server.Connected() {
		a.setStatus("Esperando que el celular se conecte para grabar.")
		return
	}
	server.ResetRecording()
	a.setStatus("Grabando desde el celular durante 5 segundos...")
	go func() {
		time.Sleep(5 * time.Second)
		data := server.Recording()
		if len(data) == 0 {
			a.setStatus("No llegó audio desde el celular.")
			return
		}
		a.recordingMu.Lock()
		a.recordedInput = data
		a.recordingMu.Unlock()
		a.setStatus("Grabación del celular lista. Usá 'Reproducir grabación'.")
	}()
}

func (a *App) stopPhoneMic() {
	a.phoneMicMu.Lock()
	server := a.phoneMic
	a.phoneMic = nil
	a.phoneMicMu.Unlock()
	if server != nil {
		server.Stop()
	}
	if a.cfg.InputDevice == phoneMicInput {
		a.cfg.InputDevice = "Sistema predeterminado"
	}
}

func (a *App) receivePhonePCM(frame []byte) {
	a.playPhonePreview(frame)
	if !a.phoneMicEnabled() || a.transport == nil {
		return
	}
	var peer *net.UDPAddr
	if !a.hostMode.Load() {
		var err error
		peer, err = net.ResolveUDPAddr("udp", a.endpoint)
		if err != nil {
			logging.Errorf("resolver host para micrófono de celular: %s", logging.FormatError(err))
			return
		}
	}
	a.sendOutgoingPCM(frame, a.transport, peer, a.hostMode.Load())
}

func (a *App) phoneMicEnabled() bool {
	a.phoneMicMu.RLock()
	defer a.phoneMicMu.RUnlock()
	return a.phoneMic != nil
}

func (a *App) setPreviewPlayer(player *audio.Player) {
	a.previewMu.Lock()
	previous := a.previewPlayer
	a.previewPlayer = player
	a.previewMu.Unlock()
	if previous != nil {
		previous.Stop()
	}
}

func (a *App) stopPreviewAudio() {
	a.previewMu.Lock()
	player := a.previewPlayer
	engine := a.previewEngine
	a.previewPlayer = nil
	a.previewEngine = nil
	a.previewMu.Unlock()
	if engine != nil {
		engine.Stop()
	}
	if player != nil {
		player.Stop()
	}
}

func (a *App) playPhonePreview(frame []byte) {
	a.previewMu.RLock()
	player := a.previewPlayer
	a.previewMu.RUnlock()
	if player == nil {
		return
	}
	payload, shouldPlay := a.prepareOutgoingPCM(frame)
	if shouldPlay {
		player.Play(payload)
	}
}

func (a *App) testInput(name string) {
	if name == phoneMicInput {
		a.testPhoneMic()
		return
	}
	a.setStatus("Grabando desde " + name + "...")
	filterEnabled := a.cfg.AudioFilterEnabled
	thresholdDB := a.cfg.ThresholdDB
	go func() {
		data, err := audio.RecordInput(name, 2*time.Second)
		if err != nil {
			a.setStatus("No se pudo grabar: " + err.Error())
			return
		}
		if filterEnabled {
			data, _, err = audio.PrepareOutgoingPCM(data, a.voiceProcessor, audio.NoiseGate{ThresholdDB: thresholdDB}, true, a.cfg.VADThreshold)
		} else {
			data, err = audio.PreparePCM(data, audio.NoiseGate{ThresholdDB: thresholdDB}, false)
		}
		if err != nil {
			a.setStatus("No se pudo aplicar el filtro: " + err.Error())
			return
		}
		a.recordingMu.Lock()
		a.recordedInput = append(a.recordedInput[:0], data...)
		a.recordingMu.Unlock()
		mode := "audio crudo"
		if filterEnabled {
			mode = "audio con filtro"
		}
		a.setStatus("Micrófono OK: " + mode + ", " + strconv.Itoa(len(data)) + " bytes.")
	}()
}

func (a *App) testOutput(name string) {
	a.setStatus("Reproduciendo tono en " + name + "...")
	go func() {
		if err := audio.PlayTestTone(name, time.Second); err != nil {
			a.setStatus("No se pudo reproducir: " + err.Error())
			return
		}
		a.setStatus("Salida de audio OK.")
	}()
}

func (a *App) playRecording(name string) {
	a.recordingMu.RLock()
	data := append([]byte(nil), a.recordedInput...)
	a.recordingMu.RUnlock()
	if len(data) == 0 {
		a.setStatus("Primero graba una prueba de micrófono.")
		return
	}
	a.setStatus("Reproduciendo la grabación...")
	go func() {
		if err := audio.PlayPCM(name, data); err != nil {
			a.setStatus("No se pudo reproducir la grabación: " + err.Error())
			return
		}
		a.setStatus("Grabación reproducida.")
	}()
}

type tappableImage struct {
	widget.BaseWidget
	image    *canvas.Image
	onTapped func()
}

func newTappableImage(img *canvas.Image, onTapped func()) *tappableImage {
	t := &tappableImage{image: img, onTapped: onTapped}
	t.ExtendBaseWidget(t)
	return t
}

func (t *tappableImage) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.image)
}

func (t *tappableImage) Tapped(_ *fyne.PointEvent) {
	if t.onTapped != nil {
		t.onTapped()
	}
}

func (t *tappableImage) MinSize() fyne.Size {
	if t.image != nil {
		return t.image.MinSize()
	}
	return fyne.NewSize(0, 0)
}

func (a *App) showLightbox(imagePath string) {
	if a.window == nil {
		return
	}
	cSize := a.window.Canvas().Size()
	targetW := float32(float64(cSize.Width) * 0.85)
	targetH := float32(float64(cSize.Height) * 0.85)
	if targetW < 320 {
		targetW = 320
	}
	if targetH < 220 {
		targetH = 220
	}

	largeImg := &canvas.Image{File: imagePath, FillMode: canvas.ImageFillContain}
	largeImg.SetMinSize(fyne.NewSize(targetW, targetH))

	var pop *widget.PopUp
	tapImg := newTappableImage(largeImg, func() {
		if pop != nil {
			pop.Hide()
		}
	})
	pop = widget.NewPopUp(tapImg, a.window.Canvas())
	posX := (cSize.Width - targetW) / 2
	posY := (cSize.Height - targetH) / 2
	if posX < 0 {
		posX = 0
	}
	if posY < 0 {
		posY = 0
	}
	pop.ShowAtPosition(fyne.NewPos(posX, posY))
}

func buildChatSegments(m history.Message, userColor fyne.ThemeColorName) []widget.RichTextSegment {
	stamp := m.Timestamp.Local().Format("15:04")
	prefix := m.Username

	stampStyle := widget.RichTextStyleInline
	stampStyle.ColorName = theme.ColorNamePlaceHolder

	userStyle := widget.RichTextStyleInline
	userStyle.TextStyle = fyne.TextStyle{Bold: true}
	userStyle.ColorName = userColor

	segments := []widget.RichTextSegment{
		&widget.TextSegment{
			Text:  stamp + " ",
			Style: stampStyle,
		},
		&widget.TextSegment{
			Text:  prefix + ": ",
			Style: userStyle,
		},
	}

	text := m.Text
	if m.Kind == "image" && text == "" {
		text = "📷 Imagen compartida"
	}

	matches := urlRegex.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		segments = append(segments, &widget.TextSegment{
			Text:  text,
			Style: widget.RichTextStyleInline,
		})
		return segments
	}

	lastIdx := 0
	for _, loc := range matches {
		start, end := loc[0], loc[1]
		if start > lastIdx {
			segments = append(segments, &widget.TextSegment{
				Text:  text[lastIdx:start],
				Style: widget.RichTextStyleInline,
			})
		}
		rawURL := text[start:end]
		parsedURL, err := url.Parse(rawURL)
		if err == nil {
			segments = append(segments, &widget.HyperlinkSegment{
				Text: rawURL,
				URL:  parsedURL,
			})
		} else {
			segments = append(segments, &widget.TextSegment{
				Text:  rawURL,
				Style: widget.RichTextStyleInline,
			})
		}
		lastIdx = end
	}
	if lastIdx < len(text) {
		segments = append(segments, &widget.TextSegment{
			Text:  text[lastIdx:],
			Style: widget.RichTextStyleInline,
		})
	}
	return segments
}

func (a *App) buildChatMessageObject(m history.Message, isMatch bool) fyne.CanvasObject {
	var userColor fyne.ThemeColorName
	if m.SenderID == a.participantID || (a.cfg.Username != "" && m.Username == a.cfg.Username) {
		userColor = theme.ColorNameSuccess
	} else {
		userColor = theme.ColorNamePrimary
	}

	segments := buildChatSegments(m, userColor)
	rt := widget.NewRichText(segments...)
	rt.Wrapping = fyne.TextWrapWord

	copyText := m.Text
	if copyText == "" && m.Kind == "image" {
		copyText = "Imagen: " + m.ImageID + m.ImageExt
	}
	copyBtn := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() {
		_ = clipboard.WriteAll(copyText)
		a.setStatus("Mensaje copiado al portapapeles.")
	})
	copyBtn.Importance = widget.LowImportance
	copyWrap := container.NewGridWrap(fyne.NewSize(28, 24), copyBtn)

	headerRow := container.NewBorder(nil, nil, nil, copyWrap, rt)
	var row fyne.CanvasObject = headerRow

	imagePath := ""
	if m.Kind == "image" && a.historyStore != nil {
		imagePath = a.historyStore.ImagePath(m.ImageID, m.ImageExt)
	}
	if imagePath != "" && fileExists(imagePath) {
		img := &canvas.Image{File: imagePath, FillMode: canvas.ImageFillContain}
		img.SetMinSize(fyne.NewSize(240, 160))
		p := imagePath
		clickableThumb := newTappableImage(img, func() {
			a.showLightbox(p)
		})
		row = container.NewVBox(headerRow, container.NewHBox(clickableThumb))
	}

	if isMatch {
		highlightBg := canvas.NewRectangle(color.NRGBA{R: 70, G: 65, B: 30, A: 255})
		highlightBg.CornerRadius = 4
		row = container.NewStack(highlightBg, container.NewPadded(row))
	}

	return row
}

func (a *App) refreshChat() {
	fyne.Do(func() {
		if a.chatBox == nil {
			return
		}
		a.chatMu.RLock()
		msgs := append([]history.Message(nil), a.chatMessages...)
		a.chatMu.RUnlock()

		var searchFilter string
		if a.chatSearch != nil {
			searchFilter = strings.ToLower(strings.TrimSpace(a.chatSearch.Text))
		}

		matchCount := 0
		objects := make([]fyne.CanvasObject, 0, len(msgs))
		for _, msg := range msgs {
			m := msg
			if m.System || m.Kind == "system" || strings.EqualFold(m.Username, "SISTEMA") {
				continue
			}
			isMatch := false
			if searchFilter != "" {
				lowerText := strings.ToLower(m.Text)
				lowerUser := strings.ToLower(m.Username)
				if strings.Contains(lowerText, searchFilter) || strings.Contains(lowerUser, searchFilter) {
					isMatch = true
					matchCount++
				}
			}

			objects = append(objects, a.buildChatMessageObject(m, isMatch))
		}

		if a.chatSearchCount != nil {
			if searchFilter == "" {
				a.chatSearchCount.SetText("")
			} else if matchCount == 0 {
				a.chatSearchCount.SetText("(0)")
			} else {
				a.chatSearchCount.SetText(fmt.Sprintf("(%d)", matchCount))
			}
		}

		a.chatBox.Objects = objects
		a.chatBox.Refresh()
		if a.chatScroll != nil && searchFilter == "" {
			a.chatScroll.ScrollToBottom()
		}
	})
}

func (a *App) initHistory(roomName string) {
	root, err := os.UserConfigDir()
	if err != nil {
		return
	}
	store, err := history.New(filepath.Join(root, "VoxMesh"), roomName)
	if err != nil {
		return
	}
	a.historyStore = store
	a.activeRoomName = roomName
	a.viewingRoomName = roomName
	messages, err := store.Load()
	if err != nil {
		return
	}
	chatOnly := make([]history.Message, 0, len(messages))
	for _, m := range messages {
		if m.System || m.Kind == "system" || strings.EqualFold(m.Username, "SISTEMA") {
			a.addActivity(m.Text)
			continue
		}
		chatOnly = append(chatOnly, m)
	}
	a.chatMu.Lock()
	a.chatMessages = chatOnly
	a.chatMu.Unlock()
	sounds, _ := store.ListSounds()
	a.soundMu.Lock()
	a.sounds = a.sounds[:0]
	for _, meta := range sounds {
		a.sounds = append(a.sounds, room.Sound{ID: meta.ID, Ext: meta.Ext, Name: meta.Name})
	}
	a.soundMu.Unlock()
	a.rebuildSoundboard()
	a.refreshRoomList(roomName)
	a.refreshChat()
}

func (a *App) addChatMessage(message history.Message) {
	if message.System || message.Kind == "system" || strings.EqualFold(message.Username, "SISTEMA") {
		a.addActivity(message.Text)
		return
	}
	if a.historyStore != nil {
		go func(m history.Message) {
			_ = a.historyStore.Add(m)
		}(message)
	}
	if a.viewingRoomName != "" && a.activeRoomName != "" && a.viewingRoomName != a.activeRoomName {
		return
	}
	a.chatMu.Lock()
	for _, existing := range a.chatMessages {
		if existing.ID == message.ID {
			a.chatMu.Unlock()
			return
		}
	}
	a.chatMessages = append(a.chatMessages, message)
	a.chatMu.Unlock()

	var searchFilter string
	if a.chatSearch != nil {
		searchFilter = strings.ToLower(strings.TrimSpace(a.chatSearch.Text))
	}

	if searchFilter == "" {
		row := a.buildChatMessageObject(message, false)
		fyne.Do(func() {
			if a.chatBox != nil {
				a.chatBox.Add(row)
				if a.chatScroll != nil {
					a.chatScroll.ScrollToBottom()
				}
			}
		})
	} else {
		a.refreshChat()
	}
}

func (a *App) addSystemMessage(text string) {
	a.addActivity(text)
	if a.transport != nil && a.roomState.HostID == a.participantID {
		message := history.Message{
			ID:        history.NewID("system"),
			Timestamp: time.Now().UTC(),
			Username:  "SISTEMA",
			Kind:      "system",
			Text:      text,
			System:    true,
		}
		payload, _ := room.Encode(room.Envelope{Kind: room.SystemMessage, Message: &message})
		a.transport.Broadcast(transport.Packet{Kind: transport.PacketChat, Sequence: a.sequence.Add(1), Payload: payload}, nil)
	}
}

func (a *App) handleChatPacket(payload []byte) {
	envelope, err := room.Decode(payload)
	if err != nil {
		return
	}
	switch envelope.Kind {
	case room.ChatMessage:
		if envelope.Message != nil {
			if envelope.Message.System || envelope.Message.Kind == "system" || strings.EqualFold(envelope.Message.Username, "SISTEMA") {
				a.addActivity(envelope.Message.Text)
			} else {
				a.addChatMessage(*envelope.Message)
				if envelope.Message.SenderID != a.participantID {
					a.playEventSound(audio.SFXMessage)
				}
			}
		}
	case room.SystemMessage:
		if envelope.Message != nil {
			a.addActivity(envelope.Message.Text)
		}
	case room.SoundAddMessage:
		if envelope.Sound != nil {
			a.addSound(*envelope.Sound)
		}
	case room.SoundRemoveMessage:
		if envelope.Sound != nil {
			a.removeSound(envelope.Sound.ID)
		}
	case room.SoundPlayMessage:
		if envelope.Sound != nil {
			a.playIncomingSound(*envelope.Sound)
		}
	}
}

func (a *App) handleAssetChunk(payload []byte, origin string) {
	chunk, err := assets.Decode(payload)
	if err != nil || a.assetAssembler == nil {
		return
	}
	data, complete := a.assetAssembler.Ingest(chunk, origin)
	if !complete || a.historyStore == nil {
		return
	}
	go func() {
		if chunk.Kind == assets.KindSound {
			a.soundMu.RLock()
			name := ""
			for _, sound := range a.sounds {
				if sound.ID == chunk.ID {
					name = sound.Name
					break
				}
			}
			_, pending := a.pendingPlays[chunk.ID]
			a.soundMu.RUnlock()
			if _, saveErr := a.historyStore.SaveSound(history.SoundMeta{ID: chunk.ID, Ext: chunk.Ext, Name: name}, data); saveErr != nil {
				logging.Errorf("guardar sonido recibido %q: %s", chunk.ID, logging.FormatError(saveErr))
				return
			}
			if pending {
				a.soundMu.Lock()
				delete(a.pendingPlays, chunk.ID)
				a.soundMu.Unlock()
				a.playSoundPCM(data)
			}
			return
		}
		if _, saveErr := a.historyStore.SaveImage(chunk.ID, chunk.Ext, data); saveErr != nil {
			logging.Errorf("guardar archivo recibido %q: %s", chunk.ID, logging.FormatError(saveErr))
			return
		}
		a.refreshChat()
	}()
}

func (a *App) handleAssetMissing(udp *transport.UDP, addr *net.UDPAddr, payload []byte) {
	request, err := assets.DecodeMissing(payload)
	if err != nil || a.historyStore == nil || addr == nil {
		return
	}
	go func() {
		var data []byte
		var readErr error
		if request.Kind == assets.KindSound {
			data, readErr = a.historyStore.ReadSound(request.ID, request.Ext)
		} else {
			data, readErr = a.historyStore.ReadImage(request.ID, request.Ext)
		}
		if readErr != nil || len(data) == 0 {
			return
		}
		chunks, splitErr := assets.Split(request.Kind, request.ID, request.Ext, data)
		if splitErr != nil {
			return
		}
		for _, chunk := range chunks {
			if len(request.Missing) > 0 && !containsIndex(request.Missing, chunk.Index) {
				continue
			}
			_ = udp.Send(addr, transport.Packet{Kind: transport.PacketAssetChunk, Sequence: a.sequence.Add(1), Payload: assets.Encode(chunk)})
		}
	}()
}

func containsIndex(indices []uint32, index uint32) bool {
	for _, value := range indices {
		if value == index {
			return true
		}
	}
	return false
}

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
		if decodeErr != nil {
			a.setStatus("No se pudo procesar el sonido: " + decodeErr.Error())
			return
		}
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		fyne.Do(func() {
			a.showSoundTrimDialog(name, pcm)
		})
	}()
}

func renderWaveform(pcm []byte, startFrac, endFrac float64, width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	bgColor := color.RGBA{R: 24, G: 28, B: 34, A: 255}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, bgColor)
		}
	}
	samples, err := audio.DecodePCM(pcm)
	if err != nil || len(samples) == 0 {
		return img
	}
	midY := height / 2
	startX := int(startFrac * float64(width))
	endX := int(endFrac * float64(width))
	if startX < 0 {
		startX = 0
	}
	if endX > width {
		endX = width
	}

	for x := 0; x < width; x++ {
		sStart := x * len(samples) / width
		sEnd := (x + 1) * len(samples) / width
		if sEnd > len(samples) {
			sEnd = len(samples)
		}
		if sStart >= sEnd {
			continue
		}

		maxVal := int16(0)
		for _, s := range samples[sStart:sEnd] {
			abs := s
			if abs < 0 {
				abs = -abs
			}
			if abs > maxVal {
				maxVal = abs
			}
		}

		barHeight := int(float64(maxVal) / 32767.0 * float64(midY-2))
		if barHeight < 1 {
			barHeight = 1
		}

		isSelected := x >= startX && x <= endX
		var barColor color.RGBA
		if isSelected {
			barColor = color.RGBA{R: 46, G: 204, B: 113, A: 255}
		} else {
			barColor = color.RGBA{R: 70, G: 80, B: 95, A: 255}
		}

		for y := midY - barHeight; y <= midY+barHeight; y++ {
			if y >= 0 && y < height {
				img.SetRGBA(x, y, barColor)
			}
		}
	}
	return img
}

func (a *App) showSoundTrimDialog(defaultName string, pcm []byte) {
	totalSec := float64(len(pcm)) / 96000.0
	if totalSec <= 0.1 {
		a.saveAndBroadcastSound(defaultName, pcm)
		return
	}

	activePCM := pcm

	nameEntry := widget.NewEntry()
	nameEntry.SetText(defaultName)

	durationLabel := widget.NewLabel(fmt.Sprintf("Duración visible: %.2f s", totalSec))

	waveformImg := canvas.NewImageFromImage(renderWaveform(activePCM, 0, 1, 420, 80))
	waveformImg.SetMinSize(fyne.NewSize(420, 80))
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
		curTotal := float64(len(activePCM)) / 96000.0
		sFrac := 0.0
		eFrac := 1.0
		if curTotal > 0 {
			sFrac = startSlider.Value / curTotal
			eFrac = endSlider.Value / curTotal
		}
		waveformImg.Image = renderWaveform(activePCM, sFrac, eFrac, 420, 80)
		waveformImg.Refresh()
	}

	applyStart := func(v float64) {
		if isUpdating {
			return
		}
		isUpdating = true
		curTotal := float64(len(activePCM)) / 96000.0
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
		curTotal := float64(len(activePCM)) / 96000.0
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
		curTotal := float64(len(activePCM)) / 96000.0
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
		curTotal := float64(len(activePCM)) / 96000.0
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
		s := int(startSlider.Value*96000) &^ 1
		e := int(endSlider.Value*96000) &^ 1
		if s < 0 {
			s = 0
		}
		if e > len(activePCM) {
			e = len(activePCM)
		}
		if e <= s {
			e = len(activePCM)
		}
		return append([]byte(nil), activePCM[s:e]...)
	}

	var zoomOutBtn *widget.Button
	var historyPCMs [][]byte

	zoomInBtn := widget.NewButtonWithIcon("Zoom a selección", theme.ZoomInIcon(), func() {
		trimmed := slicePCM()
		if len(trimmed) < 4800 { // at least 50 ms
			return
		}
		historyPCMs = append(historyPCMs, activePCM)
		activePCM = trimmed
		newTotal := float64(len(activePCM)) / 96000.0
		durationLabel.SetText(fmt.Sprintf("Duración visible: %.2f s", newTotal))
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
		if len(historyPCMs) == 0 {
			return
		}
		prevPCM := historyPCMs[len(historyPCMs)-1]
		historyPCMs = historyPCMs[:len(historyPCMs)-1]
		activePCM = prevPCM
		prevTotal := float64(len(activePCM)) / 96000.0
		durationLabel.SetText(fmt.Sprintf("Duración visible: %.2f s", prevTotal))
		isUpdating = true
		startSlider.Max = prevTotal
		startSlider.SetValue(0)
		endSlider.Max = prevTotal
		endSlider.SetValue(prevTotal)
		startEntry.SetText("0.00")
		endEntry.SetText(fmt.Sprintf("%.2f", prevTotal))
		isUpdating = false
		if len(historyPCMs) == 0 {
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
		if !confirmed {
			return
		}
		soundName := strings.TrimSpace(nameEntry.Text)
		if soundName == "" {
			soundName = defaultName
		}
		trimmed := slicePCM()
		a.saveAndBroadcastSound(soundName, trimmed)
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

func (a *App) playIncomingSound(sound room.Sound) {
	if a.cfg.SoundboardMuted || a.historyStore == nil {
		return
	}
	go func() {
		data, readErr := a.historyStore.ReadSound(sound.ID, sound.Ext)
		if readErr == nil {
			a.playSoundPCM(data)
			return
		}
		a.soundMu.Lock()
		if a.pendingPlays == nil {
			a.pendingPlays = make(map[string]struct{})
		}
		a.pendingPlays[sound.ID] = struct{}{}
		a.soundMu.Unlock()
		if a.transport == nil || a.roomState.HostID == a.participantID {
			return
		}
		peer, resolveErr := net.ResolveUDPAddr("udp", a.endpoint)
		if resolveErr != nil {
			return
		}
		request := assets.MissingRequest{Kind: assets.KindSound, ID: sound.ID, Ext: sound.Ext}
		_ = a.transport.Send(peer, transport.Packet{Kind: transport.PacketAssetMissing, Sequence: a.sequence.Add(1), Payload: assets.EncodeMissing(request)})
	}()
}

func (a *App) ensureSoundPlayer() *audio.Player {
	a.soundMu.Lock()
	defer a.soundMu.Unlock()
	if a.soundPlayer != nil {
		return a.soundPlayer
	}
	player, err := audio.NewPlayer(a.cfg.OutputDevice)
	if err != nil {
		logging.Errorf("iniciar salida de sonidos: %s", logging.FormatError(err))
		return nil
	}
	a.soundPlayer = player
	return player
}

func applyVolume(pcm []byte, volume float32) []byte {
	if volume >= 0.99 && volume <= 1.01 {
		return pcm
	}
	if volume <= 0.01 {
		return make([]byte, len(pcm))
	}
	out := make([]byte, len(pcm))
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int16(binary.LittleEndian.Uint16(pcm[i : i+2]))
		scaled := int32(float32(sample) * volume)
		if scaled > 32767 {
			scaled = 32767
		} else if scaled < -32768 {
			scaled = -32768
		}
		binary.LittleEndian.PutUint16(out[i:i+2], uint16(int16(scaled)))
	}
	return out
}

func (a *App) playSoundPCM(data []byte) {
	vol := a.cfg.SoundboardVolume
	if vol <= 0.01 {
		vol = 1.0
	}
	scaled := applyVolume(data, vol)
	if player := a.ensureSoundPlayer(); player != nil {
		player.Clear()
		player.Play(scaled)
	}
}

func (a *App) playEventSound(sfx []byte) {
	if !a.cfg.EventSoundsEnabled || len(sfx) == 0 {
		return
	}
	if a.audioEngine != nil {
		a.audioEngine.Play(sfx)
	} else if player := a.ensureSoundPlayer(); player != nil {
		player.Play(sfx)
	}
}

func (a *App) stopSoundboard() {
	a.soundMu.RLock()
	player := a.soundPlayer
	a.soundMu.RUnlock()
	if player != nil {
		player.Clear()
	}
}

func (a *App) viewRoom(roomName string) {
	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return
	}
	a.viewingRoomName = roomName
	root, err := os.UserConfigDir()
	if err != nil {
		return
	}
	store, err := history.New(filepath.Join(root, "VoxMesh"), roomName)
	if err != nil {
		a.setStatus("No se pudo abrir el historial de " + roomName)
		return
	}
	messages, _ := store.Load()
	chatOnly := make([]history.Message, 0, len(messages))
	for _, m := range messages {
		if m.System || m.Kind == "system" || strings.EqualFold(m.Username, "SISTEMA") {
			continue
		}
		chatOnly = append(chatOnly, m)
	}
	a.chatMu.Lock()
	a.chatMessages = chatOnly
	a.chatMu.Unlock()
	a.refreshChat()
}

func (a *App) refreshRoomList(selected string) {
	root, err := os.UserConfigDir()
	if err != nil {
		return
	}
	rooms, err := history.ListRooms(filepath.Join(root, "VoxMesh"))
	if err != nil {
		return
	}
	found := false
	for _, name := range rooms {
		if name == selected {
			found = true
			break
		}
	}
	if selected != "" && !found {
		rooms = append(rooms, selected)
		sort.Strings(rooms)
	}
	fyne.Do(func() {
		if a.roomSelect == nil {
			return
		}
		a.roomSelect.Options = rooms
		a.roomSelect.Refresh()
		if selected != "" {
			a.roomSelect.SetSelected(selected)
		}
	})
}

func (a *App) sendAsset(kind byte, id, ext string, data []byte) {
	chunks, err := assets.Split(kind, id, ext, data)
	if err != nil {
		a.setStatus("No se pudo preparar el archivo: " + err.Error())
		return
	}
	if a.transport == nil {
		return
	}
	go func() {
		for i, chunk := range chunks {
			if a.transport == nil {
				return
			}
			packet := transport.Packet{Kind: transport.PacketAssetChunk, Sequence: a.sequence.Add(1), Payload: assets.Encode(chunk)}
			if a.roomState.HostID == a.participantID {
				a.transport.Broadcast(packet, nil)
			} else {
				peer, resolveErr := net.ResolveUDPAddr("udp", a.endpoint)
				if resolveErr == nil {
					_ = a.transport.Send(peer, packet)
				}
			}
			if i%4 == 3 {
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()
}

func (a *App) sweepAssetLoop(udp *transport.UDP, stop <-chan struct{}) {
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.sweepSpeakingPeers()
			if a.assetAssembler == nil {
				continue
			}
			for _, notice := range a.assetAssembler.Sweep(600*time.Millisecond, 32) {
				addr, resolveErr := net.ResolveUDPAddr("udp", notice.Origin)
				if resolveErr != nil {
					continue
				}
				_ = udp.Send(addr, transport.Packet{Kind: transport.PacketAssetMissing, Sequence: a.sequence.Add(1), Payload: assets.EncodeMissing(notice.Request)})
			}
		}
	}
}

func (a *App) sendHistorySync(udp *transport.UDP, address *net.UDPAddr) {
	if a.historyStore == nil || address == nil {
		return
	}
	payload, err := room.Encode(room.Envelope{Kind: room.SyncRequestMessage, KnownIDs: a.historyStore.KnownIDs()})
	if err == nil {
		_ = udp.Send(address, transport.Packet{Kind: transport.PacketChatSync, Sequence: a.sequence.Add(1), Payload: payload})
	}
}

func (a *App) respondHistorySync(udp *transport.UDP, address *net.UDPAddr, knownIDs []string) {
	if a.historyStore == nil || address == nil {
		return
	}
	missing, err := a.historyStore.MessagesMissingFrom(knownIDs)
	if err != nil {
		return
	}
	payload, err := room.Encode(room.Envelope{Kind: room.SyncResponseMessage, Messages: missing})
	if err == nil {
		_ = udp.Send(address, transport.Packet{Kind: transport.PacketChatSync, Sequence: a.sequence.Add(1), Payload: payload})
	}
}

func (a *App) handleSyncPacket(udp *transport.UDP, addr *net.UDPAddr, payload []byte, host bool) {
	envelope, err := room.Decode(payload)
	if err != nil {
		return
	}
	if envelope.Kind == room.SyncRequestMessage {
		if host {
			a.respondHistorySync(udp, addr, envelope.KnownIDs)
			envelope.ReplyTo = addr.String()
			forwarded, encodeErr := room.Encode(envelope)
			if encodeErr == nil {
				udp.Broadcast(transport.Packet{Kind: transport.PacketChatSync, Sequence: a.sequence.Add(1), Payload: forwarded}, addr)
			}
			return
		}
		if envelope.ReplyTo == "" {
			return
		}
		reply, resolveErr := net.ResolveUDPAddr("udp", envelope.ReplyTo)
		if resolveErr == nil {
			a.respondHistorySync(udp, reply, envelope.KnownIDs)
		}
		return
	}
	if envelope.Kind == room.SyncResponseMessage {
		for _, message := range envelope.Messages {
			a.addChatMessage(message)
			if message.ImageID == "" || a.historyStore == nil {
				continue
			}
			if _, readErr := a.historyStore.ReadImage(message.ImageID, message.ImageExt); readErr == nil {
				continue
			}
			request := assets.MissingRequest{Kind: assets.KindImage, ID: message.ImageID, Ext: message.ImageExt}
			_ = udp.Send(addr, transport.Packet{Kind: transport.PacketAssetMissing, Sequence: a.sequence.Add(1), Payload: assets.EncodeMissing(request)})
		}
	}
}

func (a *App) refreshPeersFromRoom() {
	a.peerMu.Lock()
	lagByAddress := make(map[string]time.Duration, len(a.peerNames))
	for _, peer := range a.peerNames {
		lagByAddress[peer.address] = peer.lag
	}
	a.peerNames = nil
	if a.transport != nil {
		selfName := a.cfg.Username
		if selfName == "" {
			selfName = "Usuario"
		}
		a.peerNames = append(a.peerNames, peerView{
			id:        a.participantID,
			name:      selfName,
			address:   "127.0.0.1",
			isSelf:    true,
			speaking:  a.voiceActive.Load(),
			speakStop: time.Now().Add(250 * time.Millisecond),
		})
	}
	for _, participant := range a.roomState.Participants {
		if participant.ID == a.participantID || !participant.Connected {
			continue
		}
		a.peerNames = append(a.peerNames, peerView{
			id:      participant.ID,
			name:    participant.Username,
			address: participant.Address,
			lag:     lagByAddress[participant.Address],
		})
	}
	a.peerMu.Unlock()
	a.refreshPeerAvatars()
}

func (a *App) sendChatMessage(text string) {
	text = strings.TrimSpace(text)
	if text == "" || a.transport == nil {
		return
	}
	message := history.Message{ID: history.NewID(a.participantID), Timestamp: time.Now().UTC(), SenderID: a.participantID, Username: a.cfg.Username, Kind: "text", Text: text}
	a.addChatMessage(message)
	a.playEventSound(audio.SFXMessage)
	payload, err := room.Encode(room.Envelope{Kind: room.ChatMessage, Message: &message})
	if err != nil {
		return
	}
	if a.chatInput != nil {
		a.chatInput.SetText("")
	}
	a.sendChatPayload(payload)
}

func (a *App) sendChatPayload(payload []byte) {
	if a.transport == nil {
		return
	}
	if a.roomState.HostID == a.participantID {
		a.transport.Broadcast(transport.Packet{Kind: transport.PacketChat, Sequence: a.sequence.Add(1), Payload: payload}, nil)
		return
	}
	peer, err := net.ResolveUDPAddr("udp", a.endpoint)
	if err == nil {
		_ = a.transport.Send(peer, transport.Packet{Kind: transport.PacketChat, Sequence: a.sequence.Add(1), Payload: payload})
	}
}

func (a *App) selectChatImage() {
	defer func() {
		if value := recover(); value != nil {
			logging.Panic(value)
			a.setStatus("No se pudo abrir el selector de imágenes.")
		}
	}()
	if a.historyStore == nil || a.transport == nil {
		a.setStatus("Conecta a una sala antes de enviar una imagen.")
		return
	}
	path, err := selectImageFile()
	if err != nil {
		if err.Error() != "selección cancelada" {
			logging.Errorf("selector de imágenes: %s", logging.FormatError(err))
		}
		return
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		logging.Errorf("leer imagen %q: %s", path, logging.FormatError(readErr))
		a.setStatus("No se pudo leer la imagen: " + readErr.Error())
		return
	}
	if len(data) > assets.MaxBytes {
		a.setStatus(fmt.Sprintf("El archivo supera el límite de %d MiB.", assets.MaxBytes/1024/1024))
		return
	}
	imageID := history.NewID(a.participantID)
	extension := filepath.Ext(path)
	if extension == "" {
		extension = ".bin"
	}
	path, saveErr := a.historyStore.SaveImage(imageID, extension, data)
	if saveErr != nil {
		logging.Errorf("guardar imagen %q: %s", path, logging.FormatError(saveErr))
		a.setStatus("No se pudo guardar la imagen: " + saveErr.Error())
		return
	}
	message := history.Message{ID: history.NewID(a.participantID), Timestamp: time.Now().UTC(), SenderID: a.participantID, Username: a.cfg.Username, Kind: "image", ImageID: imageID, ImageExt: extension, ImagePath: path}
	a.addChatMessage(message)
	a.playEventSound(audio.SFXMessage)
	payload, encodeErr := room.Encode(room.Envelope{Kind: room.ChatMessage, Message: &message})
	if encodeErr == nil {
		a.sendChatPayload(payload)
	}
	a.sendAsset(assets.KindImage, imageID, extension, data)
}

func (a *App) stopConnection() {
	if a.transport == nil {
		a.setStatus("No hay una sala o conexión activa.")
		return
	}
	udp := a.transport
	oldEndpoint := a.endpoint
	a.transport = nil
	a.endpoint = ""
	a.activeRoomName = ""
	a.peerMu.Lock()
	a.peerNames = nil
	a.peerMu.Unlock()
	a.refreshPeerAvatars()
	if a.createButton != nil {
		a.createButton.Enable()
	}
	if a.stopButton != nil {
		a.stopButton.Disable()
	}
	if a.topHangupBtn != nil {
		a.topHangupBtn.Disable()
	}
	if a.connectButton != nil {
		a.connectButton.Enable()
	}
	if a.leaveButton != nil {
		a.leaveButton.Disable()
	}
	a.showConnectionPanels()
	if a.endpointEntry != nil && a.cfg.LastPeer != "" {
		a.endpointEntry.SetText(a.cfg.LastPeer)
	}
	a.setStatus("Sala detenida.")

	go func() {
		a.playEventSound(audio.SFXDisconnect)
		time.Sleep(150 * time.Millisecond)
		if a.audioEngine != nil {
			a.audioEngine.Stop()
			a.audioEngine = nil
		}
		a.soundMu.Lock()
		if a.soundPlayer != nil {
			a.soundPlayer.Stop()
			a.soundPlayer = nil
		}
		a.soundMu.Unlock()
		if a.pingStop != nil {
			close(a.pingStop)
			a.pingStop = nil
		}
		if !a.hostMode.Load() && oldEndpoint != "" {
			peer, err := net.ResolveUDPAddr("udp", oldEndpoint)
			if err == nil {
				participant := room.Participant{ID: a.participantID, Username: a.cfg.Username, Address: udp.Address().String()}
				payload, _ := room.Encode(room.Envelope{Kind: room.LeaveMessage, Participant: &participant})
				_ = udp.Send(peer, transport.Packet{Kind: transport.PacketRoomLeave, Sequence: a.sequence.Add(1), Payload: payload})
			}
		} else if a.hostMode.Load() {
			localPort := uint16(udp.Address().Port)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = netinfo.UnmapUDP(ctx, localPort)
			cancel()
			participant := room.Participant{ID: a.participantID, Username: a.cfg.Username, Address: udp.Address().String()}
			payload, _ := room.Encode(room.Envelope{Kind: room.LeaveMessage, Participant: &participant})
			udp.Broadcast(transport.Packet{Kind: transport.PacketRoomLeave, Sequence: a.sequence.Add(1), Payload: payload}, nil)
		}
		if err := udp.Close(); err != nil {
			logging.Errorf("cerrar UDP: %s", logging.FormatError(err))
		}
	}()
}

func (a *App) createHost() {
	root, _ := os.UserConfigDir()
	existingRooms, _ := history.ListRooms(filepath.Join(root, "VoxMesh"))

	var d dialog.Dialog

	newRoomEntry := widget.NewEntry()
	newRoomEntry.SetPlaceHolder("Ej: Charla, Gaming...")
	newRoomEntry.SetText(a.cfg.RoomName)

	btnCreate := widget.NewButtonWithIcon("Crear y abrir", theme.ContentAddIcon(), func() {
		roomName := strings.TrimSpace(newRoomEntry.Text)
		if roomName == "" {
			a.setStatus("Escribe un nombre para la nueva sala.")
			return
		}
		d.Hide()
		username := strings.TrimSpace(a.cfg.Username)
		if username == "" {
			username = "Usuario"
		}
		a.openHost(roomName, username)
	})
	newRoomEntry.OnSubmitted = func(string) {
		btnCreate.OnTapped()
	}

	var content *fyne.Container

	if len(existingRooms) > 0 {
		existingSelect := widget.NewSelect(existingRooms, nil)
		existingSelect.PlaceHolder = "Seleccionar de la lista..."
		if a.cfg.RoomName != "" {
			for _, r := range existingRooms {
				if r == a.cfg.RoomName {
					existingSelect.SetSelected(r)
					break
				}
			}
		}

		btnOpenExisting := widget.NewButtonWithIcon("Abrir seleccionada", theme.MediaPlayIcon(), func() {
			roomName := strings.TrimSpace(existingSelect.Selected)
			if roomName == "" {
				a.setStatus("Selecciona una sala guardada de la lista.")
				return
			}
			d.Hide()
			username := strings.TrimSpace(a.cfg.Username)
			if username == "" {
				username = "Usuario"
			}
			a.openHost(roomName, username)
		})

		content = container.NewVBox(
			widget.NewLabelWithStyle("Salas guardadas", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			container.NewBorder(nil, nil, nil, btnOpenExisting, existingSelect),
			widget.NewSeparator(),
			widget.NewLabelWithStyle("Nueva sala", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			container.NewBorder(nil, nil, nil, btnCreate, newRoomEntry),
		)
	} else {
		content = container.NewVBox(
			widget.NewLabelWithStyle("Nueva sala", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			container.NewBorder(nil, nil, nil, btnCreate, newRoomEntry),
		)
	}

	d = dialog.NewCustom("Iniciar Sala", "Cancelar", content, a.window)
	d.Resize(fyne.NewSize(440, 220))
	d.Show()
}

func (a *App) openHost(roomName, username string) {
	if a.transport != nil {
		a.setStatus("La sala ya está activa en " + a.endpoint)
		return
	}
	listenAddr := a.cfg.ListenAddress
	if listenAddr == "" {
		listenAddr = ":47830"
	}
	var udp *transport.UDP
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		udp, err = transport.Listen(listenAddr)
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		a.setStatus(fmt.Sprintf("No se pudo abrir el puerto %s: %s (comprueba que no haya otra copia de VoxMesh abierta)", listenAddr, err.Error()))
		return
	}
	a.transport = udp
	a.hostMode.Store(true)
	a.cfg.RoomName = roomName
	a.cfg.Username = username
	if a.selfNameLabel != nil {
		a.selfNameLabel.SetText(username)
	}
	a.participantID = room.ParticipantID(username, udp.Address().String())
	state := room.New(roomName, username, udp.Address())
	if len(state.Participants) > 0 {
		state.Participants[0].CanBeHost = a.cfg.AllowHostMigration
	}
	a.roomState = state
	a.roomStore = room.NewStore(state)
	a.initHistory(roomName)
	a.refreshPeersFromRoom()
	a.pingStop = make(chan struct{})
	a.createButton.Disable()
	a.stopButton.Enable()
	if a.topHangupBtn != nil {
		a.topHangupBtn.Enable()
	}
	a.hideConnectionPanels()
	port := udp.Address().Port
	localIP, err := netinfo.LocalIPv4()
	if err == nil && localIP != nil {
		a.endpoint = net.JoinHostPort(localIP.String(), strconv.Itoa(port))
	} else {
		a.endpoint = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	}
	for i := range a.roomState.Participants {
		if a.roomState.Participants[i].ID == a.participantID {
			a.roomState.Participants[i].Address = a.endpoint
			break
		}
	}
	a.setStatus("Sala abierta en UDP " + strconv.Itoa(port) + "; buscando IP pública...")
	go a.receiveLoop(udp)
	go a.pingPeers(udp, a.pingStop)
	go a.sweepAssetLoop(udp, a.pingStop)
	a.startAudio(udp, nil, true)
	go a.configureHostNetwork(port)
	a.playEventSound(audio.SFXConnect)
}

func (a *App) configureHostNetwork(port int) {
	mapContext, cancelMap := context.WithTimeout(context.Background(), 8*time.Second)
	mapErr := netinfo.TryMapUDP(mapContext, uint16(port), 3600)
	cancelMap()
	if mapErr != nil {
		logging.Errorf("UPnP UDP puerto %d: %s", port, logging.FormatError(mapErr))
	} else {
		logging.Infof("UPnP UDP activo en el puerto %d", port)
	}

	ipContext, cancelIP := context.WithTimeout(context.Background(), 4*time.Second)
	ip, ipErr := netinfo.PublicIP(ipContext)
	cancelIP()
	if ipErr != nil {
		logging.Errorf("obtener IP pública: %s", logging.FormatError(ipErr))
		if mapErr == nil {
			a.setStatus("UPnP activo. IP pública no disponible; usa la dirección local de la sala.")
			return
		}
		a.setStatus("UDP activo. No se pudo abrir UPnP ni obtener la IP pública; configura el router.")
		return
	}

	a.endpoint = net.JoinHostPort(ip.String(), strconv.Itoa(port))
	for i := range a.roomState.Participants {
		if a.roomState.Participants[i].ID == a.participantID {
			a.roomState.Participants[i].Address = a.endpoint
			break
		}
	}
	if a.roomStore != nil {
		a.roomStore.Replace(a.roomState)
	}
	if mapErr == nil {
		a.setStatus("Sala lista (UPnP): " + a.endpoint)
		return
	}
	a.setStatus("Comparte: " + a.endpoint + " (UPnP no disponible)")
}

func (a *App) connectClient(raw string) {
	if a.transport != nil {
		a.setStatus("Ya existe una conexión UDP.")
		return
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		a.setStatus("Ingresá la IP del host.")
		return
	}
	if !strings.Contains(raw, ":") {
		raw = net.JoinHostPort(raw, "47830")
	}
	addr, err := net.ResolveUDPAddr("udp", raw)
	if err != nil {
		a.setStatus("Dirección inválida: " + err.Error())
		return
	}
	listenAddr := a.cfg.ListenAddress
	if listenAddr == "" {
		listenAddr = ":47830"
	}
	udp, err := transport.Listen(listenAddr)
	if err != nil {
		if listenAddr != ":0" {
			udp, err = transport.Listen(":0")
		}
		if err != nil {
			a.setStatus("No se pudo abrir UDP: " + err.Error())
			return
		}
	}
	a.transport = udp
	a.hostMode.Store(false)
	a.connectButton.Disable()
	a.leaveButton.Enable()
	if a.topHangupBtn != nil {
		a.topHangupBtn.Enable()
	}
	a.cfg.RoomName = strings.TrimSpace(a.cfg.RoomName)
	if a.selfNameLabel != nil {
		a.selfNameLabel.SetText(a.cfg.Username)
	}
	a.participantID = room.ParticipantID(a.cfg.Username, udp.Address().String())
	a.roomState = room.State{Name: a.cfg.RoomName, Participants: []room.Participant{{
		ID:          a.participantID,
		Username:    a.cfg.Username,
		Address:     udp.Address().String(),
		Order:       0,
		Connected:   true,
		CanBeHost:   a.cfg.AllowHostMigration,
		LastSeenUTC: time.Now().UTC(),
	}}}
	a.initHistory(a.cfg.RoomName)
	a.refreshPeersFromRoom()
	a.cfg.LastPeer = raw
	_ = config.Save(a.cfgPath, a.cfg)
	if a.endpointEntry != nil {
		a.endpointEntry.SetText(raw)
	}
	a.endpoint = addr.String()
	participant := room.Participant{
		ID:          a.participantID,
		Username:    a.cfg.Username,
		Address:     udp.Address().String(),
		Connected:   true,
		CanBeHost:   a.cfg.AllowHostMigration,
		LastSeenUTC: time.Now().UTC(),
	}
	payload, _ := room.Encode(room.Envelope{Kind: room.HelloMessage, Room: &a.roomState, Participant: &participant})
	_ = udp.Send(addr, transport.Packet{Kind: transport.PacketRoomHello, Sequence: a.sequence.Add(1), Payload: payload})
	a.setStatus("Conectando con " + raw)
	a.startAudio(udp, addr, false)
	a.lastHostSeen.Store(time.Now().UnixNano())
	go a.receiveLoop(udp)
	a.monitorStop = make(chan struct{})
	go a.monitorHost(udp, a.monitorStop)
	go a.sweepAssetLoop(udp, a.monitorStop)
	a.hideConnectionPanels()
	a.playEventSound(audio.SFXConnect)
}

func (a *App) startAudio(udp *transport.UDP, peer *net.UDPAddr, host bool) {
	inputDevice := a.cfg.InputDevice
	if inputDevice == phoneMicInput {
		inputDevice = "Sistema predeterminado"
	}
	engine, err := audio.NewEngine(inputDevice, a.cfg.OutputDevice, func(frame []byte) {
		if a.phoneMicEnabled() {
			return
		}
		a.sendOutgoingPCM(frame, udp, peer, host)
	})
	if err != nil {
		a.setStatus("Audio no disponible: " + err.Error())
		return
	}
	a.audioEngine = engine
}

func (a *App) restartAudioInRoom() {
	if a.audioEngine != nil {
		a.audioEngine.Stop()
		a.audioEngine = nil
	}
	if a.transport != nil {
		var peer *net.UDPAddr
		if !a.hostMode.Load() && a.endpoint != "" {
			peer, _ = net.ResolveUDPAddr("udp", a.endpoint)
		}
		a.startAudio(a.transport, peer, a.hostMode.Load())
	}
}

func (a *App) sendOutgoingPCM(frame []byte, udp *transport.UDP, peer *net.UDPAddr, host bool) {
	payload, shouldSend := a.prepareOutgoingPCM(frame)
	if !shouldSend {
		return
	}
	if a.cfg.LiveMonitoring && a.audioEngine != nil {
		a.audioEngine.Play(payload)
	}
	a.flashVoiceIndicator()
	idBytes := []byte(a.participantID)
	audioPayload := make([]byte, 1+len(idBytes)+len(payload))
	audioPayload[0] = byte(len(idBytes))
	copy(audioPayload[1:], idBytes)
	copy(audioPayload[1+len(idBytes):], payload)
	packet := transport.Packet{Kind: transport.PacketAudio, Sequence: a.sequence.Add(1), Payload: audioPayload}
	if host {
		udp.Broadcast(packet, nil)
		return
	}
	if peer == nil || peer.IP == nil || peer.IP.IsUnspecified() {
		if a.endpoint != "" {
			var err error
			peer, err = net.ResolveUDPAddr("udp", a.endpoint)
			if err != nil || peer.IP == nil || peer.IP.IsUnspecified() {
				return
			}
		} else {
			logging.Errorf("enviar audio saliente: host no disponible")
			return
		}
	}
	if err := udp.Send(peer, packet); err != nil {
		logging.Errorf("enviar audio saliente a %s: %s", peer, logging.FormatError(err))
	}
}

func (a *App) prepareIncomingPCM(frame []byte) []byte {
	if !a.cfg.OutputFilterEnabled {
		return frame
	}
	settings := audio.FilterSettings{
		GainDB:           a.cfg.OutputGainDB,
		HighPassEnabled:  a.cfg.OutputHighPassEnabled,
		HighPassHz:       a.cfg.OutputHighPassHz,
		LowPassEnabled:   a.cfg.OutputLowPassEnabled,
		LowPassHz:        a.cfg.OutputLowPassHz,
		LimiterEnabled:   a.cfg.OutputLimiterEnabled,
		LimiterThreshold: a.cfg.OutputLimiterDB,
	}
	filtered, err := a.outputEffects.Apply(frame, settings)
	if err != nil {
		logging.Errorf("procesar audio entrante: %s", logging.FormatError(err))
		return frame
	}
	return filtered
}

func (a *App) prepareOutgoingPCM(frame []byte) ([]byte, bool) {
	gate := audio.NoiseGate{ThresholdDB: a.cfg.ThresholdDB}
	settings := audio.FilterSettings{
		RNNoiseEnabled:      a.cfg.RNNoiseEnabled,
		VADEnabled:          a.cfg.VADEnabled,
		NoiseGateEnabled:    a.cfg.NoiseGateEnabled,
		GainDB:              a.cfg.InputGainDB,
		HighPassEnabled:     a.cfg.HighPassEnabled,
		HighPassHz:          a.cfg.HighPassHz,
		LowPassEnabled:      a.cfg.LowPassEnabled,
		LowPassHz:           a.cfg.LowPassHz,
		NotchEnabled:        a.cfg.NotchEnabled,
		NotchHz:             a.cfg.NotchHz,
		CompressorEnabled:   a.cfg.CompressorEnabled,
		CompressorThreshold: a.cfg.CompressorThresholdDB,
		CompressorRatio:     a.cfg.CompressorRatio,
		ExpanderEnabled:     a.cfg.ExpanderEnabled,
		ExpanderThreshold:   a.cfg.ExpanderThresholdDB,
		ExpanderRatio:       a.cfg.ExpanderRatio,
		LimiterEnabled:      a.cfg.LimiterEnabled,
		LimiterThreshold:    a.cfg.LimiterThresholdDB,
		VADHoldMS:           a.cfg.VADHoldMS,
		GateHoldMS:          a.cfg.GateHoldMS,
	}
	payload, shouldSend, err := audio.PrepareOutgoingPCMWithFilters(frame, a.voiceProcessor, gate, a.cfg.AudioFilterEnabled, a.cfg.VADThreshold, settings, a.effects)
	if err != nil || !shouldSend {
		if err != nil {
			logging.Errorf("procesar audio saliente: %s", logging.FormatError(err))
		}
		return nil, false
	}
	return payload, true
}

func (a *App) receiveLoop(udp *transport.UDP) {
	for {
		packet, addr, err := udp.Receive()
		if err != nil {
			logging.Infof("bucle UDP detenido: %s", logging.FormatError(err))
			return
		}
		if a.hostMode.Load() {
			udp.AddPeer(addr)
			for index := range a.roomState.Participants {
				if a.roomState.Participants[index].Address == addr.String() {
					a.roomState.Participants[index].LastSeenUTC = time.Now().UTC()
					break
				}
			}
			if packet.Kind == transport.PacketRoomHello {
				envelope, err := room.Decode(packet.Payload)
				if err != nil || envelope.Participant == nil {
					continue
				}
				envelope.Participant.Address = addr.String()
				envelope.Participant.Connected = true
				envelope.Participant.LastSeenUTC = time.Now().UTC()

				// Asegurar nombre único: si ya existe otro participante conectado con el mismo nombre, añadir _PUTO
				candidateName := strings.TrimSpace(envelope.Participant.Username)
				if candidateName == "" {
					candidateName = "Usuario"
				}
				for {
					taken := false
					for _, p := range a.roomState.Participants {
						if p.Connected && p.ID != envelope.Participant.ID && strings.EqualFold(strings.TrimSpace(p.Username), candidateName) {
							taken = true
							break
						}
					}
					if !taken {
						break
					}
					candidateName += "_PUTO"
				}
				envelope.Participant.Username = candidateName

				a.roomState.Participants = append([]room.Participant(nil), a.roomState.Participants...)
				if a.roomStore != nil {
					a.roomStore.Replace(a.roomState)
				}
				known := false
				for index := range a.roomState.Participants {
					if a.roomState.Participants[index].ID == envelope.Participant.ID {
						a.roomState.Participants[index] = *envelope.Participant
						known = true
						break
					}
				}
				if !known {
					envelope.Participant.Order = len(a.roomState.Participants)
					a.roomState.Participants = append(a.roomState.Participants, *envelope.Participant)
				}
				statePayload, _ := room.Encode(room.Envelope{Kind: room.StateMessage, Room: &a.roomState})
				_ = udp.Send(addr, transport.Packet{Kind: transport.PacketRoomState, Sequence: a.sequence.Add(1), Payload: statePayload})
				a.refreshPeersFromRoom()
				if candidate, ok := a.roomState.NextHost(); ok {
					if hostIP, _, err := net.SplitHostPort(candidate.Address); err == nil && hostIP != "" {
						candidateTarget := net.JoinHostPort(hostIP, "47830")
						a.cfg.LastPeer = candidateTarget
						_ = config.Save(a.cfgPath, a.cfg)
						if a.endpointEntry != nil {
							a.endpointEntry.SetText(candidateTarget)
						}
					}
				}
				logging.Infof("Cliente conectado: %s (%s)", envelope.Participant.Username, addr.String())
				joinedUser := envelope.Participant.Username
				go func(uName string) {
					if a.roomStore != nil {
						a.roomStore.Replace(a.roomState)
					}
					a.addSystemMessage(uName + " se conectó.")
				}(joinedUser)
				a.playEventSound(audio.SFXConnect)
				udp.Broadcast(transport.Packet{Kind: transport.PacketRoomState, Sequence: a.sequence.Add(1), Payload: statePayload}, addr)
			} else if packet.Kind == transport.PacketRoomLeave {
				envelope, err := room.Decode(packet.Payload)
				username := ""
				participantID := ""
				if err == nil && envelope.Participant != nil {
					username = envelope.Participant.Username
					participantID = envelope.Participant.ID
				}
				a.handleParticipantLeave(participantID, username, addr, udp)
			} else if packet.Kind == transport.PacketHello {
				username := strings.TrimSpace(string(packet.Payload))
				if username == "" {
					username = "Usuario"
				}
				candidate := username
				a.peerMu.Lock()
				for {
					taken := false
					for _, p := range a.peerNames {
						if p.address != addr.String() && strings.EqualFold(p.name, candidate) {
							taken = true
							break
						}
					}
					if !taken && strings.EqualFold(a.cfg.Username, candidate) {
						taken = true
					}
					if !taken {
						break
					}
					candidate += "_PUTO"
				}
				a.peerMu.Unlock()
				if a.addPeer(candidate, addr.String()) {
					a.playEventSound(audio.SFXConnect)
				}
			}
			if packet.Kind == transport.PacketPing && len(packet.Payload) == 8 {
				_ = udp.Send(addr, transport.Packet{Kind: transport.PacketPong, Sequence: packet.Sequence, Payload: packet.Payload})
			}
			if packet.Kind == transport.PacketPong && len(packet.Payload) == 8 {
				timestamp := int64(binary.BigEndian.Uint64(packet.Payload))
				a.updatePeerLag(addr.String(), time.Since(time.Unix(0, timestamp)))
			}
			if packet.Kind == transport.PacketAudio {
				senderID := ""
				pcm := packet.Payload
				if len(packet.Payload) > 1 && int(packet.Payload[0]) > 0 && len(packet.Payload) > 1+int(packet.Payload[0]) {
					idLen := int(packet.Payload[0])
					senderID = string(packet.Payload[1 : 1+idLen])
					pcm = packet.Payload[1+idLen:]
				}
				if len(pcm) >= 960 && len(pcm)%2 == 0 && a.isAudioSequenceValid(senderID, packet.Sequence) {
					a.markPeerSpeaking(senderID, addr.String())
					if a.audioEngine != nil {
						a.audioEngine.PlayStream(senderID, a.prepareIncomingPCM(pcm))
					}
				}
				udp.Broadcast(packet, addr)
			}
			if packet.Kind == transport.PacketChat {
				a.handleChatPacket(packet.Payload)
				udp.Broadcast(packet, addr)
			}
			if packet.Kind == transport.PacketChatSync {
				a.handleSyncPacket(udp, addr, packet.Payload, true)
			}
			if packet.Kind == transport.PacketAssetChunk {
				a.handleAssetChunk(packet.Payload, addr.String())
				udp.Broadcast(packet, addr)
			}
			if packet.Kind == transport.PacketAssetMissing {
				a.handleAssetMissing(udp, addr, packet.Payload)
			}
		} else if packet.Kind == transport.PacketPing {
			a.lastHostSeen.Store(time.Now().UnixNano())
			_ = udp.Send(addr, transport.Packet{Kind: transport.PacketPong, Sequence: packet.Sequence, Payload: packet.Payload})
		} else if packet.Kind == transport.PacketPong && len(packet.Payload) == 8 {
			timestamp := int64(binary.BigEndian.Uint64(packet.Payload))
			a.updatePeerLag(addr.String(), time.Since(time.Unix(0, timestamp)))
		} else if packet.Kind == transport.PacketRoomState {
			envelope, err := room.Decode(packet.Payload)
			if err == nil && envelope.Room != nil {
				a.handleRoomState(*envelope.Room, udp)
			}
		} else if packet.Kind == transport.PacketRoomLeave {
			envelope, err := room.Decode(packet.Payload)
			username := "El host"
			if err == nil && envelope.Participant != nil && envelope.Participant.Username != "" {
				username = envelope.Participant.Username
			}
			logging.Infof("Notificación de desconexión recibida: %s", username)
			go a.addSystemMessage(username + " se desconectó.")
			a.playEventSound(audio.SFXDisconnect)
			if envelope.Participant != nil {
				a.roomState.MarkDisconnected(envelope.Participant.ID)
				a.refreshPeersFromRoom()
			}
		} else if packet.Kind == transport.PacketChat {
			a.handleChatPacket(packet.Payload)
		} else if packet.Kind == transport.PacketChatSync {
			a.handleSyncPacket(udp, addr, packet.Payload, false)
		} else if packet.Kind == transport.PacketAssetChunk {
			a.handleAssetChunk(packet.Payload, addr.String())
		} else if packet.Kind == transport.PacketAssetMissing {
			a.handleAssetMissing(udp, addr, packet.Payload)
		} else if packet.Kind == transport.PacketHello {
			a.setStatus("Conectado a " + addr.String())
		} else if packet.Kind == transport.PacketAudio {
			senderID := ""
			pcm := packet.Payload
			if len(packet.Payload) > 1 && int(packet.Payload[0]) > 0 && len(packet.Payload) > 1+int(packet.Payload[0]) {
				idLen := int(packet.Payload[0])
				senderID = string(packet.Payload[1 : 1+idLen])
				pcm = packet.Payload[1+idLen:]
			}
			if len(pcm) >= 960 && len(pcm)%2 == 0 && a.isAudioSequenceValid(senderID, packet.Sequence) {
				a.markPeerSpeaking(senderID, addr.String())
				if a.audioEngine != nil {
					a.audioEngine.PlayStream(senderID, a.prepareIncomingPCM(pcm))
				}
			}
		}
	}
}

func (a *App) handleParticipantLeave(participantID, username string, addr *net.UDPAddr, udp *transport.UDP) {
	if addr != nil {
		udp.RemovePeer(addr)
	}
	found := false
	for index := range a.roomState.Participants {
		p := &a.roomState.Participants[index]
		if (participantID != "" && p.ID == participantID) || (addr != nil && p.Address == addr.String()) {
			if !p.Connected {
				return
			}
			p.Connected = false
			p.LastSeenUTC = time.Now().UTC()
			if username == "" {
				username = p.Username
			}
			found = true
			break
		}
	}
	if !found && username == "" {
		if addr != nil {
			username = addr.String()
		} else {
			username = "Usuario"
		}
	}
	addrStr := ""
	if addr != nil {
		addrStr = addr.String()
	}
	logging.Infof("Cliente desconectado: %s (%s)", username, addrStr)
	a.refreshPeersFromRoom()
	leftName := username
	go func(uName string) {
		a.addSystemMessage(uName + " se desconectó.")
		if a.roomStore != nil {
			a.roomStore.Replace(a.roomState)
		}
	}(leftName)
	a.playEventSound(audio.SFXDisconnect)
	statePayload, _ := room.Encode(room.Envelope{Kind: room.StateMessage, Room: &a.roomState})
	udp.Broadcast(transport.Packet{Kind: transport.PacketRoomState, Sequence: a.sequence.Add(1), Payload: statePayload}, addr)
}

func (a *App) handleRoomState(state room.State, udp *transport.UDP) {
	previousHost := a.roomState.HostID
	for _, p := range state.Participants {
		if p.ID == a.participantID {
			if p.Username != "" && p.Username != a.cfg.Username {
				oldName := a.cfg.Username
				a.cfg.Username = p.Username
				_ = config.Save(a.cfgPath, a.cfg)
				if a.selfNameLabel != nil {
					a.selfNameLabel.SetText(p.Username)
				}
				a.setStatus("Nombre en uso. Tu nuevo nombre es: " + p.Username)
				a.addSystemMessage("Tu nombre de usuario se cambió a " + p.Username + " porque ya existía otro en la sala.")
				logging.Infof("Nombre cambiado por el host de '%s' a '%s' por duplicado", oldName, p.Username)
			}
			continue
		}
		if !p.Connected {
			continue
		}
		wasConnected := false
		for _, prev := range a.roomState.Participants {
			if prev.ID == p.ID && prev.Connected {
				wasConnected = true
				break
			}
		}
		if !wasConnected && len(a.roomState.Participants) > 1 {
			a.playEventSound(audio.SFXConnect)
		}
	}
	a.roomState = state
	a.refreshPeersFromRoom()
	if state.Name != "" && state.Name != a.activeRoomName {
		a.activeRoomName = state.Name
		a.cfg.RoomName = state.Name
		a.initHistory(state.Name)
	}
	if state.HostID == a.participantID || a.hostMode.Load() {
		return
	}
	if previousHost != "" && state.HostID != previousHost {
		for _, participant := range state.Participants {
			if participant.ID == state.HostID {
				address, err := net.ResolveUDPAddr("udp", participant.Address)
				if err == nil && address.IP != nil && !address.IP.IsUnspecified() {
					a.endpoint = address.String()
					a.cfg.LastPeer = a.endpoint
					_ = config.Save(a.cfgPath, a.cfg)
					if a.endpointEntry != nil {
						a.endpointEntry.SetText(a.endpoint)
					}
					if a.audioEngine != nil {
						a.audioEngine.Stop()
					}
					a.startAudio(udp, address, false)
					a.sendRoomHello(udp, address)
					a.sendHistorySync(udp, address)
				}
				break
			}
		}
	}
}

func (a *App) sendRoomHello(udp *transport.UDP, address *net.UDPAddr) {
	participant := room.Participant{
		ID:          a.participantID,
		Username:    a.cfg.Username,
		Address:     udp.Address().String(),
		Connected:   true,
		CanBeHost:   a.cfg.AllowHostMigration,
		LastSeenUTC: time.Now().UTC(),
	}
	payload, _ := room.Encode(room.Envelope{Kind: room.HelloMessage, Room: &a.roomState, Participant: &participant})
	_ = udp.Send(address, transport.Packet{Kind: transport.PacketRoomHello, Sequence: a.sequence.Add(1), Payload: payload})
	a.sendHistorySync(udp, address)
}

func (a *App) monitorHost(udp *transport.UDP, stop <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var failedHost string
	var candidateID string
	var candidateDeadline time.Time
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if !a.hostMode.Load() && a.endpoint != "" {
				if hostAddr, err := net.ResolveUDPAddr("udp", a.endpoint); err == nil {
					payload := make([]byte, 8)
					binary.BigEndian.PutUint64(payload, uint64(time.Now().UnixNano()))
					_ = udp.Send(hostAddr, transport.Packet{Kind: transport.PacketPing, Sequence: a.sequence.Add(1), Payload: payload})
				}
			}
			if a.hostMode.Load() || time.Since(time.Unix(0, a.lastHostSeen.Load())) < 6*time.Second {
				continue
			}
			if failedHost == "" {
				failedHost = a.roomState.HostID
				a.roomState.MarkDisconnected(failedHost)
				a.addSystemMessage("El host se desconectó.")
			}
			candidate, ok := a.roomState.NextHost()
			if !ok {
				return
			}
			if candidate.ID == a.participantID {
				a.becomeHost(udp)
				return
			}
			if candidate.ID != candidateID {
				candidateID = candidate.ID
				candidateDeadline = time.Now().Add(6 * time.Second)
				continue
			}
			if time.Now().After(candidateDeadline) {
				a.addSystemMessage("El candidato " + candidate.Username + " no está disponible.")
				a.roomState.MarkDisconnected(candidate.ID)
				candidateID = ""
			}
		}
	}
}

func (a *App) becomeHost(udp *transport.UDP) {
	a.hostMode.Store(true)
	a.roomState.HostID = a.participantID
	a.roomState.Epoch++
	for i := range a.roomState.Participants {
		if a.roomState.Participants[i].ID == a.participantID {
			a.roomState.Participants[i].CanBeHost = true
			break
		}
	}
	go a.configureHostNetwork(udp.Address().Port)
	for _, participant := range a.roomState.Participants {
		if participant.ID == a.participantID {
			continue
		}
		address, err := net.ResolveUDPAddr("udp", participant.Address)
		if err == nil {
			udp.AddPeer(address)
		}
	}
	if a.audioEngine != nil {
		a.audioEngine.Stop()
	}
	a.startAudio(udp, nil, true)
	a.addSystemMessage(a.cfg.Username + " será el nuevo host.")
	a.addSystemMessage("Creando nuevamente la sala...")
	payload, _ := room.Encode(room.Envelope{Kind: room.StateMessage, Room: &a.roomState})
	udp.Broadcast(transport.Packet{Kind: transport.PacketRoomState, Sequence: a.sequence.Add(1), Payload: payload}, nil)
	a.addSystemMessage("Sala disponible.")
	a.addSystemMessage("Reconectando participantes...")
	if a.pingStop == nil {
		a.pingStop = make(chan struct{})
	}
	go a.pingPeers(udp, a.pingStop)
	go a.sweepAssetLoop(udp, a.pingStop)
}

func (a *App) pingPeers(udp *transport.UDP, stop <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			now := time.Now().UTC()
			for _, peer := range udp.Peers() {
				payload := make([]byte, 8)
				binary.BigEndian.PutUint64(payload, uint64(time.Now().UnixNano()))
				_ = udp.Send(peer, transport.Packet{Kind: transport.PacketPing, Sequence: a.sequence.Add(1), Payload: payload})
			}
			for index := range a.roomState.Participants {
				p := &a.roomState.Participants[index]
				if p.ID == a.participantID || !p.Connected {
					continue
				}
				if !p.LastSeenUTC.IsZero() && now.Sub(p.LastSeenUTC) > 8*time.Second {
					peerAddr, _ := net.ResolveUDPAddr("udp", p.Address)
					a.handleParticipantLeave(p.ID, p.Username, peerAddr, udp)
				}
			}
		}
	}
}

func (a *App) refreshPeerAvatars() {
	if a.peerHBox == nil {
		return
	}
	fyne.Do(func() {
		a.peerMu.RLock()
		defer a.peerMu.RUnlock()
		if len(a.peerNames) == 0 {
			a.peerHBox.Objects = []fyne.CanvasObject{widget.NewLabel("Sin usuarios")}
			a.peerHBox.Refresh()
			return
		}
		now := time.Now()
		if len(a.peerHBox.Objects) == len(a.peerNames) {
			canUpdateInPlace := true
			for i, peer := range a.peerNames {
				grid, ok := a.peerHBox.Objects[i].(*fyne.Container)
				if !ok || len(grid.Objects) == 0 {
					canUpdateInPlace = false
					break
				}
				stack, ok := grid.Objects[0].(*fyne.Container)
				if !ok || len(stack.Objects) < 2 {
					canUpdateInPlace = false
					break
				}
				circle, okCircle := stack.Objects[0].(*canvas.Circle)
				center, okCenter := stack.Objects[1].(*fyne.Container)
				if !okCircle || !okCenter || len(center.Objects) == 0 {
					canUpdateInPlace = false
					break
				}
				txt, okTxt := center.Objects[0].(*canvas.Text)
				if !okTxt {
					canUpdateInPlace = false
					break
				}

				isSpeaking := peer.speaking && now.Before(peer.speakStop)
				targetCircleColor := color.NRGBA{R: 60, G: 70, B: 85, A: 255}
				targetTxtColor := color.NRGBA{R: 240, G: 245, B: 255, A: 255}
				if isSpeaking {
					targetCircleColor = color.NRGBA{R: 46, G: 204, B: 113, A: 255}
					targetTxtColor = color.NRGBA{R: 15, G: 35, B: 15, A: 255}
				}
				if circle.FillColor != targetCircleColor {
					circle.FillColor = targetCircleColor
					circle.Refresh()
				}
				if txt.Color != targetTxtColor {
					txt.Color = targetTxtColor
					txt.Refresh()
				}
			}
			if canUpdateInPlace {
				return
			}
		}

		objects := make([]fyne.CanvasObject, 0, len(a.peerNames))
		for _, peer := range a.peerNames {
			p := peer
			initial := "U"
			trimmed := strings.TrimSpace(p.name)
			if len(trimmed) > 0 {
				initial = strings.ToUpper(string([]rune(trimmed)[:1]))
			}
			isSpeaking := p.speaking && now.Before(p.speakStop)
			bg := canvas.NewCircle(color.NRGBA{R: 60, G: 70, B: 85, A: 255})
			bg.Resize(fyne.NewSize(28, 28))
			txt := canvas.NewText(initial, color.NRGBA{R: 240, G: 245, B: 255, A: 255})
			if isSpeaking {
				bg.FillColor = color.NRGBA{R: 46, G: 204, B: 113, A: 255}
				txt.Color = color.NRGBA{R: 15, G: 35, B: 15, A: 255}
			}
			txt.TextStyle = fyne.TextStyle{Bold: true}
			txt.TextSize = 13
			txt.Alignment = fyne.TextAlignCenter
			avatarStack := container.NewStack(bg, container.NewCenter(txt))
			avatarBox := container.NewGridWrap(fyne.NewSize(28, 28), avatarStack)
			objects = append(objects, avatarBox)
		}
		a.peerHBox.Objects = objects
		a.peerHBox.Refresh()
	})
}

func (a *App) addPeer(name, address string) bool {
	a.peerMu.Lock()
	defer a.peerMu.Unlock()
	for index, peer := range a.peerNames {
		if (address != "" && peer.address == address) || peer.name == name {
			a.peerNames[index].name = name
			if address != "" {
				a.peerNames[index].address = address
			}
			return false
		}
	}
	a.peerNames = append(a.peerNames, peerView{name: name, address: address})
	a.refreshPeerAvatars()
	return true
}

func (a *App) updatePeerLag(address string, lag time.Duration) {
	a.peerMu.Lock()
	defer a.peerMu.Unlock()
	for index := range a.peerNames {
		if a.peerNames[index].address == address || strings.Contains(a.peerNames[index].name, "("+address+")") {
			a.peerNames[index].lag = lag
			return
		}
	}
}

func (a *App) markPeerSpeaking(id, address string) {
	a.peerMu.Lock()
	now := time.Now()
	for i := range a.peerNames {
		p := &a.peerNames[i]
		match := false
		if p.isSelf && (id == "" || id == a.participantID) {
			match = true
		} else if id != "" && p.id == id {
			match = true
		} else if address != "" && (p.address == address || strings.Contains(p.address, address) || strings.Contains(address, p.address)) {
			match = true
		}
		if match {
			wasSpeaking := p.speaking
			p.speaking = true
			p.speakStop = now.Add(350 * time.Millisecond)
			a.peerMu.Unlock()
			if !wasSpeaking {
				a.refreshPeerAvatars()
			}
			return
		}
	}
	a.peerMu.Unlock()
}

func (a *App) sweepSpeakingPeers() {
	a.peerMu.Lock()
	now := time.Now()
	changed := false
	for i := range a.peerNames {
		p := &a.peerNames[i]
		if p.speaking && (now.After(p.speakStop) || p.speakStop.Sub(now) < 50*time.Millisecond) {
			p.speaking = false
			changed = true
		}
	}
	a.peerMu.Unlock()
	if changed {
		a.refreshPeerAvatars()
	}
}
