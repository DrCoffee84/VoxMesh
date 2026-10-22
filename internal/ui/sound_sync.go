//go:build cgo

package ui

import (
	"net"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"voxmesh/internal/assets"
	"voxmesh/internal/history"
	"voxmesh/internal/logging"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

// Sync de la botonera. Al conectar (y al reconectar tras una migración) el cliente le manda al
// host los sonidos que tiene en disco y sus lápidas; el host los mezcla con los suyos y responde
// la lista completa de la sala. Viaja solo metadata: el audio de un sonido se pide con
// AssetMissing recién cuando alguien lo toca (playOrFetchSound), así entrar a una sala con 100
// sonidos no mueve cientos de MB.
const (
	soundSyncRetry  = 2 * time.Second  // reenvío del pedido mientras no llegue la respuesta
	soundFetchRetry = 3 * time.Second  // un pedido de audio por sonido cada tanto, no uno por play
	pendingPlayTTL  = 10 * time.Second // un audio que llega más tarde se guarda pero ya no suena
)

// requestSoundSync marca que falta sincronizar la botonera con el host. Lo manda
// maybeSendSoundSync cuando ya llegó el estado de la sala (y con él el nombre real de la sala).
func (a *App) requestSoundSync() {
	a.soundSyncSentAt.Store(0)
	a.soundSyncPending.Store(true)
}

// maybeSendSoundSync (cliente) manda el pedido mientras no llegue la respuesta: lo llaman
// handleRoomState y monitorHost (cada 2 s), por si UDP pierde el pedido o la respuesta.
func (a *App) maybeSendSoundSync(udp *transport.UDP) {
	store := a.historyStore
	if !a.soundSyncPending.Load() || a.hostMode.Load() || store == nil || a.roomState.HostID == "" {
		return
	}
	last := a.soundSyncSentAt.Load()
	if last != 0 && time.Since(time.Unix(0, last)) < soundSyncRetry {
		return
	}
	if !a.soundSyncSentAt.CompareAndSwap(last, time.Now().UnixNano()) {
		return
	}
	host := a.endpointAddr()
	if host == nil {
		return
	}
	local, _ := store.ListSounds()
	sounds := make([]room.Sound, 0, len(local))
	for _, meta := range local {
		sounds = append(sounds, room.Sound{ID: meta.ID, Ext: meta.Ext, Name: meta.Name})
	}
	payload, err := room.Encode(room.Envelope{Kind: room.SoundSyncRequestMessage, Sounds: sounds, DeletedIDs: store.DeletedSounds()})
	if err != nil {
		return
	}
	if len(payload) > transport.MaxPayloadSize {
		logging.Errorf("sync de sonidos: la lista (%d sonidos) no entra en un paquete", len(sounds))
		a.soundSyncPending.Store(false)
		return
	}
	_ = udp.Send(host, transport.Packet{Kind: transport.PacketChatSync, Sequence: a.sequence.Add(1), Payload: payload})
}

// handleSoundSyncRequest (host): aplica las lápidas del cliente, suma los sonidos que solo tiene
// él (y lo anota como fuente de su audio), avisa los cambios al resto y le responde la lista
// completa. Es idempotente: un pedido reenviado no vuelve a anunciar nada.
func (a *App) handleSoundSyncRequest(udp *transport.UDP, addr *net.UDPAddr, envelope room.Envelope) {
	store := a.historyStore
	if store == nil || addr == nil {
		return
	}
	if err := store.AddSoundTombstones(envelope.DeletedIDs); err != nil {
		logging.Errorf("sync de sonidos: guardar lápidas: %s", logging.FormatError(err))
	}
	removed := a.dropSounds(envelope.DeletedIDs)
	deleted := stringSet(store.DeletedSounds())

	var added []room.Sound
	a.soundMu.Lock()
	known := make(map[string]struct{}, len(a.sounds))
	for _, sound := range a.sounds {
		known[sound.ID] = struct{}{}
	}
	for _, sound := range envelope.Sounds {
		if _, gone := deleted[sound.ID]; gone {
			continue
		}
		a.addSoundHolderLocked(sound.ID, addr)
		if _, exists := known[sound.ID]; exists {
			continue
		}
		known[sound.ID] = struct{}{}
		a.sounds = append(a.sounds, sound)
		added = append(added, sound)
	}
	roomSounds := append([]room.Sound(nil), a.sounds...)
	a.soundMu.Unlock()
	if len(added) > 0 {
		a.rebuildSoundboard()
	}

	// El cliente ya conoce sus propios cambios: el resto se entera por los mensajes de siempre.
	for _, sound := range removed {
		a.broadcastSoundEnvelope(udp, room.SoundRemoveMessage, sound, addr)
	}
	for _, sound := range added {
		a.broadcastSoundEnvelope(udp, room.SoundAddMessage, sound, addr)
	}
	payload, err := room.Encode(room.Envelope{Kind: room.SoundSyncResponseMessage, Sounds: roomSounds, DeletedIDs: store.DeletedSounds()})
	if err != nil {
		return
	}
	if len(payload) > transport.MaxPayloadSize {
		logging.Errorf("sync de sonidos: la lista de la sala (%d sonidos) no entra en un paquete", len(roomSounds))
		return
	}
	_ = udp.Send(addr, transport.Packet{Kind: transport.PacketChatSync, Sequence: a.sequence.Add(1), Payload: payload})
	logging.Infof("Sync de sonidos con %s: %d recibidos, %d nuevos, %d borrados, %d en la sala", addr, len(envelope.Sounds), len(added), len(removed), len(roomSounds))
}

// handleSoundSyncResponse (cliente): la lista del host pasa a ser la botonera (en su orden). Se
// conservan al final los sonidos locales que el host todavía no tiene (pedido en vuelo).
func (a *App) handleSoundSyncResponse(envelope room.Envelope) {
	store := a.historyStore
	if store == nil {
		return
	}
	a.soundSyncPending.Store(false)
	if err := store.AddSoundTombstones(envelope.DeletedIDs); err != nil {
		logging.Errorf("sync de sonidos: guardar lápidas: %s", logging.FormatError(err))
	}
	removed := a.dropSounds(envelope.DeletedIDs)
	deleted := stringSet(envelope.DeletedIDs)

	var renamed []room.Sound
	a.soundMu.Lock()
	current := make(map[string]room.Sound, len(a.sounds))
	for _, sound := range a.sounds {
		current[sound.ID] = sound
	}
	merged := make([]room.Sound, 0, len(envelope.Sounds)+len(a.sounds))
	inRoom := make(map[string]struct{}, len(envelope.Sounds))
	for _, sound := range envelope.Sounds {
		if _, gone := deleted[sound.ID]; gone {
			continue
		}
		if _, dup := inRoom[sound.ID]; dup {
			continue
		}
		inRoom[sound.ID] = struct{}{}
		merged = append(merged, sound)
		if previous, ok := current[sound.ID]; ok && previous.Name != sound.Name {
			renamed = append(renamed, sound)
		}
	}
	for _, sound := range a.sounds {
		if _, ok := inRoom[sound.ID]; !ok {
			merged = append(merged, sound)
		}
	}
	a.sounds = merged
	a.soundMu.Unlock()

	// Solo hay metadata que actualizar si el audio ya está en disco (si no, no está en el manifest).
	for _, sound := range renamed {
		if err := store.UpdateSoundMeta(history.SoundMeta{ID: sound.ID, Ext: sound.Ext, Name: sound.Name}); err != nil && !os.IsNotExist(err) {
			logging.Errorf("sync de sonidos: renombrar %q: %s", sound.ID, logging.FormatError(err))
		}
	}
	a.rebuildSoundboard()
	logging.Infof("Sync de sonidos: %d en la sala, %d borrados, %d renombrados", len(merged), len(removed), len(renamed))
}

// dropSounds saca de la botonera los sonidos con esos IDs, borra su audio (DeleteSound deja la
// lápida) y sus atajos. No avisa a la sala. Devuelve los que estaban.
func (a *App) dropSounds(ids []string) []room.Sound {
	if len(ids) == 0 {
		return nil
	}
	drop := stringSet(ids)
	a.soundMu.Lock()
	var removed []room.Sound
	kept := make([]room.Sound, 0, len(a.sounds))
	for _, sound := range a.sounds {
		if _, ok := drop[sound.ID]; ok {
			removed = append(removed, sound)
			delete(a.soundHolders, sound.ID)
			continue
		}
		kept = append(kept, sound)
	}
	if len(removed) > 0 {
		a.sounds = kept
	}
	a.soundMu.Unlock()
	if len(removed) == 0 {
		return nil
	}

	binds := a.conf().SoundBinds
	bound := false
	for _, sound := range removed {
		if _, ok := binds[sound.ID]; ok {
			bound = true
			break
		}
	}
	if bound {
		a.updateSoundBinds(func(binds map[string]string) {
			for _, sound := range removed {
				delete(binds, sound.ID)
			}
		})
	}
	a.rebuildSoundboard()

	if store := a.historyStore; store != nil {
		for _, sound := range removed {
			if err := store.DeleteSound(sound.ID, sound.Ext); err != nil && !os.IsNotExist(err) {
				logging.Errorf("no se pudo eliminar el archivo del sonido local: %s", logging.FormatError(err))
			}
		}
	}
	go func() {
		runtime.GC()
		debug.FreeOSMemory()
	}()
	return removed
}

// playOrFetchSound reproduce el sonido si el audio está en disco. Si no, lo deja pendiente y lo
// pide: handleAssetChunk lo reproduce cuando termina de llegar. Devuelve true si hubo que pedirlo.
func (a *App) playOrFetchSound(sound room.Sound) bool {
	store := a.historyStore
	if store == nil {
		return false
	}
	if data, err := store.ReadSound(sound.ID, sound.Ext); err == nil {
		a.playSoundPCM(data)
		return false
	}
	a.soundMu.Lock()
	if a.pendingPlays == nil {
		a.pendingPlays = make(map[string]time.Time)
	}
	a.pendingPlays[sound.ID] = time.Now()
	a.soundMu.Unlock()
	a.fetchSound(sound.ID, sound.Ext, nil)
	return true
}

// fetchSound pide el audio de un sonido que no está en disco. Un cliente se lo pide al host. El
// host puede no tenerlo (solo lo tiene quien lo agregó o ya lo tocó): se lo pide a uno de los
// peers que lo anunciaron en su sync, rotando en cada intento y nunca a requester. Los chunks
// que recibe el host se reenvían a toda la sala (receiveLoop), así le llegan también a quien
// lo pidió y al resto, que lo necesita porque el sound_play les llegó a todos.
func (a *App) fetchSound(id, ext string, requester *net.UDPAddr) {
	udp := a.transport.Load()
	if udp == nil {
		return
	}
	host := a.hostMode.Load()
	var target *net.UDPAddr
	a.soundMu.Lock()
	if last, ok := a.soundFetchAt[id]; ok && time.Since(last) < soundFetchRetry {
		a.soundMu.Unlock()
		return
	}
	if host {
		holders := a.soundHolders[id]
		for index, holder := range holders {
			if requester != nil && holder.String() == requester.String() {
				continue
			}
			target = holder
			// Al final de la lista: el próximo intento prueba con otro (por si este se fue).
			a.soundHolders[id] = append(append(append([]*net.UDPAddr(nil), holders[:index]...), holders[index+1:]...), holder)
			break
		}
	} else {
		target = a.endpointAddr()
	}
	if target != nil {
		if a.soundFetchAt == nil {
			a.soundFetchAt = make(map[string]time.Time)
		}
		a.soundFetchAt[id] = time.Now()
	}
	a.soundMu.Unlock()
	if target == nil {
		if host {
			logging.Infof("Nadie conectado tiene el audio del sonido %q", id)
		}
		return
	}
	request := assets.MissingRequest{Kind: assets.KindSound, ID: id, Ext: ext}
	_ = udp.Send(target, transport.Packet{Kind: transport.PacketAssetMissing, Sequence: a.sequence.Add(1), Payload: assets.EncodeMissing(request)})
}

// addSoundHolderLocked anota (host) que addr tiene el audio de id. Requiere soundMu.
func (a *App) addSoundHolderLocked(id string, addr *net.UDPAddr) {
	if addr == nil {
		return
	}
	if a.soundHolders == nil {
		a.soundHolders = make(map[string][]*net.UDPAddr)
	}
	for _, holder := range a.soundHolders[id] {
		if holder.String() == addr.String() {
			return
		}
	}
	a.soundHolders[id] = append(a.soundHolders[id], addr)
}

// broadcastSoundEnvelope (host) avisa un cambio de la botonera a todos menos except.
func (a *App) broadcastSoundEnvelope(udp *transport.UDP, kind string, sound room.Sound, except *net.UDPAddr) {
	payload, err := room.Encode(room.Envelope{Kind: kind, Sound: &sound})
	if err == nil {
		udp.Broadcast(transport.Packet{Kind: transport.PacketChat, Sequence: a.sequence.Add(1), Payload: payload}, except)
	}
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}
