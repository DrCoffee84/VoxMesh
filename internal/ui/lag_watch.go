//go:build cgo

package ui

import (
	"time"

	"voxmesh/internal/i18n"
)

// Lag prolongado en el registro de actividad (AGENDA C13). Una máquina por persona (lagWatch)
// lee las muestras del historial de lag (C18) a medida que se cierra cada casillero de 2 s: no mide
// nada nuevo. "Lag" = más de lagWarnMS de ida y vuelta (naranja o peor en el chip) o lagLossPct de
// pérdida, sostenido lagSustain. Al empezar, una línea; al volver a lo normal por lagSustain, otra.
// Los tramos quedan marcados en el gráfico grande.

const (
	// lagLossPct es la pérdida de audio que cuenta como lag (en un intervalo de 2 s, 5 de 100 frames).
	lagLossPct = 5
	// lagSustain es cuánto tiene que durar el lag para avisarlo, y lo normal para darlo por terminado
	// (la histéresis: un pico corto no avisa y no alterna).
	lagSustain = 10 * time.Second
	// lagStreakGap: más que esto sin muestras corta la racha (no se sabe qué pasó en el medio).
	lagStreakGap = 6 * time.Second
	// lagLineCooldown es el tope por persona: como mucho un aviso de lag cada tanto. Los tramos del
	// medio se marcan igual en el gráfico, sin líneas.
	lagLineCooldown = 5 * time.Minute
)

// lagEpisode es un tramo con lag de una persona.
type lagEpisode struct {
	start     time.Time // primera muestra con lag
	end       time.Time // primera muestra normal de los lagSustain que lo cerraron; cero = sigue
	last      time.Time // última muestra con lag
	worstRTT  int       // ms; -1 = sin medir
	worstLoss int       // %; -1 = sin medir
	silent    bool      // empezó dentro del tope (lagLineCooldown): sin líneas en el registro
}

func (e *lagEpisode) add(point lagPoint) {
	e.last = point.at
	e.worstRTT = max(e.worstRTT, point.rtt)
	e.worstLoss = max(e.worstLoss, point.loss)
}

type lagEventKind int

const (
	lagStarted lagEventKind = iota + 1
	lagEnded
)

// lagEvent es lo que lagWatch avisa al registro.
type lagEvent struct {
	kind    lagEventKind
	name    string
	episode lagEpisode
}

// lagWatch es la máquina de una persona. Pura: la hora sale de las muestras.
type lagWatch struct {
	lagging  bool
	streak   time.Time  // primera muestra de la racha contraria al estado; cero = ninguna
	last     time.Time  // última muestra con algo medido
	pending  lagEpisode // la racha con lag en curso, todavía sin avisar
	episodes []lagEpisode
	lastLine time.Time // inicio del último tramo avisado (tope)
}

// lagBad dice si una muestra tiene lag y si tiene algo medido.
func lagBad(point lagPoint) (bad, known bool) {
	return point.rtt > lagWarnMS || point.loss >= lagLossPct, point.rtt >= 0 || point.loss >= 0
}

// observe procesa una muestra cerrada (en orden de hora). Devuelve el aviso, si hay uno.
func (w *lagWatch) observe(point lagPoint) (lagEventKind, lagEpisode) {
	bad, known := lagBad(point)
	if !known {
		return 0, lagEpisode{}
	}
	if !w.last.IsZero() && point.at.Sub(w.last) > lagStreakGap {
		w.streak = time.Time{} // un hueco corta la racha
	}
	w.last = point.at
	if !w.lagging {
		if !bad {
			w.streak = time.Time{}
			return 0, lagEpisode{}
		}
		if w.streak.IsZero() {
			w.streak = point.at
			w.pending = lagEpisode{start: point.at, worstRTT: -1, worstLoss: -1}
		}
		w.pending.add(point)
		if point.at.Sub(w.streak) < lagSustain {
			return 0, lagEpisode{}
		}
		w.lagging, w.streak = true, time.Time{}
		episode := w.pending
		episode.silent = !w.lastLine.IsZero() && episode.start.Sub(w.lastLine) < lagLineCooldown
		if !episode.silent {
			w.lastLine = episode.start
		}
		w.episodes = append(w.episodes, episode)
		if episode.silent {
			return 0, lagEpisode{}
		}
		return lagStarted, episode
	}
	current := &w.episodes[len(w.episodes)-1]
	if bad {
		current.add(point)
		w.streak = time.Time{}
		return 0, lagEpisode{}
	}
	if w.streak.IsZero() {
		w.streak = point.at
	}
	if point.at.Sub(w.streak) < lagSustain {
		return 0, lagEpisode{}
	}
	w.lagging = false
	current.end, w.streak = w.streak, time.Time{}
	if current.silent {
		return 0, lagEpisode{}
	}
	return lagEnded, *current
}

// lagEventMsg es la línea del registro de un aviso. host: esta PC es cliente y la persona es su
// host (el cliente solo mide al host: "con el host (Pepe)").
func lagEventMsg(event lagEvent, host bool) i18n.Msg {
	episode := event.episode
	worst := lagDetailMsg(episode.worstRTT, episode.worstLoss)
	if event.kind == lagStarted {
		key := "lag.started"
		if host {
			key = "lag.started_host"
		}
		return i18n.Join(i18n.M(key, event.name, episode.start.Format("15:04"), worst), i18n.Plain(" "), i18n.M("lag.help"))
	}
	key := "lag.ended"
	if host {
		key = "lag.ended_host"
	}
	return i18n.M(key, event.name, lagDurationMsg(episode.end.Sub(episode.start)), worst)
}

// lagDetailMsg es "350 ms, 8 % de pérdida" con lo que se midió.
func lagDetailMsg(rtt, loss int) i18n.Msg {
	var parts []i18n.Msg
	if rtt >= 0 {
		parts = append(parts, i18n.M("lag.point_ms", rtt))
	}
	if loss >= 0 {
		if len(parts) > 0 {
			parts = append(parts, i18n.Plain(", "))
		}
		parts = append(parts, i18n.M("lag.point_loss", loss))
	}
	return i18n.Join(parts...)
}

// lagDurationMsg es "1 min 20 s", "45 s" o "1 h 5 min".
func lagDurationMsg(d time.Duration) i18n.Msg {
	seconds := int(d.Round(time.Second) / time.Second)
	switch {
	case seconds < 60:
		return i18n.M("lag.duration_s", seconds)
	case seconds < 3600:
		return i18n.M("lag.duration_min_s", seconds/60, seconds%60)
	}
	return i18n.M("lag.duration_h_min", seconds/3600, seconds%3600/60)
}
