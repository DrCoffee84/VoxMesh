//go:build cgo

package ui

import (
	"sync"
	"time"

	"voxmesh/internal/i18n"
	"voxmesh/internal/logging"
)

// Problemas que le importan al usuario, en el registro de actividad (AGENDA C14). Cada uno es un
// tipo con su ayuda: la línea es el mismo texto de la barra de estado (misma clave), si lo hay, y
// después qué hacer. Tope por tipo: la primera vez en la sesión y después como mucho una línea cada
// problemLineCooldown. Solo local (addActivity, nunca addSystemMessage): son datos de esta PC. El
// detalle técnico queda en el log, donde lo deja quien detecta el problema.

type problemKind int

const (
	problemDevice           problemKind = iota + 1 // un dispositivo elegido no está: se usa el predeterminado
	problemAudioDropped                            // se cayó el audio y se reabre
	problemAudioUnavailable                        // no se pudo abrir el audio
	problemPhoneLost                               // el celular se desconectó (en sala, con el celular de entrada)
	problemPhoneServer                             // el servidor del celular se detuvo o no arrancó
	problemSlowMic                                 // el micrófono entrega menos audio que tiempo
	problemBusyPC                                  // la PC tuvo que tirar audio capturado
	problemEchoFailed                              // la cancelación de eco falló y se apagó
	problemScreenFailed                            // no se pudo capturar la pantalla
	problemConfigSave                              // no se pudo guardar la configuración
	problemPortFailed                              // "Iniciar sala" no pudo abrir el puerto UDP
	problemInviteInvalid                           // lo pegado no es una invitación válida
	problemWrongKey                                // host: llegan paquetes de VoxMesh con otra clave
)

// problemLineCooldown es el tope por tipo: después de la primera línea, como mucho una cada tanto.
const problemLineCooldown = 5 * time.Minute

// problemHelp es qué hacer con cada problema, lo que va detrás del texto.
var problemHelp = map[problemKind]string{
	problemDevice:           "audio.device_help",
	problemAudioDropped:     "audio.dropped_help",
	problemAudioUnavailable: "audio.unavailable_help",
	problemPhoneLost:        "phone.disconnected_help",
	problemPhoneServer:      "phone.server_help",
	problemSlowMic:          "audio.slow_mic_help",
	problemBusyPC:           "audio.busy_pc_help",
	problemEchoFailed:       "audio.echo_failed_help",
	problemScreenFailed:     "screen.failed_help",
	problemConfigSave:       "status.config_save_help",
	problemPortFailed:       "conn.port_failed_help",
	problemInviteInvalid:    "conn.invite_invalid_help",
	problemWrongKey:         "conn.wrong_key_help",
}

// problemLine es la línea del registro: el texto del problema y qué hacer.
func problemLine(kind problemKind, msg i18n.Msg) i18n.Msg {
	if help, ok := problemHelp[kind]; ok {
		return sentences(msg, i18n.M(help))
	}
	return msg
}

// sentences junta oraciones con un espacio entre cada una.
func sentences(parts ...i18n.Msg) i18n.Msg {
	joined := make([]i18n.Msg, 0, 2*len(parts))
	for i, part := range parts {
		if i > 0 {
			joined = append(joined, i18n.Plain(" "))
		}
		joined = append(joined, part)
	}
	return i18n.Join(joined...)
}

// problemCap es el tope por tipo. Lo usan varias goroutines (audio, red, servidor del celular).
type problemCap struct {
	mu   sync.Mutex
	last map[problemKind]time.Time
}

// allow dice si el problema va al registro ahora y, si va, lo anota.
func (c *problemCap) allow(kind problemKind, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if last, ok := c.last[kind]; ok && now.Sub(last) < problemLineCooldown {
		return false
	}
	if c.last == nil {
		c.last = make(map[problemKind]time.Time)
	}
	c.last[kind] = now
	return true
}

// reportProblem deja el problema en el registro de actividad (y la misma línea en castellano en el
// log), salvo que el tope lo frene. Devuelve si lo escribió. No toca la barra de estado: quien lo
// detecta sigue escribiendo ahí su texto corto.
func (a *App) reportProblem(kind problemKind, msg i18n.Msg) bool {
	if !a.problems.allow(kind, time.Now()) {
		return false
	}
	line := problemLine(kind, msg)
	logging.Infof("Registro de actividad: %s", line.In(i18n.ES))
	a.addActivity(line.String())
	return true
}

// captureWarnDropped son los frames tirados en un intervalo de la línea "Envío" (10 s) desde los
// que se avisa: 100 ms de audio. Menos es un tirón que casi no se nota.
const captureWarnDropped = 5

// sendSustain es cuántos intervalos seguidos (de 10 s) tiene que verse el mismo problema del envío
// para avisarlo: uno solo puede ser la vuelta de una suspensión o el tirón de abrir un juego.
const sendSustain = 2

// sendProblem dice qué problema del lado que envía muestra un intervalo: el micrófono entrega menos
// audio que tiempo (Bluetooth, driver) o la PC tuvo que tirar frames (estaba ocupada). Una cola o un
// frame lento solo atrasan: quedan en el log.
func sendProblem(c captureInterval) problemKind {
	switch {
	case c.rate() < captureMinRate:
		return problemSlowMic
	case c.dropped >= captureWarnDropped:
		return problemBusyPC
	}
	return 0
}

// sendWatch es la racha de un problema del envío. Avisa una vez por racha, cuando llega a
// sendSustain; un intervalo sano (o sin medir) la corta.
type sendWatch struct {
	kind  problemKind
	count int
}

// observe recibe el problema de un intervalo (0 = ninguno o sin medir) y devuelve el que hay que
// avisar ahora, si hay uno.
func (w *sendWatch) observe(kind problemKind) problemKind {
	if kind != w.kind {
		w.kind, w.count = kind, 0
	}
	if kind == 0 {
		return 0
	}
	w.count++
	if w.count == sendSustain {
		return kind
	}
	return 0
}

// sendProblemKeys es el texto de cada problema del envío (no tienen texto en la barra de estado).
var sendProblemKeys = map[problemKind]string{
	problemSlowMic: "audio.slow_mic",
	problemBusyPC:  "audio.busy_pc",
}

// watchSend es el paso de audioMonitorLoop después de medir un intervalo del envío. Con el celular
// de entrada la captura que se mide es la del micrófono de la PC, que no se usa; muteado (o
// ensordecido) no sale nada, así que no hay a quién avisarle que te escuchan cortado.
func (a *App) watchSend(w *sendWatch, interval captureInterval, measured bool) {
	var kind problemKind
	if measured && !a.phoneMicEnabled() && !a.isMuted.Load() {
		kind = sendProblem(interval)
	}
	if report := w.observe(kind); report != 0 {
		a.reportProblem(report, i18n.M(sendProblemKeys[report]))
	}
}
