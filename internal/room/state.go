package room

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"
)

type Participant struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	Address     string    `json:"address"`
	Order       int       `json:"order"`
	Connected   bool      `json:"connected"`
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
		Participants: []Participant{{ID: id, Username: username, Address: address.String(), Order: 0, Connected: true, LastSeenUTC: time.Now().UTC()}},
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
	participant.Order = len(s.Participants)
	s.Participants = append(s.Participants, participant)
	s.Sort()
}

func (s *State) MarkDisconnected(id string) {
	for index := range s.Participants {
		if s.Participants[index].ID == id {
			s.Participants[index].Connected = false
			s.Participants[index].LastSeenUTC = time.Now().UTC()
		}
	}
}

func (s *State) Sort() {
	sort.SliceStable(s.Participants, func(left, right int) bool {
		return s.Participants[left].Order < s.Participants[right].Order
	})
}

func (s State) NextHost() (Participant, bool) {
	participants := append([]Participant(nil), s.Participants...)
	sort.Slice(participants, func(left, right int) bool { return participants[left].Order < participants[right].Order })
	for _, participant := range participants {
		if participant.Connected && participant.ID != s.HostID {
			return participant, true
		}
	}
	return Participant{}, false
}

// ElectNextHost promotes exactly the first connected participant after a
// disconnected host. Callers should broadcast the resulting state together
// with its incremented epoch.
func (s *State) ElectNextHost() (Participant, bool) {
	if s == nil {
		return Participant{}, false
	}
	for _, participant := range s.Participants {
		if participant.ID == s.HostID && participant.Connected {
			return Participant{}, false
		}
	}
	candidate, ok := s.NextHost()
	if !ok {
		return Participant{}, false
	}
	s.HostID = candidate.ID
	s.Epoch++
	return candidate, true
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
	mu    sync.RWMutex
	state State
}

func NewStore(state State) *Store { return &Store{state: state} }

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
