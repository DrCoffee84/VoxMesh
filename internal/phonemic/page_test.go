package phonemic

import (
	"strings"
	"testing"

	"voxmesh/internal/i18n"
)

// La página del celular sale en el idioma de la PC, con lang y los textos de JavaScript escapados.
func TestPhonePageLanguage(t *testing.T) {
	prev := i18n.Current()
	t.Cleanup(func() { i18n.Set(string(prev)) })
	cases := []struct {
		lang string
		want []string
	}{
		{"en", []string{`<html lang="en">`, "<p>Welcome, Ana &lt;3</p>", "Use this phone as a microphone", `state.textContent="Connected. Keep this screen open."`}},
		{"es", []string{`<html lang="es">`, "<p>Bienvenido, Ana &lt;3</p>", "Usar este celular como micrófono", `state.textContent="Conectado. Dejá esta pantalla abierta."`}},
	}
	for _, c := range cases {
		i18n.Set(c.lang)
		var page strings.Builder
		if err := phoneTemplate.Execute(&page, newPhonePage("Ana <3")); err != nil {
			t.Fatal(err)
		}
		for _, want := range c.want {
			if !strings.Contains(page.String(), want) {
				t.Errorf("%s: falta %q en la página", c.lang, want)
			}
		}
	}
}
