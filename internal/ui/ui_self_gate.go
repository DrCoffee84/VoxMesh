//go:build cgo

package ui

import (
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/config"
	"voxmesh/internal/i18n"
	"voxmesh/internal/logging"
)

// Debajo del propio nombre, el slider de "desde qué volumen me toma la voz" (AGENDA C22): mueve
// threshold_db (la puerta de ruido, perfil de Entrada), como la "sensibilidad de entrada" de
// Discord. Detrás, una barrita con el nivel después de los filtros (postDB) en vivo: verde cuando
// se manda, gris si no o si estás muteado. Solo existe dentro de una sala (el chip propio).

const (
	// selfGatePaintInterval limita los repintados de la barrita (≤ 20 por segundo).
	selfGatePaintInterval = 50 * time.Millisecond
	// selfGateBarHeight es el alto de la barrita; la línea del slider (4 px) pasa por el medio.
	selfGateBarHeight = 10
	// peerSliderWidth es el ancho de los sliders de los chips (volumen y este).
	peerSliderWidth = 110
)

// selfGate son los widgets del slider propio. Solo el hilo de UI.
type selfGate struct {
	app    *App
	slider *gateSlider
	bar    *levelBar
	value  *canvas.Text
	object fyne.CanvasObject
	// Lo último que se pintó, para no refrescar de más (cada SetText re-layoutea).
	shownDB              float32
	shownOn, shownFilter bool
}

// gateSlider es el slider con su leyenda al pasar el mouse (a.tooltip, como hintButton).
type gateSlider struct {
	widget.Slider
	app      *App
	hint     string
	hovered  bool
	hoverGen int
}

func (s *gateSlider) MouseIn(event *desktop.MouseEvent) {
	s.Slider.MouseIn(event)
	s.hovered = true
	s.hoverGen++
	gen := s.hoverGen
	time.AfterFunc(bindTooltipDelay, func() {
		fyne.Do(func() {
			if s.hovered && s.hoverGen == gen && s.hint != "" && s.app.tooltip != nil {
				s.app.tooltip.show(s, s.hint)
			}
		})
	})
}

func (s *gateSlider) MouseOut() {
	s.Slider.MouseOut()
	s.hovered = false
	s.app.hideTooltip()
}

// newSelfGate arma el slider propio con los valores de la config.
func (a *App) newSelfGate() *selfGate {
	param := config.ParamByKey("threshold_db")
	g := &selfGate{app: a, bar: newLevelBar()}
	g.slider = &gateSlider{app: a}
	g.slider.Min, g.slider.Max, g.slider.Step = param.Min, param.Max, param.Step
	g.slider.Orientation = widget.Horizontal
	g.slider.ExtendBaseWidget(g.slider)
	g.value = canvas.NewText("", mutedTextColor)
	g.value.TextSize = 10
	g.slider.OnChanged = func(value float64) {
		a.setSelfGate(value)
		g.refresh(a.conf())
	}
	g.slider.OnChangeEnded = func(value float64) {
		a.setSelfGate(value)
		g.refresh(a.conf())
		go a.saveSelfGate()
	}
	g.bar.SetColor(meterQuietColor)
	g.refresh(a.conf())
	track := container.New(sliderTrackLayout{}, g.bar)
	box := container.NewGridWrap(fyne.NewSize(peerSliderWidth, g.slider.MinSize().Height), container.NewStack(track, g.slider))
	g.object = container.NewHBox(box, container.NewCenter(g.value))
	return g
}

// setSelfGate publica el umbral (en vivo) y prende la puerta de ruido: mover el slider es pedir
// que te tome la voz desde ahí, también con un perfil que la tiene apagada ("Sin filtros").
func (a *App) setSelfGate(value float64) {
	param := config.ParamByKey("threshold_db")
	a.updateConfig(func(c *config.Config) {
		param.Set(&c.LiveSettings, value)
		c.NoiseGateEnabled = true
	})
}

// refresh pone el slider, el valor y la leyenda según cfg, sin disparar los callbacks del slider
// (SetValue de Fyne 2.6.3 también llama a OnChangeEnded y guardaría).
func (g *selfGate) refresh(cfg *config.Config) {
	on, filter := cfg.NoiseGateEnabled, cfg.AudioFilterEnabled
	if cfg.ThresholdDB == g.shownDB && on == g.shownOn && filter == g.shownFilter && g.value.Text != "" {
		return
	}
	g.shownDB, g.shownOn, g.shownFilter = cfg.ThresholdDB, on, filter
	if g.slider.Value != float64(cfg.ThresholdDB) {
		g.slider.Value = float64(cfg.ThresholdDB)
		g.slider.Refresh()
	}
	g.value.Text = strconv.FormatFloat(float64(cfg.ThresholdDB), 'f', -1, 32) + " dB"
	g.value.Color = mutedTextColor
	switch {
	case !filter:
		g.slider.hint = i18n.T("main.self_gate_filter_off")
		g.slider.Disable()
	case !on:
		g.slider.hint = i18n.T("main.self_gate_hint") + " " + i18n.T("main.self_gate_off")
		g.value.Color = meterMarkerOffColor
		g.slider.Enable()
	default:
		g.slider.hint = i18n.T("main.self_gate_hint")
		g.slider.Enable()
	}
	g.value.Refresh()
}

// paint pinta el nivel (hilo de UI) en la escala del slider, así la barra pasa el botón justo
// cuando el nivel pasa el umbral.
func (g *selfGate) paint(cfg *config.Config, postDB float32, sent bool) {
	g.refresh(cfg)
	g.bar.SetValue(selfGatePos(postDB))
	fill := meterQuietColor
	if sent {
		fill = levelFillColor
	}
	g.bar.SetColor(fill)
}

// selfGatePos lleva un nivel en dBFS a la posición del slider (su rango, el de threshold_db).
func selfGatePos(db float32) float64 {
	param := config.ParamByKey("threshold_db")
	return (float64(db) - param.Min) / (param.Max - param.Min)
}

// onSelfGateMeter recibe cada frame de entrada procesado (reportMeter, goroutine de envío) y
// repinta la barrita a lo sumo cada selfGatePaintInterval, con un solo fyne.Do. Con Ajustes abierto
// no pinta: la vista principal no está en pantalla.
func (a *App) onSelfGateMeter(m inputMeter) {
	now := time.Now().UnixNano()
	if m.sent {
		a.selfGateSent.Store(now)
	}
	last := a.selfGatePaint.Load()
	if now-last < int64(selfGatePaintInterval) || !a.selfGatePaint.CompareAndSwap(last, now) {
		return
	}
	if a.cfgBaseline.Load() != nil {
		return
	}
	sent := !m.muted && now-a.selfGateSent.Load() < int64(meterHold)
	postDB := m.postDB
	fyne.Do(func() {
		if g := a.selfGate; g != nil {
			g.paint(a.conf(), postDB, sent)
		}
	})
}

// setSelfGateWidgets engancha (o suelta, con nil) el slider propio y el oyente del medidor. Hilo
// de UI: lo llama refreshPeerAvatars al rearmar los chips.
func (a *App) setSelfGateWidgets(g *selfGate) {
	a.selfGate = g
	if g == nil {
		a.selfGateMeter.Store(nil)
		return
	}
	listener := a.onSelfGateMeter
	a.selfGateMeter.Store(&listener)
}

// saveSelfGate guarda el umbral al soltar el slider (goroutine: lee y escribe perfiles). Con un
// perfil propio activo se guarda ese; con uno de fábrica se crea "Personalizado" por el mismo
// camino que el Guardar de Ajustes (config.ProfileForBuiltinChanges), así el de fábrica no se
// pisa y el valor sobrevive al reinicio.
func (a *App) saveSelfGate() {
	a.selfGateSaveMu.Lock()
	defer a.selfGateSaveMu.Unlock()
	cfg := a.conf()
	active := activeProfile(config.InputProfile, cfg)
	if !config.IsBuiltinProfile(active) {
		if err := a.profiles.Save(config.InputProfile, active, cfg.LiveSettings); err != nil {
			a.setStatus(i18n.M("profile.save_failed", errorMsg(err)))
		}
		a.saveConfig()
		return
	}
	names, err := a.profiles.List(config.InputProfile)
	if err != nil {
		logging.Errorf("perfiles de entrada: %s", logging.FormatError(err))
	}
	name, ok := config.ProfileForBuiltinChanges(config.InputProfile, cfg.LiveSettings, names, i18n.T("profile.custom_name"))
	if !ok {
		a.saveConfig() // volvió al valor de fábrica: no hace falta un perfil
		return
	}
	if err := a.profiles.Save(config.InputProfile, name, cfg.LiveSettings); err != nil {
		a.setStatus(i18n.M("profile.create_failed", name, errorMsg(err)))
		return
	}
	a.updateConfig(func(c *config.Config) { setActiveProfile(config.InputProfile, c, name) })
	a.saveConfig()
	a.setStatus(i18n.M("main.self_gate_profile", name))
}

// sliderTrackLayout pone la barrita a lo largo de la línea del slider que tiene encima: Fyne
// 2.6.3 deja a cada lado el radio del botón + el relleno interno − 1.5 (Slider.endOffset).
type sliderTrackLayout struct{}

func (sliderTrackLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	inset := (theme.Size(theme.SizeNameInlineIcon)-4)/2 + theme.Size(theme.SizeNameInnerPadding) - 1.5
	for _, object := range objects {
		object.Move(fyne.NewPos(inset, (size.Height-selfGateBarHeight)/2))
		object.Resize(fyne.NewSize(max(size.Width-2*inset, 0), selfGateBarHeight))
	}
}

func (sliderTrackLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(0, 0) }
