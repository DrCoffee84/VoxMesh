//go:build cgo

package ui

import (
	"context"
	"fmt"
	"net"
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
	"voxmesh/internal/invite"
	"voxmesh/internal/logging"
	"voxmesh/internal/netinfo"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

func (a *App) stopConnection() {
	a.stopConnectionAsync()
}

// stopConnectionAsync devuelve un canal que se cierra cuando terminó la limpieza en segundo plano
// (Leave a los peers, UPnP, cierre del socket). Al salir de la app se espera ese canal: si el
// proceso termina antes, los peers nunca reciben el Leave.
func (a *App) stopConnectionAsync() <-chan struct{} {
	done := make(chan struct{})
	// Swap: solo un llamador se queda con el transporte (doble clic en "Salir" + cierre de
	// ventana no cierran dos veces). Se suelta antes que el Engine, ver publishEngine.
	udp := a.transport.Swap(nil)
	if udp == nil {
		a.setStatus("No hay una sala o conexión activa.")
		close(done)
		return done
	}
	// Ya mismo: la migración de host y el resto de las goroutines de la conexión no pueden actuar
	// sobre una sala que se cerró (antes el monitor del cliente seguía y a los segundos se nombraba
	// host de una sala fantasma).
	a.connLife.stop()
	start := time.Now()
	oldEndpoint := a.endpointText()
	logging.Infof("stopConnection: cerrando sala/conexión en %s", oldEndpoint)
	a.setEndpoint("")
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
		if rejoin := a.rejoinInvite.Load(); a.endpointEntry != nil && rejoin != nil {
			a.endpointEntry.SetText(*rejoin)
		}
	})
	a.setStatus("Sala detenida.")

	go func() {
		defer close(done)
		a.playEventSound(audio.SFXDisconnect)
		// La regla del router primero: es un pedido rápido y al cerrar la app quit no espera
		// más de 3 s (stopEngine solo ya tarda ~2 s).
		a.releaseHostPort()
		time.Sleep(150 * time.Millisecond)
		a.stopEngine()
		a.dropSoundPlayer()
		a.hostRole.stop()
		username := a.conf().Username
		participant := room.Participant{ID: a.participantID, Username: username, Address: udp.Address().String()}
		payload, _ := room.Encode(room.Envelope{Kind: room.LeaveMessage, Participant: &participant})
		leave := transport.Packet{Kind: transport.PacketRoomLeave, Sequence: a.sequence.Add(1), Payload: payload}
		if !a.hostMode.Load() && oldEndpoint != "" {
			if peer, err := net.ResolveUDPAddr("udp", oldEndpoint); err == nil {
				sendRepeated(func() { _ = udp.Send(peer, leave) })
			}
		} else if a.hostMode.Load() {
			// Los clientes migran ya, sin esperar el período de gracia.
			sendRepeated(func() { udp.Broadcast(leave, nil) })
		}
		if err := udp.Close(); err != nil {
			logging.Errorf("cerrar UDP: %s", logging.FormatError(err))
		}
		logging.Infof("stopConnection: recursos liberados para %s (tiempo de ejecución: %v)", oldEndpoint, time.Since(start))
	}()
	return done
}

// El RoomLeave sale varias veces: es un solo paquete UDP y, si se pierde, los demás recién se
// enteran al vencer el período de gracia.
const (
	leaveRepeats = 3
	leaveSpacing = 50 * time.Millisecond
)

func sendRepeated(send func()) {
	for i := range leaveRepeats {
		if i > 0 {
			time.Sleep(leaveSpacing)
		}
		send()
	}
}

func (a *App) createHost() {
	root, _ := config.AppDir()
	existingRooms, _ := history.ListRooms(root)

	var d dialog.Dialog

	newRoomEntry := widget.NewEntry()
	newRoomEntry.SetPlaceHolder("Ej: Charla, Gaming...")
	newRoomEntry.SetText(a.conf().RoomName)

	btnCreate := widget.NewButtonWithIcon("Crear y abrir", theme.ContentAddIcon(), func() {
		roomName := history.ClampRoomName(newRoomEntry.Text)
		if roomName == "" {
			a.setStatus("Escribe un nombre para la nueva sala.")
			return
		}
		d.Hide()
		username := strings.TrimSpace(a.conf().Username)
		if username == "" {
			username = config.DefaultUsername
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
		if savedRoom := a.conf().RoomName; savedRoom != "" {
			for _, r := range existingRooms {
				if r == savedRoom {
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
			username := strings.TrimSpace(a.conf().Username)
			if username == "" {
				username = config.DefaultUsername
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
	if a.connected() {
		a.setStatus("La sala ya está activa en " + a.endpointText())
		return
	}

	a.setStatus("Iniciando sala, por favor espera...")

	go func() {
		start := time.Now()
		listenAddr := a.conf().ListenAddress
		if listenAddr == "" {
			listenAddr = config.DefaultListenAddress
		}
		// Clave nueva en cada "Iniciar sala", solo en memoria: la invitación vieja deja de servir.
		key, err := transport.NewKey()
		if err != nil {
			a.setStatus("No se pudo generar la clave de la sala: " + err.Error())
			return
		}
		var udp *transport.UDP
		for attempt := 0; attempt < 3; attempt++ {
			udp, err = transport.Listen(listenAddr, key)
			if err == nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if err != nil {
			a.setStatus(fmt.Sprintf("No se pudo abrir el puerto %s: %s (comprueba que no haya otra copia abierta)", listenAddr, err.Error()))
			return
		}

		// CAS: el chequeo de connected() de arriba y este punto no son atómicos; si otro
		// openHost/connectClient ganó en el medio, este socket sobra.
		if !a.transport.CompareAndSwap(nil, udp) {
			_ = udp.Close()
			a.setStatus("Ya existe una sala o conexión activa.")
			return
		}
		a.hostMode.Store(true)
		a.rejoinInvite.Store(nil)
		cfg := a.updateConfig(func(c *config.Config) {
			c.RoomName = roomName
			c.Username = username
		})

		a.participantID = room.ParticipantID(username, udp.Address().String())
		state := room.New(roomName, username, udp.Address())
		if len(state.Participants) > 0 {
			state.Participants[0].CanBeHost = cfg.AllowHostMigration
			state.Participants[0].Muted = a.isMuted.Load()
		}
		a.roomState = state
		a.roomStore = room.NewStore(state)

		// Operaciones de disco
		a.initHistory(roomName)
		a.refreshPeersFromRoom()

		connStop := a.connLife.start()
		hostStop := a.hostRole.start()
		failover := room.NewFailover(a.participantID, room.DefaultFailoverTimings())

		port := udp.Address().Port
		localIP, err := netinfo.LocalIPv4()
		if err == nil && localIP != nil {
			a.setEndpoint(net.JoinHostPort(localIP.String(), strconv.Itoa(port)))
		} else {
			a.setEndpoint(net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		}
		for i := range a.roomState.Participants {
			if a.roomState.Participants[i].ID == a.participantID {
				a.roomState.Participants[i].Address = a.endpointText()
				break
			}
		}

		a.setStatus("Sala abierta en UDP " + strconv.Itoa(port) + "; buscando IP pública...")

		failover.Hosting(a.roomState, time.Now())
		go a.receiveLoop(udp, failover)
		go a.failoverLoop(udp, failover, connStop)
		go a.pingPeers(udp, hostStop)
		go a.sweepAssetLoop(udp, connStop)

		// Inicialización del hardware de audio (Suele ser lo que más traba)
		go a.startAudio(udp)
		go a.configureHostNetwork(port, hostStop)
		a.playEventSound(audio.SFXConnect)
		logging.Infof("openHost: sala %q iniciada en %s (tiempo de ejecución: %v)", roomName, a.endpointText(), time.Since(start))

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

// UPnP: lease de 8 h, más que cualquier llamada, y sin renovar (este router rechaza volver a
// agregar una regla que ya existe: la renovación fallaba y el puerto se cerraba a la hora). La
// regla se borra al cerrar la sala o la app; si quedó de un cierre forzado, vence sola.
const (
	upnpLeaseSeconds = 8 * 3600
	upnpPortAttempts = 6 // puerto del socket y los 5 siguientes como puerto externo
)

// configureHostNetwork abre el puerto en el router (UPnP) y publica la dirección pública de la
// sala. El socket escucha en listenPort: si el router no acepta ese puerto afuera (otra regla lo
// ocupa), se prueba otro puerto externo que apunte igual a listenPort y se anuncia ese. Antes se
// probaba listenPort+1 → listenPort+1, donde nadie escuchaba, y la sala anunciaba un puerto
// muerto. El mapeo se renueva hasta que se cierre la sala (stop).
func (a *App) configureHostNetwork(listenPort int, stop <-chan struct{}) {
	start := time.Now()
	candidates := make([]uint16, 0, upnpPortAttempts)
	for offset := range upnpPortAttempts {
		candidates = append(candidates, uint16(listenPort+offset))
	}
	mapContext, cancelMap := context.WithTimeout(context.Background(), 15*time.Second)
	mapping, mapErr := netinfo.MapUDP(mapContext, candidates, uint16(listenPort), upnpLeaseSeconds)
	cancelMap()
	publicPort := listenPort
	if mapErr != nil {
		logging.Errorf("UPnP UDP puerto %d: %s", listenPort, logging.FormatError(mapErr))
	} else {
		select {
		case <-stop:
			// La sala se cerró mientras el router respondía: stopConnectionAsync no la vio.
			unmapHostPort(mapping)
			return
		default:
		}
		publicPort = int(mapping.External)
		a.upnpMapping.Store(mapping)
		logging.Infof("UPnP UDP activo: puerto externo %d → %d (tiempo: %v)", mapping.External, listenPort, time.Since(start))
	}

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

	endpoint := net.JoinHostPort(ip.String(), strconv.Itoa(publicPort))
	a.setEndpoint(endpoint)
	for i := range a.roomState.Participants {
		if a.roomState.Participants[i].ID == a.participantID {
			a.roomState.Participants[i].Address = endpoint
			break
		}
	}
	// Que todos tengan la dirección alcanzable del host (la que usan si después hay que migrar).
	if udp := a.transport.Load(); udp != nil && a.hostMode.Load() {
		a.broadcastRoomState(udp, nil)
	} else if a.roomStore != nil {
		a.roomStore.Replace(a.roomState)
	}
	logging.Infof("IP pública obtenida: %s en %s", ip.String(), endpoint)

	if mapErr != nil {
		a.setStatus(fmt.Sprintf("Comparte: %s (UPnP no funcionó: abrí a mano el puerto UDP %d del router hacia esta PC)", endpoint, listenPort))
		return
	}
	a.setStatus("Sala lista (UPnP): " + endpoint)
	a.publishRoomCloudFlare()
}

// releaseHostPort borra la regla UPnP de la sala, si hay (al cerrar la sala o al dejar de ser host).
func (a *App) releaseHostPort() {
	if mapping := a.upnpMapping.Swap(nil); mapping != nil {
		unmapHostPort(mapping)
	}
}

func unmapHostPort(mapping *netinfo.Mapping) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := mapping.Unmap(ctx); err != nil {
		logging.Errorf("borrar UPnP del puerto %d: %s", mapping.External, logging.FormatError(err))
		return
	}
	logging.Infof("UPnP UDP cerrado: puerto externo %d", mapping.External)
}

func (a *App) publishRoomCloudFlare() {
	// TODO: Implementación de la publicación en CloudFlare
}

func (a *App) connectClient(raw string) {
	if a.connected() {
		a.setStatus("Ya existe una conexión UDP.")
		return
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		a.setStatus("Pegá la invitación de la sala.")
		return
	}
	code, err := invite.Parse(raw)
	if err != nil {
		a.setStatus("Invitación inválida: " + err.Error())
		return
	}
	raw = invite.Format(code.Addr, code.Key)
	addr := net.UDPAddrFromAddrPort(code.Addr)
	target := addr.String()

	a.setStatus("Conectando con " + target + "...")

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
		listenAddr := a.conf().ListenAddress
		if listenAddr == "" {
			listenAddr = config.DefaultListenAddress
		}
		udp, err := transport.Listen(listenAddr, code.Key)
		if err != nil {
			if listenAddr != ":0" {
				udp, err = transport.Listen(":0", code.Key)
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

		if !a.transport.CompareAndSwap(nil, udp) {
			_ = udp.Close()
			a.setStatus("Ya existe una conexión UDP.")
			return
		}
		a.hostMode.Store(false)
		a.rejoinInvite.Store(&raw)
		cfg := a.updateConfig(func(c *config.Config) {
			c.RoomName = strings.TrimSpace(c.RoomName)
		})
		a.participantID = room.ParticipantID(cfg.Username, udp.Address().String())
		a.roomState = room.State{Name: cfg.RoomName, Participants: []room.Participant{{
			ID:          a.participantID,
			Username:    cfg.Username,
			Address:     udp.Address().String(),
			Order:       0,
			Connected:   true,
			CanBeHost:   cfg.AllowHostMigration,
			Muted:       a.isMuted.Load(),
			LastSeenUTC: time.Now().UTC(),
		}}}

		// Disco + peers (puede tardar)
		a.initHistory(cfg.RoomName)
		a.refreshPeersFromRoom()

		a.setEndpoint(addr.String())
		a.requestSoundSync()
		participant := room.Participant{
			ID:          a.participantID,
			Username:    cfg.Username,
			Address:     udp.Address().String(),
			Connected:   true,
			CanBeHost:   cfg.AllowHostMigration,
			Muted:       a.isMuted.Load(),
			LastSeenUTC: time.Now().UTC(),
		}
		payload, _ := room.Encode(room.Envelope{Kind: room.HelloMessage, Room: &a.roomState, Participant: &participant})
		logging.Infof("Enviando PacketRoomHello a %s: usuario=%q, id=%s", addr.String(), participant.Username, participant.ID)
		_ = udp.Send(addr, transport.Packet{Kind: transport.PacketRoomHello, Sequence: a.sequence.Add(1), Payload: payload})

		a.setStatus("Conectando con " + target)

		// Audio (lo más pesado)
		go a.startAudio(udp)

		// Si el RoomHello se pierde, la migración lo reintenta (el host "se calla").
		failover := room.NewFailover(a.participantID, room.DefaultFailoverTimings())
		failover.JoinedAsClient(addr.String(), time.Now())
		connStop := a.connLife.start()
		go a.receiveLoop(udp, failover)
		go a.failoverLoop(udp, failover, connStop)
		go a.sweepAssetLoop(udp, connStop)

		a.playEventSound(audio.SFXConnect)
		logging.Infof("connectClient: conexión iniciada hacia %s (tiempo de ejecución: %v)", target, time.Since(start))

		// Todo lo visual en fyne.Do
		fyne.Do(func() {
			if a.selfNameLabel != nil {
				a.selfNameLabel.SetText(cfg.Username)
			}
			if a.endpointEntry != nil {
				a.endpointEntry.SetText(raw)
			}
			a.hideConnectionPanels()
		})
	}()
}
