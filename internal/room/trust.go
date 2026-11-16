package room

import (
	"crypto/sha256"
	"encoding/json"
	"sort"
	"time"

	"voxmesh/internal/identity"
)

// Identidad en la sala y lista de confianza (AGENDA F10).
//
// El Hello lleva la llave pública de la instalación (Participant.Key) y su firma sobre la sesión:
// el SHA-256 de la clave de la sala (nueva en cada "Iniciar sala") más el ID de participante. Así
// una firma no sirve en otra sesión ni para otro participante, y nadie se anota con la llave de
// otro sin su privada. Dentro de la misma sesión solo los que tienen la clave pueden mandar un
// Hello, y los miembros maliciosos quedan fuera del modelo (AGENT.md §K).
//
// La lista de confianza ("Dejar entrar siempre") es de la sala: viaja en el estado (State.Trusted),
// cada miembro la guarda en salas/<sala>/confianza.json y la junta con la que trae el host. Así
// cualquiera de adentro, y el que queda de host si el otro se va, deja pasar al marcado.

// helloContext es el uso de la firma del Hello (identity.Sign).
const helloContext = "hello"

// helloMessage es lo que firma el Hello: SHA-256(clave de la sala) + ID de participante.
func helloMessage(roomKey [32]byte, participantID string) []byte {
	sum := sha256.Sum256(roomKey[:])
	return append(sum[:], participantID...)
}

// SignHello firma el Hello de participantID en la sala de roomKey.
func SignHello(id *identity.Identity, roomKey [32]byte, participantID string) []byte {
	return id.Sign(helloContext, helloMessage(roomKey, participantID))
}

// VerifyHello dice si sig es la firma de la llave de p (p.Key) para p.ID en la sala de roomKey. Sin
// llave o con una que no se puede leer, false.
func VerifyHello(p Participant, roomKey [32]byte, sig []byte) bool {
	key, err := identity.ParseKey(p.Key)
	if err != nil {
		return false
	}
	return identity.Verify(key, helloContext, helloMessage(roomKey, p.ID), sig)
}

// MaxTrusted es el tope de la lista de confianza de una sala: viaja entera en cada RoomState (cada
// entrada ocupa ~180 bytes). Con más, quedan las marcadas más recientemente.
const MaxTrusted = 32

// Trusted es alguien de la lista de confianza: su llave, el último nombre con que se lo vio, quién
// lo marcó y cuándo.
type Trusted struct {
	Key  string    `json:"key"`
	Name string    `json:"name"`
	By   string    `json:"by,omitempty"`
	At   time.Time `json:"at"`
}

// CleanTrusted deja una lista que vino de la red o de un archivo lista para usar: sin llaves que no
// se pueden leer, nombres recortados (ClampUsername), una entrada por llave (la más reciente) y a
// lo sumo MaxTrusted (las más recientes), en orden de llave. Nunca devuelve la misma memoria que
// recibió.
func CleanTrusted(list []Trusted) []Trusted {
	byKey := make(map[string]Trusted, len(list))
	for _, entry := range list {
		key, err := identity.ParseKey(entry.Key)
		if err != nil {
			continue
		}
		entry.Key = key.String()
		entry.Name = ClampUsername(entry.Name)
		entry.By = ClampUsername(entry.By)
		entry.At = entry.At.UTC().Truncate(time.Second)
		if old, ok := byKey[entry.Key]; ok && !entry.At.After(old.At) {
			continue
		}
		byKey[entry.Key] = entry
	}
	out := make([]Trusted, 0, len(byKey))
	for _, entry := range byKey {
		out = append(out, entry)
	}
	if len(out) > MaxTrusted {
		sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
		out = out[:MaxTrusted]
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	if len(out) == 0 {
		return nil
	}
	return out
}

// MergeTrusted junta dos listas (CleanTrusted de las dos).
func MergeTrusted(a, b []Trusted) []Trusted {
	return CleanTrusted(append(append([]Trusted(nil), a...), b...))
}

// SameTrusted dice si dos listas limpias son iguales.
func SameTrusted(a, b []Trusted) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Name != b[i].Name || a[i].By != b[i].By || !a[i].At.Equal(b[i].At) {
			return false
		}
	}
	return true
}

// TrustedHas dice si key está en la lista.
func TrustedHas(list []Trusted, key identity.Key) bool {
	text := key.String()
	for _, entry := range list {
		if entry.Key == text {
			return true
		}
	}
	return false
}

// MissingTrusted son las entradas de mine que faltan en theirs (o que en theirs son más viejas):
// lo que un cliente le manda al host para que la lista de la sala tenga todo.
func MissingTrusted(mine, theirs []Trusted) []Trusted {
	have := make(map[string]time.Time, len(theirs))
	for _, entry := range theirs {
		have[entry.Key] = entry.At
	}
	var missing []Trusted
	for _, entry := range mine {
		if at, ok := have[entry.Key]; !ok || entry.At.After(at) {
			missing = append(missing, entry)
		}
	}
	return missing
}

// trustFile es el formato de confianza.json.
type trustFile struct {
	Version int       `json:"version"`
	Trusted []Trusted `json:"trusted"`
}

const trustFileVersion = 1

// EncodeTrustFile arma el contenido de confianza.json.
func EncodeTrustFile(list []Trusted) ([]byte, error) {
	return json.MarshalIndent(trustFile{Version: trustFileVersion, Trusted: CleanTrusted(list)}, "", "  ")
}

// DecodeTrustFile lee confianza.json. Roto o de otra versión: lista vacía (sin compatibilidad
// hacia atrás: lo que no carga se ignora).
func DecodeTrustFile(data []byte) []Trusted {
	var f trustFile
	if json.Unmarshal(data, &f) != nil || f.Version != trustFileVersion {
		return nil
	}
	return CleanTrusted(f.Trusted)
}
