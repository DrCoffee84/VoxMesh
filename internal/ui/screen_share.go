//go:build cgo

package ui

import (
	"errors"
	"fmt"
	"image"
	"math"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"voxmesh/internal/i18n"
	"voxmesh/internal/logging"
	"voxmesh/internal/screen"
	"voxmesh/internal/transport"
)

// shareSession es la pantalla propia que se comparte. Solo en memoria: se corta al salir de la
// sala y nunca empieza sola (solo desde el diálogo de "Compartir").
type shareSession struct {
	device   string             // monitor elegido (screen.Monitor.Device); si desaparece, el principal
	maxWidth atomic.Int32       // tope de ancho (screen.Resolution.MaxWidth; 0 = resolución completa)
	repairs  chan screen.Repair // pedidos de los que miran (receiveLoop, sin esperar)
	stop     chan struct{}
	stopOnce sync.Once

	// Entre la goroutine de captura y la del cursor (runScreenCursor): la de captura publica después
	// de cada vuelta el monitor, cuánto se achica, la sesión y a quién mandar (nil = nadie mira); la
	// del cursor publica la última forma que vio.
	monitor     atomic.Pointer[image.Rectangle]
	scale       atomic.Uint64 // math.Float64bits
	session     atomic.Uint32
	targets     atomic.Pointer[[]*net.UDPAddr]
	cursorShape atomic.Pointer[screen.CursorShape]
}

func (s *shareSession) close() { s.stopOnce.Do(func() { close(s.stop) }) }

// startScreenShare empieza a compartir (desde el diálogo, hilo de UI).
func (a *App) startScreenShare(monitor screen.Monitor, resolution screen.Resolution) {
	if !a.connected() {
		return
	}
	session := &shareSession{device: monitor.Device, repairs: make(chan screen.Repair, 32), stop: make(chan struct{})}
	session.maxWidth.Store(int32(resolution.MaxWidth()))
	if !a.screenShare.CompareAndSwap(nil, session) {
		return
	}
	a.showBannerResolution(session, monitor.Rect.Dx(), resolution)
	go func() {
		logging.Guard("pantalla compartida", func() { a.runScreenShare(session) })
		if a.screenShare.Load() == session { // terminó sola (falla o panic): que no quede el banner
			a.stopScreenShare(false)
		}
	}()
	go logging.Guard("cursor de la pantalla compartida", func() { a.runScreenCursor(session) })
	a.refreshShareControls()
	a.refreshPeersFromRoom() // insignia en el avatar propio
	go a.publishScreenIntent()
	a.setStatus(i18n.M("status.screen_started"))
}

// stopScreenShare deja de compartir al instante (la captura corta sin esperar al host) y avisa a la
// sala. Desde cualquier goroutine; con status false no muestra el aviso (al salir de la sala).
func (a *App) stopScreenShare(status bool) {
	session := a.screenShare.Swap(nil)
	if session == nil {
		return
	}
	session.close()
	a.refreshShareControls()
	a.refreshPeersFromRoom()
	go a.publishScreenIntent()
	if status {
		a.setStatus(i18n.M("status.screen_stopped"))
	}
}

// shareLog son los contadores de quien comparte para el log (nunca el contenido).
type shareLog struct {
	start, since time.Time
	watchers     int
	last         screen.SenderStats // al último resumen
	failures     int
	failLog      time.Time
}

// runScreenShare es la goroutine de quien comparte: cada screen.NextCapture (150 ms con cambios,
// 500 ms quieta) mira quién mira y hace una vuelta del Sender (captura, tiles que cambiaron, envío
// con ritmo hasta la vuelta siguiente). Sin nadie mirando no captura ni manda nada. Corre en su propio hilo del sistema
// (screen.OpenCapturer) y nunca toca widgets salvo por refreshShareControls (fyne.Do).
func (a *App) runScreenShare(session *shareSession) {
	capturer, err := screen.OpenCapturer(session.device)
	if err != nil {
		logging.Errorf("Pantalla: no se pudo abrir la captura: %s", logging.FormatError(err))
		a.stopScreenShare(false)
		a.setStatus(i18n.M("status.screen_failed", err.Error()))
		return
	}
	defer capturer.Close()
	peer, _ := screen.ParsePeerID(a.participantID)
	maxWidth := int(session.maxWidth.Load())
	var udp *transport.UDP
	var targets []*net.UDPAddr
	sender := &screen.Sender{
		ID:       peer,
		MaxWidth: maxWidth,
		Emit: func(chunk []byte) bool {
			// El Sender reusa chunk: la cola lo sella más tarde, así que va una copia.
			packet := transport.Packet{Kind: transport.PacketScreenTile, Sequence: a.sequence.Add(1), Payload: slices.Clone(chunk)}
			return udp.SendScreen(targets, packet, session.stop)
		},
		EmitDigest: func(digest []byte) bool {
			packet := transport.Packet{Kind: transport.PacketScreenDigest, Sequence: a.sequence.Add(1), Payload: slices.Clone(digest)}
			return udp.SendScreen(targets, packet, session.stop)
		},
	}
	monitor := capturer.Monitor()
	logging.Infof("Pantalla: empezó a compartir (pantalla %d, %d×%d, %s, DPI del hilo de captura %s)",
		monitor.Number, monitor.Rect.Dx(), monitor.Rect.Dy(), widthLimitText(maxWidth), dpiAwarenessText(capturer.DPIAwareness()))
	now := time.Now()
	stats := shareLog{start: now, since: now}
	defer func() {
		if udp != nil {
			udp.SetScreenRate(transport.ScreenRateBytes) // el host que reenvía usa la cola entera
		}
		total := sender.Stats
		elapsed := time.Since(stats.start)
		logging.Infof("Pantalla: terminó (%s, %s)", elapsed.Round(time.Second), shareStatsText(total, screen.SenderStats{}, elapsed))
	}()

	// Ritmo de captura: rápido mientras la pantalla cambia, lento quieta (screen.NextCapture).
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-session.stop:
			return
		case <-timer.C:
		}
		tick := time.Now()
		if udp = a.transport.Load(); udp == nil {
			timer.Reset(screen.CaptureIdle)
			continue
		}
		state := a.screenSnapshot()
		watchers := state.myWatcherIDs
		if a.hostMode.Load() {
			targets = state.selfTargets // el ritmo de la cola lo fija updateScreenRates
		} else {
			// El host dice cuánto (LAN o internet, repartido entre las copias que reenvía).
			udp.SetScreenRate(a.clientScreenRate(state.copies))
			targets = nil
			if host := a.endpointAddr(); host != nil {
				targets = []*net.UDPAddr{host}
			}
		}
		if len(targets) == 0 {
			watchers = nil // nadie a quien mandarle (el host lo recibe directo si mira: no está en selfTargets)
		}
		if len(watchers) != stats.watchers {
			stats.watchers = len(watchers)
			logging.Infof("Pantalla: la miran %d", len(watchers))
		}
		if shape := session.cursorShape.Load(); shape != nil {
			sender.SetCursorShape(shape) // si cambió, sale en esta vuelta antes que los tiles
		}
		// Lo que pidieron los que miran sale en esta vuelta.
	drain:
		for {
			select {
			case repair := <-session.repairs:
				sender.MarkTiles(repair.Session, repair.Tiles)
			default:
				break drain
			}
		}
		// La resolución se cambia desde el banner: un tamaño nuevo es una sesión nueva con imagen
		// completa (Sender.Step), sin cortar a los que miran.
		if width := int(session.maxWidth.Load()); width != sender.MaxWidth {
			sender.MaxWidth = width
			logging.Infof("Pantalla: resolución: %s", resolutionLogText(width))
		}
		interval := screen.NextCapture(sender.LastChange(), tick)
		if len(watchers) == 0 {
			interval = screen.CaptureIdle // nadie mira: ni se captura
		}
		timer.Reset(interval)
		err := sender.Step(capturer, watchers, tick.Add(interval))
		// Para el cursor: dónde está la pantalla, cuánto se achica y a quién mandarle.
		monitor := capturer.Monitor().Rect
		session.monitor.Store(&monitor)
		session.scale.Store(math.Float64bits(sender.Scale()))
		session.session.Store(sender.Session())
		if len(watchers) > 0 {
			published := targets // copia: targets es de esta goroutine y cambia en la vuelta siguiente
			session.targets.Store(&published)
		} else {
			session.targets.Store(nil)
		}
		switch {
		case errors.Is(err, screen.ErrStopped):
			return
		case err != nil:
			// El escritorio seguro (UAC, bloqueo) no se puede capturar: queda la última imagen.
			stats.failures++
			if time.Since(stats.failLog) >= receiveErrorLogEvery {
				logging.Infof("Pantalla: no se pudo capturar (%d veces): %s", stats.failures, logging.FormatError(err))
				stats.failures, stats.failLog = 0, time.Now()
			}
		}
		if elapsed := time.Since(stats.since); elapsed >= netStatsSummaryInterval {
			logging.Infof("Resumen pantalla (%s): %s", elapsed.Round(time.Second), shareStatsText(sender.Stats, stats.last, elapsed))
			stats.last, stats.since = sender.Stats, time.Now()
		}
	}
}

// runScreenCursor sigue el cursor de quien comparte en su propio hilo (screen.CursorTracker) y
// manda la posición hasta 20 veces por segundo sin cola (chica y urgente: el cursor no espera a los
// tiles), solo si cambió o cada screen.CursorResend. La forma la publica para la goroutine de
// captura, que la manda como un tile especial.
func (a *App) runScreenCursor(session *shareSession) {
	tracker, err := screen.OpenCursorTracker()
	if err != nil {
		logging.Infof("Pantalla: sin cursor aparte: %s", logging.FormatError(err))
		return
	}
	defer tracker.Close()
	peer, _ := screen.ParsePeerID(a.participantID)
	ticker := time.NewTicker(screen.CursorRate)
	defer ticker.Stop()
	var last screen.Cursor
	var lastSent time.Time
	var payload []byte
	for {
		select {
		case <-session.stop:
			return
		case <-ticker.C:
		}
		state := tracker.Poll()
		if state.Shape != nil {
			session.cursorShape.Store(state.Shape)
		}
		targets, monitor := session.targets.Load(), session.monitor.Load()
		udp := a.transport.Load()
		if targets == nil || monitor == nil || udp == nil {
			continue
		}
		x, y, visible := screen.CursorInFrame(state, *monitor, math.Float64frombits(session.scale.Load()))
		cursor := screen.Cursor{Sharer: peer, Session: session.session.Load(), Visible: visible, X: x, Y: y}
		if state.Shape != nil {
			cursor.Shape = state.Shape.ID
		}
		if cursor == last && time.Since(lastSent) < screen.CursorResend {
			continue
		}
		payload = screen.AppendCursor(payload[:0], cursor)
		udp.SendTo(*targets, transport.Packet{Kind: transport.PacketScreenCursor, Sequence: a.sequence.Add(1), Payload: payload})
		last, lastSent = cursor, time.Now()
	}
}

// shareStatsText es la línea de log de lo que salió entre before y now.
func shareStatsText(now, before screen.SenderStats, elapsed time.Duration) string {
	bytes := now.Bytes - before.Bytes
	return fmt.Sprintf("capturas=%d atrasadas=%d tiles=%d imágenes completas=%d reparados=%d enviado=%s (%.0f kbps)",
		now.Captures-before.Captures, now.Behind-before.Behind, now.Tiles-before.Tiles, now.FullImages-before.FullImages,
		now.Repaired-before.Repaired, formatMB(uint64(bytes)), float64(bytes)*8/max(elapsed.Seconds(), 1)/1000)
}

func widthLimitText(maxWidth int) string {
	if maxWidth == 0 {
		return "resolución completa"
	}
	return fmt.Sprintf("hasta %d de ancho", maxWidth)
}

// resolutionLogText es el evento de cambio de resolución para el log ("1280 de ancho").
func resolutionLogText(maxWidth int) string {
	if maxWidth == 0 {
		return "completa"
	}
	return fmt.Sprintf("%d de ancho", maxWidth)
}

func dpiAwarenessText(level int) string {
	switch level {
	case 2:
		return "por monitor"
	case 1:
		return "de sistema"
	case 0:
		return "sin DPI"
	}
	return "desconocido"
}
