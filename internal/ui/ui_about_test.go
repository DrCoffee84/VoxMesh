//go:build cgo

package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/i18n"
	"voxmesh/internal/version"
)

// "Acerca de": versión y commit ("dev" si el build no lo trae), el autor con el link al repo, la
// licencia y "Buscar actualizaciones", en los dos idiomas.
func TestAboutContent(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	savedValue, savedCommit := version.Value, version.Commit
	defer func() { version.Value, version.Commit = savedValue, savedCommit }()
	for _, c := range []struct{ lang, value, commit, want string }{
		{"es", "1.0.1", "c1d2462", "Versión 1.0.1 · commit c1d2462"},
		{"en", "1.0.1", "c1d2462", "Version 1.0.1 · commit c1d2462"},
		{"es", "0.9.0-build_0", "", "Versión 0.9.0-build_0 · commit dev"},
	} {
		setLanguage(t, c.lang)
		version.Value, version.Commit = c.value, c.commit
		checked := false
		content := aboutContent(func() { checked = true })
		var labels []string
		var link *widget.Hyperlink
		var button *widget.Button
		walkObjects(content, func(object fyne.CanvasObject) {
			switch o := object.(type) {
			case *widget.Label:
				labels = append(labels, o.Text)
			case *widget.Hyperlink:
				link = o
			case *widget.Button:
				button = o
			}
		})
		if !contains(labels, c.want) || !contains(labels, i18n.T("about.license")) || !contains(labels, i18n.T("about.author")) {
			t.Errorf("%s: textos %q, quería %q, la licencia y el autor", c.lang, labels, c.want)
		}
		if link == nil || link.Text != aboutAuthor || link.URL == nil || link.URL.String() != aboutRepoURL {
			t.Errorf("%s: link del autor %+v", c.lang, link)
		}
		if button == nil || button.Text != i18n.T("settings.check_updates") {
			t.Fatalf("%s: falta Buscar actualizaciones", c.lang)
		}
		test.Tap(button)
		if !checked {
			t.Errorf("%s: Buscar actualizaciones no hizo nada", c.lang)
		}
	}
}

// walkObjects recorre los contenedores (no entra en los widgets).
func walkObjects(object fyne.CanvasObject, visit func(fyne.CanvasObject)) {
	visit(object)
	if c, ok := object.(*fyne.Container); ok {
		for _, child := range c.Objects {
			walkObjects(child, visit)
		}
	}
}

func contains(list []string, text string) bool {
	for _, item := range list {
		if item == text {
			return true
		}
	}
	return false
}
