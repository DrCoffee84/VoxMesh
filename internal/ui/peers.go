//go:build cgo

package ui

import (
	"strings"
	"time"
)

type peerView struct {
	id        string
	name      string
	address   string
	lag       time.Duration
	speaking  bool
	speakStop time.Time
	isSelf    bool
}

func (a *App) refreshPeersFromRoom() {
	a.peerMu.Lock()
	lagByAddress := make(map[string]time.Duration, len(a.peerNames))
	for _, peer := range a.peerNames {
		lagByAddress[peer.address] = peer.lag
	}
	a.peerNames = nil
	if a.transport != nil {
		selfName := a.cfg.Username
		if selfName == "" {
			selfName = "Usuario"
		}
		a.peerNames = append(a.peerNames, peerView{
			id:        a.participantID,
			name:      selfName,
			address:   "127.0.0.1",
			isSelf:    true,
			speaking:  a.voiceActive.Load(),
			speakStop: time.Now().Add(250 * time.Millisecond),
		})
	}
	for _, participant := range a.roomState.Participants {
		if participant.ID == a.participantID || !participant.Connected {
			continue
		}
		a.peerNames = append(a.peerNames, peerView{
			id:      participant.ID,
			name:    participant.Username,
			address: participant.Address,
			lag:     lagByAddress[participant.Address],
		})
	}
	a.peerMu.Unlock()
	a.refreshPeerAvatars()
}

func (a *App) addPeer(name, address string) bool {
	a.peerMu.Lock()
	defer a.peerMu.Unlock()
	for index, peer := range a.peerNames {
		if (address != "" && peer.address == address) || peer.name == name {
			a.peerNames[index].name = name
			if address != "" {
				a.peerNames[index].address = address
			}
			return false
		}
	}
	a.peerNames = append(a.peerNames, peerView{name: name, address: address})
	a.refreshPeerAvatars()
	return true
}

func (a *App) updatePeerLag(address string, lag time.Duration) {
	a.peerMu.Lock()
	for index := range a.peerNames {
		if a.peerNames[index].address == address || strings.Contains(a.peerNames[index].name, "("+address+")") {
			a.peerNames[index].lag = lag
			break
		}
	}
	a.peerMu.Unlock()
	a.refreshPeerAvatars()
}

func (a *App) markPeerSpeaking(id, address string) {
	a.peerMu.Lock()
	now := time.Now()
	for i := range a.peerNames {
		p := &a.peerNames[i]
		match := false
		if p.isSelf && (id == "" || id == a.participantID) {
			match = true
		} else if id != "" && p.id == id {
			match = true
		} else if address != "" && (p.address == address || strings.Contains(p.address, address) || strings.Contains(address, p.address)) {
			match = true
		}
		if match {
			wasSpeaking := p.speaking
			p.speaking = true
			p.speakStop = now.Add(350 * time.Millisecond)
			a.peerMu.Unlock()
			if !wasSpeaking {
				a.refreshPeerAvatars()
			}
			return
		}
	}
	a.peerMu.Unlock()
}

func (a *App) sweepSpeakingPeers() {
	a.peerMu.Lock()
	now := time.Now()
	changed := false
	for i := range a.peerNames {
		p := &a.peerNames[i]
		if p.speaking && (now.After(p.speakStop) || p.speakStop.Sub(now) < 50*time.Millisecond) {
			p.speaking = false
			changed = true
		}
	}
	a.peerMu.Unlock()
	if changed {
		a.refreshPeerAvatars()
	}
}
