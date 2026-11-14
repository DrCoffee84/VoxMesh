//go:build cgo

package ui

import (
	"errors"
	"image/color"

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
// pisan, no se renombran ni se borran.

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
type paletteDraft struct {
	name   string // paleta de la que salió (activa al abrir el editor)
	light  bool
	colors map[fyne.ThemeColorName]color.Color
}

func newPaletteDraft(p palette) *paletteDraft {
	draft := &paletteDraft{name: p.name, light: p.light, colors: make(map[fyne.ThemeColorName]color.Color, len(paletteEntries))}
	for _, entry := range paletteEntries {
		draft.colors[entry.name] = p.color(entry.name)
	}
	return draft
}

func (d *paletteDraft) palette(name string) palette {
	colors := make(map[fyne.ThemeColorName]color.Color, len(d.colors))
	for k, v := range d.colors {
		colors[k] = v
	}
	return palette{name: name, light: d.light, colors: colors}
}

// showPaletteEditor abre el editor de la paleta activa (hilo de UI).
func (a *App) showPaletteEditor(bar *paletteBar) {
	original := a.palette
	draft := newPaletteDraft(original)
	applied := false // se aplicó el borrador y no se guardó: al cerrar vuelve la paleta activa
	rows := container.NewVBox()
	for _, entry := range paletteEntries {
		entry := entry
		swatch := canvas.NewRectangle(draft.colors[entry.name])
		swatch.CornerRadius = 4
		swatch.StrokeColor = color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff}
		swatch.StrokeWidth = 1
		swatch.SetMinSize(fyne.NewSize(56, 22))
		pick := widget.NewButton("", func() {
			picker := dialog.NewColorPicker(i18n.T(entry.label), "", func(c color.Color) {
				draft.colors[entry.name] = c
				swatch.FillColor = c
				swatch.Refresh()
			}, a.window)
			picker.Advanced = true
			picker.SetColor(draft.colors[entry.name])
			picker.Show()
		})
		rows.Add(container.NewBorder(nil, nil, nil, container.NewStack(pick, container.NewPadded(swatch)), widget.NewLabel(i18n.T(entry.label))))
	}
	lightCheck := widget.NewCheck(i18n.T("palette.light"), func(on bool) { draft.light = on })
	lightCheck.SetChecked(draft.light)
	help := widget.NewLabel(i18n.T("palette.editor_help"))
	help.Wrapping = fyne.TextWrapWord
	help.Importance = widget.LowImportance
	scroll := container.NewVScroll(rows)
	scroll.SetMinSize(fyne.NewSize(380, 360))
	content := container.NewBorder(container.NewVBox(help, lightCheck), nil, nil, nil, scroll)

	var editor *dialog.CustomDialog
	write := func(name string) {
		file := draft.palette(name).file(name)
		go func() {
			err := a.palettes.Save(file, paletteReservedNames())
			names, _ := a.palettes.List(paletteReservedNames())
			fyne.Do(func() {
				if err != nil {
					a.setStatus(i18n.M("palette.save_failed", errorMsg(err)))
					return
				}
				applied = false
				a.updateConfig(func(c *config.Config) { setActivePalette(c, name) })
				a.applyPalette(paletteFromFile(file))
				bar.setOptions(names, name)
				a.setStatus(i18n.M("palette.saved", name))
				editor.Hide()
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
	applyButton := widget.NewButton(i18n.T("palette.apply"), func() {
		applied = true
		a.applyPalette(draft.palette(original.name))
	})
	saveButton := widget.NewButton(i18n.T("common.save"), func() {
		if isBuiltinPalette(original.name) {
			saveAs() // las de fábrica no se pisan
			return
		}
		write(original.name)
	})
	saveButton.Importance = widget.HighImportance
	closeButton := widget.NewButton(i18n.T("common.close"), func() {
		if applied {
			a.applyPalette(original) // lo aplicado sin guardar no queda
		}
		editor.Hide()
	})
	editor = dialog.NewCustomWithoutButtons(i18n.T("palette.editor_title", paletteLabel(original.name)), content, a.window)
	editor.SetButtons([]fyne.CanvasObject{closeButton, applyButton, widget.NewButton(i18n.T("profile.save_as"), saveAs), saveButton})
	editor.Resize(fyne.NewSize(460, 560))
	editor.Show()
}
