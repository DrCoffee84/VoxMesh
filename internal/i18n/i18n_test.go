package i18n

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

// verbs devuelve los verbos de fmt de un texto, completos ("%s", "%.1f", "%q"), en orden. %% es
// un literal. Un % suelto o con un verbo que fmt no conoce es un error: el texto saldría con
// "%!".
func verbs(text string) ([]string, error) {
	var found []string
	for i := 0; i < len(text); i++ {
		if text[i] != '%' {
			continue
		}
		start := i
		i++
		if i < len(text) && text[i] == '%' {
			continue
		}
		for i < len(text) && strings.IndexByte("+-# 0", text[i]) >= 0 {
			i++
		}
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
		if i < len(text) && text[i] == '.' {
			i++
			for i < len(text) && text[i] >= '0' && text[i] <= '9' {
				i++
			}
		}
		if i >= len(text) || strings.IndexByte("vTtbcdoOqxXUeEfFgGsp", text[i]) < 0 {
			return nil, fmt.Errorf("%% suelto o verbo inválido en la posición %d", start)
		}
		found = append(found, text[start:i+1])
	}
	return found, nil
}

func TestVerbs(t *testing.T) {
	got, err := verbs("%s tiene %d%% · %.1f ms (%q)")
	if err != nil || !slices.Equal(got, []string{"%s", "%d", "%.1f", "%q"}) {
		t.Fatalf("verbs = %q, %v", got, err)
	}
	for _, bad := range []string{"100 % = todo", "fin %", "%z"} {
		if _, err := verbs(bad); err == nil {
			t.Fatalf("verbs(%q) no dio error", bad)
		}
	}
}

// TestCatalogParity: un texto agregado en un solo idioma, o con otros datos en el otro, falla acá.
func TestCatalogParity(t *testing.T) {
	for key := range en {
		if _, ok := es[key]; !ok {
			t.Errorf("%q está en en.go y falta en es.go", key)
		}
	}
	for key := range es {
		if _, ok := en[key]; !ok {
			t.Errorf("%q está en es.go y falta en en.go", key)
		}
	}
	for key, enText := range en {
		esText, ok := es[key]
		if !ok {
			continue
		}
		if strings.TrimSpace(enText) == "" || strings.TrimSpace(esText) == "" {
			t.Errorf("%q tiene un texto vacío", key)
		}
		enVerbs, enErr := verbs(enText)
		esVerbs, esErr := verbs(esText)
		if enErr != nil {
			t.Errorf("%q (en): %v", key, enErr)
		}
		if esErr != nil {
			t.Errorf("%q (es): %v", key, esErr)
		}
		if enErr == nil && esErr == nil && !slices.Equal(enVerbs, esVerbs) {
			t.Errorf("%q: verbos distintos: en %q, es %q", key, enVerbs, esVerbs)
		}
	}
}

func TestLookupFallback(t *testing.T) {
	catalogs := map[Lang]map[string]string{
		EN: {"both": "Hello", "en.only": "English only"},
		ES: {"both": "Hola"},
	}
	cases := []struct {
		lang      Lang
		key, want string
	}{
		{ES, "both", "Hola"},
		{EN, "both", "Hello"},
		{ES, "en.only", "English only"}, // le falta a es: inglés
		{ES, "missing", "missing"},      // no está en ninguno: la clave
		{"fr", "both", "Hello"},
	}
	for _, c := range cases {
		if got := lookup(catalogs, c.lang, c.key); got != c.want {
			t.Errorf("lookup(%s, %q) = %q, quería %q", c.lang, c.key, got, c.want)
		}
	}
}

func TestNormalizeAndSet(t *testing.T) {
	prev := Current()
	t.Cleanup(func() { Set(string(prev)) })
	for code, want := range map[string]Lang{"": EN, "en": EN, "es": ES, " ES ": ES, "fr": EN, "es-AR": EN} {
		if got := Normalize(code); got != want {
			t.Errorf("Normalize(%q) = %s, quería %s", code, got, want)
		}
	}
	if got := Set("es"); got != ES || Current() != ES {
		t.Fatalf("Set(es) = %s, Current = %s", got, Current())
	}
	if got := Set("xx"); got != EN || Current() != EN {
		t.Fatalf("Set(xx) = %s, Current = %s", got, Current())
	}
}

func TestMsg(t *testing.T) {
	prev := Current()
	t.Cleanup(func() { Set(string(prev)) })
	Set("en")
	key := "param.rnnoise_mix.help" // tiene un %% literal
	if got := T(key); strings.Contains(got, "%%") || !strings.HasPrefix(got, "100 % ") {
		t.Fatalf("T(%q) = %q", key, got)
	}
	if got := Plain("100 %% raw").In(ES); got != "100 %% raw" {
		t.Fatalf("Plain cambió el texto: %q", got)
	}
	if got := T("does.not.exist"); got != "does.not.exist" {
		t.Fatalf("clave desconocida = %q", got)
	}
	// Un Msg dentro de los args se escribe en el idioma del de afuera (el log va en español
	// aunque la pantalla esté en inglés).
	nested := M("param.shout_enabled.label")
	outer := M("param.audio_filter_enabled.label")
	outer.Args = []any{nested}
	args := render(ES, outer.Args)
	if args[0] != es["param.shout_enabled.label"] {
		t.Fatalf("Msg anidado en es = %v", args[0])
	}
	if got := nested.String(); got != en["param.shout_enabled.label"] {
		t.Fatalf("String() en inglés = %q", got)
	}
	joined := Join(Plain("» "), nested, Join(), Plain(" «"))
	if got := joined.In(ES); got != "» "+es["param.shout_enabled.label"]+" «" {
		t.Fatalf("Join en es = %q", got)
	}
}

func TestConcurrentT(t *testing.T) {
	prev := Current()
	t.Cleanup(func() { Set(string(prev)) })
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				_ = T("param.shout_enabled.label")
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				Set([]string{"en", "es"}[j%2])
			}
		}()
	}
	wg.Wait()
}
