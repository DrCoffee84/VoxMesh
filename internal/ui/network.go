//go:build cgo

package ui

import (
	"encoding/binary"
	"net"
	"strings"
	"time"

	"voxmesh/internal/audio"
	"voxmesh/internal/config"
	"voxmesh/internal/logging"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

func (a *App) receiveLoop(udp *transport.UDP) {
	for {
		packet, addr, err := udp.Receive()
		if err != nil {
			logging.Infof("bucle UDP detenido: %s", logging.FormatError(err))
			return
		}
		logging.Tracef("Tráfico UDP recibido: tipo=%d, origen=%s, tamaño=%d bytes", packet.Kind, addr.String(), len(packet.Payload))
		if a.hostMode.Load() {
			udp.AddPeer(addr)
			for index := range a.roomState.Participants {
				if a.roomState.Participants[index].Address == addr.String() {
					a.roomState.Participants[index].LastSeenUTC = time.Now().UTC()
					break
				}
			}
			if packet.Kind == transport.PacketRoomHello {
				logging.Infof("Procesando PacketRoomHello de %s", addr.String())
				envelope, err := room.Decode(packet.Payload)
				if err != nil || envelope.Participant == nil {
					continue
				}
				envelope.Participant.Address = addr.String()
				envelope.Participant.Connected = true
				envelope.Participant.LastSeenUTC = time.Now().UTC()

				// Asegurar nombre único: si ya existe otro participante conectado con el mismo nombre, añadir _PUTO
				candidateName := strings.TrimSpace(envelope.Participant.Username)
				if candidateName == "" {
					candidateName = "Usuario"
				}
				for {
					taken := false
					for _, p := range a.roomState.Participants {
						if p.Connected && p.ID != envelope.Participant.ID && strings.EqualFold(strings.TrimSpace(p.Username), candidateName) {
							taken = true
							break
						}
					}
					if !taken {
						break
					}
					candidateName += "_PUTO"
				}
				envelope.Participant.Username = candidateName

				a.roomState.Participants = append([]room.Participant(nil), a.roomState.Participants...)
				if a.roomStore != nil {
					a.roomStore.Replace(a.roomState)
				}
				known := false
				for index := range a.roomState.Participants {
					if a.roomState.Participants[index].ID == envelope.Participant.ID {
						a.roomState.Participants[index] = *envelope.Participant
						known = true
						break
					}
				}
				if !known {
					envelope.Participant.Order = len(a.roomState.Participants)
					a.roomState.Participants = append(a.roomState.Participants, *envelope.Participant)
				}
				statePayload, _ := room.Encode(room.Envelope{Kind: room.StateMessage, Room: &a.roomState})
				_ = udp.Send(addr, transport.Packet{Kind: transport.PacketRoomState, Sequence: a.sequence.Add(1), Payload: statePayload})
				a.refreshPeersFromRoom()
				if candidate, ok := a.roomState.NextHost(); ok {
					if hostIP, _, err := net.SplitHostPort(candidate.Address); err == nil && hostIP != "" {
						candidateTarget := net.JoinHostPort(hostIP, "47830")
						a.cfg.LastPeer = candidateTarget
						_ = config.Save(a.cfgPath, a.cfg)
						if a.endpointEntry != nil {
							a.endpointEntry.SetText(candidateTarget)
						}
					}
				}
				logging.Infof("Cliente conectado: %s (%s)", envelope.Participant.Username, addr.String())
				joinedUser := envelope.Participant.Username
				go func(uName string) {
					if a.roomStore != nil {
						a.roomStore.Replace(a.roomState)
					}
					a.addSystemMessage(uName + " se conectó.")
				}(joinedUser)
				a.playEventSound(audio.SFXConnect)
				udp.Broadcast(transport.Packet{Kind: transport.PacketRoomState, Sequence: a.sequence.Add(1), Payload: statePayload}, addr)
			} else if packet.Kind == transport.PacketRoomLeave {
				envelope, err := room.Decode(packet.Payload)
				username := ""
				participantID := ""
				if err == nil && envelope.Participant != nil {
					username = envelope.Participant.Username
					participantID = envelope.Participant.ID
				}
				a.handleParticipantLeave(participantID, username, addr, udp)
			} else if packet.Kind == transport.PacketHello {
				username := strings.TrimSpace(string(packet.Payload))
				if username == "" {
					username = "Usuario"
				}
				candidate := username
				a.peerMu.Lock()
				for {
					taken := false
					for _, p := range a.peerNames {
						if p.address != addr.String() && strings.EqualFold(p.name, candidate) {
							taken = true
							break
						}
					}
					if !taken && strings.EqualFold(a.cfg.Username, candidate) {
						taken = true
					}
					if !taken {
						break
					}
					candidate += "_PUTO"
				}
				a.peerMu.Unlock()
				if a.addPeer(candidate, addr.String()) {
					a.playEventSound(audio.SFXConnect)
				}
			}
			if packet.Kind == transport.PacketPing && len(packet.Payload) == 8 {
				_ = udp.Send(addr, transport.Packet{Kind: transport.PacketPong, Sequence: packet.Sequence, Payload: packet.Payload})
			}
			if packet.Kind == transport.PacketPong && len(packet.Payload) == 8 {
				timestamp := int64(binary.BigEndian.Uint64(packet.Payload))
				a.updatePeerLag(addr.String(), time.Since(time.Unix(0, timestamp)))
			}
			if packet.Kind == transport.PacketAudio {
				a.handleAudioPacket(packet, addr)
				udp.Broadcast(packet, addr)
			}
			if packet.Kind == transport.PacketChat {
				a.handleChatPacket(packet.Payload)
				udp.Broadcast(packet, addr)
			}
			if packet.Kind == transport.PacketChatSync {
				a.handleSyncPacket(udp, addr, packet.Payload, true)
			}
			if packet.Kind == transport.PacketAssetChunk {
				a.handleAssetChunk(packet.Payload, addr.String())
				udp.Broadcast(packet, addr)
			}
			if packet.Kind == transport.PacketAssetMissing {
				a.handleAssetMissing(udp, addr, packet.Payload)
			}
		} else if packet.Kind == transport.PacketPing {
			a.lastHostSeen.Store(time.Now().UnixNano())
			_ = udp.Send(addr, transport.Packet{Kind: transport.PacketPong, Sequence: packet.Sequence, Payload: packet.Payload})
		} else if packet.Kind == transport.PacketPong && len(packet.Payload) == 8 {
			timestamp := int64(binary.BigEndian.Uint64(packet.Payload))
			a.updatePeerLag(addr.String(), time.Since(time.Unix(0, timestamp)))
		} else if packet.Kind == transport.PacketRoomState {
			logging.Infof("Procesando PacketRoomState de %s", addr.String())
			envelope, err := room.Decode(packet.Payload)
			if err == nil && envelope.Room != nil {
				a.handleRoomState(*envelope.Room, udp)
			}
		} else if packet.Kind == transport.PacketRoomLeave {
			envelope, err := room.Decode(packet.Payload)
			username := "El host"
			if err == nil && envelope.Participant != nil && envelope.Participant.Username != "" {
				username = envelope.Participant.Username
			}
			logging.Infof("Notificación de desconexión recibida: %s", username)
			go a.addSystemMessage(username + " se desconectó.")
			a.playEventSound(audio.SFXDisconnect)
			if envelope.Participant != nil {
				a.roomState.MarkDisconnected(envelope.Participant.ID)
				a.refreshPeersFromRoom()
			}
		} else if packet.Kind == transport.PacketChat {
			a.handleChatPacket(packet.Payload)
		} else if packet.Kind == transport.PacketChatSync {
			a.handleSyncPacket(udp, addr, packet.Payload, false)
		} else if packet.Kind == transport.PacketAssetChunk {
			a.handleAssetChunk(packet.Payload, addr.String())
		} else if packet.Kind == transport.PacketAssetMissing {
			a.handleAssetMissing(udp, addr, packet.Payload)
		} else if packet.Kind == transport.PacketHello {
			a.setStatus("Conectado a " + addr.String())
		} else if packet.Kind == transport.PacketAudio {
			a.handleAudioPacket(packet, addr)
		}
	}
}

func (a *App) handleParticipantLeave(participantID, username string, addr *net.UDPAddr, udp *transport.UDP) {
	if addr != nil {
		udp.RemovePeer(addr)
	}
	found := false
	for index := range a.roomState.Participants {
		p := &a.roomState.Participants[index]
		if (participantID != "" && p.ID == participantID) || (addr != nil && p.Address == addr.String()) {
			if !p.Connected {
				return
			}
			p.Connected = false
			p.LastSeenUTC = time.Now().UTC()
			if username == "" {
				username = p.Username
			}
			found = true
			break
		}
	}
	if !found && username == "" {
		if addr != nil {
			username = addr.String()
		} else {
			username = "Usuario"
		}
	}
	addrStr := ""
	if addr != nil {
		addrStr = addr.String()
	}
	logging.Infof("Cliente desconectado: %s (%s)", username, addrStr)
	a.refreshPeersFromRoom()
	leftName := username
	go func(uName string) {
		a.addSystemMessage(uName + " se desconectó.")
		if a.roomStore != nil {
			a.roomStore.Replace(a.roomState)
		}
	}(leftName)
	a.playEventSound(audio.SFXDisconnect)
	statePayload, _ := room.Encode(room.Envelope{Kind: room.StateMessage, Room: &a.roomState})
	udp.Broadcast(transport.Packet{Kind: transport.PacketRoomState, Sequence: a.sequence.Add(1), Payload: statePayload}, addr)
}

func (a *App) handleRoomState(state room.State, udp *transport.UDP) {
	previousHost := a.roomState.HostID
	for _, p := range state.Participants {
		if p.ID == a.participantID {
			if p.Username != "" && p.Username != a.cfg.Username {
				oldName := a.cfg.Username
				a.cfg.Username = p.Username
				_ = config.Save(a.cfgPath, a.cfg)
				if a.selfNameLabel != nil {
					a.selfNameLabel.SetText(p.Username)
				}
				a.setStatus("Nombre en uso. Tu nuevo nombre es: " + p.Username)
				a.addSystemMessage("Tu nombre de usuario se cambió a " + p.Username + " porque ya existía otro en la sala.")
				logging.Infof("Nombre cambiado por el host de '%s' a '%s' por duplicado", oldName, p.Username)
			}
			continue
		}
		if !p.Connected {
			continue
		}
		wasConnected := false
		for _, prev := range a.roomState.Participants {
			if prev.ID == p.ID && prev.Connected {
				wasConnected = true
				break
			}
		}
		if !wasConnected && len(a.roomState.Participants) > 1 {
			a.playEventSound(audio.SFXConnect)
		}
	}
	a.roomState = state
	a.refreshPeersFromRoom()
	if state.Name != "" && state.Name != a.activeRoomName {
		a.activeRoomName = state.Name
		a.cfg.RoomName = state.Name
		a.initHistory(state.Name)
	}
	if state.HostID == a.participantID || a.hostMode.Load() {
		return
	}
	if previousHost != "" && state.HostID != previousHost {
		for _, participant := range state.Participants {
			if participant.ID == state.HostID {
				address, err := net.ResolveUDPAddr("udp", participant.Address)
				if err == nil && address.IP != nil && !address.IP.IsUnspecified() {
					a.endpoint = address.String()
					a.cfg.LastPeer = a.endpoint
					_ = config.Save(a.cfgPath, a.cfg)
					if a.endpointEntry != nil {
						a.endpointEntry.SetText(a.endpoint)
					}
					if a.audioEngine != nil {
						a.audioEngine.Stop()
					}
					a.startAudio(udp, address, false)
					a.sendRoomHello(udp, address)
					a.sendHistorySync(udp, address)
				}
				break
			}
		}
	}
}

func (a *App) sendRoomHello(udp *transport.UDP, address *net.UDPAddr) {
	participant := room.Participant{
		ID:          a.participantID,
		Username:    a.cfg.Username,
		Address:     udp.Address().String(),
		Connected:   true,
		CanBeHost:   a.cfg.AllowHostMigration,
		LastSeenUTC: time.Now().UTC(),
	}
	payload, _ := room.Encode(room.Envelope{Kind: room.HelloMessage, Room: &a.roomState, Participant: &participant})
	_ = udp.Send(address, transport.Packet{Kind: transport.PacketRoomHello, Sequence: a.sequence.Add(1), Payload: payload})
	a.sendHistorySync(udp, address)
}

func (a *App) monitorHost(udp *transport.UDP, stop <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var failedHost string
	var candidateID string
	var candidateDeadline time.Time
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if !a.hostMode.Load() && a.endpoint != "" {
				if hostAddr, err := net.ResolveUDPAddr("udp", a.endpoint); err == nil {
					payload := make([]byte, 8)
					binary.BigEndian.PutUint64(payload, uint64(time.Now().UnixNano()))
					_ = udp.Send(hostAddr, transport.Packet{Kind: transport.PacketPing, Sequence: a.sequence.Add(1), Payload: payload})
				}
			}
			if a.hostMode.Load() || time.Since(time.Unix(0, a.lastHostSeen.Load())) < 6*time.Second {
				continue
			}
			if failedHost == "" {
				failedHost = a.roomState.HostID
				a.roomState.MarkDisconnected(failedHost)
				a.addSystemMessage("El host se desconectó.")
			}
			candidate, ok := a.roomState.NextHost()
			if !ok {
				return
			}
			if candidate.ID == a.participantID {
				a.becomeHost(udp)
				return
			}
			if candidate.ID != candidateID {
				candidateID = candidate.ID
				candidateDeadline = time.Now().Add(6 * time.Second)
				continue
			}
			if time.Now().After(candidateDeadline) {
				a.addSystemMessage("El candidato " + candidate.Username + " no está disponible.")
				a.roomState.MarkDisconnected(candidate.ID)
				candidateID = ""
			}
		}
	}
}

func (a *App) becomeHost(udp *transport.UDP) {
	logging.Infof("Migración iniciada: asumiendo el rol de host")
	a.hostMode.Store(true)
	a.roomState.HostID = a.participantID
	a.roomState.Epoch++
	for i := range a.roomState.Participants {
		if a.roomState.Participants[i].ID == a.participantID {
			a.roomState.Participants[i].CanBeHost = true
			break
		}
	}
	go a.configureHostNetwork(udp.Address().Port)
	for _, participant := range a.roomState.Participants {
		if participant.ID == a.participantID {
			continue
		}
		address, err := net.ResolveUDPAddr("udp", participant.Address)
		if err == nil {
			udp.AddPeer(address)
		}
	}
	if a.audioEngine != nil {
		a.audioEngine.Stop()
	}
	a.startAudio(udp, nil, true)
	a.addSystemMessage(a.cfg.Username + " será el nuevo host.")
	a.addSystemMessage("Creando nuevamente la sala...")
	payload, _ := room.Encode(room.Envelope{Kind: room.StateMessage, Room: &a.roomState})
	udp.Broadcast(transport.Packet{Kind: transport.PacketRoomState, Sequence: a.sequence.Add(1), Payload: payload}, nil)
	a.addSystemMessage("Sala disponible.")
	a.addSystemMessage("Reconectando participantes...")
	if a.pingStop == nil {
		a.pingStop = make(chan struct{})
	}
	go a.pingPeers(udp, a.pingStop)
	go a.sweepAssetLoop(udp, a.pingStop)
}

func (a *App) pingPeers(udp *transport.UDP, stop <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			now := time.Now().UTC()
			for _, peer := range udp.Peers() {
				payload := make([]byte, 8)
				binary.BigEndian.PutUint64(payload, uint64(time.Now().UnixNano()))
				_ = udp.Send(peer, transport.Packet{Kind: transport.PacketPing, Sequence: a.sequence.Add(1), Payload: payload})
			}
			for index := range a.roomState.Participants {
				p := &a.roomState.Participants[index]
				if p.ID == a.participantID || !p.Connected {
					continue
				}
				if !p.LastSeenUTC.IsZero() && now.Sub(p.LastSeenUTC) > 8*time.Second {
					peerAddr, _ := net.ResolveUDPAddr("udp", p.Address)
					a.handleParticipantLeave(p.ID, p.Username, peerAddr, udp)
				}
			}
		}
	}
}
