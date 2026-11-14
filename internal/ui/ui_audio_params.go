//go:build cgo

package ui

import (
	"errors"
	"fmt"
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/config"
	"voxmesh/internal/i18n"
)

// Controles de Ajustes generados desde la tabla config.Params: cada parámetro es un check o un
// slider con su rango, paso y unidad. Los cambios se publican en vivo (borrador de ajustes, ver
// renderSettingsView) y los perfiles cargan/guardan los de su tipo.

// paramSection es un grupo del acordeón de Configuración avanzada.
type paramSection struct {
	title string // clave de i18n
	keys  []string
}

// inputSections cubre todos los parámetros del perfil de entrada salvo audio_filter_enabled
// (el check principal, fuera del acordeón). Lo verifica TestParamSectionsCoverProfiles.
var inputSections = []paramSection{
	{"settings.section.shout", []string{"shout_enabled", "shout_threshold_db", "shout_on_clip", "shout_dry_mix", "shout_hold_ms"}},
	{"settings.section.noise", []string{"aec_enabled", "rnnoise_enabled", "rnnoise_mix", "vad_enabled", "vad_threshold", "vad_hold_ms", "noise_gate_enabled", "threshold_db", "gate_hold_ms"}},
	{"settings.section.eq", []string{"high_pass_enabled", "high_pass_hz", "low_pass_enabled", "low_pass_hz", "notch_enabled", "notch_hz", "notch_q"}},
	{"settings.section.dynamics", []string{"input_gain_db", "compressor_enabled", "compressor_threshold_db", "compressor_ratio", "compressor_attack_ms", "compressor_release_ms",
		"expander_enabled", "expander_threshold_db", "expander_ratio", "expander_attack_ms", "expander_release_ms", "limiter_enabled", "limiter_threshold_db"}},
	{"settings.section.send", []string{"send_ceiling_db", "send_limiter_release_ms", "preroll_ms", "fade_in_ms"}},
	{"settings.section.opus", []string{"opus_music_mode", "opus_bitrate_kbps", "opus_complexity", "opus_fec", "opus_loss_perc"}},
	{"settings.section.phone", []string{"phone_mic_buffer_enabled", "phone_mic_buffer_ms", "phone_drift_slack_ms", "phone_drift_force_ms"}},
}

// outputSections: igual para la salida (sin output_filter_enabled).
var outputSections = []paramSection{
	{"settings.section.shout_duck", []string{"shout_duck_enabled", "shout_duck_db", "shout_duck_threshold_db"}},
	{"settings.section.filters", []string{"output_gain_db", "output_high_pass_enabled", "output_high_pass_hz", "output_low_pass_enabled", "output_low_pass_hz", "output_limiter_enabled", "output_limiter_db"}},
	{"settings.section.receive_buffer", []string{"jitter_buffer_ms", "jitter_min_ms", "jitter_percentile", "jitter_margin_ms", "jitter_plc_max_ms", "jitter_talkspurt_gap_ms", "jitter_drift_slack_ms", "jitter_drift_force_ms"}},
	{"settings.section.mix", []string{"bus_limiter_db", "bus_limiter_release_ms", "peer_volume_max"}},
}

// paramControls arma los widgets de los parámetros y sabe repintarlos desde la config (al cargar
// un perfil). Solo se usa en el hilo de UI.
type paramControls struct {
	app        *App
	refreshers []func(cfg *config.Config)
	onChange   []func() // después de cada cambio (indicador "(modificado)" de los perfiles)
}

func (c *paramControls) changed() {
	for _, fn := range c.onChange {
		fn()
	}
}

// refresh vuelve a pintar todos los controles con los valores de cfg.
func (c *paramControls) refresh(cfg *config.Config) {
	for _, fn := range c.refreshers {
		fn(cfg)
	}
}

// control devuelve el check o el slider (con su texto) de un parámetro.
func (c *paramControls) control(key string) fyne.CanvasObject {
	param := config.ParamByKey(key)
	cfg := c.app.conf()
	var objects []fyne.CanvasObject
	if param.IsBool() {
		check := widget.NewCheck(paramLabel(param), nil)
		check.SetChecked(param.Get(&cfg.LiveSettings) != 0)
		check.OnChanged = func(checked bool) {
			value := 0.0
			if checked {
				value = 1
			}
			c.app.updateConfig(func(cc *config.Config) { param.Set(&cc.LiveSettings, value) })
			c.changed()
		}
		c.refreshers = append(c.refreshers, func(cfg *config.Config) {
			check.SetChecked(param.Get(&cfg.LiveSettings) != 0)
		})
		objects = append(objects, check)
	} else {
		scale := param.DisplayScale()
		label := widget.NewLabel("")
		label.Wrapping = fyne.TextWrapWord // "Etiqueta: valor" largo no ensancha Ajustes
		setText := func(display float64) {
			label.SetText(fmt.Sprintf("%s: %s", paramLabel(param), formatParamValue(param, display)))
		}
		slider := widget.NewSlider(param.Min*scale, param.Max*scale)
		slider.Step = param.Step * scale
		slider.Value = param.Get(&cfg.LiveSettings) * scale
		setText(slider.Value)
		slider.OnChanged = func(display float64) {
			c.app.updateConfig(func(cc *config.Config) { param.Set(&cc.LiveSettings, display/scale) })
			setText(display)
			c.changed()
		}
		c.refreshers = append(c.refreshers, func(cfg *config.Config) {
			slider.SetValue(param.Get(&cfg.LiveSettings) * scale)
		})
		objects = append(objects, label, slider)
	}
	if text, ok := paramHelp(param); ok {
		help := widget.NewLabel(text)
		help.Wrapping = fyne.TextWrapWord
		help.Importance = widget.LowImportance
		help.TextStyle = fyne.TextStyle{Italic: true}
		objects = append(objects, help)
	}
	return container.NewVBox(objects...)
}

// paramLabel es el nombre de un parámetro en el idioma de la app ("param.<clave>.label").
func paramLabel(param config.Param) string {
	return i18n.T("param." + param.Key + ".label")
}

// paramHelp es la aclaración de un parámetro, si tiene ("param.<clave>.help").
func paramHelp(param config.Param) (string, bool) {
	key := "param." + param.Key + ".help"
	if !i18n.Has(key) {
		return "", false
	}
	return i18n.T(key), true
}

// formatParamValue muestra el valor con los decimales del paso y su unidad.
func formatParamValue(param config.Param, display float64) string {
	decimals := int(math.Max(0, math.Ceil(-math.Log10(param.Step*param.DisplayScale())-1e-9)))
	text := fmt.Sprintf("%.*f", decimals, display)
	if param.Unit != "" {
		text += " " + param.Unit
	}
	return text
}

// accordion arma el acordeón de secciones (todas cerradas al abrir).
func (c *paramControls) accordion(sections []paramSection) *widget.Accordion {
	accordion := widget.NewAccordion()
	accordion.MultiOpen = true
	for _, section := range sections {
		var items []fyne.CanvasObject
		for _, key := range section.keys {
			items = append(items, c.control(key))
		}
		accordion.Append(widget.NewAccordionItem(i18n.T(section.title), container.NewVBox(items...)))
	}
	return accordion
}

// profileState es lo que se lee del disco al abrir ajustes (fuera del hilo de UI).
type profileState struct {
	names  []string
	loaded config.LiveSettings // valores del perfil activo, para el indicador "(modificado)"
}

// loadProfileState lista los perfiles de un tipo y lee el activo. Hace I/O: fuera del hilo de UI.
func (a *App) loadProfileState(kind config.ProfileKind, cfg *config.Config) profileState {
	names, err := a.profiles.List(kind)
	if err != nil {
		a.setStatus(i18n.M("profile.list_failed", err.Error()))
	}
	loaded, err := a.profiles.Values(kind, activeProfile(kind, cfg), cfg.LiveSettings)
	if err != nil {
		// El archivo del perfil activo ya no está (se borró a mano): se toman los valores actuales.
		loaded = cfg.LiveSettings
	}
	return profileState{names: names, loaded: loaded}
}

// activeProfile es el perfil activo de kind: un nombre guardado o la clave de uno de fábrica
// (config.DefaultProfileName, config.NoFiltersProfileName).
func activeProfile(kind config.ProfileKind, cfg *config.Config) string {
	name := cfg.ActiveInputProfile
	if kind == config.OutputProfile {
		name = cfg.ActiveOutputProfile
	}
	return config.CanonicalProfileName(name)
}

// profileLabel es cómo se ve un perfil en el selector: "Sin filtros" en el idioma de la app; el
// resto tal cual ("Default" no se traduce).
func profileLabel(name string) string {
	if name == config.NoFiltersProfileName {
		return i18n.T("profile.no_filters")
	}
	return name
}

// profileFromLabel es la inversa de profileLabel. No hay ambigüedad: ningún perfil guardado se
// llama como uno de fábrica (config.ValidateProfileName, Profiles.List).
func profileFromLabel(label string) string {
	if label == i18n.T("profile.no_filters") {
		return config.NoFiltersProfileName
	}
	return label
}

func setActiveProfile(kind config.ProfileKind, c *config.Config, name string) {
	if name == config.DefaultProfileName {
		name = ""
	}
	if kind == config.OutputProfile {
		c.ActiveOutputProfile = name
	} else {
		c.ActiveInputProfile = name
	}
}

// profileBar es la fila de perfiles arriba de cada Configuración avanzada: elegir, guardar,
// guardar como, renombrar y borrar. Cargar un perfil cambia el borrador en vivo (se puede
// escuchar y descartar); guardar/renombrar/borrar escriben en disco al instante.
type profileBar struct {
	app      *App
	kind     config.ProfileKind
	controls *paramControls
	state    profileState

	selector *widget.Select
	modified *widget.Label
	help     *widget.Label // qué es "Sin filtros" (solo con ese perfil elegido)
	rename   *widget.Button
	remove   *widget.Button
}

func (a *App) newProfileBar(kind config.ProfileKind, controls *paramControls, state profileState) (*profileBar, fyne.CanvasObject) {
	bar := &profileBar{app: a, kind: kind, controls: controls, state: state}
	bar.selector = widget.NewSelect(nil, nil)
	bar.modified = widget.NewLabel("")
	bar.modified.Importance = widget.WarningImportance
	bar.help = widget.NewLabel("")
	bar.help.Wrapping = fyne.TextWrapWord
	bar.help.Importance = widget.LowImportance
	bar.help.TextStyle = fyne.TextStyle{Italic: true}
	save := widget.NewButton(i18n.T("common.save"), bar.save)
	saveAs := widget.NewButton(i18n.T("profile.save_as"), bar.saveAs)
	bar.rename = widget.NewButton(i18n.T("profile.rename"), bar.renameProfile)
	bar.remove = widget.NewButton(i18n.T("profile.delete"), bar.deleteProfile)
	bar.setOptions(state.names, activeProfile(kind, a.conf()))
	bar.selector.OnChanged = func(label string) {
		if name := profileFromLabel(label); name != activeProfile(kind, a.conf()) {
			bar.load(name)
		}
	}
	controls.onChange = append(controls.onChange, bar.updateModified)
	bar.updateModified()
	row := container.NewBorder(nil, nil, widget.NewLabel(i18n.T("profile.label")), bar.modified, bar.selector)
	// 2×2: la tarjeta ocupa media ventana y cuatro botones en fila la ensanchaban.
	return bar, container.NewVBox(row, bar.help, container.NewGridWithColumns(2, save, saveAs, bar.rename, bar.remove))
}

// setOptions pone la lista (los de fábrica primero) y selecciona name sin disparar una carga.
func (b *profileBar) setOptions(names []string, name string) {
	b.state.names = names
	options := make([]string, 0, len(config.BuiltinProfiles)+len(names))
	for _, builtin := range config.BuiltinProfiles {
		options = append(options, profileLabel(builtin))
	}
	b.selector.Options = append(options, names...)
	onChanged := b.selector.OnChanged
	b.selector.OnChanged = nil
	b.selector.SetSelected(profileLabel(name))
	b.selector.OnChanged = onChanged
	if config.IsBuiltinProfile(name) {
		b.rename.Disable()
		b.remove.Disable()
	} else {
		b.rename.Enable()
		b.remove.Enable()
	}
	if name == config.NoFiltersProfileName {
		key := "profile.no_filters_help_input"
		if b.kind == config.OutputProfile {
			key = "profile.no_filters_help_output"
		}
		b.help.SetText(i18n.T(key))
		b.help.Show()
	} else {
		b.help.Hide()
	}
}

func (b *profileBar) updateModified() {
	if config.SameProfileValues(b.kind, b.state.loaded, b.app.conf().LiveSettings) {
		b.modified.SetText("")
	} else {
		b.modified.SetText(i18n.T("profile.modified"))
	}
}

// load lee el perfil (en segundo plano) y lo aplica al borrador.
func (b *profileBar) load(name string) {
	go func() {
		values, err := b.app.profiles.Values(b.kind, name, b.app.conf().LiveSettings)
		fyne.Do(func() { b.applyLoaded(name, values, err) })
	}()
}

// applyLoaded pone en el borrador los valores leídos del perfil name (hilo de UI).
func (b *profileBar) applyLoaded(name string, values config.LiveSettings, err error) {
	if err != nil {
		b.app.setStatus(i18n.M("profile.load_failed", errorMsg(err)))
		b.setOptions(b.state.names, activeProfile(b.kind, b.app.conf()))
		return
	}
	cfg := b.app.updateConfig(func(c *config.Config) {
		for _, param := range config.ProfileParams(b.kind) {
			param.Set(&c.LiveSettings, param.Get(&values))
		}
		setActiveProfile(b.kind, c, name)
	})
	b.state.loaded = values
	b.setOptions(b.state.names, name)
	b.controls.refresh(cfg)
	b.updateModified()
	b.app.setStatus(i18n.M("profile.loaded", profileLabel(name)))
}

// saveBuiltinChangesAsProfile se llama al Guardar la configuración: si el perfil activo es de
// fábrica (Default o Sin filtros) y los valores cambiaron, crea un perfil nuevo ("Personalizado",
// "Personalizado 2"...) con ellos y lo deja activo. Los de fábrica se vuelven a aplicar al arrancar
// (config.ApplyBuiltinProfiles), así que si no esos cambios se perderían.
func (b *profileBar) saveBuiltinChangesAsProfile() (string, bool) {
	cfg := b.app.conf()
	name, ok := config.ProfileForBuiltinChanges(b.kind, cfg.LiveSettings, b.state.names, i18n.T("profile.custom_name"))
	if !ok {
		return "", false
	}
	current := cfg.LiveSettings
	b.app.updateConfig(func(c *config.Config) { setActiveProfile(b.kind, c, name) })
	go func() {
		if err := b.app.profiles.Save(b.kind, name, current); err != nil {
			b.app.setStatus(i18n.M("profile.create_failed", name, errorMsg(err)))
		}
	}()
	return name, true
}

// loadDefault es el botón ↺: carga el perfil de fábrica.
func (b *profileBar) loadDefault() {
	b.load(config.DefaultProfileName)
}

func (b *profileBar) save() {
	name := activeProfile(b.kind, b.app.conf())
	if config.IsBuiltinProfile(name) {
		b.saveAs() // los de fábrica no se pisan: pide un nombre
		return
	}
	b.write(name, false)
}

// write guarda los valores actuales como name y lo deja activo; reload relee la lista.
func (b *profileBar) write(name string, reload bool) {
	current := b.app.conf().LiveSettings
	go func() {
		err := b.app.profiles.Save(b.kind, name, current)
		var names []string
		if err == nil && reload {
			names, _ = b.app.profiles.List(b.kind)
		}
		fyne.Do(func() {
			if err != nil {
				b.app.setStatus(i18n.M("profile.save_failed", errorMsg(err)))
				return
			}
			b.app.updateConfig(func(c *config.Config) { setActiveProfile(b.kind, c, name) })
			b.state.loaded = current
			if !reload {
				names = b.state.names
			}
			b.setOptions(names, name)
			b.updateModified()
			b.app.setStatus(i18n.M("profile.saved", name))
		})
	}()
}

// askName pide un nombre de perfil y llama a done con el nombre válido.
func (b *profileBar) askName(title, initial string, done func(name string)) {
	entry := widget.NewEntry()
	entry.SetText(initial)
	entry.Validator = func(text string) error {
		if _, err := config.ValidateProfileName(text); err != nil {
			return errors.New(errorMsg(err).String())
		}
		return nil
	}
	form := dialog.NewForm(title, i18n.T("common.accept"), i18n.T("common.cancel"), []*widget.FormItem{widget.NewFormItem(i18n.T("profile.name"), entry)}, func(ok bool) {
		if ok {
			name, _ := config.ValidateProfileName(entry.Text)
			done(name)
		}
	}, b.app.window)
	form.Resize(fyne.NewSize(380, form.MinSize().Height))
	form.Show()
}

func (b *profileBar) saveAs() {
	b.askName(i18n.T("profile.save_as_title"), "", func(name string) {
		for _, existing := range b.state.names {
			if existing == name {
				dialog.ShowConfirm(i18n.T("profile.exists_title"), i18n.T("profile.exists_replace", name), func(ok bool) {
					if ok {
						b.write(name, true)
					}
				}, b.app.window)
				return
			}
		}
		b.write(name, true)
	})
}

func (b *profileBar) renameProfile() {
	oldName := activeProfile(b.kind, b.app.conf())
	if config.IsBuiltinProfile(oldName) {
		return
	}
	b.askName(i18n.T("profile.rename_title"), oldName, func(newName string) {
		go func() {
			err := b.app.profiles.Rename(b.kind, oldName, newName)
			names, _ := b.app.profiles.List(b.kind)
			fyne.Do(func() {
				if err != nil {
					b.app.setStatus(i18n.M("profile.rename_failed", errorMsg(err)))
					return
				}
				b.app.updateConfig(func(c *config.Config) { setActiveProfile(b.kind, c, newName) })
				b.setOptions(names, newName)
				b.app.setStatus(i18n.M("profile.renamed", newName))
			})
		}()
	})
}

func (b *profileBar) deleteProfile() {
	name := activeProfile(b.kind, b.app.conf())
	if config.IsBuiltinProfile(name) {
		return
	}
	dialog.ShowConfirm(i18n.T("profile.delete_title"), i18n.T("profile.delete_confirm", name), func(ok bool) {
		if !ok {
			return
		}
		go func() {
			err := b.app.profiles.Delete(b.kind, name)
			names, _ := b.app.profiles.List(b.kind)
			fyne.Do(func() {
				if err != nil {
					b.app.setStatus(i18n.M("profile.delete_failed", errorMsg(err)))
					return
				}
				b.app.updateConfig(func(c *config.Config) { setActiveProfile(b.kind, c, config.DefaultProfileName) })
				cfg := config.Default()
				b.state.loaded = cfg.LiveSettings
				b.setOptions(names, config.DefaultProfileName)
				b.updateModified()
				b.app.setStatus(i18n.M("profile.deleted", name))
			})
		}()
	}, b.app.window)
}
