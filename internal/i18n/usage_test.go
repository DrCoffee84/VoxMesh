package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// keyLiteral es un literal de Go que tiene forma de clave del catálogo ("main.connect").
var keyLiteral = regexp.MustCompile(`"((?:common|main|status|chat|settings|sound|hotkey|room|phone|update|err|device|default|profile|conn|invite|audio|file|screen|about)(?:\.[a-z0-9_]+)+)"`)

// TestKeysUsedInCode: toda clave que usa el código está en el catálogo (si no, la pantalla
// mostraría la clave) y toda clave del catálogo se usa en algún lado. Las de los parámetros
// ("param.*") se arman con la clave de config.Params: las cubre TestParamTextsHaveParams.
func TestKeysUsedInCode(t *testing.T) {
	used := map[string]bool{}
	for _, dir := range []string{"../ui", "../room", "../phonemic", "../config"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			source, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			for _, match := range keyLiteral.FindAllStringSubmatch(string(source), -1) {
				used[match[1]] = true
				if !Has(match[1]) {
					t.Errorf("%s usa %q, que no está en el catálogo", filepath.Base(file), match[1])
				}
			}
		}
	}
	if len(used) == 0 {
		t.Fatal("no encontré ninguna clave en el código: ¿cambió la carpeta?")
	}
	for _, key := range Keys() {
		if !used[key] && !strings.HasPrefix(key, "param.") {
			t.Errorf("%q está en el catálogo y no la usa nadie", key)
		}
	}
}
