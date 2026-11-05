//go:build cgo

package ui

import (
	"image"
	"math"
	"strings"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/config"
	"voxmesh/internal/i18n"
	"voxmesh/internal/screen"
)

// Widgets de la pantalla compartida: botón "Compartir" de la barra de arriba, diálogo, banner de
// "Estás compartiendo" y la ventana del visor. La lógica está en screen_share.go (quien comparte),
// screen_view.go (quien mira) y network_screen.go (red).

// newShareControls arma el botón de la barra de arriba y el banner (content()). El botón se
// habilita en una sala; mientras comparto dice "Detener".
func (a *App) newShareControls() (*widget.Button, *fyne.Container) {
	a.shareButton = widget.NewButtonWithIcon(i18n.T("main.share_screen"), theme.ComputerIcon(), a.onShareButton)
	a.shareButton.Importance = widget.LowImportance
	a.shareButton.Disable()

	a.shareBannerText = widget.NewLabel("")
	a.shareBannerText.Truncation = fyne.TextTruncateEllipsis
	a.shareBannerText.TextStyle = fyne.TextStyle{Bold: true}
	stop := widget.NewButtonWithIcon(i18n.T("screen.stop"), theme.MediaStopIcon(), func() { go a.stopScreenShare(true) })
	a.shareResolution = widget.NewSelect(nil, nil) // las opciones las pone showBannerResolution
	background := canvas.NewRectangle(sharingColor)
	background.CornerRadius = 6
	a.shareBanner = container.NewStack(background, container.NewBorder(nil, nil, widget.NewIcon(theme.ComputerIcon()),
		container.NewHBox(a.shareResolution, stop), a.shareBannerText))
	a.shareBanner.Hide()
	return a.shareButton, a.shareBanner
}

// setShareButtonEnabled sigue a la conexión (openHost, connectClient, stopConnectionAsync).
// Hilo de UI.
func (a *App) setShareButtonEnabled(enabled bool) {
	if a.shareButton == nil {
		return
	}
	if enabled {
		a.shareButton.Enable()
	} else {
		a.shareButton.Disable()
	}
}

func (a *App) onShareButton() {
	if a.screenShare.Load() != nil {
		go a.stopScreenShare(true)
		return
	}
	if !a.connected() {
		return
	}
	go func() {
		monitors, err := screen.Monitors()
		fyne.Do(func() { a.showShareDialog(monitors, err) })
	}()
}

// showShareDialog pregunta antes de compartir: nunca empieza sola. Con más de un monitor se elige
// cuál (el principal por defecto). La resolución arranca en la última que se usó (config);
// "Completa" solo aparece si el monitor elegido pasa de screen.DefaultMaxWidth.
func (a *App) showShareDialog(monitors []screen.Monitor, err error) {
	if err != nil {
		a.setStatus(i18n.M("status.screen_failed", err.Error()))
		return
	}
	text := widget.NewLabel(i18n.T("screen.dialog_text"))
	text.Wrapping = fyne.TextWrapWord
	content := container.NewVBox(text)
	chosen := screen.PickMonitor(monitors, "")
	// preferred es la que eligió el usuario (al abrir, la guardada). Se muestra la que ofrece el
	// monitor elegido: si es "Completa" en uno de hasta 1920, 1080p, sin olvidar la preferida.
	preferred := screen.ParseResolution(a.conf().ScreenResolution)
	resolution := widget.NewSelect(nil, nil)
	showResolutions := func() {
		width := chosen.Rect.Dx()
		setResolutionOptions(resolution, screen.Resolutions(width), screen.OfferedResolution(preferred, width),
			func(picked screen.Resolution) { preferred = picked })
	}
	showResolutions()
	if len(monitors) > 1 {
		labels := make([]string, len(monitors))
		for i, monitor := range monitors {
			labels[i] = monitorLabel(monitor)
		}
		picker := widget.NewSelect(labels, func(label string) {
			for i := range labels {
				if labels[i] == label {
					chosen = monitors[i]
				}
			}
			showResolutions()
		})
		picker.SetSelected(monitorLabel(chosen))
		content.Add(picker)
	}
	content.Add(container.NewBorder(nil, nil, widget.NewLabel(i18n.T("screen.resolution")), nil, resolution))
	confirm := dialog.NewCustomConfirm(i18n.T("screen.dialog_title"), i18n.T("screen.start"), i18n.T("common.cancel"), content, func(ok bool) {
		if !ok {
			return
		}
		a.rememberResolution(preferred)
		a.startScreenShare(chosen, screen.OfferedResolution(preferred, chosen.Rect.Dx()))
	}, a.window)
	confirm.Resize(fyne.NewSize(440, confirm.MinSize().Height))
	confirm.Show()
}

// setResolutionOptions pone en sel las opciones offered con selected elegida, sin llamar a onPick
// (SetSelected dispara OnChanged, y lo que se muestra no es una elección); onPick corre cuando el
// usuario elige una. Hilo de UI.
func setResolutionOptions(sel *widget.Select, offered []screen.Resolution, selected screen.Resolution, onPick func(screen.Resolution)) {
	labels := make([]string, len(offered))
	for i, option := range offered {
		labels[i] = resolutionLabel(option)
	}
	sel.OnChanged = nil
	sel.SetOptions(labels)
	sel.SetSelected(resolutionLabel(selected))
	sel.OnChanged = func(string) {
		if i := sel.SelectedIndex(); i >= 0 && i < len(offered) {
			onPick(offered[i])
		}
	}
}

// showBannerResolution pone en el banner las opciones del monitor que se comparte, con la elegida.
// Cambiarla ahí la aplica en vivo: la captura la lee en cada vuelta (shareSession.maxWidth) y un
// tamaño nuevo es una sesión nueva con imagen completa, sin cortar a los que miran. Hilo de UI.
func (a *App) showBannerResolution(session *shareSession, monitorWidth int, selected screen.Resolution) {
	if a.shareResolution == nil {
		return
	}
	setResolutionOptions(a.shareResolution, screen.Resolutions(monitorWidth), selected, func(picked screen.Resolution) {
		session.maxWidth.Store(int32(picked.MaxWidth()))
		a.rememberResolution(picked)
	})
}

// rememberResolution guarda la resolución elegida para la próxima vez (solo si cambió).
func (a *App) rememberResolution(resolution screen.Resolution) {
	if string(resolution) == a.conf().ScreenResolution {
		return
	}
	a.updateConfig(func(c *config.Config) { c.ScreenResolution = string(resolution) })
	a.saveConfig()
}

// resolutionLabel es el texto de una opción del selector de resolución.
func resolutionLabel(resolution screen.Resolution) string {
	switch resolution {
	case screen.ResolutionFull:
		return i18n.T("screen.res_full")
	case screen.Resolution900:
		return i18n.T("screen.res_lines", 900)
	case screen.Resolution720:
		return i18n.T("screen.res_lines", 720)
	}
	return i18n.T("screen.res_lines", 1080)
}

func monitorLabel(monitor screen.Monitor) string {
	if monitor.Primary {
		return i18n.T("screen.monitor_primary", monitor.Number, monitor.Rect.Dx(), monitor.Rect.Dy())
	}
	return i18n.T("screen.monitor", monitor.Number, monitor.Rect.Dx(), monitor.Rect.Dy())
}

// refreshShareControls muestra el banner y el texto del botón según si comparto y quién mira.
// Desde cualquier goroutine.
func (a *App) refreshShareControls() {
	sharing := a.screenShare.Load() != nil
	names := a.screenSnapshot().myWatcherNames
	fyne.Do(func() {
		if a.shareButton == nil || a.shareBanner == nil {
			return
		}
		if sharing {
			a.shareButton.SetText(i18n.T("screen.stop"))
			a.shareButton.Importance = widget.DangerImportance
		} else {
			a.shareButton.SetText(i18n.T("main.share_screen"))
			a.shareButton.Importance = widget.LowImportance
		}
		a.shareButton.Refresh()
		if sharing {
			text := i18n.T("screen.banner_none")
			if len(names) > 0 {
				text = i18n.T("screen.banner_watchers", strings.Join(names, ", "))
			}
			a.shareBannerText.SetText(text)
		}
		if sharing == a.shareBanner.Visible() {
			return
		}
		if sharing {
			a.shareBanner.Show()
		} else {
			a.shareBanner.Hide()
		}
		a.setSharingIndicator(sharing)
		// Mostrar u ocultar no rearma el Border de afuera: sin esto el banner tapaba los avatares.
		if a.mainFrame != nil {
			a.mainFrame.Refresh()
		}
	})
}

// setSharingIndicator prende o apaga el aviso de que comparto que se ve fuera de la ventana: el
// título ("VoxMesh · Compartiendo", también en Alt+Tab) y el punto rojo sobre el botón de la barra
// de tareas (taskbar_windows.go). Se llama solo desde refreshShareControls, junto con el banner,
// así nunca quedan distintos: Detener, desconectarse (también al cerrar la app) y una falla o
// panic de la captura pasan todos por stopScreenShare. Hilo de UI.
func (a *App) setSharingIndicator(on bool) {
	if a.window == nil {
		return // tests que arman solo el contenido
	}
	title := appTitle
	if on {
		title = i18n.T("main.title_sharing")
	}
	a.window.SetTitle(title)
	a.setTaskbarOverlay(on)
}

// watchScreen abre la pantalla de id ("Ver pantalla" en su avatar). Si ya la miro, trae la ventana
// al frente; si miraba otra, la reemplaza. Hilo de UI.
func (a *App) watchScreen(id, name string) {
	if current := a.screenWatch.Load(); current != nil && current.target == id {
		a.screenView.window.Show()
		restoreWindow(a.screenView.window)
		a.screenView.window.RequestFocus()
		return
	}
	peer, ok := screen.ParsePeerID(id)
	if !ok {
		return
	}
	watch := newWatchSession(id, name, peer)
	if previous := a.screenWatch.Swap(watch); previous != nil {
		previous.close()
	}
	view := a.ensureScreenView()
	view.reset(name)
	view.window.Show()
	restoreWindow(view.window) // si la última vez se minimizó, Show la deja minimizada
	go a.runScreenViewer(watch, view)
	go a.publishScreenIntent()
}

// stopWatching deja de mirar: corta el visor, oculta la ventana y le avisa al host. Desde cualquier
// goroutine.
func (a *App) stopWatching() {
	watch := a.screenWatch.Swap(nil)
	if watch == nil {
		return
	}
	watch.close()
	fyne.Do(func() {
		if a.screenView != nil && a.screenWatch.Load() == nil {
			a.screenView.window.Hide()
			a.screenView.clear()
		}
	})
	go a.publishScreenIntent()
}

// screenView es la ventana del visor: una sola, creada al primer uso y reusada. Nunca Close():
// cerrarla la oculta (SetCloseIntercept), por la trampa de Fyne 2.6.3 de §3.G. Los widgets solo
// desde el hilo de UI; actual, areaW/areaH y resize los lee la goroutine del visor.
type screenView struct {
	window  fyne.Window
	image   *canvas.Image
	cursor  *canvas.Image // capa del cursor de quien comparte, encima de image
	scroll  *container.Scroll
	modeBtn *widget.Button
	status  *widget.Label
	actual  atomic.Bool  // tamaño real (1:1) en vez de ajustar a la ventana
	areaW   atomic.Int32 // tamaño del área de la imagen en píxeles físicos
	areaH   atomic.Int32
	resize  chan struct{}
}

func (a *App) ensureScreenView() *screenView {
	if a.screenView != nil {
		return a.screenView
	}
	view := &screenView{resize: make(chan struct{}, 1)}
	view.window = fyne.CurrentApp().NewWindow("")
	if len(appIconBytes) > 0 {
		view.window.SetIcon(fyne.NewStaticResource("icon.png", appIconBytes))
	}
	view.window.SetCloseIntercept(a.stopWatching)
	view.image = canvas.NewImageFromImage(nil)
	view.image.FillMode = canvas.ImageFillStretch
	// Pixels: sin escalado por software en el hilo de UI (Smooth hace un CatmullRom en cada
	// Refresh); la imagen ya llega del tamaño justo.
	view.image.ScaleMode = canvas.ImageScalePixels
	// El cursor va en una capa sin layout del tamaño de la imagen: se mueve con ella (y con el
	// scroll en 1:1) sin volver a subir la pantalla.
	view.cursor = canvas.NewImageFromImage(nil)
	view.cursor.FillMode = canvas.ImageFillStretch
	view.cursor.ScaleMode = canvas.ImageScaleSmooth // es chico: achicarlo en el hilo de UI no cuesta
	view.cursor.Hide()
	view.scroll = container.NewScroll(container.NewCenter(container.NewStack(view.image, container.NewWithoutLayout(view.cursor))))
	view.scroll.Direction = container.ScrollNone
	view.status = widget.NewLabel("")
	view.status.Truncation = fyne.TextTruncateEllipsis
	view.modeBtn = widget.NewButton(i18n.T("screen.actual_size"), func() { view.setActual(!view.actual.Load()) })
	area := container.New(&screenAreaLayout{view: view}, view.scroll)
	view.window.SetContent(container.NewBorder(container.NewBorder(nil, nil, nil, view.modeBtn, view.status), nil, nil, nil, area))
	view.window.Resize(fyne.NewSize(1100, 700))
	a.screenView = view
	return view
}

// reset prepara la ventana para otra pantalla: título, "Esperando imagen…" y modo ajustar.
func (v *screenView) reset(name string) {
	v.window.SetTitle(i18n.T("screen.window_title", name))
	v.status.SetText(i18n.T("screen.waiting"))
	v.clear()
	v.setActual(false)
}

// placeCursor pone el cursor de quien comparte en x, y (esquina) con tamaño w×h, en píxeles de la
// imagen que se ve.
func (v *screenView) placeCursor(img image.Image, x, y, w, h int, visible bool) {
	if !visible || v.image.Image == nil {
		v.cursor.Hide()
		return
	}
	scale := v.window.Canvas().Scale()
	if v.cursor.Image != img {
		v.cursor.Image = img
		v.cursor.Refresh()
	}
	v.cursor.Resize(fyne.NewSize(float32(w)/scale, float32(h)/scale))
	v.cursor.Move(fyne.NewPos(float32(x)/scale, float32(y)/scale))
	v.cursor.Show()
}

// clear suelta la imagen (la ventana oculta no retiene un cuadro de 8 MB).
func (v *screenView) clear() {
	v.cursor.Hide()
	v.image.Image = nil
	v.image.SetMinSize(fyne.NewSize(0, 0))
	v.image.Refresh()
}

func (v *screenView) setActual(actual bool) {
	v.actual.Store(actual)
	if actual {
		v.scroll.Direction = container.ScrollBoth
		v.modeBtn.SetText(i18n.T("screen.fit"))
	} else {
		v.scroll.Direction = container.ScrollNone
		v.modeBtn.SetText(i18n.T("screen.actual_size"))
	}
	v.scroll.Refresh()
	v.poke()
}

func (v *screenView) poke() {
	select {
	case v.resize <- struct{}{}:
	default:
	}
}

// show pone una imagen nueva del tamaño justo: en píxeles físicos 1:1 con la pantalla (min size =
// píxeles / escala del canvas, también con Windows al 125 % o 150 %).
func (v *screenView) show(img *image.RGBA, status string) {
	scale := v.window.Canvas().Scale()
	v.image.Image = img
	v.image.SetMinSize(fyne.NewSize(float32(img.Rect.Dx())/scale, float32(img.Rect.Dy())/scale))
	v.image.Refresh()
	v.scroll.Refresh()
	if v.status.Text != status {
		v.status.SetText(status)
	}
}

// screenAreaLayout estira el contenido al área y anota su tamaño en píxeles físicos para que la
// goroutine del visor achique a ese tamaño. Su MinSize no depende de la imagen: la ventana se puede
// achicar siempre.
type screenAreaLayout struct{ view *screenView }

func (l *screenAreaLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(200, 150) }

func (l *screenAreaLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		object.Move(fyne.NewPos(0, 0))
		object.Resize(size)
	}
	scale := l.view.window.Canvas().Scale()
	w, h := int32(math.Floor(float64(size.Width*scale))), int32(math.Floor(float64(size.Height*scale)))
	// Los dos Swap siempre: con `||` el del alto no corría si cambiaba el ancho, y al reabrir el
	// visor después de minimizado (−8×−48 → 1092×652) quedaba alto −48 y no se pintaba nunca.
	oldW, oldH := l.view.areaW.Swap(w), l.view.areaH.Swap(h)
	if oldW != w || oldH != h {
		l.view.poke()
	}
}
