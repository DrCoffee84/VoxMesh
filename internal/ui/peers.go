//go:build cgo

package ui

import (
	"strings"
	"time"

	"voxmesh/internal/audio"
	"voxmesh/internal/config"
)

type peerView struct {
	id        string
	name      string
	address   string
	lag       time.Duration
	speaking  bool
	speakStop time.Time
	isSelf    bool
	muted     bool
	net       peerNetStats
}

// peerNetStats es lo que mide el jitter buffer del emisor en el último intervalo (audioMonitorLoop).
type peerNetStats struct {
	known    bool
	lossPct  float64
	jitterMS float64
}

func (a *App) refreshPeersFromRoom() {
	selfName := a.conf().Username
	if selfName == "" {
		selfName = "Usuario"
	}
	connected := a.connected()
	a.peerMu.Lock()
	lagByAddress := make(map[string]time.Duration, len(a.peerNames))
	netByID := make(map[string]peerNetStats, len(a.peerNames))
	for _, peer := range a.peerNames {
		lagByAddress[peer.address] = peer.lag
		netByID[peer.id] = peer.net
	}
	a.peerNames = nil
	if connected {
		a.peerNames = append(a.peerNames, peerView{
			id:        a.participantID,
			name:      selfName,
			address:   "127.0.0.1",
			isSelf:    true,
			muted:     a.isMuted.Load(),
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
			muted:   participant.Muted,
			net:     netByID[participant.ID],
		})
	}
	a.peerMu.Unlock()
	a.loadSavedPeerVolumes()
	a.refreshPeerAvatars()
}

// setSelfMuted actualiza solo el avatar propio: el mute local se ve al instante, sin esperar
// la vuelta del PacketRoomState del host.
func (a *App) setSelfMuted(muted bool) {
	a.peerMu.Lock()
	changed := false
	for index := range a.peerNames {
		if a.peerNames[index].isSelf && a.peerNames[index].muted != muted {
			a.peerNames[index].muted = muted
			changed = true
		}
	}
	a.peerMu.Unlock()
	if changed {
		a.refreshPeerAvatars()
	}
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

// updatePeerNetStats publica pérdida y jitter medidos por emisor (ID de participante). Los que no
// están en stats (no hablaron en el intervalo) conservan el último valor.
func (a *App) updatePeerNetStats(stats map[string]peerNetStats) {
	if len(stats) == 0 {
		return
	}
	a.peerMu.Lock()
	changed := false
	for i := range a.peerNames {
		p := &a.peerNames[i]
		if p.isSelf {
			continue
		}
		next, ok := stats[p.id]
		if ok && next != p.net {
			p.net = next
			changed = true
		}
	}
	a.peerMu.Unlock()
	if changed {
		a.refreshPeerAvatars()
	}
}

// peerVolume devuelve el volumen de un participante: el elegido en esta sesión o, si no, el
// guardado por nombre de usuario.
func (a *App) peerVolume(id, name string) float64 {
	a.peerVolumeMu.RLock()
	volume, ok := a.peerVolumes[id]
	a.peerVolumeMu.RUnlock()
	if ok {
		return volume
	}
	if saved, ok := a.conf().PeerVolumes[name]; ok {
		return saved
	}
	return 1
}

// setPeerVolume fija el volumen de un participante en el mapa y en el Engine (al instante). Con
// persist también lo guarda por nombre (al soltar el slider, no en cada paso del arrastre).
func (a *App) setPeerVolume(id, name string, volume float64, persist bool) {
	if volume < 0 {
		volume = 0
	} else if volume > config.PeerVolumeMax {
		volume = config.PeerVolumeMax
	}
	a.peerVolumeMu.Lock()
	a.peerVolumes[id] = volume
	a.peerVolumeMu.Unlock()
	if engine := a.engine(); engine != nil {
		engine.SetStreamVolume(id, volume)
	}
	if !persist || name == "" {
		return
	}
	a.updateConfig(func(c *config.Config) {
		if volume == 1 {
			delete(c.PeerVolumes, name)
			return
		}
		if c.PeerVolumes == nil {
			c.PeerVolumes = make(map[string]float64)
		}
		c.PeerVolumes[name] = volume
	})
	a.saveConfig()
}

// loadSavedPeerVolumes aplica el volumen guardado por nombre a los participantes que todavía no
// tienen uno elegido en esta sesión (su ID cambia en cada conexión, el nombre no).
func (a *App) loadSavedPeerVolumes() {
	saved := a.conf().PeerVolumes
	if len(saved) == 0 {
		return
	}
	a.peerMu.RLock()
	peers := make([]peerView, len(a.peerNames))
	copy(peers, a.peerNames)
	a.peerMu.RUnlock()
	for _, peer := range peers {
		if peer.isSelf || peer.id == "" {
			continue
		}
		volume, ok := saved[peer.name]
		if !ok {
			continue
		}
		a.peerVolumeMu.RLock()
		_, chosen := a.peerVolumes[peer.id]
		a.peerVolumeMu.RUnlock()
		if !chosen {
			a.setPeerVolume(peer.id, peer.name, volume, false)
		}
	}
}

// applyPeerVolumes copia los volúmenes por participante a un Engine recién abierto.
func (a *App) applyPeerVolumes(engine *audio.Engine) {
	a.peerVolumeMu.RLock()
	defer a.peerVolumeMu.RUnlock()
	for id, volume := range a.peerVolumes {
		engine.SetStreamVolume(id, volume)
	}
}
