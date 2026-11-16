// Package identity es el ID de cada instalación de VoxMesh (AGENDA F10): un par de llaves Ed25519
// que se genera la primera vez y queda en la carpeta de datos (config.AppDir). La llave pública es
// el ID (no cambia con el nombre ni con la PC); la privada nunca sale de la PC y sirve para firmar
// (el Hello de la sala, el pedido para entrar de "En tu red"), así nadie se hace pasar por otro.
//
// Go puro y portable (crypto/ed25519, sin cgo ni APIs de Windows): la app se va a portar a Linux.
// La privada queda en texto plano en un archivo con permisos solo para el usuario (0600; en
// Windows la carpeta %AppData% ya es solo del usuario).
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileName es el archivo de la identidad dentro de la carpeta de datos.
const FileName = "identidad.json"

// Key es una llave pública: el ID de una instalación.
type Key [ed25519.PublicKeySize]byte

// String es la llave en base64url sin relleno (43 caracteres): así viaja en el estado de la sala y
// se guarda en la lista de confianza.
func (k Key) String() string { return base64.RawURLEncoding.EncodeToString(k[:]) }

// IsZero dice si la llave no se asignó.
func (k Key) IsZero() bool { return k == Key{} }

// ErrKey: el texto no es una llave.
var ErrKey = errors.New("llave inválida")

// ParseKey lee una llave de String. Cualquier otra cosa (largo, alfabeto) es ErrKey.
func ParseKey(s string) (Key, error) {
	var k Key
	if base64.RawURLEncoding.EncodedLen(len(k)) != len(s) {
		return Key{}, ErrKey
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil || len(raw) != len(k) {
		return Key{}, ErrKey
	}
	copy(k[:], raw)
	if k.IsZero() {
		return Key{}, ErrKey
	}
	return k, nil
}

// shortAlphabet es el base32 de Crockford: sin I, L, O ni U, que se confunden al leerlos.
const shortAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Short es una versión corta del ID para mostrar y comparar a ojo ("K7QF-2M9X", en "Acerca de"):
// los primeros 40 bits del SHA-256 de la llave. No sirve para identificar a nadie en la red (para
// eso va la llave entera): solo para que dos personas vean si es la misma instalación.
func (k Key) Short() string {
	sum := sha256.Sum256(k[:])
	bits := uint64(sum[0])<<32 | uint64(sum[1])<<24 | uint64(sum[2])<<16 | uint64(sum[3])<<8 | uint64(sum[4])
	var out [9]byte
	pos := len(out) - 1
	for i := 0; i < 8; i++ {
		if i == 4 {
			out[pos] = '-'
			pos--
		}
		out[pos] = shortAlphabet[bits&31]
		bits >>= 5
		pos--
	}
	return string(out[:])
}

// Identity es el par de llaves de esta instalación.
type Identity struct {
	private ed25519.PrivateKey
	public  Key
}

// New genera un par nuevo, al azar.
func New() *Identity {
	seed := make([]byte, ed25519.SeedSize)
	_, _ = rand.Read(seed) // nunca falla (Go 1.24+)
	return fromSeed(seed)
}

func fromSeed(seed []byte) *Identity {
	private := ed25519.NewKeyFromSeed(seed)
	var public Key
	copy(public[:], private.Public().(ed25519.PublicKey))
	return &Identity{private: private, public: public}
}

// Public es la llave pública (el ID).
func (id *Identity) Public() Key { return id.public }

// signed es lo que se firma de verdad: el contexto separa los usos, así una firma hecha para el
// Hello de la sala no sirve como respuesta a un desafío de "En tu red" (ni al revés).
func signed(context string, msg []byte) []byte {
	out := make([]byte, 0, len("VoxMesh/")+len(context)+1+len(msg))
	out = append(out, "VoxMesh/"...)
	out = append(out, context...)
	out = append(out, 0)
	return append(out, msg...)
}

// Sign firma msg para el uso context ("hello", "join"…).
func (id *Identity) Sign(context string, msg []byte) []byte {
	return ed25519.Sign(id.private, signed(context, msg))
}

// Verify dice si sig es la firma de msg para context con la privada de key.
func Verify(key Key, context string, msg, sig []byte) bool {
	if key.IsZero() || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(key[:]), signed(context, msg), sig)
}

// file es el formato de identidad.json.
type file struct {
	Version int    `json:"version"`
	Seed    string `json:"seed"` // la privada (semilla de 32 bytes), base64url
}

const fileVersion = 1

// Load lee la identidad de dir. Un archivo que falta, roto o de otra versión es un error.
func Load(dir string) (*Identity, error) {
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("identidad rota: %w", err)
	}
	if f.Version != fileVersion {
		return nil, fmt.Errorf("identidad rota: versión %d", f.Version)
	}
	seed, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimSpace(f.Seed))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("identidad rota: semilla inválida")
	}
	return fromSeed(seed), nil
}

// LoadOrCreate lee la identidad de dir y, si no está o el archivo está roto, genera una nueva y la
// guarda (para los demás es alguien nuevo: un Aceptar). created dice si es nueva. Si no se pudo
// guardar devuelve igual la nueva (vale para esta sesión) con el error.
func LoadOrCreate(dir string) (id *Identity, created bool, err error) {
	if id, err := Load(dir); err == nil {
		return id, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		// Rota: se pisa con una nueva.
		id := New()
		return id, true, save(dir, id, true)
	}
	id = New()
	if err := save(dir, id, false); err != nil {
		if errors.Is(err, os.ErrExist) {
			// Otra instancia la creó recién (las dos abrieron la app por primera vez a la vez).
			if other, loadErr := Load(dir); loadErr == nil {
				return other, false, nil
			}
		}
		return id, true, err
	}
	return id, true, nil
}

// save escribe la identidad con permisos solo para el usuario. Sin replace, falla si el archivo ya
// existe (os.ErrExist); con replace la escribe al lado y la renombra encima.
func save(dir string, id *Identity, replace bool) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file{Version: fileVersion, Seed: base64.RawURLEncoding.EncodeToString(id.private.Seed())}, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, FileName)
	target := path
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if replace {
		target = path + ".nueva"
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(target, flags, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(data, '\n'))
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(target)
		return err
	}
	if replace {
		return os.Rename(target, path)
	}
	return nil
}
