//go:build cgo

package ui

import (
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/i18n"
	"voxmesh/internal/version"
)

// Autor y repositorio que muestra "Acerca de": el mismo repo del que baja las actualizaciones.
const (
	aboutAuthor  = "DrCoffee84"
	aboutRepoURL = "https://github.com/DrCoffee84/VoxMesh"
)

// aboutVersionText: "Versión 1.0.1 · commit c1d2462" ("commit dev" si el build no lo trae).
func aboutVersionText() string {
	return i18n.T("about.version", version.Value, version.CommitText())
}

// showAbout (hilo de UI) abre "Acerca de VoxMesh": desde Ajustes → Interfaz y Sistema y desde la
// versión de la barra de estado.
func (a *App) showAbout() {
	if a.window == nil {
		return
	}
	content := aboutContent(func() { go a.checkForUpdates(true) })
	dialog.NewCustom(i18n.T("about.title"), i18n.T("common.close"), content, a.window).Show()
}

// aboutContent arma el cuerpo del diálogo: ícono, versión y commit, autor con el link al repo,
// licencia y "Buscar actualizaciones" (que deja el diálogo abierto: su resultado sale encima).
func aboutContent(checkUpdates func()) fyne.CanvasObject {
	center := func(text string, style fyne.TextStyle) *widget.Label {
		return widget.NewLabelWithStyle(text, fyne.TextAlignCenter, style)
	}
	rows := []fyne.CanvasObject{}
	if len(appIconBytes) > 0 {
		// El ícono embebido (256 px): Fyne lo decodifica al pintarlo, es chico.
		icon := canvas.NewImageFromResource(fyne.NewStaticResource("icon.png", appIconBytes))
		icon.FillMode = canvas.ImageFillContain
		icon.SetMinSize(fyne.NewSize(64, 64))
		rows = append(rows, container.NewCenter(icon))
	}
	rows = append(rows,
		center("VoxMesh", fyne.TextStyle{Bold: true}),
		center(aboutVersionText(), fyne.TextStyle{}),
	)
	author := widget.NewLabel(i18n.T("about.author"))
	if repo, err := url.Parse(aboutRepoURL); err == nil {
		rows = append(rows, container.NewCenter(container.NewHBox(author, widget.NewHyperlink(aboutAuthor, repo))))
	} else {
		rows = append(rows, container.NewCenter(container.NewHBox(author, widget.NewLabel(aboutAuthor))))
	}
	rows = append(rows,
		center(i18n.T("about.license"), fyne.TextStyle{}),
		container.NewCenter(widget.NewButtonWithIcon(i18n.T("settings.check_updates"), theme.ViewRefreshIcon(), checkUpdates)),
	)
	return container.NewVBox(rows...)
}
