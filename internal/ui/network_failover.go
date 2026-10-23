//go:build cgo

package ui

import (
	"context"
	"net"
	"time"

	"fyne.io/fyne/v2"

	"voxmesh/internal/config"
	"voxmesh/internal/logging"
	"voxmesh/internal/netinfo"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

// Migración de host del lado de la app. room.Failover decide; failoverLoop le da el pulso y
// ejecuta lo que decide, de a una cosa por vez y siempre en esta goroutine (los cambios de rol no
// se pisan). Cambiar de rol no toca el audio: sendOutgoingPCM mira hostMode y endpointAddr en cada
// frame, así nadie reabre el micrófono ni pierde lo medido de la red de cada uno.

// failoverTick es el pulso de la migración (la máquina reintenta cada 500 ms).
const failoverTick = 250 * time.Millisecond

// clientResync es cada cuánto el cliente reenvía lo que UDP pudo perder (mute, sync de sonidos).
const clientResync = 2 * time.Second

// failoverLoop vive lo que la conexión (stop). Se detiene también si el socket ya no es el de la
// sala: nada de lo que decida fo se ejecuta sobre una conexión cerrada.
func (a *App) failoverLoop(udp *transport.UDP, fo *room.Failover, stop <-chan struct{}) {
	defer fo.Stop()
	ticker := time.NewTicker(failoverTick)
	defer ticker.Stop()
	var lastResync time.Time
	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			fo.Tick(now)
			if !a.hostMode.Load() && now.Sub(lastResync) >= clientResync {
				lastResync = now
				if !a.selfMuteSynced() {
					a.sendMuteState(udp)
				}
				a.maybeSendSoundSync(udp)
			}
		case <-fo.Wake():
		}
		for _, action := range fo.Drain() {
			if a.transport.Load() != udp {
				return // la sala se cerró (también por un ActGiveUp de este mismo lote)
			}
			a.runFailoverAction(udp, fo, action)
		}
	}
}

func (a *App) runFailoverAction(udp *transport.UDP, fo *room.Failover, action room.Action) {
	if action.Kind == room.ActStatus || action.Kind == room.ActGiveUp {
		// Solo local: addSystemMessage lo difundiría a la sala si soy el host, y el texto es mío.
		logging.Infof("Migración de host: %s", action.Text)
		if action.Kind == room.ActGiveUp {
			a.stopConnection() // antes del aviso: su "Sala detenida." no lo tapa
		}
		a.setStatus(action.Text)
		a.addActivity(action.Text)
		return
	}
	if action.Kind == room.ActBecomeHost {
		a.takeOverHost(udp, fo, action.State)
		return
	}
	address, err := net.ResolveUDPAddr("udp", action.Addr) // "IP:puerto" literal: sin DNS
	if err != nil {
		return
	}
	switch action.Kind {
	case room.ActPing:
		a.sendPing(udp, address)
	case room.ActHello:
		a.sendHelloOnly(udp, address)
	case room.ActSendState:
		a.sendRoomState(udp, address)
	case room.ActSwitchHost:
		a.switchHost(udp, address, action.State)
	case room.ActStepDown:
		a.stepDown(udp, address, action.State)
	}
}

// takeOverHost: el host se fue (o no volvió) y este participante queda de host. state ya trae el
// epoch nuevo y el host viejo desconectado; fo les sigue mandando el estado a los que faltan.
func (a *App) takeOverHost(udp *transport.UDP, fo *room.Failover, state room.State) {
	logging.Infof("Migración: asumiendo el rol de host (epoch %d)", state.Epoch)
	state.SetMuted(a.participantID, "", a.isMuted.Load()) // el mute local, no el último que vio el host viejo
	a.roomState = state
	if a.roomStore == nil {
		a.roomStore = room.NewStore(state)
	}
	a.hostMode.Store(true)
	for _, participant := range state.Participants {
		if participant.ID == a.participantID || !participant.Connected {
			continue
		}
		if address, err := net.ResolveUDPAddr("udp", participant.Address); err == nil {
			udp.AddPeer(address)
		}
	}
	hostStop := a.hostRole.start()
	go a.pingPeers(udp, hostStop)
	go a.configureHostNetwork(udp.Address().Port, hostStop)
	a.refreshPeersFromRoom()
	a.broadcastRoomState(udp, nil)
}

// switchHost (cliente): pasarse al host de address (uno nuevo, o el mismo que respondió desde otra
// dirección), sin cortar el audio.
func (a *App) switchHost(udp *transport.UDP, address *net.UDPAddr, state room.State) {
	endpoint := address.String()
	logging.Infof("Host de la sala en %s (epoch %d)", endpoint, state.Epoch)
	a.setEndpoint(endpoint)
	a.updateConfig(func(c *config.Config) { c.LastPeer = endpoint })
	a.saveConfig()
	fyne.Do(func() {
		if a.endpointEntry != nil {
			a.endpointEntry.SetText(endpoint)
		}
	})
	a.handleRoomState(state, udp)
	a.requestSoundSync()
	a.sendRoomHello(udp, address)
}

// stepDown (host): apareció un host con más autoridad (una migración mientras este estaba
// cortado, o dos hosts que se encuentran). Les pasa la sala nueva a los que siguen acá (su
// Failover los lleva hacia el otro host) y se suma como cliente.
func (a *App) stepDown(udp *transport.UDP, address *net.UDPAddr, state room.State) {
	logging.Infof("Migración: hay un host con más autoridad en %s; pasando a cliente", address)
	if payload, err := room.Encode(room.Envelope{Kind: room.StateMessage, Room: &state}); err == nil {
		udp.Broadcast(transport.Packet{Kind: transport.PacketRoomState, Sequence: a.sequence.Add(1), Payload: payload}, address)
	}
	a.hostMode.Store(false)
	a.hostRole.stop() // pingPeers y la renovación UPnP
	if external := a.upnpPort.Swap(0); external != 0 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = netinfo.UnmapUDP(ctx, uint16(external))
		}()
	}
	for _, peer := range udp.Peers() {
		udp.RemovePeer(peer) // como cliente no reenvía nada a nadie
	}
	a.switchHost(udp, address, state)
}
