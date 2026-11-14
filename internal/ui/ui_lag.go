//go:build cgo

package ui

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/config"
	"voxmesh/internal/i18n"
)

// Detalle de lag (AGENDA C18): una tira debajo de cada participante con los últimos 5 min y, al
// tocarla, el gráfico grande de toda la sesión. Arranca oculto; el ícono de la barra de
// participantes lo muestra u oculta (config ShowLagGraph). Los datos salen de a.lagHistory; todo lo
// de acá corre en el hilo de UI.

const (
	// La tira mide lo que el slider de volumen.
	lagStripWidth  = 110
	lagStripHeight = 16
	// lagStripScaleMS es el tope de la tira. La altura va con la raíz cuadrada: un buen ping se ve
	// (30 ms ≈ un cuarto) y 400 ms casi la llena.
	lagStripScaleMS = 500
)

// Ícono del detalle de lag (Material "show_chart"); prendido, en el color de acento.
var (
	showChartResource = fyne.NewStaticResource("show_chart.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24"><path d="M3.5 18.49l6-6.01 4 4L22 6.92l-1.41-1.41-7.09 7.97-4-4L2 16.99z"/></svg>`))
	showChartIcon     = theme.NewThemedResource(showChartResource)
	showChartOnIcon   = theme.NewPrimaryThemedResource(showChartResource)
)

// Colores del dibujo (fondo oscuro siempre, AGENT.md §3.D).
var (
	lagStripBackground = color.NRGBA{R: 0, G: 0, B: 0, A: 60}
	lagNoRTTColor      = color.NRGBA{R: 120, G: 130, B: 145, A: 255} // hay pérdida medida pero no ms
	lagBandAlpha       = uint8(26)
	lagLineAlpha       = uint8(70)
	lagLossColor       = color.NRGBA{R: 231, G: 76, B: 60, A: 170}
)

// hintButton es un botón con una leyenda al pasar el mouse (a.tooltip, igual que bindButton:
// Fyne 2.6.3 no tiene tooltips).
type hintButton struct {
	widget.Button
	app      *App
	hint     string
	hovered  bool
	hoverGen int // descarta el timer de un MouseIn viejo
}

func (a *App) newHintButton(icon fyne.Resource, hint string, tapped func()) *hintButton {
	b := &hintButton{app: a, hint: hint}
	b.Icon = icon
	b.OnTapped = tapped
	b.Importance = widget.LowImportance
	b.ExtendBaseWidget(b)
	return b
}

func (b *hintButton) MouseIn(event *desktop.MouseEvent) {
	b.Button.MouseIn(event)
	b.hovered = true
	b.hoverGen++
	gen := b.hoverGen
	time.AfterFunc(bindTooltipDelay, func() {
		fyne.Do(func() {
			if b.hovered && b.hoverGen == gen && b.hint != "" && b.app.tooltip != nil {
				b.app.tooltip.show(b, b.hint)
			}
		})
	})
}

func (b *hintButton) MouseOut() {
	b.Button.MouseOut()
	b.hovered = false
	b.app.hideTooltip()
}

func (b *hintButton) Tapped(event *fyne.PointEvent) {
	b.hovered = false
	b.app.hideTooltip()
	b.Button.Tapped(event)
}

// newLagToggle es el ícono de la barra de participantes que muestra u oculta las tiras.
func (a *App) newLagToggle() *hintButton {
	a.lagToggle = a.newHintButton(showChartIcon, "", a.toggleLagGraph)
	a.setLagToggleLook(a.conf().ShowLagGraph)
	return a.lagToggle
}

// setLagToggleLook pone el ícono y la leyenda según si las tiras se ven (sin Refresh: sirve antes de
// que el botón esté en la ventana).
func (a *App) setLagToggleLook(shown bool) {
	if shown {
		a.lagToggle.Icon, a.lagToggle.hint = showChartOnIcon, i18n.T("lag.hide")
	} else {
		a.lagToggle.Icon, a.lagToggle.hint = showChartIcon, i18n.T("lag.show")
	}
}

// toggleLagGraph (hilo de UI, el ícono) muestra u oculta las tiras de todos y lo guarda.
func (a *App) toggleLagGraph() {
	shown := !a.conf().ShowLagGraph
	a.updateConfig(func(c *config.Config) { c.ShowLagGraph = shown })
	a.saveConfig()
	if a.lagToggle != nil {
		a.setLagToggleLook(shown)
		a.lagToggle.Refresh()
	}
	for _, chip := range a.peerChips {
		if chip.strip == nil {
			continue
		}
		if shown {
			chip.strip.Show()
			chip.strip.redraw()
		} else {
			chip.strip.Hide()
		}
	}
	if a.peerHBox != nil {
		a.peerHBox.Refresh() // el chip cambia de alto
	}
}

// paintLag (audioMonitorLoop, cada lagSlot) redibuja las tiras que se ven y el gráfico grande si hay
// medidas nuevas o pasó un casillero (la ventana se corre aunque no lleguen). Un solo fyne.Do.
func (a *App) paintLag(now time.Time) {
	fyne.Do(func() {
		if a.conf().ShowLagGraph {
			for _, chip := range a.peerChips {
				if chip.strip != nil {
					chip.strip.refreshIfStale(now)
				}
			}
		}
		if a.lagView != nil {
			a.lagView.refreshIfStale(now)
		}
	})
}

// lagPointText es la leyenda de una muestra ("21:34:12 · 350 ms · 8 % de pérdida"). Una columna vacía
// usa la hora when y dice "sin datos"; withJitter suma el jitter (gráfico grande).
func lagPointText(point lagPoint, when time.Time, withJitter bool) string {
	if !point.empty() {
		when = point.at
	}
	parts := []string{when.Format("15:04:05")}
	if point.rtt >= 0 {
		parts = append(parts, i18n.T("lag.point_ms", point.rtt))
	}
	if point.loss >= 0 {
		parts = append(parts, i18n.T("lag.point_loss", point.loss))
	}
	if withJitter && point.jitter >= 0 {
		parts = append(parts, i18n.T("lag.point_jitter", point.jitter))
	}
	if len(parts) == 1 {
		parts = append(parts, i18n.T("lag.point_none"))
	}
	return strings.Join(parts, " · ")
}

// lagColumnTime es la hora del medio de la columna c de n en [from, to).
func lagColumnTime(from, to time.Time, c, n int) time.Time {
	return from.Add(time.Duration((float64(c) + 0.5) / float64(n) * float64(to.Sub(from))))
}

// lagStrip es la tira del chip: una barrita por columna (alto = ms, color del semáforo) y una marca
// arriba si hubo pérdida. El mouse encima muestra la muestra; tocarla abre el gráfico grande.
type lagStrip struct {
	widget.BaseWidget
	app      *App
	name     string // del participante: la clave del historial
	raster   *canvas.Raster
	columns  []lagPoint // las del último dibujo (para la leyenda)
	from, to time.Time
	drawn    uint64 // versión del historial del último dibujo
	drawnAt  time.Time
}

func (a *App) newLagStrip(name string) *lagStrip {
	s := &lagStrip{app: a, name: name}
	s.raster = canvas.NewRaster(s.draw)
	s.raster.SetMinSize(fyne.NewSize(lagStripWidth, lagStripHeight))
	s.ExtendBaseWidget(s)
	return s
}

func (s *lagStrip) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(s.raster) }

// draw es el generador del raster (lo llama Fyne al pintar, en el hilo de UI).
func (s *lagStrip) draw(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	s.to = time.Now()
	s.from = s.to.Add(-lagStripWindow)
	s.columns = lagColumns(s.app.lagHistory.points(s.name, s.from, s.to), s.from, s.to, width)
	s.drawnAt = s.to
	drawLagStrip(img, s.columns)
	return img
}

// redraw dibuja de nuevo (con lo que haya ahora en el historial).
func (s *lagStrip) redraw() {
	s.drawn = s.app.lagHistory.version(s.name)
	s.raster.Refresh()
}

// refreshIfStale redibuja si se ve y llegaron medidas o pasó un casillero desde el último dibujo.
func (s *lagStrip) refreshIfStale(now time.Time) {
	if !s.Visible() {
		return
	}
	if s.app.lagHistory.version(s.name) != s.drawn || now.Sub(s.drawnAt) >= lagSlot {
		s.redraw()
	}
}

// setName cambia de participante (el chip se reusa si el host lo renombró).
func (s *lagStrip) setName(name string) {
	if s.name == name {
		return
	}
	s.name = name
	if s.Visible() {
		s.redraw()
	}
}

func (s *lagStrip) MouseIn(event *desktop.MouseEvent)    { s.hover(event.Position) }
func (s *lagStrip) MouseMoved(event *desktop.MouseEvent) { s.hover(event.Position) }
func (s *lagStrip) MouseOut()                            { s.app.hideTooltip() }

func (s *lagStrip) hover(position fyne.Position) {
	width := s.Size().Width
	if len(s.columns) == 0 || width <= 0 || s.app.tooltip == nil {
		return
	}
	c := min(max(int(position.X/width*float32(len(s.columns))), 0), len(s.columns)-1)
	s.app.tooltip.show(s, lagPointText(s.columns[c], lagColumnTime(s.from, s.to, c, len(s.columns)), false))
}

func (s *lagStrip) Tapped(*fyne.PointEvent) {
	s.app.hideTooltip()
	s.app.showLagView(s.name)
}

// drawLagStrip dibuja las columnas de la tira en img.
func drawLagStrip(img *image.RGBA, columns []lagPoint) {
	bounds := img.Bounds()
	height := bounds.Dy()
	fillRect(img, bounds, lagStripBackground)
	for x, column := range columns {
		if column.empty() {
			continue
		}
		if column.rtt >= 0 {
			frac := math.Sqrt(float64(min(column.rtt, lagStripScaleMS)) / lagStripScaleMS)
			bar := max(int(math.Round(frac*float64(height))), 2)
			fillRect(img, image.Rect(x, height-bar, x+1, height), lagColor(column.rtt))
		} else {
			fillRect(img, image.Rect(x, height-1, x+1, height), lagNoRTTColor)
		}
		if column.loss > 0 {
			fillRect(img, image.Rect(x, 0, x+1, 2), lagRed)
		}
	}
}

func fillRect(img *image.RGBA, rect image.Rectangle, c color.Color) {
	draw.Draw(img, rect.Intersect(img.Bounds()), image.NewUniform(c), image.Point{}, draw.Over)
}

func withAlpha(c color.Color, alpha uint8) color.NRGBA {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	n.A = alpha
	return n
}

// lagView es el gráfico grande de un participante (un diálogo; uno a la vez).
type lagView struct {
	app     *App
	name    string
	rng     time.Duration // 0 = toda la sesión
	graph   *lagGraph
	fromTxt *widget.Label
	toTxt   *widget.Label
	summary *widget.Label
	readout *widget.Label
	dialog  *dialog.CustomDialog
	drawn   uint64
	drawnAt time.Time
}

// lagRanges son los rangos del gráfico grande, en el orden del selector.
var lagRanges = []struct {
	key string
	rng time.Duration
}{{"lag.range_5m", 5 * time.Minute}, {"lag.range_1h", time.Hour}, {"lag.range_all", 0}}

// showLagView abre el gráfico grande de name (hilo de UI). Si había otro abierto, lo cierra.
func (a *App) showLagView(name string) {
	if a.window == nil {
		return
	}
	if a.lagView != nil {
		a.lagView.dialog.Hide()
	}
	isHost := false
	a.peerMu.RLock()
	for _, peer := range a.peerNames {
		if peer.name == name && !peer.isSelf {
			isHost = peer.isHost
			break
		}
	}
	a.peerMu.RUnlock()
	client := !a.hostMode.Load()
	title := i18n.T("lag.title", name)
	if client && isHost {
		title = i18n.T("lag.title_host", name)
	}

	view := &lagView{app: a, name: name}
	view.graph = newLagGraph(view)
	view.fromTxt = widget.NewLabel("")
	view.toTxt = widget.NewLabel("")
	view.summary = widget.NewLabel("")
	view.summary.Wrapping = fyne.TextWrapWord
	view.readout = widget.NewLabel(i18n.T("lag.hover_hint"))
	view.readout.Wrapping = fyne.TextWrapWord
	legend := widget.NewLabel(i18n.T("lag.legend"))
	legend.Wrapping = fyne.TextWrapWord
	legend.Importance = widget.LowImportance

	options := make([]string, len(lagRanges))
	for i, r := range lagRanges {
		options[i] = i18n.T(r.key)
	}
	ranges := widget.NewRadioGroup(options, func(selected string) {
		for i, option := range options {
			if option == selected {
				view.rng = lagRanges[i].rng
			}
		}
		view.update(time.Now())
	})
	ranges.Horizontal = true
	ranges.Required = true
	top := container.NewVBox(ranges)
	if client && !isHost {
		note := widget.NewLabel(i18n.T("lag.client_note", name))
		note.Wrapping = fyne.TextWrapWord
		top.Add(note)
	}
	axis := container.NewBorder(nil, nil, view.fromTxt, view.toTxt)
	bottom := container.NewVBox(axis, view.summary, view.readout, legend)
	content := container.NewBorder(top, bottom, nil, nil, view.graph)

	view.dialog = dialog.NewCustom(title, i18n.T("common.close"), content, a.window)
	view.dialog.SetOnClosed(func() {
		if a.lagView == view {
			a.lagView = nil
		}
	})
	a.lagView = view
	ranges.SetSelected(options[len(options)-1]) // toda la sesión (llama a update)
	view.dialog.Resize(fyne.NewSize(720, 480))
	view.dialog.Show()
}

// window es el rango que se muestra ahora: [from, to).
func (v *lagView) window(now time.Time) (time.Time, time.Time) {
	if v.rng > 0 {
		return now.Add(-v.rng), now
	}
	first, ok := v.app.lagHistory.first(v.name)
	if !ok || now.Sub(first) < time.Minute {
		return now.Add(-time.Minute), now
	}
	return first, now
}

// update recalcula todo con el historial de ahora y redibuja.
func (v *lagView) update(now time.Time) {
	from, to := v.window(now)
	points := v.app.lagHistory.points(v.name, from, to)
	summary := summarizeLag(points)
	v.graph.set(points, v.app.lagHistory.episodes(v.name, from, to), from, to, lagScaleMS(summary.maxRTT))
	layout := "15:04"
	if to.Sub(from) <= 10*time.Minute {
		layout = "15:04:05"
	}
	v.fromTxt.SetText(from.Format(layout))
	v.toTxt.SetText(to.Format(layout))
	v.summary.SetText(lagSummaryText(summary))
	v.drawn, v.drawnAt = v.app.lagHistory.version(v.name), now
}

// refreshIfStale actualiza si llegaron medidas o pasó un casillero.
func (v *lagView) refreshIfStale(now time.Time) {
	if v.app.lagHistory.version(v.name) != v.drawn || now.Sub(v.drawnAt) >= lagSlot {
		v.update(now)
	}
}

// lagSummaryText es "Máximo 600 ms · promedio 120 ms · pérdida máxima 12 %" (solo lo medido).
func lagSummaryText(summary lagSummary) string {
	var parts []string
	if summary.maxRTT >= 0 {
		parts = append(parts, i18n.T("lag.summary_max", summary.maxRTT), i18n.T("lag.summary_avg", summary.avgRTT))
	}
	if summary.maxLoss >= 0 {
		parts = append(parts, i18n.T("lag.summary_loss", summary.maxLoss))
	}
	if len(parts) == 0 {
		return i18n.T("lag.summary_none")
	}
	text := strings.Join(parts, " · ")
	runes := []rune(text)
	return strings.ToUpper(string(runes[:1])) + string(runes[1:])
}

// lagGraph es el dibujo del gráfico grande: fondo con las franjas del semáforo, la línea de ms
// (con huecos donde no hay datos) y la pérdida en barras abajo. El mouse encima muestra cada muestra
// en view.readout (la leyenda de la ventana quedaría debajo del diálogo).
type lagGraph struct {
	widget.BaseWidget
	view     *lagView
	points   []lagPoint
	episodes []lagEpisode // tramos con lag (C13): franja roja arriba
	from, to time.Time
	scale    int        // ms del borde de arriba
	columns  []lagPoint // las del último dibujo
}

func newLagGraph(view *lagView) *lagGraph {
	g := &lagGraph{view: view, scale: lagScaleMS(0)}
	g.ExtendBaseWidget(g)
	return g
}

func (g *lagGraph) set(points []lagPoint, episodes []lagEpisode, from, to time.Time, scale int) {
	g.points, g.episodes, g.from, g.to, g.scale = points, episodes, from, to, scale
	g.Refresh()
}

func (g *lagGraph) MouseIn(event *desktop.MouseEvent)    { g.hover(event.Position) }
func (g *lagGraph) MouseMoved(event *desktop.MouseEvent) { g.hover(event.Position) }
func (g *lagGraph) MouseOut()                            { g.view.readout.SetText(i18n.T("lag.hover_hint")) }

func (g *lagGraph) hover(position fyne.Position) {
	width := g.Size().Width
	if len(g.columns) == 0 || width <= 0 {
		return
	}
	c := min(max(int(position.X/width*float32(len(g.columns))), 0), len(g.columns)-1)
	g.view.readout.SetText(lagPointText(g.columns[c], lagColumnTime(g.from, g.to, c, len(g.columns)), true))
}

// lagGraphMarks son las líneas con número a la izquierda del gráfico (los umbrales del semáforo).
var lagGraphMarks = []int{lagGoodMS, lagWarnMS, lagBadMS}

func (g *lagGraph) CreateRenderer() fyne.WidgetRenderer {
	r := &lagGraphRenderer{graph: g}
	r.raster = canvas.NewRaster(func(width, height int) image.Image {
		img := image.NewRGBA(image.Rect(0, 0, width, height))
		g.columns = lagColumns(g.points, g.from, g.to, width)
		drawLagEpisodes(img, lagEpisodeSpans(g.episodes, g.from, g.to, width))
		drawLagGraph(img, g.columns, g.scale)
		return img
	})
	r.top = canvas.NewText("", mutedTextColor())
	r.top.TextSize = 10
	r.objects = []fyne.CanvasObject{r.raster, r.top}
	for range lagGraphMarks {
		mark := canvas.NewText("", mutedTextColor())
		mark.TextSize = 10
		r.marks = append(r.marks, mark)
		r.objects = append(r.objects, mark)
	}
	return r
}

type lagGraphRenderer struct {
	graph   *lagGraph
	raster  *canvas.Raster
	top     *canvas.Text
	marks   []*canvas.Text
	objects []fyne.CanvasObject
}

func (r *lagGraphRenderer) Layout(size fyne.Size) {
	r.raster.Resize(size)
	r.raster.Move(fyne.NewPos(0, 0))
	r.top.Text = i18n.T("lag.point_ms", r.graph.scale)
	r.top.Move(fyne.NewPos(4, 2))
	for i, ms := range lagGraphMarks {
		mark := r.marks[i]
		mark.Text = i18n.T("lag.point_ms", ms)
		y := size.Height*(1-float32(ms)/float32(r.graph.scale)) - mark.MinSize().Height
		if ms >= r.graph.scale || y < r.top.MinSize().Height+2 {
			mark.Hide()
			continue
		}
		mark.Show()
		mark.Move(fyne.NewPos(4, y))
	}
}

func (r *lagGraphRenderer) MinSize() fyne.Size { return fyne.NewSize(480, 220) }

func (r *lagGraphRenderer) Refresh() {
	r.Layout(r.graph.Size())
	r.raster.Refresh()
	r.top.Refresh()
	for _, mark := range r.marks {
		mark.Refresh()
	}
}

func (r *lagGraphRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *lagGraphRenderer) Destroy()                     {}

// lagEpisodeSpans son las columnas [desde, hasta] de cada tramo con lag en width columnas de
// [from, to). Un tramo que sigue llega hasta lo que cubre su última muestra.
func lagEpisodeSpans(episodes []lagEpisode, from, to time.Time, width int) [][2]int {
	span := to.Sub(from)
	if width <= 0 || span <= 0 {
		return nil
	}
	column := func(at time.Time) int {
		return min(max(int(at.Sub(from)*time.Duration(width)/span), 0), width-1)
	}
	var spans [][2]int
	for _, episode := range episodes {
		end := episode.drawEnd()
		if !end.After(from) || !episode.start.Before(to) {
			continue
		}
		spans = append(spans, [2]int{column(episode.start), column(end.Add(-time.Nanosecond))})
	}
	return spans
}

// drawLagEpisodes marca los tramos con lag: el fondo apenas más rojo y una franja roja arriba.
func drawLagEpisodes(img *image.RGBA, spans [][2]int) {
	height := img.Bounds().Dy()
	for _, span := range spans {
		fillRect(img, image.Rect(span[0], 0, span[1]+1, height), withAlpha(lagRed, 22))
		fillRect(img, image.Rect(span[0], 0, span[1]+1, 5), withAlpha(lagRed, 210))
	}
}

// drawLagGraph dibuja el gráfico grande: franjas del semáforo de fondo con su línea, la pérdida en
// barras desde abajo (100 % = todo el alto) y la línea de ms en el color de cada tramo. Una columna
// vacía corta la línea.
func drawLagGraph(img *image.RGBA, columns []lagPoint, scale int) {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	y := func(ms int) int {
		return height - 1 - int(math.Round(float64(min(ms, scale))/float64(scale)*float64(height-1)))
	}
	bands := []struct {
		from, to int
		color    color.Color
	}{{0, lagGoodMS, speakingColor()}, {lagGoodMS, lagWarnMS, lagYellow}, {lagWarnMS, lagBadMS, lagOrange}, {lagBadMS, scale, lagRed}}
	for _, band := range bands {
		if band.from >= scale {
			break
		}
		fillRect(img, image.Rect(0, y(band.to), width, y(band.from)+1), withAlpha(band.color, lagBandAlpha))
		if band.from > 0 {
			fillRect(img, image.Rect(0, y(band.from), width, y(band.from)+1), withAlpha(band.color, lagLineAlpha))
		}
	}
	for x, column := range columns {
		if column.loss > 0 {
			bar := max(int(math.Round(float64(column.loss)/100*float64(height))), 2)
			fillRect(img, image.Rect(x, height-bar, x+1, height), lagLossColor)
		}
	}
	previousRTT := -1 // ms de la columna anterior (-1 = hueco: no se une)
	for x, column := range columns {
		if column.empty() || column.rtt < 0 {
			previousRTT = -1
			continue
		}
		current := y(column.rtt)
		if previousRTT >= 0 {
			// La unión con la anterior va en el color del peor de los dos: un pico queda entero en su
			// color, también la bajada.
			previous := y(previousRTT)
			fillRect(img, image.Rect(x, min(previous, current)-1, x+1, max(previous, current)+1), lagColor(max(previousRTT, column.rtt)))
		}
		fillRect(img, image.Rect(x, current-1, x+1, current+1), lagColor(column.rtt))
		previousRTT = column.rtt
	}
}
