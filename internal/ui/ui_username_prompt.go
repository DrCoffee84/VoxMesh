//go:build cgo

package ui

import (
	"regexp"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/config"
	"voxmesh/internal/i18n"
	"voxmesh/internal/room"
)

// hostNameSuffix es el " (2)" que agrega el host cuando el nombre ya está en la sala
// (room.State.UniqueUsername): el cliente lo guarda en su config.
var hostNameSuffix = regexp.MustCompile(` \(\d+\)$`)

// isFactoryUsername: el nombre está vacío o es el de fábrica en algún idioma ("Usuario", "User"),
// sin importar mayúsculas y también con el sufijo del host ("Usuario (2)").
func isFactoryUsername(name string) bool {
	name = strings.TrimSpace(hostNameSuffix.ReplaceAllString(strings.TrimSpace(name), ""))
	if name == "" {
		return true
	}
	factory := []string{config.DefaultUsername, i18n.M("default.username").In(i18n.ES), i18n.M("default.username").In(i18n.EN)}
	for _, candidate := range factory {
		if strings.EqualFold(name, candidate) {
			return true
		}
	}
	return false
}

// needsUsernamePrompt: al abrir la app se pide el nombre si es uno de fábrica y no se marcó
// "No volver a preguntar".
func needsUsernamePrompt(cfg *config.Config) bool {
	return !cfg.UsernamePromptOff && isFactoryUsername(cfg.Username)
}

// usernamePrompt es el diálogo que pide el nombre al abrir la app (los campos son para los tests).
type usernamePrompt struct {
	dialog  *dialog.CustomDialog
	entry   *escapeEntry
	dontAsk *widget.Check
	save    *widget.Button
	later   *widget.Button
}

// askUsernameAtStart (hilo de UI, una vez desde Run, con la ventana ya visible) pide el nombre si
// hace falta (needsUsernamePrompt). then corre cuando se cierra el diálogo, o enseguida si no se
// muestra: ahí arranca lo que puede abrir otro diálogo de inicio (las actualizaciones), así nada
// lo tapa.
func (a *App) askUsernameAtStart(then func()) {
	if a.window == nil || !needsUsernamePrompt(a.conf()) {
		then()
		return
	}
	a.showUsernamePrompt(then)
}

// showUsernamePrompt (hilo de UI) muestra el diálogo. Guardar hace lo mismo que Ajustes con el
// nombre (ClampUsername, updateConfig + saveConfig, selfNameLabel). "Ahora no" y Esc lo cierran
// sin tocar el nombre: la próxima vez vuelve a preguntar, salvo que se haya marcado el check.
func (a *App) showUsernamePrompt(then func()) *usernamePrompt {
	p := &usernamePrompt{dontAsk: widget.NewCheck(i18n.T("main.name_prompt.dont_ask"), nil)}
	finish := func(save bool) {
		name := room.ClampUsername(p.entry.Text)
		if save && name == "" {
			return // Enter con la entrada vacía
		}
		off := p.dontAsk.Checked
		if save || off {
			cfg := a.updateConfig(func(c *config.Config) {
				if save {
					c.Username = name
				}
				if off {
					c.UsernamePromptOff = true
				}
			})
			a.saveConfig()
			if save && a.selfNameLabel != nil {
				a.selfNameLabel.SetText(cfg.Username)
			}
		}
		p.dialog.Hide()
	}
	p.entry = newEscapeEntry(func() { finish(false) })
	p.entry.SetPlaceHolder(i18n.T("main.name_prompt.placeholder"))
	p.entry.OnSubmitted = func(string) { finish(true) }
	p.save = widget.NewButton(i18n.T("common.save"), func() { finish(true) })
	p.save.Importance = widget.HighImportance
	p.save.Disable()
	p.entry.OnChanged = func(text string) {
		if room.ClampUsername(text) == "" {
			p.save.Disable()
		} else {
			p.save.Enable()
		}
	}
	p.later = widget.NewButton(i18n.T("main.name_prompt.later"), func() { finish(false) })

	text := widget.NewLabel(i18n.T("main.name_prompt.text"))
	text.Wrapping = fyne.TextWrapWord
	content := container.NewVBox(text, p.entry, p.dontAsk)
	p.dialog = dialog.NewCustomWithoutButtons(i18n.T("main.name_prompt.title"), content, a.window)
	p.dialog.SetButtons([]fyne.CanvasObject{p.later, p.save})
	p.dialog.SetOnClosed(then)
	p.dialog.Resize(fyne.NewSize(420, 0)) // el texto hace salto de línea: sin esto el diálogo queda angosto
	p.dialog.Show()
	a.window.Canvas().Focus(p.entry)
	return p
}

// escapeEntry es una entrada de una línea que avisa cuando se aprieta Esc (los diálogos de Fyne
// 2.6.3 no se cierran con Esc).
type escapeEntry struct {
	widget.Entry
	onEscape func()
}

func newEscapeEntry(onEscape func()) *escapeEntry {
	entry := &escapeEntry{onEscape: onEscape}
	entry.ExtendBaseWidget(entry)
	return entry
}

func (e *escapeEntry) TypedKey(key *fyne.KeyEvent) {
	if key.Name == fyne.KeyEscape && e.onEscape != nil {
		e.onEscape()
		return
	}
	e.Entry.TypedKey(key)
}
