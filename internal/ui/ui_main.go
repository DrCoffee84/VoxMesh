//go:build cgo

package ui

import (
	_ "embed"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/assets"
	"voxmesh/internal/audio"
	"voxmesh/internal/config"
	"voxmesh/internal/history"
	"voxmesh/internal/logging"
	"voxmesh/internal/phonemic"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
	"voxmesh/internal/updater"
	"voxmesh/internal/version"
)

//go:embed icon.png
var appIconBytes []byte

type App struct {
	window          fyne.Window
	cfg             config.Config
	cfgPath         string
	transport       *transport.UDP
	sequence        atomic.Uint32
	audioSequence   atomic.Uint32 // secuencia propia del audio saliente (contigua, ver transport.FlagAudioSequence)
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
	peerMu          logging.TracedRWMutex
	recordingMu     logging.TracedRWMutex
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
	chatMu          logging.TracedRWMutex
	selfNameLabel   *widget.Label
	voiceIndicator  *canvas.Circle
	voiceTimerMu    sync.Mutex
	voiceTimer      *time.Timer
	voiceActive     atomic.Bool
	audioSeqMu      sync.Mutex
	peerAudioSeq    map[string]uint32
	peerAudioTime   map[string]time.Time
	phoneMicMu      logging.TracedRWMutex
	phoneMic        *phonemic.Server
	previewMu       logging.TracedRWMutex
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
	soundMu         logging.TracedRWMutex
	sounds          []room.Sound
	pendingPlays    map[string]struct{}
}

const phoneMicInput = "Micrófono del celular"

func New(executable string) *App {
	cfgPath := config.Path(executable)
	cfg, _ := config.Load(cfgPath)
	logging.SetLevel(cfg.LogLevel)
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
		peerMu:         logging.NewTracedRWMutex("peerMu"),
		recordingMu:    logging.NewTracedRWMutex("recordingMu"),
		chatMu:         logging.NewTracedRWMutex("chatMu"),
		phoneMicMu:     logging.NewTracedRWMutex("phoneMicMu"),
		previewMu:      logging.NewTracedRWMutex("previewMu"),
		soundMu:        logging.NewTracedRWMutex("soundMu"),
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
	if len(appIconBytes) > 0 {
		appIcon := fyne.NewStaticResource("icon.png", appIconBytes)
		application.SetIcon(appIcon)
	}
	a.window = application.NewWindow("VoxMesh")
	if len(appIconBytes) > 0 {
		appIcon := fyne.NewStaticResource("icon.png", appIconBytes)
		a.window.SetIcon(appIcon)
	}
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
	go func() {
		time.Sleep(1 * time.Second)
		a.checkForUpdates(false)
	}()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			debug.FreeOSMemory()
		}
	}()
	a.window.ShowAndRun()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (a *App) checkForUpdates(manual bool) {
	if manual {
		a.setStatus("Buscando actualizaciones...")
	}
	info, err := updater.CheckForUpdate(version.Value)
	if err != nil {
		if manual {
			fyne.Do(func() {
				a.setStatus("Error al buscar actualizaciones.")
				dialog.ShowError(err, a.window)
			})
		} else {
			logging.Errorf("verificar actualizaciones: %v", err)
		}
		return
	}
	if info == nil {
		if manual {
			fyne.Do(func() {
				a.setStatus("Tienes la versión más reciente.")
				dialog.ShowInformation("Actualizaciones", "Ya tienes instalada la versión más reciente ("+version.Value+").", a.window)
			})
		}
		return
	}

	fyne.Do(func() {
		a.promptUpdate(info)
	})
}

func (a *App) promptUpdate(info *updater.ReleaseInfo) {
	msg := fmt.Sprintf("¡Hay una nueva versión disponible!\n\nVersión nueva: %s\nVersión actual: %s", info.Version, version.Value)
	if info.Changelog != "" {
		msg += "\n\nNovedades:\n" + info.Changelog
	}
	msg += "\n\n¿Deseas actualizar ahora automáticamente?"

	dialog.ShowConfirm("Actualización disponible", msg, func(confirm bool) {
		if !confirm {
			return
		}
		a.executeAutoUpdate(info)
	}, a.window)
}

func (a *App) executeAutoUpdate(info *updater.ReleaseInfo) {
	progress := widget.NewProgressBar()
	progress.SetValue(0)
	statusText := widget.NewLabel("Descargando " + info.Version + "...")
	box := container.NewVBox(statusText, progress)

	updateDialog := dialog.NewCustomWithoutButtons("Actualizando VoxMesh", box, a.window)
	updateDialog.Show()

	go func() {
		err := updater.ApplyUpdate(info.DownloadURL, func(fraction float64) {
			fyne.Do(func() {
				progress.SetValue(fraction)
			})
		})
		if err != nil {
			fyne.Do(func() {
				updateDialog.Hide()
				dialog.ShowError(fmt.Errorf("no se pudo completar la actualización: %w", err), a.window)
			})
		}
	}()
}
