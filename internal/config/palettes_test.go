package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

var testReserved = []string{"Default", "Naranja", "Orange"}

func TestPaletteSaveLoadRenameDelete(t *testing.T) {
	palettes := Palettes{Dir: t.TempDir()}
	file := PaletteFile{Name: " Mi tema ", Light: true, Colors: map[string]string{"primary": "#fb8c00", "background": "#f3f4f6"}}
	if err := palettes.Save(file, testReserved); err != nil {
		t.Fatal(err)
	}
	loaded, err := palettes.Load("Mi tema")
	if err != nil || loaded.Name != "Mi tema" || !loaded.Light || loaded.Colors["primary"] != "#fb8c00" || loaded.Version != 1 {
		t.Fatalf("Load = %+v, %v", loaded, err)
	}
	if err := palettes.Rename("Mi tema", "Otro", testReserved); err != nil {
		t.Fatal(err)
	}
	if palettes.Exists("Mi tema") || !palettes.Exists("Otro") {
		t.Fatal("rename no movió el archivo")
	}
	names, err := palettes.List(testReserved)
	if err != nil || len(names) != 1 || names[0] != "Otro" {
		t.Fatalf("List = %v, %v", names, err)
	}
	if err := palettes.Delete("Otro"); err != nil {
		t.Fatal(err)
	}
	if names, _ := palettes.List(testReserved); len(names) != 0 {
		t.Fatalf("después de borrar: %v", names)
	}
}

func TestPaletteNames(t *testing.T) {
	palettes := Palettes{Dir: t.TempDir()}
	for name, want := range map[string]error{"": ErrPaletteNameEmpty, "  ": ErrPaletteNameEmpty, "orange": ErrPaletteNameReserved, "DEFAULT": ErrPaletteNameReserved} {
		if err := palettes.Save(PaletteFile{Name: name}, testReserved); !errors.Is(err, want) {
			t.Errorf("%q: %v, quería %v", name, err, want)
		}
	}
	// Una guardada antes con un nombre de fábrica (o rota) no se lista.
	if err := os.MkdirAll(palettes.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(palettes.Dir, "naranja.json"), []byte(`{"version":1,"name":"Naranja","colors":{}}`), 0600)
	_ = os.WriteFile(filepath.Join(palettes.Dir, "rota.json"), []byte(`{"name":`), 0600)
	if names, err := palettes.List(testReserved); err != nil || len(names) != 0 {
		t.Fatalf("List = %v, %v", names, err)
	}
}
