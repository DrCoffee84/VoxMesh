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
	cfg             atomic.Pointer[config.Config] // instantánea inmutable, ver config_store.go
	cfgPath         string
	cfgSave         chan struct{}
	cfgSaveMu       sync.Mutex
	cfgBaseline     atomic.Pointer[config.LiveSettings] // != nil con ajustes abiertos: se persiste esto, no el borrador
	transport       atomic.Pointer[transport.UDP]       // nil = sin sala, ver connection_state.go
	sequence        atomic.Uint32
	audioSequence   atomic.Uint32                     // secuencia propia del audio saliente (contigua, ver transport.FlagAudioSequence)
	encoder         atomic.Pointer[audio.OpusEncoder] // encoder Opus del audio saliente, se recrea al conectar
	sendMu          sync.Mutex                        // envío de audio: talkspurt, lastAudioSent, sendBuf
	talkspurt       uint8                             // número de frase del audio saliente (transport.Packet.Talkspurt)
	lastAudioSent   time.Time
	sendBuf         []byte
	endpoint        atomic.Pointer[endpointInfo]
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
	audioEngine     atomic.Pointer[audio.Engine]
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
	isMuted         atomic.Bool // mute del micrófono: lo lee el callback de captura por frame (sendOutgoingPCM)
	muteButton      *widget.Button
	peerVolumeMu    sync.RWMutex       // volumen por participante (slider de cada avatar)
	peerVolumes     map[string]float64 // por ID de participante; persistido por nombre en config.PeerVolumes
	peerChips       []*peerChip        // widgets de la barra de avatares; solo el hilo de UI
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
	soundFilter     atomic.Pointer[string] // texto del buscador en minúsculas; lo lee rebuildSoundboard desde cualquier goroutine
	soundPicking    atomic.Bool            // hay un selector de sonido abierto (evita abrir dos con doble clic)
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
	a := &App{
		cfgPath:        cfgPath,
		cfgSave:        make(chan struct{}, 1),
		voiceProcessor: voiceProcessor,
		effects:        &audio.Effects{},
		assetAssembler: assets.NewAssembler(),
		peerVolumes:    make(map[string]float64),
		peerMu:         logging.NewTracedRWMutex("peerMu"),
		recordingMu:    logging.NewTracedRWMutex("recordingMu"),
		chatMu:         logging.NewTracedRWMutex("chatMu"),
		phoneMicMu:     logging.NewTracedRWMutex("phoneMicMu"),
		previewMu:      logging.NewTracedRWMutex("previewMu"),
		soundMu:        logging.NewTracedRWMutex("soundMu"),
	}
	a.cfg.Store(&cfg)
	go a.configSaver()
	return a
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
	cfg := a.conf()
	a.window.Resize(fyne.NewSize(cfg.WindowWidth, cfg.WindowHeight))
	a.window.SetCloseIntercept(func() {
		if a.connected() {
			dialog.ShowConfirm("Salir de VoxMesh", "Estás en una sala activa. ¿Deseas salir y cerrar VoxMesh?", func(confirmed bool) {
				if confirmed {
					a.quit(a.stopConnectionAsync())
				}
			}, a.window)
		} else {
			a.quit(nil)
		}
	})
	a.window.SetContent(a.content())
	a.refreshRoomList("")
	if a.conf().InputDevice == phoneMicInput {
		if _, err := a.ensurePhoneMicServer(); err != nil {
			logging.Errorf("iniciar micrófono de celular: %s", logging.FormatError(err))
			a.setStatus("No se pudo iniciar el servidor del celular: " + err.Error())
		}
	}
	a.startHotkeyListener()
	a.startUIWatchdog()
	go a.audioMonitorLoop()
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
	// Equivale a ShowAndRun, pero maximizar necesita la ventana ya creada (el HWND existe
	// recién después de Show). Se muestra un instante con el tamaño normal guardado, que es
	// al que vuelve al restaurar.
	a.window.Show()
	if cfg.WindowMaximized {
		a.maximizeWindow()
	}
	application.Run()
}

// rememberWindowState guarda tamaño y maximizado para el próximo inicio. Hilo de UI (lee el
// canvas y el HWND). Maximizada o minimizada conserva el último tamaño normal guardado: el del
// canvas sería el de la pantalla, o basura si está minimizada.
func (a *App) rememberWindowState() {
	maximized, minimized := a.windowShowState()
	size := a.window.Canvas().Size()
	a.updateConfig(func(c *config.Config) {
		c.WindowMaximized = maximized
		if !maximized && !minimized && size.Width > 0 && size.Height > 0 {
			c.WindowWidth, c.WindowHeight = size.Width, size.Height
		}
	})
}

// quit cierra la app sin pasar por window.Close(). En Fyne 2.6.3 Close() marca la ventana como
// "closing" (view() pasa a devolver nil) pero el viewport sigue recibiendo eventos hasta el
// próximo tick: un mouse-move en ese PollEvents hace panic en processMouseMoved →
// SetInputMode(nil) (está en los logs, goroutine 1). Ese panic lo atrapa el recover de Run, que
// sale "limpio" pero mata en el acto la goroutine que mandaba el Leave. Ocultar + Quit no deja
// ese estado intermedio, y Quit se demora hasta que termina la limpieza (máx. 3 s).
func (a *App) quit(cleanup <-chan struct{}) {
	a.rememberWindowState() // antes de Hide: oculta, el placement ya no es el que vio el usuario
	a.window.Hide()
	go func() {
		if cleanup != nil {
			select {
			case <-cleanup:
			case <-time.After(3 * time.Second):
			}
		}
		a.flushConfig()
		fyne.Do(fyne.CurrentApp().Quit)
	}()
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
