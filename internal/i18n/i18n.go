// Package i18n tiene los textos visibles de la app en inglés y español (en.go, es.go). No
// depende de ningún otro paquete de VoxMesh.
//
// Cada texto es un formato de fmt: los datos van como argumentos (%s, %d...) y un % literal se
// escribe %%. Los dos catálogos tienen las mismas claves y los mismos verbos en el mismo orden
// (lo verifica TestCatalogParity).
package i18n

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
)

type Lang string

const (
	EN Lang = "en"
	ES Lang = "es"
)

// Language es una opción del selector de idioma: el nombre va en su propio idioma.
type Language struct {
	Code Lang
	Name string
}

var Languages = []Language{{EN, "English"}, {ES, "Español"}}

var catalogs = map[Lang]map[string]string{EN: en, ES: es}

// current es el idioma activo. Se fija al arrancar (Set) y se lee desde cualquier goroutine.
var current atomic.Value // Lang

// Normalize devuelve el idioma de un código guardado: "es" es español; vacío o cualquier otro,
// inglés.
func Normalize(code string) Lang {
	if Lang(strings.ToLower(strings.TrimSpace(code))) == ES {
		return ES
	}
	return EN
}

// Set cambia el idioma activo y devuelve el que quedó.
func Set(code string) Lang {
	lang := Normalize(code)
	current.Store(lang)
	return lang
}

// Current es el idioma activo (inglés si nunca se llamó a Set).
func Current() Lang {
	if lang, ok := current.Load().(Lang); ok {
		return lang
	}
	return EN
}

// T devuelve el texto de key en el idioma activo, con args aplicados.
func T(key string, args ...any) string {
	return In(Current(), key, args...)
}

// In devuelve el texto de key en lang. Si a lang le falta, usa inglés; si tampoco está, la clave.
// Los args que son Msg se escriben en el mismo idioma.
func In(lang Lang, key string, args ...any) string {
	return fmt.Sprintf(lookup(catalogs, lang, key), render(lang, args)...)
}

// Has indica si key está en el catálogo.
func Has(key string) bool {
	_, ok := en[key]
	return ok
}

// Keys devuelve todas las claves del catálogo, ordenadas.
func Keys() []string {
	keys := make([]string, 0, len(en))
	for key := range en {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func lookup(catalogs map[Lang]map[string]string, lang Lang, key string) string {
	if text, ok := catalogs[lang][key]; ok {
		return text
	}
	if text, ok := catalogs[EN][key]; ok {
		return text
	}
	return key
}

func render(lang Lang, args []any) []any {
	var out []any
	for i, arg := range args {
		if msg, ok := arg.(Msg); ok {
			if out == nil {
				out = append([]any(nil), args...)
			}
			out[i] = msg.In(lang)
		}
	}
	if out == nil {
		return args
	}
	return out
}

// Msg es un texto todavía sin traducir: se escribe en el idioma activo para mostrarlo y en
// español para el log (setStatus, avisos de la migración de host).
type Msg struct {
	Key   string // clave del catálogo; vacía = texto fijo (Plain) o partes (Join)
	Args  []any
	text  string
	parts []Msg
}

// M arma un Msg del catálogo.
func M(key string, args ...any) Msg {
	return Msg{Key: key, Args: args}
}

// Plain es un texto que no sale del catálogo y se muestra igual en todos los idiomas (el detalle
// técnico de un error, un nombre).
func Plain(text string) Msg {
	return Msg{text: text}
}

// Join junta mensajes uno detrás del otro, en el mismo idioma (cada parte trae sus espacios).
func Join(parts ...Msg) Msg {
	return Msg{parts: parts}
}

// In escribe el mensaje en lang.
func (m Msg) In(lang Lang) string {
	switch {
	case m.parts != nil:
		var b strings.Builder
		for _, part := range m.parts {
			b.WriteString(part.In(lang))
		}
		return b.String()
	case m.Key == "":
		return m.text
	}
	return In(lang, m.Key, m.Args...)
}

// String escribe el mensaje en el idioma activo.
func (m Msg) String() string {
	return m.In(Current())
}
