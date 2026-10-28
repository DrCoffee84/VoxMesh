package room

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"time"

	"voxmesh/internal/logging"
)

type Participant struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	Address     string    `json:"address"`
	Order       int       `json:"order"`
	Connected   bool      `json:"connected"`
	CanBeHost   bool      `json:"can_be_host"`
	Muted       bool      `json:"muted,omitempty"`
	LastSeenUTC time.Time `json:"last_seen_utc"`
}

type State struct {
	Name         string        `json:"name"`
	Epoch        uint64        `json:"epoch"`
	HostID       string        `json:"host_id"`
	Participants []Participant `json:"participants"`
}

func New(name, username string, address *net.UDPAddr) State {
	id := ParticipantID(username, address.String())
	return State{
		Name:         name,
		Epoch:        1,
		HostID:       id,
		Participants: []Participant{{ID: id, Username: username, Address: address.String(), Order: 0, Connected: true, CanBeHost: true, LastSeenUTC: time.Now().UTC()}},
	}
}

func ParticipantID(username, address string) string {
	hash := sha256.Sum256([]byte(username + "|" + address))
	return hex.EncodeToString(hash[:8])
}

func (s *State) Upsert(participant Participant) {
	for index := range s.Participants {
		if s.Participants[index].ID == participant.ID {
			participant.Order = s.Participants[index].Order
			s.Participants[index] = participant
			return
		}
	}
	participant.Order = s.NextOrder()
	s.Participants = append(s.Participants, participant)
	s.Sort()
}

// NextOrder es el Order para el que entra: uno más que el mayor. Con la poda la lista tiene
// huecos, así que len(Participants) puede repetir un Order (y Validate lo rechaza).
func (s State) NextOrder() int {
	next := 0
	for _, participant := range s.Participants {
		if participant.Order >= next {
			next = participant.Order + 1
		}
	}
	return next
}

// SetMuted cambia el mute del participante id. Si address no es vacío también tiene que
// coincidir (el host solo acepta el cambio desde la dirección registrada en el Hello).
// Reemplaza el slice en vez de escribir en el compartido: quien tenga una copia del State
// anterior no ve el cambio a medias. Devuelve si el estado cambió.
func (s *State) SetMuted(id, address string, muted bool) bool {
	for index, participant := range s.Participants {
		if participant.ID != id || (address != "" && participant.Address != address) {
			continue
		}
		if participant.Muted == muted {
			return false
		}
		participants := append([]Participant(nil), s.Participants...)
		participants[index].Muted = muted
		s.Participants = participants
		return true
	}
	return false
}

func (s *State) MarkDisconnected(id string) {
	for index := range s.Participants {
		if s.Participants[index].ID == id {
			s.Participants[index].Connected = false
			s.Participants[index].LastSeenUTC = time.Now().UTC()
		}
	}
}

// DisconnectedTTL es cuánto queda en la lista un desconectado: más que el aviso del host nuevo
// tras una migración (FailoverTimings.Announce), así el host viejo que vuelve sigue siendo de la sala.
const DisconnectedTTL = 10 * time.Minute

// MaxParticipants es el tope de la lista (red de seguridad: el RoomState viaja en un datagrama).
const MaxParticipants = 16

// PruneDisconnected saca a los desconectados hace más de olderThan. Nunca al host ni a un
// conectado. Reemplaza el slice (como SetMuted) y devuelve los que sacó.
func (s *State) PruneDisconnected(now time.Time, olderThan time.Duration) []Participant {
	var kept, removed []Participant
	for _, participant := range s.Participants {
		if s.prunable(participant, now, olderThan) {
			removed = append(removed, participant)
		} else {
			kept = append(kept, participant)
		}
	}
	if len(removed) > 0 {
		s.Participants = kept
	}
	return removed
}

// MakeRoom deja lugar para uno más: con la lista llena (max) saca a los desconectados hace más
// de minAge, del más viejo al más nuevo, hasta que entre uno. Devuelve los que sacó y si hay lugar.
func (s *State) MakeRoom(now time.Time, max int, minAge time.Duration) ([]Participant, bool) {
	excess := len(s.Participants) - max + 1
	if excess <= 0 {
		return nil, true
	}
	var oldest []Participant
	for _, participant := range s.Participants {
		if s.prunable(participant, now, minAge) {
			oldest = append(oldest, participant)
		}
	}
	sort.SliceStable(oldest, func(left, right int) bool { return oldest[left].LastSeenUTC.Before(oldest[right].LastSeenUTC) })
	if len(oldest) > excess {
		oldest = oldest[:excess]
	}
	if len(oldest) > 0 {
		drop := make(map[string]bool, len(oldest))
		for _, participant := range oldest {
			drop[participant.ID] = true
		}
		kept := make([]Participant, 0, len(s.Participants)-len(oldest))
		for _, participant := range s.Participants {
			if !drop[participant.ID] {
				kept = append(kept, participant)
			}
		}
		s.Participants = kept
	}
	return oldest, len(s.Participants) < max
}

func (s State) prunable(participant Participant, now time.Time, olderThan time.Duration) bool {
	return !participant.Connected && participant.ID != s.HostID && now.Sub(participant.LastSeenUTC) > olderThan
}

func (s *State) Sort() {
	sort.SliceStable(s.Participants, func(left, right int) bool {
		return s.Participants[left].Order < s.Participants[right].Order
	})
}

// Candidates devuelve, en el orden de la sala, los participantes que pueden reemplazar al host:
// conectados, con CanBeHost y sin el host actual. Todos los que tienen el mismo estado calculan la
// misma lista (Failover).
func (s State) Candidates() []Participant {
	var candidates []Participant
	for _, participant := range s.Participants {
		if participant.Connected && participant.CanBeHost && participant.ID != s.HostID {
			candidates = append(candidates, participant)
		}
	}
	sort.SliceStable(candidates, func(left, right int) bool { return candidates[left].Order < candidates[right].Order })
	return candidates
}

// NextHost es el primero de Candidates.
func (s State) NextHost() (Participant, bool) {
	if candidates := s.Candidates(); len(candidates) > 0 {
		return candidates[0], true
	}
	return Participant{}, false
}

func (s State) Validate() error {
	if s.Name == "" || s.HostID == "" || len(s.Participants) == 0 {
		return fmt.Errorf("estado de sala incompleto")
	}
	seen := make(map[string]bool, len(s.Participants))
	orders := make(map[int]bool, len(s.Participants))
	hostFound := false
	for _, participant := range s.Participants {
		if participant.ID == "" || seen[participant.ID] {
			return fmt.Errorf("participante inválido o duplicado")
		}
		if orders[participant.Order] {
			return fmt.Errorf("orden de participante duplicado")
		}
		if participant.ID == s.HostID {
			hostFound = true
		}
		seen[participant.ID] = true
		orders[participant.Order] = true
	}
	if !hostFound {
		return fmt.Errorf("host ausente de los participantes")
	}
	return nil
}

type Store struct {
	mu    logging.TracedRWMutex
	state State
}

func NewStore(state State) *Store {
	return &Store{
		mu:    logging.NewTracedRWMutex("room.StoreMu"),
		state: state,
	}
}

func (s *Store) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copyState := s.state
	copyState.Participants = append([]Participant(nil), s.state.Participants...)
	return copyState
}

func (s *Store) Replace(state State) error {
	if err := state.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
	return nil
}
