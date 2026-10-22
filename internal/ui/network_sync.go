//go:build cgo

package ui

import (
	"net"
	"strings"
	"time"

	"voxmesh/internal/assets"
	"voxmesh/internal/audio"
	"voxmesh/internal/history"
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
		if envelope.Sound != nil {
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
		if envelope.Sound != nil {
			a.playIncomingSound(*envelope.Sound)
		}
	}
}

func (a *App) handleAssetChunk(payload []byte, origin string) {
	chunk, err := assets.Decode(payload)
	if err != nil || a.assetAssembler == nil {
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
		if _, saveErr := a.historyStore.SaveImage(chunk.ID, chunk.Ext, data); saveErr != nil {
			logging.Errorf("guardar archivo recibido %q: %s", chunk.ID, logging.FormatError(saveErr))
			return
		}
		a.refreshChat()
	}()
}

func (a *App) handleAssetMissing(udp *transport.UDP, addr *net.UDPAddr, payload []byte) {
	request, err := assets.DecodeMissing(payload)
	if err != nil || a.historyStore == nil || addr == nil {
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
			_ = udp.Send(addr, transport.Packet{Kind: transport.PacketAssetChunk, Sequence: a.sequence.Add(1), Payload: assets.Encode(chunk)})
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
	go func() {
		for i, chunk := range chunks {
			udp := a.transport.Load()
			if udp == nil {
				return
			}
			packet := transport.Packet{Kind: transport.PacketAssetChunk, Sequence: a.sequence.Add(1), Payload: assets.Encode(chunk)}
			if a.roomState.HostID == a.participantID {
				udp.Broadcast(packet, nil)
			} else if peer := a.endpointAddr(); peer != nil {
				_ = udp.Send(peer, packet)
			}
			if i%4 == 3 {
				time.Sleep(2 * time.Millisecond)
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
			if message.ImageID == "" || a.historyStore == nil {
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
