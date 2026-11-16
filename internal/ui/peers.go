//go:build cgo

package ui

import (
	"math"
	"strings"
	"time"

	"voxmesh/internal/audio"
	"voxmesh/internal/config"
	"voxmesh/internal/i18n"
	"voxmesh/internal/logging"
	"voxmesh/internal/room"
)

type peerView struct {
	id      string
	name    string
	address string
	isHost  bool // es el host de la sala (el cliente lo mide él; a los demás, el host)
	lag     time.Duration
	// lagAt es cuándo llegó lag. lagViaHost: lo midió el host con esa persona y lo mandó
	// (PacketPeerLag, C18), no esta PC; el chip lo marca con "≈" y no va al historial.
	lagAt      time.Time
	lagViaHost bool
	speaking   bool
	speakStop  time.Time
	shoutUntil time.Time // hasta cuándo va el aro rojo del avatar (habló en modo grito)
	isSelf     bool
	muted      bool
	sharing    bool // comparte su pantalla (el propio: la sesión local, sin esperar al host)
	net        peerNetStats
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
		selfName = i18n.T("default.username")
	}
	connected := a.connected()
	a.peerMu.Lock()
	lagByID := make(map[string]peerView, len(a.peerNames)) // lag, lagAt y lagViaHost
	netByID := make(map[string]peerNetStats, len(a.peerNames))
	shoutByID := make(map[string]time.Time, len(a.peerNames))
	for _, peer := range a.peerNames {
		lagByID[peer.id] = peer
		netByID[peer.id] = peer.net
		shoutByID[peer.id] = peer.shoutUntil
	}
	a.peerNames = nil
	if connected {
		a.peerNames = append(a.peerNames, peerView{
			id:         a.participantID,
			name:       selfName,
			address:    "127.0.0.1",
			isHost:     a.participantID == a.roomState.HostID, // "Local · Host" (C35)
			isSelf:     true,
			muted:      a.isMuted.Load(),
			sharing:    a.screenShare.Load() != nil,
			speaking:   a.voiceActive.Load(),
			speakStop:  time.Now().Add(250 * time.Millisecond),
			shoutUntil: shoutByID[a.participantID],
		})
	}
	for _, participant := range a.roomState.Participants {
		if participant.ID == a.participantID || !participant.Connected {
			continue
		}
		previous := lagByID[participant.ID]
		a.peerNames = append(a.peerNames, peerView{
			id:         participant.ID,
			name:       participant.Username,
			address:    participant.Address,
			isHost:     participant.ID == a.roomState.HostID,
			lag:        previous.lag,
			lagAt:      previous.lagAt,
			lagViaHost: previous.lagViaHost,
			muted:      participant.Muted,
			sharing:    participant.Sharing,
			net:        netByID[participant.ID],
			shoutUntil: shoutByID[participant.ID],
		})
	}
	a.peerMu.Unlock()
	a.publishScreenState()
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

// updatePeerLag guarda el lag (ida y vuelta) que midió el Pong de address: en el chip y en el
// historial (C18). Con id ("" = buscar por dirección, el host) busca a ese participante: el cliente
// atribuye el Pong de su host por ID, porque el host se anuncia con su dirección pública y el Pong
// puede venir de su LAN o VPN.
func (a *App) updatePeerLag(id, address string, lag time.Duration) {
	a.rttFloor.Add(address, lag) // ritmo de la pantalla compartida: ¿es LAN?
	now := time.Now()
	name, isHost := "", false
	a.peerMu.Lock()
	for index := range a.peerNames {
		p := &a.peerNames[index]
		if !p.isSelf && ((id != "" && p.id == id) || (id == "" && p.address == address)) {
			p.lag, p.lagAt, p.lagViaHost = lag, now, false
			name, isHost = p.name, p.isHost
			break
		}
	}
	a.peerMu.Unlock()
	if name != "" {
		if event, ok := a.lagHistory.addRTT(name, now, lag); ok {
			a.reportLag(event, isHost)
		}
	}
	a.refreshPeerAvatars()
}

// reportLag deja en el registro de actividad (solo local: es la red de esta PC) y en el log el aviso
// de un lag prolongado que empezó o terminó (C13). isHost: la persona es el host de la sala.
func (a *App) reportLag(event lagEvent, isHost bool) {
	msg := lagEventMsg(event, isHost && !a.hostMode.Load())
	logging.Infof("%s", msg.In(i18n.ES))
	a.addActivity(msg.String())
}

// peerLagFresh es cuánto vale un lag medido: el host no le manda a los clientes uno más viejo (el
// que no contesta hace rato), y el cliente vuelve a "… ms".
const peerLagFresh = 6 * time.Second

// peerLagsToSend (host) son los lags que midió esta PC con cada uno, frescos, para PacketPeerLag.
func (a *App) peerLagsToSend(now time.Time) []room.PeerLag {
	a.peerMu.RLock()
	defer a.peerMu.RUnlock()
	lags := make([]room.PeerLag, 0, len(a.peerNames))
	for _, p := range a.peerNames {
		if p.isSelf || p.lagViaHost || p.lag <= 0 || now.Sub(p.lagAt) > peerLagFresh || len(lags) == room.MaxParticipants {
			continue
		}
		lags = append(lags, room.PeerLag{ID: p.id, MS: int(p.lag.Milliseconds())})
	}
	return lags
}

// applyPeerLags (cliente) pone en el chip de cada uno, salvo el host (lo mide esta PC) y uno mismo,
// el lag que midió el host con él. Quien no está en lags vuelve a "… ms". No va al historial: cada
// uno guarda solo lo que mide él.
func (a *App) applyPeerLags(lags []room.PeerLag, now time.Time) {
	byID := make(map[string]time.Duration, len(lags))
	for _, lag := range lags {
		byID[lag.ID] = time.Duration(lag.MS) * time.Millisecond
	}
	changed := false
	a.peerMu.Lock()
	for index := range a.peerNames {
		p := &a.peerNames[index]
		if p.isSelf || p.isHost {
			continue
		}
		lag, ok := byID[p.id] // si esta PC era host, lo que midió ya no es fresco: también se borra
		if p.lag != lag || p.lagViaHost != ok {
			changed = true
		}
		p.lag, p.lagAt, p.lagViaHost = lag, now, ok
	}
	a.peerMu.Unlock()
	if changed {
		a.refreshPeerAvatars()
	}
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

// shoutRingHold es cuánto queda el aro rojo después del último frame en modo grito.
const shoutRingHold = 400 * time.Millisecond

// markPeerShouting le pone el aro rojo al avatar de id (el propio con a.participantID): habló en
// modo grito. Solo refresca cuando se prende; lo apaga sweepSpeakingPeers.
func (a *App) markPeerShouting(id string) {
	a.peerMu.Lock()
	now := time.Now()
	changed := false
	for i := range a.peerNames {
		p := &a.peerNames[i]
		if p.id == id || (p.isSelf && id == a.participantID) {
			changed = !now.Before(p.shoutUntil)
			p.shoutUntil = now.Add(shoutRingHold)
			break
		}
	}
	a.peerMu.Unlock()
	if changed {
		a.refreshPeerAvatars()
	}
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
		if !p.shoutUntil.IsZero() && !now.Before(p.shoutUntil) {
			p.shoutUntil = time.Time{}
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
	now := time.Now()
	type measured struct {
		name   string
		isHost bool
		net    peerNetStats
	}
	var history []measured
	a.peerMu.Lock()
	changed := false
	for i := range a.peerNames {
		p := &a.peerNames[i]
		if p.isSelf {
			continue
		}
		next, ok := stats[p.id]
		if ok {
			history = append(history, measured{p.name, p.isHost, next})
		}
		if ok && next != p.net {
			p.net = next
			changed = true
		}
	}
	a.peerMu.Unlock()
	for _, m := range history {
		if event, ok := a.lagHistory.addNet(m.name, now, m.net.lossPct, m.net.jitterMS); ok {
			a.reportLag(event, m.isHost)
		}
	}
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
	volume = math.Min(math.Max(volume, 0), float64(a.conf().PeerVolumeMax))
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

func (a *App) applyPeerVolumeMax() {
	limit := float64(a.conf().PeerVolumeMax)
	var over []string
	a.peerVolumeMu.RLock()
	for id, volume := range a.peerVolumes {
		if volume > limit {
			over = append(over, id)
		}
	}
	a.peerVolumeMu.RUnlock()
	for _, id := range over {
		a.setPeerVolume(id, "", limit, false)
	}
	a.updateConfig(func(c *config.Config) {
		for name, volume := range c.PeerVolumes {
			c.PeerVolumes[name] = min(volume, limit)
		}
	})
	a.peerChips = nil
	a.refreshPeerAvatars()
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
