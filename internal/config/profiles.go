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

	"voxmesh/internal/i18n"
)

// Perfiles de audio: un archivo JSON por perfil en <AppDir>/perfiles/<entrada|salida>/. Guardan
// solo los parámetros de su tipo (ProfileParams), nunca los dispositivos, así un perfil se puede
// pasar a otra PC tal cual. Al cargar se parte del default y se aplica el archivo encima: un
// perfil viejo sigue andando cuando se agregan parámetros.
//
// Los perfiles de fábrica ("Default" y "Sin filtros") no son archivos: son los valores de la tabla
// Params (Sin filtros, con cada filtro apagado). No se pisan, no se renombran ni se borran, y se
// vuelven a aplicar al arrancar (ApplyBuiltinProfiles).

const (
	// DefaultProfileName es el perfil fijo con los valores de fábrica.
	DefaultProfileName = "Default"
	// NoFiltersProfileName es la clave fija del perfil "Sin filtros" (AGENDA C19): se guarda así en
	// voxmesh.json en cualquier idioma; en pantalla va traducido ("profile.no_filters").
	NoFiltersProfileName = "Sin filtros"
)

// BuiltinProfiles son los perfiles de fábrica, en el orden del selector.
var BuiltinProfiles = []string{DefaultProfileName, NoFiltersProfileName}

// noFiltersValues es lo que "Sin filtros" cambia respecto de Default: cada filtro apagado (para
// prenderlos de a uno) y la ganancia en 0 dB. El check general de cada tipo queda prendido, así
// prender un filtro ya se nota. El techo de envío, Opus, el celular, el buffer de recepción y la
// mezcla quedan como en Default: no son filtros y protegen.
var noFiltersValues = map[string]float64{
	"audio_filter_enabled": 1,
	"shout_enabled":        0,
	"aec_enabled":          0,
	"rnnoise_enabled":      0,
	"vad_enabled":          0,
	"noise_gate_enabled":   0,
	"high_pass_enabled":    0,
	"low_pass_enabled":     0,
	"notch_enabled":        0,
	"input_gain_db":        0,
	"compressor_enabled":   0,
	"expander_enabled":     0,
	"limiter_enabled":      0,

	"shout_duck_enabled":       0,
	"output_filter_enabled":    1,
	"output_gain_db":           0,
	"output_high_pass_enabled": 0,
	"output_low_pass_enabled":  0,
	"output_limiter_enabled":   0,
}

// Errores de nombres de perfil que la UI le muestra al usuario (los reconoce con errors.Is para
// traducirlos).
var (
	ErrProfileNameEmpty    = errors.New("el nombre del perfil está vacío")
	ErrProfileNameReserved = errors.New("ese nombre es de un perfil de fábrica")
	ErrProfileNameTooLong  = errors.New("el nombre del perfil es demasiado largo")
	ErrProfileExists       = errors.New("ya existe un perfil")
	ErrDefaultProfileFixed = errors.New("el perfil de fábrica no se puede borrar")
)

// CanonicalProfileName devuelve la clave de un perfil de fábrica si name es uno de ellos ("" es
// Default; "Sin filtros" también en cualquier idioma del catálogo, sin distinguir mayúsculas), o
// name tal cual. Un perfil guardado con uno de esos nombres queda ignorado: nunca se lista ni se
// lee (sin migración, como pide la Visión).
func CanonicalProfileName(name string) string {
	trimmed := strings.TrimSpace(name)
	switch {
	case trimmed == "", strings.EqualFold(trimmed, DefaultProfileName):
		return DefaultProfileName
	case strings.EqualFold(trimmed, NoFiltersProfileName):
		return NoFiltersProfileName
	}
	for _, language := range i18n.Languages {
		if strings.EqualFold(trimmed, i18n.In(language.Code, "profile.no_filters")) {
			return NoFiltersProfileName
		}
	}
	return name
}

// IsBuiltinProfile indica si name es un perfil de fábrica (o uno de sus nombres).
func IsBuiltinProfile(name string) bool {
	canonical := CanonicalProfileName(name)
	return canonical == DefaultProfileName || canonical == NoFiltersProfileName
}

// BuiltinValues devuelve settings con los parámetros de kind del perfil de fábrica name (Default si
// name no es de fábrica).
func BuiltinValues(kind ProfileKind, name string, settings LiveSettings) LiveSettings {
	noFilters := CanonicalProfileName(name) == NoFiltersProfileName
	for _, param := range ProfileParams(kind) {
		value := param.Default
		if override, ok := noFiltersValues[param.Key]; ok && noFilters {
			value = override
		}
		param.Set(&settings, value)
	}
	return settings
}

// ProfileForBuiltinChanges decide qué hacer con los cambios de kind al guardarlos: si el perfil
// activo es de fábrica y current ya no tiene sus valores, devuelve el nombre del perfil nuevo que
// los guarda (base, "base 2"... sin chocar con existing; base es "Personalizado" en el idioma de
// la app) y true. Si el activo es uno guardado, o no cambió nada, devuelve false.
func ProfileForBuiltinChanges(kind ProfileKind, current LiveSettings, existing []string, base string) (string, bool) {
	active := current.ActiveInputProfile
	if kind == OutputProfile {
		active = current.ActiveOutputProfile
	}
	if !IsBuiltinProfile(active) || SameProfileValues(kind, BuiltinValues(kind, active, current), current) {
		return "", false
	}
	return NextCustomProfileName(base, existing), true
}

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
		return "", ErrProfileNameEmpty
	case IsBuiltinProfile(name):
		return "", ErrProfileNameReserved
	case len([]rune(name)) > 60:
		return "", ErrProfileNameTooLong
	}
	return name, nil
}

// List devuelve los nombres de los perfiles guardados de ese tipo, ordenados (sin los de fábrica:
// un archivo con uno de sus nombres se ignora).
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
		if err != nil || file.Name == "" || IsBuiltinProfile(file.Name) {
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
// en su default). El resto de settings no cambia. Los de fábrica no leen ningún archivo.
func (p Profiles) Values(kind ProfileKind, name string, settings LiveSettings) (LiveSettings, error) {
	if IsBuiltinProfile(name) {
		return BuiltinValues(kind, name, settings), nil
	}
	file, err := readProfile(p.path(kind, name))
	if err != nil {
		return settings, err
	}
	values := file.Values
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
		return fmt.Errorf("%w %q", ErrProfileExists, newName)
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
	if IsBuiltinProfile(name) {
		return ErrDefaultProfileFixed
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

// ApplyBuiltinProfiles pone los valores de fábrica en cada tipo cuyo perfil activo es de fábrica
// (Default o Sin filtros) y deja su nombre con la clave fija ("" para Default). Se corre al
// arrancar: los perfiles de fábrica no se guardan, así un cambio de defaults en una actualización
// le llega a todos los que los usan. Devuelve si cambió algo.
func ApplyBuiltinProfiles(settings *LiveSettings) bool {
	changed := false
	for _, kind := range []ProfileKind{InputProfile, OutputProfile} {
		active := &settings.ActiveInputProfile
		if kind == OutputProfile {
			active = &settings.ActiveOutputProfile
		}
		if !IsBuiltinProfile(*active) {
			continue
		}
		canonical := CanonicalProfileName(*active)
		if canonical == DefaultProfileName {
			canonical = ""
		}
		if *active != canonical {
			*active = canonical
			changed = true
		}
		want := BuiltinValues(kind, canonical, *settings)
		for _, param := range ProfileParams(kind) {
			if math.Abs(param.Get(settings)-param.Get(&want)) > 1e-4 { // float32 guardado vs float64 de la tabla
				param.Set(settings, param.Get(&want))
				changed = true
			}
		}
	}
	return changed
}

// NextCustomProfileName devuelve el primer base, "base 2"... que no esté en existing (sin
// distinguir mayúsculas ni nombres que darían el mismo archivo). base es el nombre del perfil que
// se crea solo al guardar cambios hechos sobre Default ("Personalizado" en español); lo pone la UI
// en su idioma.
func NextCustomProfileName(base string, existing []string) string {
	taken := make(map[string]bool, len(existing))
	for _, name := range existing {
		taken[profileFileName(name)] = true
	}
	name := base
	for n := 2; taken[profileFileName(name)]; n++ {
		name = fmt.Sprintf("%s %d", base, n)
	}
	return name
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
