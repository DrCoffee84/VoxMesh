package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Paletas de colores propias (AGENDA C15): un JSON por paleta en <AppDir>/paletas/, con el mismo
// patrón que los perfiles de audio. Las de fábrica (Default y las plantillas) las define la UI y no
// son archivos. Cada color va como "#rrggbb" o "#rrggbbaa" con su clave; una clave que falta o no
// se entiende la completa la UI con la paleta de fábrica (sin migraciones).

// PaletteFile es una paleta guardada.
type PaletteFile struct {
	Version int               `json:"version"`
	Name    string            `json:"name"`
	Light   bool              `json:"light"` // base clara (texto oscuro): los colores de Fyne que no están, del tema claro
	Colors  map[string]string `json:"colors"`
}

// Errores de nombres de paleta que la UI le muestra al usuario (los reconoce con errors.Is).
var (
	ErrPaletteNameEmpty    = errors.New("el nombre de la paleta está vacío")
	ErrPaletteNameReserved = errors.New("ese nombre es de una paleta de fábrica")
	ErrPaletteNameTooLong  = errors.New("el nombre de la paleta es demasiado largo")
	ErrPaletteExists       = errors.New("ya existe una paleta")
)

const paletteVersion = 1

// Palettes maneja las paletas guardadas en Dir (normalmente PalettesDir()).
type Palettes struct {
	Dir string
}

// PalettesDir es la carpeta de las paletas propias.
func PalettesDir() (string, error) {
	root, err := AppDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "paletas"), nil
}

func (p Palettes) path(name string) string {
	return filepath.Join(p.Dir, profileFileName(name)+".json")
}

// ValidatePaletteName devuelve el nombre limpio o un error si no se puede usar. reserved son los
// nombres de las paletas de fábrica (en todos los idiomas): se comparan sin mayúsculas.
func ValidatePaletteName(name string, reserved []string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", ErrPaletteNameEmpty
	case len([]rune(name)) > 60:
		return "", ErrPaletteNameTooLong
	}
	for _, r := range reserved {
		if strings.EqualFold(name, r) {
			return "", ErrPaletteNameReserved
		}
	}
	return name, nil
}

// List devuelve los nombres de las paletas guardadas, ordenados, sin las que se llaman como una de
// fábrica (reserved): esas se ignoran.
func (p Palettes) List(reserved []string) ([]string, error) {
	entries, err := os.ReadDir(p.Dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		file, err := readPalette(filepath.Join(p.Dir, entry.Name()))
		if err != nil {
			continue
		}
		if _, err := ValidatePaletteName(file.Name, reserved); err != nil {
			continue
		}
		names = append(names, file.Name)
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	return names, nil
}

// Exists indica si hay una paleta guardada con ese nombre.
func (p Palettes) Exists(name string) bool {
	_, err := os.Stat(p.path(name))
	return err == nil
}

// Load lee una paleta guardada.
func (p Palettes) Load(name string) (PaletteFile, error) {
	return readPalette(p.path(name))
}

// Save escribe la paleta (pisa una con el mismo nombre).
func (p Palettes) Save(file PaletteFile, reserved []string) error {
	name, err := ValidatePaletteName(file.Name, reserved)
	if err != nil {
		return err
	}
	file.Name, file.Version = name, paletteVersion
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.Dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(p.path(name), append(data, '\n'), 0600)
}

// Rename cambia el nombre (y el archivo) de una paleta.
func (p Palettes) Rename(oldName, newName string, reserved []string) error {
	newName, err := ValidatePaletteName(newName, reserved)
	if err != nil {
		return err
	}
	file, err := p.Load(oldName)
	if err != nil {
		return err
	}
	if profileFileName(newName) != profileFileName(oldName) && p.Exists(newName) {
		return fmt.Errorf("%w %q", ErrPaletteExists, newName)
	}
	file.Name = newName
	if err := p.Save(file, reserved); err != nil {
		return err
	}
	if profileFileName(newName) == profileFileName(oldName) {
		return nil
	}
	return os.Remove(p.path(oldName))
}

// Delete borra una paleta guardada.
func (p Palettes) Delete(name string) error {
	return os.Remove(p.path(name))
}

func readPalette(path string) (PaletteFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PaletteFile{}, err
	}
	var file PaletteFile
	if err := json.Unmarshal(data, &file); err != nil {
		return PaletteFile{}, fmt.Errorf("paleta %s: %w", filepath.Base(path), err)
	}
	if strings.TrimSpace(file.Name) == "" {
		return PaletteFile{}, fmt.Errorf("paleta %s: sin nombre", filepath.Base(path))
	}
	return file, nil
}
