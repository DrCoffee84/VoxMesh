//go:build cgo

package ui

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/audio"
	"voxmesh/internal/config"
	"voxmesh/internal/history"
	"voxmesh/internal/logging"
	"voxmesh/internal/netinfo"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

func (a *App) stopConnection() {
	if a.transport == nil {
		a.setStatus("No hay una sala o conexión activa.")
		return
	}
	start := time.Now()
	udp := a.transport
	oldEndpoint := a.endpoint
	logging.Infof("stopConnection: cerrando sala/conexión en %s", oldEndpoint)
	a.transport = nil
	a.endpoint = ""
	a.activeRoomName = ""
	a.peerMu.Lock()
	a.peerNames = nil
	a.peerMu.Unlock()
	a.refreshPeerAvatars()
	fyne.Do(func() {
		if a.createButton != nil {
			a.createButton.Enable()
		}
		if a.stopButton != nil {
			a.stopButton.Disable()
		}
		if a.topHangupBtn != nil {
			a.topHangupBtn.Disable()
		}
		if a.connectButton != nil {
			a.connectButton.Enable()
		}
		if a.leaveButton != nil {
			a.leaveButton.Disable()
		}
		a.showConnectionPanels()
		if a.endpointEntry != nil && a.cfg.LastPeer != "" {
			a.endpointEntry.SetText(a.cfg.LastPeer)
		}
	})
	a.setStatus("Sala detenida.")

	go func() {
		a.playEventSound(audio.SFXDisconnect)
		time.Sleep(150 * time.Millisecond)
		if a.audioEngine != nil {
			a.audioEngine.Stop()
			a.audioEngine = nil
		}
		a.soundMu.Lock()
		if a.soundPlayer != nil {
			a.soundPlayer.Stop()
			a.soundPlayer = nil
		}
		a.soundMu.Unlock()
		if a.pingStop != nil {
			close(a.pingStop)
			a.pingStop = nil
		}
		if !a.hostMode.Load() && oldEndpoint != "" {
			peer, err := net.ResolveUDPAddr("udp", oldEndpoint)
			if err == nil {
				participant := room.Participant{ID: a.participantID, Username: a.cfg.Username, Address: udp.Address().String()}
				payload, _ := room.Encode(room.Envelope{Kind: room.LeaveMessage, Participant: &participant})
				_ = udp.Send(peer, transport.Packet{Kind: transport.PacketRoomLeave, Sequence: a.sequence.Add(1), Payload: payload})
			}
		} else if a.hostMode.Load() {
			localPort := uint16(udp.Address().Port)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = netinfo.UnmapUDP(ctx, localPort)
			cancel()
			participant := room.Participant{ID: a.participantID, Username: a.cfg.Username, Address: udp.Address().String()}
			payload, _ := room.Encode(room.Envelope{Kind: room.LeaveMessage, Participant: &participant})
			udp.Broadcast(transport.Packet{Kind: transport.PacketRoomLeave, Sequence: a.sequence.Add(1), Payload: payload}, nil)
		}
		if err := udp.Close(); err != nil {
			logging.Errorf("cerrar UDP: %s", logging.FormatError(err))
		}
		logging.Infof("stopConnection: recursos liberados para %s (tiempo de ejecución: %v)", oldEndpoint, time.Since(start))
	}()
}

func (a *App) createHost() {
	root, _ := os.UserConfigDir()
	existingRooms, _ := history.ListRooms(filepath.Join(root, "VoxMesh"))

	var d dialog.Dialog

	newRoomEntry := widget.NewEntry()
	newRoomEntry.SetPlaceHolder("Ej: Charla, Gaming...")
	newRoomEntry.SetText(a.cfg.RoomName)

	btnCreate := widget.NewButtonWithIcon("Crear y abrir", theme.ContentAddIcon(), func() {
		roomName := strings.TrimSpace(newRoomEntry.Text)
		if roomName == "" {
			a.setStatus("Escribe un nombre para la nueva sala.")
			return
		}
		d.Hide()
		username := strings.TrimSpace(a.cfg.Username)
		if username == "" {
			username = "Usuario"
		}
		go a.openHost(roomName, username)
	})
	newRoomEntry.OnSubmitted = func(string) {
		btnCreate.OnTapped()
	}

	var content *fyne.Container

	if len(existingRooms) > 0 {
		existingSelect := widget.NewSelect(existingRooms, nil)
		existingSelect.PlaceHolder = "Seleccionar de la lista..."
		if a.cfg.RoomName != "" {
			for _, r := range existingRooms {
				if r == a.cfg.RoomName {
					existingSelect.SetSelected(r)
					break
				}
			}
		}

		btnOpenExisting := widget.NewButtonWithIcon("Abrir seleccionada", theme.MediaPlayIcon(), func() {
			roomName := strings.TrimSpace(existingSelect.Selected)
			if roomName == "" {
				a.setStatus("Selecciona una sala guardada de la lista.")
				return
			}
			d.Hide()
			username := strings.TrimSpace(a.cfg.Username)
			if username == "" {
				username = "Usuario"
			}
			go a.openHost(roomName, username)
		})

		content = container.NewVBox(
			widget.NewLabelWithStyle("Salas guardadas", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			container.NewBorder(nil, nil, nil, btnOpenExisting, existingSelect),
			widget.NewSeparator(),
			widget.NewLabelWithStyle("Nueva sala", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			container.NewBorder(nil, nil, nil, btnCreate, newRoomEntry),
		)
	} else {
		content = container.NewVBox(
			widget.NewLabelWithStyle("Nueva sala", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			container.NewBorder(nil, nil, nil, btnCreate, newRoomEntry),
		)
	}

	d = dialog.NewCustom("Iniciar Sala", "Cancelar", content, a.window)
	d.Resize(fyne.NewSize(440, 220))
	d.Show()
}

func (a *App) openHost(roomName, username string) {
	if a.transport != nil {
		a.setStatus("La sala ya está activa en " + a.endpoint)
		return
	}

	a.setStatus("Iniciando sala, por favor espera...")

	go func() {
		start := time.Now()
		listenAddr := a.cfg.ListenAddress
		if listenAddr == "" {
			listenAddr = ":47830"
		}
		var udp *transport.UDP
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			udp, err = transport.Listen(listenAddr)
			if err == nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if err != nil {
			a.setStatus(fmt.Sprintf("No se pudo abrir el puerto %s: %s (comprueba que no haya otra copia abierta)", listenAddr, err.Error()))
			return
		}

		a.transport = udp
		a.hostMode.Store(true)
		a.cfg.RoomName = roomName
		a.cfg.Username = username

		a.participantID = room.ParticipantID(username, udp.Address().String())
		state := room.New(roomName, username, udp.Address())
		if len(state.Participants) > 0 {
			state.Participants[0].CanBeHost = a.cfg.AllowHostMigration
		}
		a.roomState = state
		a.roomStore = room.NewStore(state)

		// Operaciones de disco
		a.initHistory(roomName)
		a.refreshPeersFromRoom()

		a.pingStop = make(chan struct{})

		port := udp.Address().Port
		localIP, err := netinfo.LocalIPv4()
		if err == nil && localIP != nil {
			a.endpoint = net.JoinHostPort(localIP.String(), strconv.Itoa(port))
		} else {
			a.endpoint = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		}
		for i := range a.roomState.Participants {
			if a.roomState.Participants[i].ID == a.participantID {
				a.roomState.Participants[i].Address = a.endpoint
				break
			}
		}

		a.setStatus("Sala abierta en UDP " + strconv.Itoa(port) + "; buscando IP pública...")

		go a.receiveLoop(udp)
		go a.pingPeers(udp, a.pingStop)
		go a.sweepAssetLoop(udp, a.pingStop)

		// Inicialización del hardware de audio (Suele ser lo que más traba)
		go a.startAudio(udp, nil, true)
		go a.configureHostNetwork(port)
		a.playEventSound(audio.SFXConnect)
		logging.Infof("openHost: sala %q iniciada en %s (tiempo de ejecución: %v)", roomName, a.endpoint, time.Since(start))

		// Cualquier actualización visual tiene que ir obligatoriamente en fyne.Do
		fyne.Do(func() {
			if a.selfNameLabel != nil {
				a.selfNameLabel.SetText(username)
			}
			if a.createButton != nil {
				a.createButton.Disable()
			}
			if a.stopButton != nil {
				a.stopButton.Enable()
			}
			if a.topHangupBtn != nil {
				a.topHangupBtn.Enable()
			}
			a.hideConnectionPanels()
		})
	}()
}

func (a *App) configureHostNetwork(startPort int) {
	maxRetries := 5
	port := startPort

	for retry := 0; retry <= maxRetries; retry++ {
		start := time.Now()

		// 1. Intentar UPnP
		mapContext, cancelMap := context.WithTimeout(context.Background(), 8*time.Second)
		mapErr := netinfo.TryMapUDP(mapContext, uint16(port), 3600)
		cancelMap()

		if mapErr != nil {
			logging.Errorf("UPnP UDP puerto %d: %s", port, logging.FormatError(mapErr))
		} else {
			logging.Infof("UPnP UDP activo en el puerto %d (tiempo: %v)", port, time.Since(start))
		}

		// 2. Obtener IP Pública
		ipContext, cancelIP := context.WithTimeout(context.Background(), 4*time.Second)
		ip, ipErr := netinfo.PublicIP(ipContext)
		cancelIP()

		if ipErr != nil {
			logging.Errorf("obtener IP pública: %s", logging.FormatError(ipErr))
			if mapErr == nil {
				a.setStatus("UPnP activo. IP pública no disponible; usa la dirección local de la sala.")
				return
			}
			a.setStatus("UDP activo. No se pudo abrir UPnP ni obtener IP pública; revisa tu red.")
			return
		}

		// 3. Guardar estado y actualizar la sala
		a.endpoint = net.JoinHostPort(ip.String(), strconv.Itoa(port))
		for i := range a.roomState.Participants {
			if a.roomState.Participants[i].ID == a.participantID {
				a.roomState.Participants[i].Address = a.endpoint
				break
			}
		}
		if a.roomStore != nil {
			a.roomStore.Replace(a.roomState)
		}

		logging.Infof("IP pública obtenida: %s en %s", ip.String(), a.endpoint)

		// 4. Evaluar éxito o reintento
		if mapErr == nil {
			a.setStatus("Sala lista (UPnP): " + a.endpoint)
			a.publishRoomCloudFlare()
			return
		}

		a.setStatus(fmt.Sprintf("Comparte: %s (UPnP falló en puerto %d)", a.endpoint, port))

		if retry >= maxRetries {
			a.setStatus("No se pudo configurar UPnP después de varios intentos.")
			return
		}

		time.Sleep(1 * time.Second)
		port++
		a.setStatus(fmt.Sprintf("Reintentando red en puerto %d...", port))
	}
}

func (a *App) publishRoomCloudFlare() {
	// TODO: Implementación de la publicación en CloudFlare
}

func (a *App) connectClient(raw string) {
	if a.transport != nil {
		a.setStatus("Ya existe una conexión UDP.")
		return
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		a.setStatus("Ingresá la IP del host.")
		return
	}
	if !strings.Contains(raw, ":") {
		raw = net.JoinHostPort(raw, "47830")
	}

	a.setStatus("Conectando con " + raw + "...")

	// Deshabilitar botones YA en el hilo de UI para feedback inmediato
	if a.connectButton != nil {
		a.connectButton.Disable()
	}
	if a.leaveButton != nil {
		a.leaveButton.Enable()
	}
	if a.topHangupBtn != nil {
		a.topHangupBtn.Enable()
	}

	go func() {
		start := time.Now()
		addr, err := net.ResolveUDPAddr("udp", raw)
		if err != nil {
			a.setStatus("Dirección inválida: " + err.Error())
			fyne.Do(func() {
				if a.connectButton != nil {
					a.connectButton.Enable()
				}
				if a.leaveButton != nil {
					a.leaveButton.Disable()
				}
				if a.topHangupBtn != nil {
					a.topHangupBtn.Disable()
				}
			})
			return
		}

		listenAddr := a.cfg.ListenAddress
		if listenAddr == "" {
			listenAddr = ":47830"
		}
		udp, err := transport.Listen(listenAddr)
		if err != nil {
			if listenAddr != ":0" {
				udp, err = transport.Listen(":0")
			}
			if err != nil {
				a.setStatus("No se pudo abrir UDP: " + err.Error())
				fyne.Do(func() {
					if a.connectButton != nil {
						a.connectButton.Enable()
					}
					if a.leaveButton != nil {
						a.leaveButton.Disable()
					}
					if a.topHangupBtn != nil {
						a.topHangupBtn.Disable()
					}
				})
				return
			}
		}

		a.transport = udp
		a.hostMode.Store(false)
		a.cfg.RoomName = strings.TrimSpace(a.cfg.RoomName)
		a.participantID = room.ParticipantID(a.cfg.Username, udp.Address().String())
		a.roomState = room.State{Name: a.cfg.RoomName, Participants: []room.Participant{{
			ID:          a.participantID,
			Username:    a.cfg.Username,
			Address:     udp.Address().String(),
			Order:       0,
			Connected:   true,
			CanBeHost:   a.cfg.AllowHostMigration,
			LastSeenUTC: time.Now().UTC(),
		}}}

		// Disco + peers (puede tardar)
		a.initHistory(a.cfg.RoomName)
		a.refreshPeersFromRoom()

		a.cfg.LastPeer = raw
		_ = config.Save(a.cfgPath, a.cfg)

		a.endpoint = addr.String()
		participant := room.Participant{
			ID:          a.participantID,
			Username:    a.cfg.Username,
			Address:     udp.Address().String(),
			Connected:   true,
			CanBeHost:   a.cfg.AllowHostMigration,
			LastSeenUTC: time.Now().UTC(),
		}
		payload, _ := room.Encode(room.Envelope{Kind: room.HelloMessage, Room: &a.roomState, Participant: &participant})
		logging.Infof("Enviando PacketRoomHello a %s: usuario=%q, id=%s", addr.String(), participant.Username, participant.ID)
		_ = udp.Send(addr, transport.Packet{Kind: transport.PacketRoomHello, Sequence: a.sequence.Add(1), Payload: payload})

		a.setStatus("Conectando con " + raw)

		// Audio (lo más pesado)
		go a.startAudio(udp, addr, false)
		a.lastHostSeen.Store(time.Now().UnixNano())

		go a.receiveLoop(udp)
		a.monitorStop = make(chan struct{})
		go a.monitorHost(udp, a.monitorStop)
		go a.sweepAssetLoop(udp, a.monitorStop)

		a.playEventSound(audio.SFXConnect)
		logging.Infof("connectClient: conexión iniciada hacia %s (tiempo de ejecución: %v)", raw, time.Since(start))

		// Todo lo visual en fyne.Do
		fyne.Do(func() {
			if a.selfNameLabel != nil {
				a.selfNameLabel.SetText(a.cfg.Username)
			}
			if a.endpointEntry != nil {
				a.endpointEntry.SetText(raw)
			}
			a.hideConnectionPanels()
		})
	}()
}
