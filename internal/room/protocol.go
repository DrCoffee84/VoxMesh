package room

import (
	"encoding/json"

	"voxmesh/internal/history"
)

const (
	HelloMessage        = "hello"
	StateMessage        = "state"
	ChatMessage         = "chat"
	SyncRequestMessage  = "sync_request"
	SyncResponseMessage = "sync_response"
	SystemMessage       = "system"
	HostClaimMessage    = "host_claim"
	ReconnectMessage    = "reconnect"
	SoundAddMessage     = "sound_add"
	SoundRemoveMessage  = "sound_remove"
	SoundPlayMessage    = "sound_play"
	LeaveMessage        = "leave"
)

type Envelope struct {
	Kind        string            `json:"kind"`
	Epoch       uint64            `json:"epoch,omitempty"`
	CandidateID string            `json:"candidate_id,omitempty"`
	ReplyTo     string            `json:"reply_to,omitempty"`
	Room        *State            `json:"room,omitempty"`
	Participant *Participant      `json:"participant,omitempty"`
	Message     *history.Message  `json:"message,omitempty"`
	Messages    []history.Message `json:"messages,omitempty"`
	KnownIDs    []string          `json:"known_ids,omitempty"`
	ImageIDs    []string          `json:"image_ids,omitempty"`
	ImageData   []byte            `json:"image_data,omitempty"`
	Images      []Image           `json:"images,omitempty"`
	Sound       *Sound            `json:"sound,omitempty"`
}

type Image struct {
	ID   string `json:"id"`
	Ext  string `json:"ext"`
	Data []byte `json:"data"`
}

// Sound identifies a soundboard clip; Name is only meaningful on sound_add.
type Sound struct {
	ID   string `json:"id"`
	Ext  string `json:"ext"`
	Name string `json:"name"`
}

func Encode(envelope Envelope) ([]byte, error) { return json.Marshal(envelope) }

func Decode(data []byte) (Envelope, error) {
	var envelope Envelope
	err := json.Unmarshal(data, &envelope)
	return envelope, err
}
