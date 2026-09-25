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
	ImagePath string    `json:"image_path,omitempty"`
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

func (s *Store) ImagePath(id, extension string) string {
	return filepath.Join(s.root, "images", safeName(id)+extension)
}

// SoundMeta describes a soundboard clip shared in a room.
type SoundMeta struct {
	ID   string `json:"id"`
	Ext  string `json:"ext"`
	Name string `json:"name"`
}

func (s *Store) SoundPath(id, extension string) string {
	return filepath.Join(s.root, "sounds", safeName(id)+extension)
}

func (s *Store) SaveSound(meta SoundMeta, data []byte) (string, error) {
	if strings.TrimSpace(meta.ID) == "" {
		return "", fmt.Errorf("sonido sin identificador")
	}
	if strings.TrimSpace(meta.Ext) == "" {
		meta.Ext = ".wav"
	}
	path := s.SoundPath(meta.ID, meta.Ext)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}
	if err := s.appendSoundMeta(meta); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) DeleteSound(id, extension string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = os.Remove(s.SoundPath(id, extension))

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
		if meta.ID == id {
			continue
		}
		if _, statErr := os.Stat(s.SoundPath(meta.ID, meta.Ext)); statErr != nil {
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
	return os.ReadFile(s.SoundPath(id, extension))
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

// ListSounds returns the soundboard clips known for this room, deduplicated by ID.
func (s *Store) ListSounds() ([]SoundMeta, error) {
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
		if _, statErr := os.Stat(s.SoundPath(meta.ID, meta.Ext)); statErr != nil {
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
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("imagen sin identificador")
	}
	if strings.TrimSpace(extension) == "" {
		extension = ".bin"
	}
	path := s.ImagePath(id, extension)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) HasImage(id, extension string) bool {
	if strings.TrimSpace(id) == "" {
		return false
	}
	_, err := os.Stat(s.ImagePath(id, extension))
	return err == nil
}

func (s *Store) ReadImage(id, extension string) ([]byte, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("imagen sin identificador")
	}
	return os.ReadFile(s.ImagePath(id, extension))
}

func NewID(senderID string) string {
	hash := sha256.Sum256([]byte(senderID + "|" + time.Now().UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(hash[:16])
}

func safeName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\\", "_")
	value = strings.ReplaceAll(value, "/", "_")
	if value == "" || value == "." || value == ".." {
		return "sala"
	}
	return value
}
