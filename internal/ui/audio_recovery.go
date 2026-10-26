//go:build cgo

package ui

import (
	"time"

	"voxmesh/internal/audio"
	"voxmesh/internal/logging"
	"voxmesh/internal/winproc"
)

// Recuperación automática del audio de la sala. Windows invalida un dispositivo abierto sin que
// la app haga nada (USB o Bluetooth desconectado, vuelta de una suspensión, cambio de driver) y
// antes el audio quedaba muerto hasta apretar "Refresh Audio". audioMonitorLoop consulta
// Engine.Health cada audioHealthInterval y, si falla, reabre el audio como Refresh Audio (el
// elegido si sigue, si no el predeterminado de Windows).
const (
	audioHealthInterval = 2 * time.Second
	// audioFailChecks: chequeos seguidos con falla antes de reabrir. Al volver de una pausa del
	// proceso el monitor puede despertar antes que los callbacks de malgo: uno solo no alcanza.
	audioFailChecks = 2
	// audioMissingAfter: en sala y sin Engine (falló al abrir) tanto tiempo → se reintenta.
	// Abrir un Bluetooth puede tardar varios segundos.
	audioMissingAfter = 15 * time.Second
	// audioHealthyReset: tanto tiempo sano y el backoff vuelve al principio.
	audioHealthyReset = 30 * time.Second
)

// audioRetryBackoff es la espera mínima entre reaperturas seguidas (la última se repite).
var audioRetryBackoff = []time.Duration{5 * time.Second, 10 * time.Second, 30 * time.Second, time.Minute}

// audioRecovery decide cuándo reabrir el audio. Solo la usa audioMonitorLoop.
type audioRecovery struct {
	engine       *audio.Engine // el Engine que se está mirando
	failing      int           // chequeos seguidos con falla
	missingSince time.Time     // desde cuándo hay sala sin Engine
	healthySince time.Time
	attempts     int // reaperturas desde la última vez que estuvo sano audioHealthyReset
	nextAttempt  time.Time
	total        int // reaperturas desde que arrancó la app (log)
}

// check recibe el estado del momento: si hay sala, el Engine publicado (nil si no hay) y su
// salud. Devuelve si hay que reabrir el audio ahora.
func (r *audioRecovery) check(now time.Time, inRoom bool, engine *audio.Engine, ok bool) bool {
	if !inRoom {
		*r = audioRecovery{total: r.total}
		return false
	}
	if engine != r.engine {
		r.engine, r.failing, r.healthySince = engine, 0, now
	}
	if engine == nil {
		if r.missingSince.IsZero() {
			r.missingSince = now
		}
		if now.Sub(r.missingSince) < audioMissingAfter {
			return false
		}
	} else {
		r.missingSince = time.Time{}
		if ok {
			r.failing = 0
			if r.attempts > 0 && now.Sub(r.healthySince) >= audioHealthyReset {
				r.attempts = 0
			}
			return false
		}
		r.healthySince = now
		r.failing++
		if r.failing < audioFailChecks {
			return false
		}
	}
	if now.Before(r.nextAttempt) {
		return false
	}
	r.nextAttempt = now.Add(audioRetryBackoff[min(r.attempts, len(audioRetryBackoff)-1)])
	r.attempts++
	r.total++
	r.failing, r.missingSince = 0, time.Time{}
	return true
}

// checkAudioHealth es el paso de audioMonitorLoop: reabre el audio si se cayó y, mientras hay
// sala, no deja que la PC se suspenda por inactividad.
func (a *App) checkAudioHealth(now time.Time) {
	inRoom := a.connected()
	if err := winproc.KeepSystemAwake(inRoom); err != nil && !a.awakeErrLogged {
		a.awakeErrLogged = true
		logging.Errorf("evitar la suspensión durante la sala: %s", logging.FormatError(err))
	}
	engine := a.engine()
	ok, reason := engine.Health(now)
	if engine == nil {
		reason = "no hay audio abierto"
	}
	if !a.audioRecovery.check(now, inRoom, engine, ok) {
		return
	}
	logging.Errorf("audio caído (%s): reabriendo (intento %d, %d en total)", reason, a.audioRecovery.attempts, a.audioRecovery.total)
	a.setStatus("Se cayó el audio (" + reason + "), reabriendo...")
	go a.restartAudioInRoom() // cerrar un dispositivo invalidado puede tardar: el monitor sigue
}
