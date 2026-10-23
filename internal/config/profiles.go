package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Perfiles de audio: un archivo JSON por perfil en <AppDir>/perfiles/<entrada|salida>/. Guardan
// solo los parámetros de su tipo (ProfileParams), nunca los dispositivos, así un perfil se puede
// pasar a otra PC tal cual. Al cargar se parte del default y se aplica el archivo encima: un
// perfil viejo sigue andando cuando se agregan parámetros.
//
// "Default" no es un archivo: son los valores de la tabla Params.

// DefaultProfileName es el perfil fijo con los valores de fábrica.
const DefaultProfileName = "Default"

// MigratedProfileName es el perfil donde se guarda la configuración que había al actualizar.
const MigratedProfileName = "Mi configuración"

// CustomProfileName es el nombre base del perfil que se crea solo al guardar cambios hechos
// sobre Default ("Personalizado", "Personalizado 2", ...).
const CustomProfileName = "Personalizado"

const profileVersion = 1

// Profiles maneja los perfiles guardados en Dir (normalmente ProfilesDir()).
type Profiles struct {
	Dir string
}

// ProfilesDir es la carpeta raíz de los perfiles.
func ProfilesDir() (string, error) {
	root, err := AppDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "perfiles"), nil
}

type profileFile struct {
	Version int                `json:"version"`
	Name    string             `json:"name"`
	Kind    ProfileKind        `json:"kind"`
	Values  map[string]float64 `json:"-"`
}

func (p Profiles) kindDir(kind ProfileKind) string {
	return filepath.Join(p.Dir, string(kind))
}

func (p Profiles) path(kind ProfileKind, name string) string {
	return filepath.Join(p.kindDir(kind), profileFileName(name)+".json")
}

// profileFileName arma un nombre de archivo seguro (el nombre real va adentro del archivo).
func profileFileName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '.':
			b.WriteRune('_')
		case r > 127:
			b.WriteRune(r) // acentos y ñ son válidos en NTFS
		}
	}
	if b.Len() == 0 {
		return "perfil"
	}
	return b.String()
}

// ValidateProfileName devuelve el nombre limpio o un error si no se puede usar.
func ValidateProfileName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", errors.New("el nombre del perfil está vacío")
	case strings.EqualFold(name, DefaultProfileName):
		return "", fmt.Errorf("%q es el perfil de fábrica", DefaultProfileName)
	case len([]rune(name)) > 60:
		return "", errors.New("el nombre del perfil es demasiado largo")
	}
	return name, nil
}

// List devuelve los nombres de los perfiles guardados de ese tipo, ordenados (sin "Default").
func (p Profiles) List(kind ProfileKind) ([]string, error) {
	entries, err := os.ReadDir(p.kindDir(kind))
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
		file, err := readProfile(filepath.Join(p.kindDir(kind), entry.Name()))
		if err != nil || file.Name == "" {
			continue
		}
		names = append(names, file.Name)
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	return names, nil
}

// Exists indica si hay un perfil guardado con ese nombre.
func (p Profiles) Exists(kind ProfileKind, name string) bool {
	_, err := os.Stat(p.path(kind, name))
	return err == nil
}

// Save escribe el perfil con los valores de settings (pisa uno existente con el mismo nombre).
func (p Profiles) Save(kind ProfileKind, name string, settings LiveSettings) error {
	name, err := ValidateProfileName(name)
	if err != nil {
		return err
	}
	data, err := encodeProfile(kind, name, settings)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.kindDir(kind), 0700); err != nil {
		return err
	}
	return os.WriteFile(p.path(kind, name), data, 0600)
}

// Values devuelve settings con los parámetros de ese tipo tomados del perfil (los que falten,
// en su default). El resto de settings no cambia. "Default" no lee ningún archivo.
func (p Profiles) Values(kind ProfileKind, name string, settings LiveSettings) (LiveSettings, error) {
	values := map[string]float64{}
	if name != "" && name != DefaultProfileName {
		file, err := readProfile(p.path(kind, name))
		if err != nil {
			return settings, err
		}
		values = file.Values
	}
	for _, param := range ProfileParams(kind) {
		value, ok := values[param.Key]
		if !ok || !param.InRange(value) {
			value = param.Default
		}
		param.Set(&settings, value)
	}
	return settings, nil
}

// Rename cambia el nombre (y el archivo) de un perfil.
func (p Profiles) Rename(kind ProfileKind, oldName, newName string) error {
	newName, err := ValidateProfileName(newName)
	if err != nil {
		return err
	}
	settings, err := p.Values(kind, oldName, LiveSettings{})
	if err != nil {
		return err
	}
	if profileFileName(newName) != profileFileName(oldName) && p.Exists(kind, newName) {
		return fmt.Errorf("ya existe un perfil %q", newName)
	}
	if err := p.Save(kind, newName, settings); err != nil {
		return err
	}
	if profileFileName(newName) == profileFileName(oldName) {
		return nil
	}
	return p.Delete(kind, oldName)
}

// Delete borra un perfil guardado.
func (p Profiles) Delete(kind ProfileKind, name string) error {
	if name == "" || name == DefaultProfileName {
		return errors.New("el perfil de fábrica no se puede borrar")
	}
	return os.Remove(p.path(kind, name))
}

// SameProfileValues indica si los parámetros de ese tipo coinciden en a y b.
func SameProfileValues(kind ProfileKind, a, b LiveSettings) bool {
	for _, param := range ProfileParams(kind) {
		if math.Abs(param.Get(&a)-param.Get(&b)) > 1e-4 {
			return false
		}
	}
	return true
}

// ApplyDefaultProfiles pone los valores de fábrica en cada tipo cuyo perfil activo es Default.
// Se corre al arrancar (después de MigrateToProfiles): Default no se guarda, así un cambio de
// defaults en una actualización le llega a todos los que lo usan. Devuelve si cambió algo.
func ApplyDefaultProfiles(settings *LiveSettings) bool {
	changed := false
	for _, kind := range []ProfileKind{InputProfile, OutputProfile} {
		active := settings.ActiveInputProfile
		if kind == OutputProfile {
			active = settings.ActiveOutputProfile
		}
		if active != "" && active != DefaultProfileName {
			continue
		}
		for _, param := range ProfileParams(kind) {
			if math.Abs(param.Get(settings)-param.Default) > 1e-4 { // float32 guardado vs float64 de la tabla
				param.Set(settings, param.Default)
				changed = true
			}
		}
	}
	return changed
}

// NextCustomProfileName devuelve el primer "Personalizado", "Personalizado 2"... que no esté en
// existing (sin distinguir mayúsculas ni nombres que darían el mismo archivo).
func NextCustomProfileName(existing []string) string {
	taken := make(map[string]bool, len(existing))
	for _, name := range existing {
		taken[profileFileName(name)] = true
	}
	name := CustomProfileName
	for n := 2; taken[profileFileName(name)]; n++ {
		name = fmt.Sprintf("%s %d", CustomProfileName, n)
	}
	return name
}

// MigrateToProfiles se corre al arrancar: si todavía no existe la carpeta de perfiles (primera
// vez con esta versión) y había un voxmesh.json, guarda la configuración actual como "Mi
// configuración" de entrada y de salida y la deja activa. Devuelve si cambió settings.
func (p Profiles) MigrateToProfiles(settings *LiveSettings, hadConfigFile bool) (bool, error) {
	if _, err := os.Stat(p.Dir); err == nil || !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(p.Dir, 0700); err != nil {
		return false, err
	}
	if !hadConfigFile {
		return false, nil
	}
	for _, kind := range []ProfileKind{InputProfile, OutputProfile} {
		if err := p.Save(kind, MigratedProfileName, *settings); err != nil {
			return false, err
		}
	}
	settings.ActiveInputProfile = MigratedProfileName
	settings.ActiveOutputProfile = MigratedProfileName
	return true, nil
}

func encodeProfile(kind ProfileKind, name string, settings LiveSettings) ([]byte, error) {
	// Mapa ordenado por la tabla: version, name y kind primero, después los parámetros.
	var b strings.Builder
	header, err := json.Marshal(profileFile{Version: profileVersion, Name: name, Kind: kind})
	if err != nil {
		return nil, err
	}
	b.Write(header[:len(header)-1])
	for _, param := range ProfileParams(kind) {
		var value any = param.Get(&settings)
		if param.IsBool() {
			value = param.Get(&settings) != 0
		} else {
			// float32 → float64 deja colas como 0.20000000298: se redondea al paso de la tabla.
			value = roundToStep(param.Get(&settings), param.Step)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, ",\n  %q: %s", param.Key, encoded)
	}
	b.WriteString("\n}\n")
	return []byte(b.String()), nil
}

func roundToStep(value, step float64) float64 {
	if step <= 0 {
		return value
	}
	decimals := math.Max(0, math.Ceil(-math.Log10(step))) + 1
	scale := math.Pow(10, decimals)
	return math.Round(value*scale) / scale
}

func readProfile(path string) (profileFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return profileFile{}, err
	}
	var file profileFile
	if err := json.Unmarshal(data, &file); err != nil {
		return profileFile{}, fmt.Errorf("perfil %s: %w", filepath.Base(path), err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return profileFile{}, err
	}
	file.Values = map[string]float64{}
	for key, value := range raw {
		switch v := value.(type) {
		case bool:
			if v {
				file.Values[key] = 1
			} else {
				file.Values[key] = 0
			}
		case float64:
			file.Values[key] = v
		}
	}
	return file, nil
}
