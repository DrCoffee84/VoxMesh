//go:build cgo

package ui

import (
	"errors"
	"fmt"
	"image/color"
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/config"
	"voxmesh/internal/i18n"
	"voxmesh/internal/logging"
)

// Paletas en Ajustes → Interfaz (AGENDA C15): elegir una se aplica al instante (y "Descartar" la
// revierte, como el resto del borrador); el editor cambia los colores sin aplicarlos hasta
// "Aplicar", y "Guardar" / "Guardar como…" los dejan en una paleta propia. Las de fábrica no se
// pisan, no se renombran ni se borran. En el editor, tono e intensidad mueven todos los colores
// juntos y después se arregla de a uno el que quedó mal (AGENDA C32).

// readPalette devuelve la paleta name (clave de una de fábrica o nombre de una propia). Lee un
// archivo: fuera del hilo de UI. Si no está o no se lee, Default.
func (a *App) readPalette(name string) palette {
	name = canonicalPaletteName(name)
	if p, ok := builtinPalette(name); ok {
		return p
	}
	file, err := a.palettes.Load(name)
	if err != nil {
		logging.Errorf("paleta %q: %s (uso Default)", name, logging.FormatError(err))
		p, _ := builtinPalette(defaultPaletteName)
		return p
	}
	return paletteFromFile(file)
}

// applyPalette cambia los colores de toda la app sin reiniciar (hilo de UI). Fyne refresca cada
// widget (y los themedRect); los chips de la barra de participantes se vuelven a armar porque
// guardan colores que dependen de su estado.
func (a *App) applyPalette(p palette) {
	a.palette = p
	applied := p
	fyne.CurrentApp().Settings().SetTheme(voxTheme{p: &applied})
	a.peerChips = nil
	a.refreshPeerAvatars()
}

// setActivePalette guarda en c la paleta activa (Default = "").
func setActivePalette(c *config.Config, name string) {
	name = canonicalPaletteName(name)
	if name == defaultPaletteName {
		name = ""
	}
	c.ActivePalette = name
}

// askName pide un nombre (validate lo limpia o da el error a mostrar) y llama a done con él.
func (a *App) askName(title, initial string, validate func(string) (string, error), done func(name string)) {
	entry := widget.NewEntry()
	entry.SetText(initial)
	entry.Validator = func(text string) error {
		if _, err := validate(text); err != nil {
			return errors.New(errorMsg(err).String())
		}
		return nil
	}
	form := dialog.NewForm(title, i18n.T("common.accept"), i18n.T("common.cancel"), []*widget.FormItem{widget.NewFormItem(i18n.T("profile.name"), entry)}, func(ok bool) {
		if ok {
			name, _ := validate(entry.Text)
			done(name)
		}
	}, a.window)
	form.Resize(fyne.NewSize(380, form.MinSize().Height))
	form.Show()
}

func validatePaletteName(name string) (string, error) {
	return config.ValidatePaletteName(name, paletteReservedNames())
}

// paletteBar es la fila de paletas de Ajustes. Solo hilo de UI.
type paletteBar struct {
	app      *App
	names    []string // paletas propias
	selector *widget.Select
	rename   *widget.Button
	remove   *widget.Button
}

// newPaletteBar arma la fila con las paletas propias names (leídas fuera del hilo de UI).
func (a *App) newPaletteBar(names []string) (*paletteBar, fyne.CanvasObject) {
	bar := &paletteBar{app: a}
	bar.selector = widget.NewSelect(nil, nil)
	edit := widget.NewButton(i18n.T("palette.edit"), func() { a.showPaletteEditor(bar) })
	bar.rename = widget.NewButton(i18n.T("profile.rename"), bar.renamePalette)
	bar.remove = widget.NewButton(i18n.T("profile.delete"), bar.deletePalette)
	bar.setOptions(names, canonicalPaletteName(a.conf().ActivePalette))
	bar.selector.OnChanged = func(label string) {
		if name := bar.fromLabel(label); name != canonicalPaletteName(a.conf().ActivePalette) {
			bar.choose(name)
		}
	}
	help := widget.NewLabel(i18n.T("palette.help"))
	help.Wrapping = fyne.TextWrapWord
	help.Importance = widget.LowImportance
	help.TextStyle = fyne.TextStyle{Italic: true}
	return bar, container.NewVBox(
		widget.NewLabel(i18n.T("palette.label")),
		bar.selector,
		help,
		container.NewGridWithColumns(3, edit, bar.rename, bar.remove),
	)
}

// fromLabel pasa la etiqueta del selector a la clave o el nombre de la paleta.
func (b *paletteBar) fromLabel(label string) string {
	for _, builtin := range builtinPalettes {
		if label == paletteLabel(builtin) {
			return builtin
		}
	}
	return label
}

// setOptions pone la lista (las de fábrica primero) y selecciona name sin aplicar nada.
func (b *paletteBar) setOptions(names []string, name string) {
	b.names = names
	options := make([]string, 0, len(builtinPalettes)+len(names))
	for _, builtin := range builtinPalettes {
		options = append(options, paletteLabel(builtin))
	}
	b.selector.Options = append(options, names...)
	onChanged := b.selector.OnChanged
	b.selector.OnChanged = nil
	b.selector.SetSelected(paletteLabel(name))
	b.selector.OnChanged = onChanged
	if isBuiltinPalette(name) {
		b.rename.Disable()
		b.remove.Disable()
	} else {
		b.rename.Enable()
		b.remove.Enable()
	}
}

// choose aplica la paleta name y la deja activa en el borrador de Ajustes.
func (b *paletteBar) choose(name string) {
	go func() {
		p := b.app.readPalette(name)
		fyne.Do(func() {
			b.app.updateConfig(func(c *config.Config) { setActivePalette(c, name) })
			b.app.applyPalette(p)
			b.setOptions(b.names, name)
		})
	}()
}

func (b *paletteBar) renamePalette() {
	oldName := canonicalPaletteName(b.app.conf().ActivePalette)
	if isBuiltinPalette(oldName) {
		return
	}
	b.app.askName(i18n.T("palette.rename_title"), oldName, validatePaletteName, func(newName string) {
		go func() {
			err := b.app.palettes.Rename(oldName, newName, paletteReservedNames())
			names, _ := b.app.palettes.List(paletteReservedNames())
			fyne.Do(func() {
				if err != nil {
					b.app.setStatus(i18n.M("palette.rename_failed", errorMsg(err)))
					return
				}
				b.app.updateConfig(func(c *config.Config) { setActivePalette(c, newName) })
				b.app.palette.name = newName
				b.setOptions(names, newName)
				b.app.setStatus(i18n.M("palette.renamed", newName))
			})
		}()
	})
}

func (b *paletteBar) deletePalette() {
	name := canonicalPaletteName(b.app.conf().ActivePalette)
	if isBuiltinPalette(name) {
		return
	}
	dialog.ShowConfirm(i18n.T("palette.delete_title"), i18n.T("palette.delete_confirm", name), func(ok bool) {
		if !ok {
			return
		}
		go func() {
			err := b.app.palettes.Delete(name)
			names, _ := b.app.palettes.List(paletteReservedNames())
			fyne.Do(func() {
				if err != nil {
					b.app.setStatus(i18n.M("palette.delete_failed", errorMsg(err)))
					return
				}
				b.names = names
				b.choose(defaultPaletteName)
				b.app.setStatus(i18n.M("palette.deleted", name))
			})
		}()
	}, b.app.window)
}

// paletteDraft es lo que se está editando: los colores no se ven en la app hasta "Aplicar".
//
// Tono e intensidad (AGENDA C32) mueven todos los colores juntos. Cada color guarda cómo se veía y
// con qué tono e intensidad (draftColor), y lo que se ve se calcula de nuevo desde ahí en cada
// movimiento: ir y volver deja los colores exactos (los redondeos no se acumulan) y uno elegido de
// a uno gira con los demás desde donde se eligió.
type paletteDraft struct {
	name       string // paleta de la que salió (activa al abrir el editor)
	light      bool
	hue        float64 // grados que gira el tono, −180…180
	saturation float64 // intensidad: factor de la saturación, 1 = como está
	moveStatus bool    // tono e intensidad mueven también paletteStatusColors
	colors     map[fyne.ThemeColorName]draftColor
}

// draftColor es un color del borrador: c es como se veía con el tono hue y la intensidad saturation.
type draftColor struct {
	c               color.NRGBA
	hue, saturation float64
}

func newPaletteDraft(p palette) *paletteDraft {
	draft := &paletteDraft{name: p.name, light: p.light, saturation: 1, colors: make(map[fyne.ThemeColorName]draftColor, len(paletteEntries))}
	for _, entry := range paletteEntries {
		draft.colors[entry.name] = draftColor{c: color.NRGBAModel.Convert(p.color(entry.name)).(color.NRGBA), saturation: 1}
	}
	return draft
}

// moves dice si tono e intensidad mueven el color name.
func (d *paletteDraft) moves(name fyne.ThemeColorName) bool {
	return d.moveStatus || !paletteStatusColors[name]
}

// shown es el color name como se ve ahora: en las muestras y en la app al aplicar o guardar.
func (d *paletteDraft) shown(name fyne.ThemeColorName) color.NRGBA {
	dc := d.colors[name]
	if !d.moves(name) || (dc.hue == d.hue && dc.saturation == d.saturation) {
		return dc.c
	}
	factor := 1.0 // elegido con la intensidad en 0 %: conserva la saturación con la que se eligió
	if dc.saturation > 0 {
		factor = d.saturation / dc.saturation
	}
	return shiftHue(dc.c, d.hue-dc.hue, factor)
}

// pick pone un color elegido de a uno: se ve tal cual y, si tono e intensidad lo mueven, gira con
// los demás desde los valores de ahora.
func (d *paletteDraft) pick(name fyne.ThemeColorName, c color.Color) {
	dc := draftColor{c: color.NRGBAModel.Convert(c).(color.NRGBA), saturation: 1}
	if d.moves(name) {
		dc.hue, dc.saturation = d.hue, d.saturation
	}
	d.colors[name] = dc
}

func (d *paletteDraft) palette(name string) palette {
	colors := make(map[fyne.ThemeColorName]color.Color, len(d.colors))
	for k := range d.colors {
		colors[k] = d.shown(k)
	}
	return palette{name: name, light: d.light, colors: colors}
}

// hueText es el giro del tono con signo ("+40", "0", "-40").
func hueText(degrees float64) string {
	if d := int(math.Round(degrees)); d != 0 {
		return fmt.Sprintf("%+d", d)
	}
	return "0"
}

// paletteEditor es el diálogo "Editar colores…". Solo hilo de UI.
type paletteEditor struct {
	original palette // la activa al abrir: vuelve al cerrar si se aplicó sin guardar
	draft    *paletteDraft
	applied  bool                // se aplicó el borrador y no se guardó
	swatches []*canvas.Rectangle // una por paletteEntries, en ese orden

	hue, saturation           *widget.Slider
	hueLabel, saturationLabel *widget.Label
	moveStatus                *widget.Check
	applyButton               *widget.Button
	closeButton               *widget.Button
	dialog                    *dialog.CustomDialog
}

// showPaletteEditor abre el editor de la paleta activa (hilo de UI).
func (a *App) showPaletteEditor(bar *paletteBar) {
	a.newPaletteEditor(bar).dialog.Show()
}

// newPaletteEditor arma el editor sin mostrarlo.
func (a *App) newPaletteEditor(bar *paletteBar) *paletteEditor {
	e := &paletteEditor{original: a.palette, draft: newPaletteDraft(a.palette)}
	rows := container.NewVBox()
	for _, entry := range paletteEntries {
		entry := entry
		swatch := canvas.NewRectangle(e.draft.shown(entry.name))
		swatch.CornerRadius = 4
		swatch.StrokeColor = color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff}
		swatch.StrokeWidth = 1
		swatch.SetMinSize(fyne.NewSize(56, 22))
		e.swatches = append(e.swatches, swatch)
		pick := widget.NewButton("", func() {
			picker := dialog.NewColorPicker(i18n.T(entry.label), "", func(c color.Color) {
				e.draft.pick(entry.name, c)
				e.refresh()
			}, a.window)
			picker.Advanced = true
			picker.SetColor(e.draft.shown(entry.name))
			picker.Show()
		})
		rows.Add(container.NewBorder(nil, nil, nil, container.NewStack(pick, container.NewPadded(swatch)), widget.NewLabel(i18n.T(entry.label))))
	}
	lightCheck := widget.NewCheck(i18n.T("palette.light"), func(on bool) { e.draft.light = on })
	lightCheck.SetChecked(e.draft.light)
	help := widget.NewLabel(i18n.T("palette.editor_help"))
	help.Wrapping = fyne.TextWrapWord
	help.Importance = widget.LowImportance

	// Tono e intensidad: arrancan en 0° y 100 % (se guarda el resultado, no los sliders).
	e.hueLabel = widget.NewLabel("")
	e.hue = widget.NewSlider(-180, 180)
	e.hue.Step = 1
	e.hue.OnChanged = func(v float64) {
		e.draft.hue = v
		e.refresh()
	}
	e.saturationLabel = widget.NewLabel("")
	e.saturation = widget.NewSlider(0, 200)
	e.saturation.Step = 1
	e.saturation.Value = 100
	e.saturation.OnChanged = func(v float64) {
		e.draft.saturation = v / 100
		e.refresh()
	}
	e.moveStatus = widget.NewCheck(i18n.T("palette.move_status"), func(on bool) {
		e.draft.moveStatus = on
		e.refresh()
	})
	e.refresh()

	scroll := container.NewVScroll(rows)
	scroll.SetMinSize(fyne.NewSize(380, 200))
	top := container.NewVBox(help, lightCheck,
		container.NewGridWithColumns(2, container.NewVBox(e.hueLabel, e.hue), container.NewVBox(e.saturationLabel, e.saturation)),
		e.moveStatus)
	content := container.NewBorder(top, nil, nil, nil, scroll)

	write := func(name string) {
		file := e.draft.palette(name).file(name)
		go func() {
			err := a.palettes.Save(file, paletteReservedNames())
			names, _ := a.palettes.List(paletteReservedNames())
			fyne.Do(func() {
				if err != nil {
					a.setStatus(i18n.M("palette.save_failed", errorMsg(err)))
					return
				}
				e.applied = false
				a.updateConfig(func(c *config.Config) { setActivePalette(c, name) })
				a.applyPalette(paletteFromFile(file))
				bar.setOptions(names, name)
				a.setStatus(i18n.M("palette.saved", name))
				e.dialog.Hide()
			})
		}()
	}
	saveAs := func() {
		a.askName(i18n.T("palette.save_as_title"), "", validatePaletteName, func(name string) {
			for _, existing := range bar.names {
				if existing == name {
					dialog.ShowConfirm(i18n.T("palette.exists_title"), i18n.T("palette.exists_replace", name), func(ok bool) {
						if ok {
							write(name)
						}
					}, a.window)
					return
				}
			}
			write(name)
		})
	}
	e.applyButton = widget.NewButton(i18n.T("palette.apply"), func() {
		e.applied = true
		a.applyPalette(e.draft.palette(e.original.name))
	})
	saveButton := widget.NewButton(i18n.T("common.save"), func() {
		if isBuiltinPalette(e.original.name) {
			saveAs() // las de fábrica no se pisan
			return
		}
		write(e.original.name)
	})
	saveButton.Importance = widget.HighImportance
	e.closeButton = widget.NewButton(i18n.T("common.close"), func() {
		if e.applied {
			a.applyPalette(e.original) // lo aplicado sin guardar no queda
		}
		e.dialog.Hide()
	})
	e.dialog = dialog.NewCustomWithoutButtons(i18n.T("palette.editor_title", paletteLabel(e.original.name)), content, a.window)
	e.dialog.SetButtons([]fyne.CanvasObject{e.closeButton, e.applyButton, widget.NewButton(i18n.T("profile.save_as"), saveAs), saveButton})
	e.dialog.Resize(fyne.NewSize(460, 720)) // Fyne lo achica a la ventana si no entra
	return e
}

// refresh repinta las muestras y los textos de los sliders con el borrador (solo lo que cambió).
func (e *paletteEditor) refresh() {
	e.hueLabel.SetText(i18n.T("palette.hue", hueText(e.draft.hue)))
	e.saturationLabel.SetText(i18n.T("palette.saturation", int(math.Round(e.draft.saturation*100))))
	for i, entry := range paletteEntries {
		if c := e.draft.shown(entry.name); e.swatches[i].FillColor != color.Color(c) {
			e.swatches[i].FillColor = c
			e.swatches[i].Refresh()
		}
	}
}
