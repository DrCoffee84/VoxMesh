//go:build cgo

package ui

import (
	_ "embed"
	"errors"
	"net"
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
	"voxmesh/internal/i18n"
	"voxmesh/internal/logging"
	"voxmesh/internal/netinfo"
	"voxmesh/internal/phonemic"
	"voxmesh/internal/room"
	"voxmesh/internal/screen"
	"voxmesh/internal/transport"
	"voxmesh/internal/updater"
	"voxmesh/internal/version"
	"voxmesh/internal/winproc"
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
	profiles        config.Profiles                     // perfiles de audio de entrada/salida (%AppData%\VoxMesh\perfiles)
	levelListener   atomic.Pointer[func(inputMeter)]    // medidor de Ajustes abierto: cada frame de entrada procesado (reportMeter)
	transport       atomic.Pointer[transport.UDP]       // nil = sin sala, ver connection_state.go
	sequence        atomic.Uint32
	encoder         atomic.Pointer[audio.OpusEncoder] // encoder Opus del audio saliente, se recrea al conectar
	sendMu          sync.Mutex                        // envío de audio: timeline, sendBuf
	timeline        audioTimeline                     // secuencia y frase del audio saliente (ver transport.FlagAudioSequence)
	audioSent       atomic.Uint64                     // paquetes de audio mandados (línea "Envío" del log)
	sendBuf         []byte
	arrivalRec      *audio.ArrivalRecorder // grabación de llegadas con nivel de log 2 (solo audioMonitorLoop)
	arrivalEngine   *audio.Engine          // Engine al que está enganchada arrivalRec
	endpoint        atomic.Pointer[endpointInfo]
	upnpMapping     atomic.Pointer[netinfo.Mapping] // regla UPnP de la sala (nil = ninguna), para borrarla al cerrar
	hostNet         atomic.Pointer[hostNetwork]     // estado de la red del host (nil = no soy host), ver host_network.go
	rejoinInvite    atomic.Pointer[string]          // invitación para volver a entrar (solo en memoria, nunca a disco)
	status          *widget.Label
	peerHBox        *fyne.Container
	hostPanel       *fyne.Container
	createButton    *widget.Button
	stopButton      *widget.Button
	connectButton   *widget.Button
	leaveButton     *widget.Button
	topHangupBtn    *widget.Button
	inviteButton    *widget.Button // "Invitar": reloj naranja mientras se obtiene la IP pública (refreshInviteButton)
	endpointEntry   *widget.Entry
	peerNames       []peerView
	peerMu          logging.TracedRWMutex
	recordingMu     logging.TracedRWMutex
	recordedInput   []byte
	connLife        stopSignal // goroutines de la conexión (failoverLoop, sweepAssetLoop)
	hostRole        stopSignal // goroutines del rol de host (pingPeers)
	voiceProcessor  *audio.RNNoise
	effects         *audio.Effects
	audioEngine     atomic.Pointer[audio.Engine]
	audioRecovery   audioRecovery // reapertura automática del audio caído (solo audioMonitorLoop)
	awakeErrLogged  bool          // ya se registró que no se pudo evitar la suspensión (solo audioMonitorLoop)
	hostMode        atomic.Bool
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
	voiceLastAt     atomic.Int64 // UnixNano del último frame de voz mandado (lo apaga sweepVoiceIndicator)
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
	mainFrame       *fyne.Container // barra de arriba (+ banner de pantalla) | centro | barra de estado
	settingsStatus  *widget.Label
	assetAssembler  *assets.Assembler
	assetRejects    rejectedAssets
	roomFullLogged  time.Time // último "Sala llena" en el log; solo receiveLoop
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
	stopBindBtn     *bindButton
	tooltip         *tooltip // leyenda de la ventana principal (atajos de la botonera); solo el hilo de UI
	soundMu         logging.TracedRWMutex
	sounds          []room.Sound
	pendingPlays    map[string]time.Time // sonidos tocados sin audio en disco → cuándo se pidieron (sound_sync.go)
	soundHolders    map[string][]*net.UDPAddr
	soundFetchAt    map[string]time.Time
	// Sync de la botonera con el host, una vez por conexión (sound_sync.go).
	soundSyncPending atomic.Bool
	soundSyncSentAt  atomic.Int64
	// Pantalla compartida (network_screen.go, screen_share.go, screen_view.go, ui_screen.go).
	screenState      atomic.Pointer[screenState]  // quién comparte y quién mira; la rearma refreshPeersFromRoom
	screenShare      atomic.Pointer[shareSession] // mi pantalla compartida (nil = no comparto)
	screenWatch      atomic.Pointer[watchSession] // la pantalla que miro (nil = ninguna)
	screenRejects    rejectedTiles                // tiles que el host no reenvía; solo receiveLoop
	rttFloor         screen.RTTFloor              // latencias por dirección: ¿la pantalla va a ritmo de LAN?
	screenRateLAN    atomic.Bool                  // host: el último ritmo elegido fue el de LAN (log)
	screenRateLogged atomic.Bool
	screenHostRate   atomic.Int64 // cliente que comparte: ritmo que mandó el host (bytes/s)
	screenHostRateAt atomic.Int64 // UnixNano de cuándo llegó
	shareButton      *widget.Button
	shareBanner      *fyne.Container
	shareBannerText  *widget.Label
	shareResolution  *widget.Select // resolución en el banner (cambia en vivo); solo el hilo de UI
	screenView       *screenView    // ventana del visor; solo el hilo de UI
}

// appTitle es el título de la ventana principal (un nombre: no se traduce). Mientras comparto
// pantalla pasa a main.title_sharing (setSharingIndicator).
const appTitle = "VoxMesh"

// phoneMicInput es el valor que se guarda en InputDevice cuando la entrada es el celular (un
// identificador: no se traduce; el select muestra deviceOptionLabel).
const phoneMicInput = "Micrófono del celular"

// memoryLimit es el tope blando de memoria del runtime (debug.SetMemoryLimit): por encima, el GC
// trabaja más seguido. Sobra para el uso normal; es una red de seguridad, no un objetivo.
const memoryLimit = 512 << 20

func New(executable string) *App {
	cfgPath := config.Path(executable)
	_, statErr := os.Stat(cfgPath)
	cfg, _ := config.Load(cfgPath)
	// El idioma va antes que cualquier texto y que la ventana: se aplica al arrancar.
	i18n.Set(cfg.Language)
	if os.IsNotExist(statErr) {
		// Primera vez: el nombre y la sala por defecto en el idioma de la app.
		cfg.Username, cfg.RoomName = i18n.T("default.username"), i18n.T("default.room_name")
	}
	logging.SetLevel(cfg.LogLevel)
	var profiles config.Profiles
	if profilesDir, err := config.ProfilesDir(); err == nil {
		profiles.Dir = profilesDir
	} else {
		logging.Errorf("perfiles de audio: %s", logging.FormatError(err))
	}
	// Default no se guarda: el que lo usa recibe los valores de fábrica de esta versión.
	defaultsApplied := config.ApplyDefaultProfiles(&cfg.LiveSettings)
	voiceProcessor, _ := audio.NewRNNoise()
	a := &App{
		cfgPath:        cfgPath,
		cfgSave:        make(chan struct{}, 1),
		profiles:       profiles,
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
	if defaultsApplied {
		a.saveConfig()
	}
	return a
}

// setStatus muestra msg en la barra de estado (y en la de Ajustes) en el idioma de la app; el log
// lo guarda en español.
func (a *App) setStatus(msg i18n.Msg) {
	logging.Infof("%s", msg.In(i18n.ES))
	if a.status == nil && a.settingsStatus == nil {
		return
	}
	text := msg.String()
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
	// Antes de crear la ventana: prioridad, fuera del power throttling, sin ventana fantasma.
	logging.Infof("Proceso: %s", winproc.Configure())
	defer func() {
		a.stopPhoneMic()
		a.stopPreviewAudio()
		if value := recover(); value != nil {
			logging.Panic(value)
		}
		logging.Close()
	}()
	application := app.NewWithID("com.voxmesh.desktop")
	application.Settings().SetTheme(voxTheme{}) // antes de armar la ventana: uiCard lee sus colores
	if len(appIconBytes) > 0 {
		appIcon := fyne.NewStaticResource("icon.png", appIconBytes)
		application.SetIcon(appIcon)
	}
	a.window = application.NewWindow(appTitle)
	if len(appIconBytes) > 0 {
		appIcon := fyne.NewStaticResource("icon.png", appIconBytes)
		a.window.SetIcon(appIcon)
	}
	cfg := a.conf()
	a.window.Resize(fyne.NewSize(cfg.WindowWidth, cfg.WindowHeight))
	a.window.SetCloseIntercept(func() {
		if a.connected() {
			dialog.ShowConfirm(i18n.T("main.quit.title"), i18n.T("main.quit.message"), func(confirmed bool) {
				if confirmed {
					a.quit(a.stopConnectionAsync(), nil)
				}
			}, a.window)
		} else {
			a.quit(nil, nil)
		}
	})
	a.window.SetContent(a.content())
	a.refreshRoomList("")
	if a.conf().InputDevice == phoneMicInput {
		if _, err := a.ensurePhoneMicServer(); err != nil {
			logging.Errorf("iniciar micrófono de celular: %s", logging.FormatError(err))
			a.setStatus(i18n.M("status.phone_server_start_failed", errorMsg(err)))
		}
	}
	a.startHotkeyListener()
	a.startUIWatchdog()
	go a.audioMonitorLoop()
	if !version.IsDev() { // el manual (Ajustes) sigue andando en builds locales
		go func() {
			time.Sleep(1 * time.Second)
			a.checkForUpdates(false)
		}()
	}
	// Tope blando de memoria en vez del debug.FreeOSMemory() cada 30 s: ese GC completo forzado
	// frenaba las goroutines que asignan (también la de envío de audio) y competía con el hilo de
	// UI. El runtime devuelve sola la memoria libre al sistema, de a poco.
	debug.SetMemoryLimit(memoryLimit)
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
// after (opcional) corre después de la limpieza y de guardar la config, antes de Quit: lo usa la
// actualización para lanzar la versión nueva.
func (a *App) quit(cleanup <-chan struct{}, after func()) {
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
		if after != nil {
			after()
		}
		fyne.Do(fyne.CurrentApp().Quit)
	}()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (a *App) checkForUpdates(manual bool) {
	if manual {
		a.setStatus(i18n.M("update.checking"))
	}
	info, err := updater.CheckForUpdate(version.Value)
	if err != nil {
		if manual {
			fyne.Do(func() {
				a.setStatus(i18n.M("update.check_failed"))
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
				a.setStatus(i18n.M("update.up_to_date"))
				dialog.ShowInformation(i18n.T("update.title"), i18n.T("update.up_to_date_dialog", version.Value), a.window)
			})
		}
		return
	}

	fyne.Do(func() {
		a.promptUpdate(info)
	})
}

func (a *App) promptUpdate(info *updater.ReleaseInfo) {
	msg := i18n.T("update.available", info.Version, version.Value)
	if info.Changelog != "" {
		msg += i18n.T("update.changelog", info.Changelog)
	}
	msg += i18n.T("update.ask")

	dialog.ShowConfirm(i18n.T("update.available_title"), msg, func(confirm bool) {
		if !confirm {
			return
		}
		a.executeAutoUpdate(info)
	}, a.window)
}

func (a *App) executeAutoUpdate(info *updater.ReleaseInfo) {
	progress := widget.NewProgressBar()
	progress.SetValue(0)
	statusText := widget.NewLabel(i18n.T("update.downloading", info.Version))
	box := container.NewVBox(statusText, progress)

	updateDialog := dialog.NewCustomWithoutButtons(i18n.T("update.updating_title"), box, a.window)
	updateDialog.Show()

	go func() {
		err := updater.ApplyUpdate(info, func(fraction float64) {
			fyne.Do(func() {
				progress.SetValue(fraction)
			})
		})
		if err != nil {
			logging.Errorf("actualizar a %s: %s", info.Version, logging.FormatError(err))
			fyne.Do(func() {
				updateDialog.Hide()
				dialog.ShowError(errors.New(i18n.T("update.failed", err.Error())), a.window)
			})
			return
		}
		logging.Infof("Actualización %s instalada: cerrando para reiniciar", info.Version)
		// Sale por quit como cualquier cierre (Leave, UPnP, config, "=== cierre" en el log); el
		// proceso nuevo espera con --wait-pid a que este termine.
		fyne.Do(func() {
			updateDialog.Hide()
			var cleanup <-chan struct{}
			if a.connected() {
				cleanup = a.stopConnectionAsync()
			}
			a.quit(cleanup, func() {
				if err := updater.Relaunch(); err != nil {
					logging.Errorf("reiniciar después de actualizar: %s", logging.FormatError(err))
				}
			})
		})
	}()
}
