//go:build cgo

package ui

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"voxmesh/internal/assets"
	"voxmesh/internal/audio"
	"voxmesh/internal/history"
	"voxmesh/internal/imagefile"
	"voxmesh/internal/logging"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

func (a *App) handleChatPacket(payload []byte) {
	envelope, err := room.Decode(payload)
	if err != nil {
		return
	}
	switch envelope.Kind {
	case room.ChatMessage:
		if envelope.Message != nil {
			if envelope.Message.System || envelope.Message.Kind == "system" || strings.EqualFold(envelope.Message.Username, "SISTEMA") {
				a.addActivityAt(envelope.Message.Text, envelope.Message.Timestamp)
			} else {
				a.addChatMessage(*envelope.Message)
				if envelope.Message.SenderID != a.participantID {
					a.playEventSound(audio.SFXMessage)
				}
			}
		}
	case room.SystemMessage:
		if envelope.Message != nil {
			a.addActivity(envelope.Message.Text)
		}
	case room.SoundAddMessage:
		if envelope.Sound != nil && a.netAssetOK(assets.KindSound, envelope.Sound.ID, envelope.Sound.Ext, "") {
			a.addSound(*envelope.Sound)
		}
	case room.SoundRemoveMessage:
		// Sin reenviarlo: el host ya lo difunde en receiveLoop.
		if envelope.Sound != nil {
			if removed := a.dropSounds([]string{envelope.Sound.ID}); len(removed) > 0 {
				a.setStatus("Sonido eliminado: " + removed[0].Name)
			}
		}
	case room.SoundPlayMessage:
		if envelope.Sound != nil && a.netAssetOK(assets.KindSound, envelope.Sound.ID, envelope.Sound.Ext, "") {
			a.playIncomingSound(*envelope.Sound)
		}
	}
}

// rejectedAssets cuenta los archivos que llegan por red con un ID o una extensión que el Store no
// acepta (AGENDA S1). Se descartan antes de armar o tocar nada y se loguea como máximo una línea
// cada receiveErrorLogEvery.
type rejectedAssets struct {
	mu    sync.Mutex
	last  time.Time
	count int
}

// netAssetOK dice si un archivo de red (imagen o sonido) tiene un ID y una extensión válidos.
// from es solo para el log (vacío si no se conoce).
func (a *App) netAssetOK(kind byte, id, ext, from string) bool {
	var err error
	if kind == assets.KindSound {
		err = history.CheckSound(id, ext)
	} else {
		err = history.CheckImage(id, ext)
	}
	if err == nil {
		return true
	}
	a.assetRejects.note(err, from)
	return false
}

func (r *rejectedAssets) note(err error, from string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count++
	if time.Since(r.last) >= receiveErrorLogEvery {
		logging.Infof("archivos de red descartados (%d): %s (de %q)", r.count, logging.FormatError(err), from)
		r.last, r.count = time.Now(), 0
	}
}

// netSoundIDsOK filtra las lápidas de sonidos que llegan por red (AGENDA S10): solo IDs de NewID.
func (a *App) netSoundIDsOK(ids []string, from string) []string {
	valid := ids[:0:0]
	for _, id := range ids {
		if history.ValidID(id) {
			valid = append(valid, id)
		} else {
			a.assetRejects.note(fmt.Errorf("lápida con identificador inválido: %q", id), from)
		}
	}
	return valid
}

// netSoundsOK filtra los sonidos de la botonera que llegan por red con ID o extensión inválidos.
func (a *App) netSoundsOK(sounds []room.Sound, from string) []room.Sound {
	valid := sounds[:0:0]
	for _, sound := range sounds {
		if a.netAssetOK(assets.KindSound, sound.ID, sound.Ext, from) {
			valid = append(valid, sound)
		}
	}
	return valid
}

func (a *App) handleAssetChunk(payload []byte, origin string) {
	chunk, err := assets.Decode(payload)
	if err != nil || a.assetAssembler == nil || !a.netAssetOK(chunk.Kind, chunk.ID, chunk.Ext, origin) {
		return
	}
	data, complete := a.assetAssembler.Ingest(chunk, origin)
	if !complete || a.historyStore == nil {
		return
	}
	go func() {
		if chunk.Kind == assets.KindSound {
			a.soundMu.Lock()
			name := ""
			known := false
			for _, sound := range a.sounds {
				if sound.ID == chunk.ID {
					name = sound.Name
					known = true
					break
				}
			}
			requested, pending := a.pendingPlays[chunk.ID]
			delete(a.pendingPlays, chunk.ID)
			delete(a.soundFetchAt, chunk.ID)
			if known && a.hostMode.Load() {
				if from, resolveErr := net.ResolveUDPAddr("udp", origin); resolveErr == nil {
					a.addSoundHolderLocked(chunk.ID, from)
				}
			}
			a.soundMu.Unlock()
			// Un sonido que no está en la botonera (borrado, o el sound_add se perdió) no se guarda:
			// quedaría en el manifest sin nombre o resucitaría uno borrado. Si estaba pendiente suena igual.
			if known {
				if _, saveErr := a.historyStore.SaveSound(history.SoundMeta{ID: chunk.ID, Ext: chunk.Ext, Name: name}, data); saveErr != nil {
					logging.Errorf("guardar sonido recibido %q: %s", chunk.ID, logging.FormatError(saveErr))
				}
			}
			if pending && time.Since(requested) < pendingPlayTTL {
				a.playSoundPCM(data)
			}
			return
		}
		// Lo que no es una imagen decodificable dentro de los topes no se guarda (AGENDA S5).
		if checkErr := imagefile.Check(data); checkErr != nil {
			a.assetRejects.note(checkErr, origin)
			return
		}
		if _, saveErr := a.historyStore.SaveImage(chunk.ID, chunk.Ext, data); saveErr != nil {
			logging.Errorf("guardar archivo recibido %q: %s", chunk.ID, logging.FormatError(saveErr))
			return
		}
		a.refreshChat()
	}()
}

func (a *App) handleAssetMissing(udp *transport.UDP, addr *net.UDPAddr, payload []byte) {
	request, err := assets.DecodeMissing(payload)
	if err != nil || a.historyStore == nil || addr == nil || !a.netAssetOK(request.Kind, request.ID, request.Ext, addr.String()) {
		return
	}
	go func() {
		var data []byte
		var readErr error
		if request.Kind == assets.KindSound {
			data, readErr = a.historyStore.ReadSound(request.ID, request.Ext)
		} else {
			data, readErr = a.historyStore.ReadImage(request.ID, request.Ext)
		}
		if readErr != nil || len(data) == 0 {
			// Descarga a pedido: el host puede no tener el audio; se lo pide a quien sí.
			if request.Kind == assets.KindSound && a.hostMode.Load() {
				a.fetchSound(request.ID, request.Ext, addr)
			}
			return
		}
		chunks, splitErr := assets.Split(request.Kind, request.ID, request.Ext, data)
		if splitErr != nil {
			return
		}
		for _, chunk := range chunks {
			if len(request.Missing) > 0 && !containsIndex(request.Missing, chunk.Index) {
				continue
			}
			// Con ritmo (SendBulk): mandarlo de golpe le quitaba la subida a la voz.
			if !udp.SendBulk(addr, transport.Packet{Kind: transport.PacketAssetChunk, Sequence: a.sequence.Add(1), Payload: assets.Encode(chunk)}) {
				return
			}
		}
	}()
}

func containsIndex(indices []uint32, index uint32) bool {
	for _, value := range indices {
		if value == index {
			return true
		}
	}
	return false
}

func (a *App) sendAsset(kind byte, id, ext string, data []byte) {
	chunks, err := assets.Split(kind, id, ext, data)
	if err != nil {
		a.setStatus("No se pudo preparar el archivo: " + err.Error())
		return
	}
	if !a.connected() {
		return
	}
	// Con ritmo (SendBulk, ~2 Mbps en total): un sonido de 8 MB de golpe saturaba la subida y la
	// voz llegaba tarde mientras tanto.
	go func() {
		for _, chunk := range chunks {
			udp := a.transport.Load()
			if udp == nil {
				return
			}
			packet := transport.Packet{Kind: transport.PacketAssetChunk, Sequence: a.sequence.Add(1), Payload: assets.Encode(chunk)}
			sent := false
			if a.roomState.HostID == a.participantID {
				sent = udp.BroadcastBulk(packet, nil)
			} else if peer := a.endpointAddr(); peer != nil {
				sent = udp.SendBulk(peer, packet)
			}
			if !sent {
				return
			}
		}
	}()
}

func (a *App) sweepAssetLoop(udp *transport.UDP, stop <-chan struct{}) {
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.sweepSpeakingPeers()
			if a.assetAssembler == nil {
				continue
			}
			for _, notice := range a.assetAssembler.Sweep(600*time.Millisecond, 32) {
				addr, resolveErr := net.ResolveUDPAddr("udp", notice.Origin)
				if resolveErr != nil {
					continue
				}
				_ = udp.Send(addr, transport.Packet{Kind: transport.PacketAssetMissing, Sequence: a.sequence.Add(1), Payload: assets.EncodeMissing(notice.Request)})
			}
		}
	}
}

func (a *App) sendHistorySync(udp *transport.UDP, address *net.UDPAddr) {
	if a.historyStore == nil || address == nil {
		return
	}
	payload, err := room.Encode(room.Envelope{Kind: room.SyncRequestMessage, KnownIDs: a.historyStore.KnownIDs()})
	if err == nil {
		_ = udp.Send(address, transport.Packet{Kind: transport.PacketChatSync, Sequence: a.sequence.Add(1), Payload: payload})
	}
}

func (a *App) respondHistorySync(udp *transport.UDP, address *net.UDPAddr, knownIDs []string) {
	if a.historyStore == nil || address == nil {
		return
	}
	missing, err := a.historyStore.MessagesMissingFrom(knownIDs)
	if err != nil {
		return
	}
	payload, err := room.Encode(room.Envelope{Kind: room.SyncResponseMessage, Messages: missing})
	if err == nil {
		_ = udp.Send(address, transport.Packet{Kind: transport.PacketChatSync, Sequence: a.sequence.Add(1), Payload: payload})
	}
}

func (a *App) handleSyncPacket(udp *transport.UDP, addr *net.UDPAddr, payload []byte, host bool) {
	envelope, err := room.Decode(payload)
	if err != nil {
		return
	}
	// Botonera (sound_sync.go): toca disco, fuera de receiveLoop.
	switch envelope.Kind {
	case room.SoundSyncRequestMessage:
		if host {
			go a.handleSoundSyncRequest(udp, addr, envelope)
		}
		return
	case room.SoundSyncResponseMessage:
		if !host {
			go a.handleSoundSyncResponse(envelope)
		}
		return
	}
	if envelope.Kind == room.SyncRequestMessage {
		if host {
			a.respondHistorySync(udp, addr, envelope.KnownIDs)
			envelope.ReplyTo = addr.String()
			forwarded, encodeErr := room.Encode(envelope)
			if encodeErr == nil {
				udp.Broadcast(transport.Packet{Kind: transport.PacketChatSync, Sequence: a.sequence.Add(1), Payload: forwarded}, addr)
			}
			return
		}
		if envelope.ReplyTo == "" {
			return
		}
		reply, resolveErr := net.ResolveUDPAddr("udp", envelope.ReplyTo)
		if resolveErr == nil {
			a.respondHistorySync(udp, reply, envelope.KnownIDs)
		}
		return
	}
	if envelope.Kind == room.SyncResponseMessage {
		for _, message := range envelope.Messages {
			a.addChatMessage(message)
			if message.ImageID == "" || a.historyStore == nil || !a.netAssetOK(assets.KindImage, message.ImageID, message.ImageExt, addr.String()) {
				continue
			}
			if _, readErr := a.historyStore.ReadImage(message.ImageID, message.ImageExt); readErr == nil {
				continue
			}
			request := assets.MissingRequest{Kind: assets.KindImage, ID: message.ImageID, Ext: message.ImageExt}
			_ = udp.Send(addr, transport.Packet{Kind: transport.PacketAssetMissing, Sequence: a.sequence.Add(1), Payload: assets.EncodeMissing(request)})
		}
	}
}

func (a *App) sendChatMessage(text string) {
	text = strings.TrimSpace(text)
	if text == "" || !a.connected() {
		return
	}
	message := history.Message{ID: history.NewID(a.participantID), Timestamp: time.Now().UTC(), SenderID: a.participantID, Username: a.conf().Username, Kind: "text", Text: text}
	a.addChatMessage(message)
	a.playEventSound(audio.SFXMessage)
	payload, err := room.Encode(room.Envelope{Kind: room.ChatMessage, Message: &message})
	if err != nil {
		return
	}
	if a.chatInput != nil {
		a.chatInput.SetText("")
	}
	a.sendChatPayload(payload)
}

func (a *App) sendChatPayload(payload []byte) {
	udp := a.transport.Load()
	if udp == nil {
		return
	}
	if a.roomState.HostID == a.participantID {
		udp.Broadcast(transport.Packet{Kind: transport.PacketChat, Sequence: a.sequence.Add(1), Payload: payload}, nil)
		return
	}
	if peer := a.endpointAddr(); peer != nil {
		_ = udp.Send(peer, transport.Packet{Kind: transport.PacketChat, Sequence: a.sequence.Add(1), Payload: payload})
	}
}
