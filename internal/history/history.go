package history

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Message struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	SenderID  string    `json:"sender_id"`
	Username  string    `json:"username"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text,omitempty"`
	ImageID   string    `json:"image_id,omitempty"`
	ImageExt  string    `json:"image_ext,omitempty"`
	System    bool      `json:"system,omitempty"`
}

type Store struct {
	mu       sync.Mutex
	root     string
	roomName string
	seen     map[string]struct{}
}

func New(root, roomName string) (*Store, error) {
	if strings.TrimSpace(roomName) == "" {
		return nil, fmt.Errorf("nombre de sala vacío")
	}
	roomPath := filepath.Join(root, "salas", safeName(roomName))
	if err := os.MkdirAll(filepath.Join(roomPath, "images"), 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(roomPath, "sounds"), 0700); err != nil {
		return nil, err
	}
	store := &Store{root: roomPath, roomName: roomName, seen: make(map[string]struct{})}
	if err := store.loadSeen(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Add(message Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if message.ID == "" {
		return fmt.Errorf("mensaje sin identificador")
	}
	if _, exists := s.seen[message.ID]; exists {
		return nil
	}
	if message.Timestamp.IsZero() {
		message.Timestamp = time.Now().UTC()
	}
	file, err := os.OpenFile(filepath.Join(s.root, "history"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(message); err != nil {
		return err
	}
	s.seen[message.ID] = struct{}{}
	return nil
}

func (s *Store) Load() ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.Open(filepath.Join(s.root, "history"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var messages []Message
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var message Message
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			continue
		}
		messages = append(messages, message)
		s.seen[message.ID] = struct{}{}
	}
	return messages, scanner.Err()
}

func (s *Store) KnownIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.seen))
	for id := range s.seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Store) Missing(messages []Message) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	missing := make([]Message, 0, len(messages))
	for _, message := range messages {
		if message.ID == "" {
			continue
		}
		if _, exists := s.seen[message.ID]; !exists {
			missing = append(missing, message)
		}
	}
	return missing
}

func (s *Store) MessagesMissingFrom(knownIDs []string) ([]Message, error) {
	messages, err := s.Load()
	if err != nil {
		return nil, err
	}
	known := make(map[string]struct{}, len(knownIDs))
	for _, id := range knownIDs {
		known[id] = struct{}{}
	}
	missing := make([]Message, 0, len(messages))
	for _, message := range messages {
		if _, exists := known[message.ID]; !exists {
			missing = append(missing, message)
		}
	}
	return missing, nil
}

func (s *Store) loadSeen() error {
	_, err := s.Load()
	return err
}

// Extensiones que se guardan en disco. Todo lo que arma una ruta con un ID o una extensión que
// viene de la red pasa por imageName/soundName: sin esto, una extensión como `\..\..\x` sacaba
// la escritura, la lectura o el borrado fuera de la carpeta de la sala (AGENDA S1).
var (
	// Las del selector de imágenes (file_windows.go).
	imageExtensions = map[string]struct{}{".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {}}
	// .pcm es lo único que produce el recorte.
	soundExtensions = map[string]struct{}{".pcm": {}}
)

// CheckImage dice si el ID y la extensión de una imagen se pueden guardar (para descartar
// temprano lo que llega por red).
func CheckImage(id, extension string) error {
	_, _, err := imageName(id, extension)
	return err
}

// CheckSound es CheckImage para sonidos.
func CheckSound(id, extension string) error {
	_, _, err := soundName(id, extension)
	return err
}

// imageName devuelve la ruta relativa a la carpeta de la sala y la extensión normalizada.
func imageName(id, extension string) (string, string, error) {
	return assetName("images", imageExtensions, id, extension)
}

func soundName(id, extension string) (string, string, error) {
	return assetName("sounds", soundExtensions, id, extension)
}

func assetName(dir string, allowed map[string]struct{}, id, extension string) (string, string, error) {
	if !ValidID(id) {
		return "", "", fmt.Errorf("identificador de archivo inválido: %q", id)
	}
	extension = strings.ToLower(extension)
	if _, ok := allowed[extension]; !ok {
		return "", "", fmt.Errorf("extensión de archivo no permitida: %q", extension)
	}
	return filepath.Join(dir, id+extension), extension, nil
}

// ValidID acepta solo lo que genera NewID: 32 caracteres hex en minúsculas.
func ValidID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for i := 0; i < len(id); i++ {
		if c := id[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// inRoom abre la carpeta de la sala como os.Root: aunque una ruta se escapara de la validación,
// el SO no deja salir de ahí.
func (s *Store) inRoom(fn func(root *os.Root) error) error {
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return err
	}
	defer root.Close()
	return fn(root)
}

// readInRoom lee un archivo de la sala (name ya validado).
func (s *Store) readInRoom(name string) ([]byte, error) {
	var data []byte
	err := s.inRoom(func(root *os.Root) error {
		var readErr error
		data, readErr = root.ReadFile(name)
		return readErr
	})
	return data, err
}

func (s *Store) existsInRoom(name string) error {
	return s.inRoom(func(root *os.Root) error {
		_, err := root.Stat(name)
		return err
	})
}

// ImagePath devuelve la ruta absoluta de una imagen, o error si el ID o la extensión no son válidos.
func (s *Store) ImagePath(id, extension string) (string, error) {
	name, _, err := imageName(id, extension)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, name), nil
}

// SoundMeta describes a soundboard clip shared in a room.
type SoundMeta struct {
	ID   string `json:"id"`
	Ext  string `json:"ext"`
	Name string `json:"name"`
}

// SoundPath devuelve la ruta absoluta de un sonido, o error si el ID o la extensión no son válidos.
func (s *Store) SoundPath(id, extension string) (string, error) {
	name, _, err := soundName(id, extension)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, name), nil
}

func (s *Store) SaveSound(meta SoundMeta, data []byte) (string, error) {
	name, extension, err := soundName(meta.ID, meta.Ext)
	if err != nil {
		return "", err
	}
	meta.Ext = extension
	if err := s.inRoom(func(root *os.Root) error { return root.WriteFile(name, data, 0600) }); err != nil {
		return "", err
	}
	if err := s.appendSoundMeta(meta); err != nil {
		return "", err
	}
	return filepath.Join(s.root, name), nil
}

// UpdateSoundMeta records new metadata (e.g. a rename) for a clip that is already on disk.
// ListSounds keeps the last manifest entry per ID, so appending is enough.
func (s *Store) UpdateSoundMeta(meta SoundMeta) error {
	name, extension, err := soundName(meta.ID, meta.Ext)
	if err != nil {
		return err
	}
	meta.Ext = extension
	if err := s.existsInRoom(name); err != nil {
		return err
	}
	return s.appendSoundMeta(meta)
}

// soundOnDisk dice si el audio de una entrada del manifest existe (una entrada inválida cuenta
// como huérfana).
func soundOnDisk(root *os.Root, meta SoundMeta) bool {
	name, _, err := soundName(meta.ID, meta.Ext)
	if err != nil {
		return false
	}
	_, err = root.Stat(name)
	return err == nil
}

// DeleteSound removes the clip and leaves a tombstone, so a peer that was offline when it was
// deleted does not bring it back on its next soundboard sync.
func (s *Store) DeleteSound(id, extension string) error {
	name, _, err := soundName(id, extension)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return err
	}
	defer root.Close()
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = root.Remove(name)
	if err := s.appendTombstonesLocked([]string{id}); err != nil {
		return err
	}

	manifestPath := filepath.Join(s.root, "sounds", "manifest")
	file, err := os.Open(manifestPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	var remaining []SoundMeta
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var meta SoundMeta
		if json.Unmarshal(scanner.Bytes(), &meta) != nil {
			continue
		}
		if meta.ID == id || !soundOnDisk(root, meta) {
			continue
		}
		if _, exists := seen[meta.ID]; exists {
			continue
		}
		seen[meta.ID] = struct{}{}
		remaining = append(remaining, meta)
	}
	_ = scanner.Err()
	file.Close()

	tmpPath := manifestPath + ".tmp"
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(tmpFile)
	for _, m := range remaining {
		if err := enc.Encode(m); err != nil {
			tmpFile.Close()
			_ = os.Remove(tmpPath)
			return err
		}
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	_ = os.Remove(manifestPath)
	return os.Rename(tmpPath, manifestPath)
}

func (s *Store) ReadSound(id, extension string) ([]byte, error) {
	name, _, err := soundName(id, extension)
	if err != nil {
		return nil, err
	}
	return s.readInRoom(name)
}

func (s *Store) appendSoundMeta(meta SoundMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.OpenFile(filepath.Join(s.root, "sounds", "manifest"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(meta)
}

// MaxSoundTombstones bounds the tombstone list (it travels in one UDP packet on every sync).
// A peer offline for longer than the last MaxSoundTombstones deletions may resurrect a clip.
const MaxSoundTombstones = 500

// AddSoundTombstones records deletions learned from the room (e.g. the host's sync), skipping
// the ones already known and any ID that NewID could not have produced (they come from the
// network: a newline would inject lines and junk would fill the MaxSoundTombstones slots).
func (s *Store) AddSoundTombstones(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	known := make(map[string]struct{})
	for _, id := range s.readTombstonesLocked() {
		known[id] = struct{}{}
	}
	fresh := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, exists := known[id]; exists || !ValidID(id) {
			continue
		}
		known[id] = struct{}{}
		fresh = append(fresh, id)
	}
	return s.appendTombstonesLocked(fresh)
}

// DeletedSounds returns the IDs of deleted clips, oldest first, at most MaxSoundTombstones.
func (s *Store) DeletedSounds() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readTombstonesLocked()
}

func (s *Store) tombstonesPath() string {
	return filepath.Join(s.root, "sounds", "tombstones")
}

// readTombstonesLocked deduplicates, drops invalid IDs and trims the file to the newest
// MaxSoundTombstones.
func (s *Store) readTombstonesLocked() []string {
	file, err := os.Open(s.tombstonesPath())
	if err != nil {
		return nil
	}
	var ids []string
	seen := make(map[string]struct{})
	lines := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines++
		id := strings.TrimSpace(scanner.Text())
		if !ValidID(id) {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	file.Close()
	if len(ids) > MaxSoundTombstones {
		ids = ids[len(ids)-MaxSoundTombstones:]
	}
	if lines > len(ids) {
		tmpPath := s.tombstonesPath() + ".tmp"
		if err := os.WriteFile(tmpPath, []byte(strings.Join(ids, "\n")+"\n"), 0600); err == nil {
			_ = os.Remove(s.tombstonesPath())
			_ = os.Rename(tmpPath, s.tombstonesPath())
		}
	}
	return ids
}

func (s *Store) appendTombstonesLocked(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	file, err := os.OpenFile(s.tombstonesPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(strings.Join(ids, "\n") + "\n")
	return err
}

// ListSounds returns the soundboard clips known for this room, deduplicated by ID.
func (s *Store) ListSounds() ([]SoundMeta, error) {
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	manifestPath := filepath.Join(s.root, "sounds", "manifest")
	file, err := os.Open(manifestPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	seen := make(map[string]int)
	var sounds []SoundMeta
	hasOrphans := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var meta SoundMeta
		if json.Unmarshal(scanner.Bytes(), &meta) != nil {
			continue
		}
		if !soundOnDisk(root, meta) {
			hasOrphans = true
			continue
		}
		if index, exists := seen[meta.ID]; exists {
			sounds[index] = meta
			continue
		}
		seen[meta.ID] = len(sounds)
		sounds = append(sounds, meta)
	}
	scanErr := scanner.Err()
	file.Close()

	if hasOrphans {
		tmpPath := manifestPath + ".clean"
		if tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600); err == nil {
			enc := json.NewEncoder(tmpFile)
			for _, m := range sounds {
				_ = enc.Encode(m)
			}
			_ = tmpFile.Close()
			_ = os.Remove(manifestPath)
			_ = os.Rename(tmpPath, manifestPath)
		}
	}

	return sounds, scanErr
}

// ListRooms returns the names of rooms that already have local history under root.
func ListRooms(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "salas"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func (s *Store) SaveImage(id, extension string, data []byte) (string, error) {
	name, _, err := imageName(id, extension)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.inRoom(func(root *os.Root) error {
		if _, statErr := root.Stat(name); statErr == nil {
			return nil
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		return root.WriteFile(name, data, 0600)
	})
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, name), nil
}

func (s *Store) HasImage(id, extension string) bool {
	name, _, err := imageName(id, extension)
	return err == nil && s.existsInRoom(name) == nil
}

func (s *Store) ReadImage(id, extension string) ([]byte, error) {
	name, _, err := imageName(id, extension)
	if err != nil {
		return nil, err
	}
	return s.readInRoom(name)
}

func NewID(senderID string) string {
	hash := sha256.Sum256([]byte(senderID + "|" + time.Now().UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(hash[:16])
}

// MaxRoomNameRunes acota el nombre de sala: el host lo manda en el RoomState y la carpeta
// salas\<nombre>\sounds\<id>.pcm tiene que entrar en MAX_PATH (AGENDA S11).
const MaxRoomNameRunes = 64

// ClampRoomName recorta el nombre de sala a MaxRoomNameRunes (sin espacios en los bordes).
// Se aplica donde se crea (crear sala) y donde se recibe (RoomState del host).
func ClampRoomName(name string) string {
	name = strings.TrimSpace(name)
	if runes := []rune(name); len(runes) > MaxRoomNameRunes {
		name = strings.TrimSpace(string(runes[:MaxRoomNameRunes]))
	}
	return name
}

// safeName convierte el nombre de la sala (que al cliente se lo manda el host) en un nombre de
// carpeta de Windows: sin separadores, `<>:"|?*` ni caracteres de control, de MaxRoomNameRunes
// como máximo, sin puntos o espacios al final (Windows los borra) y con `_` en los nombres
// reservados (CON, NUL, COM1...).
func safeName(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`\/<>:"|?*`, r) {
			return '_'
		}
		return r
	}, value)
	value = strings.TrimRight(ClampRoomName(value), ". ")
	if value == "" {
		return "sala"
	}
	stem, rest := value, ""
	if i := strings.IndexByte(value, '.'); i >= 0 {
		stem, rest = value[:i], value[i:]
	}
	if reservedName(strings.TrimRight(stem, " ")) {
		value = stem + "_" + rest
	}
	return value
}

func reservedName(name string) bool {
	switch strings.ToUpper(name) {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(name) != 4 {
		return false
	}
	prefix := strings.ToUpper(name[:3])
	return (prefix == "COM" || prefix == "LPT") && name[3] >= '0' && name[3] <= '9'
}
