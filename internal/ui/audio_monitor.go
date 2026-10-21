//go:build cgo

package ui

import (
	"time"

	"voxmesh/internal/audio"
	"voxmesh/internal/logging"
)

// Monitoreo del audio recibido, en una goroutine propia (nunca desde el callback de malgo):
//   - indicador "hablando": se prende con lo que el Engine efectivamente reprodujo, no con la
//     llegada del paquete (que suena un colchón después);
//   - estadísticas de red por emisor (pérdida, jitter) para el chip del avatar y el log.
const (
	audioMonitorInterval = 100 * time.Millisecond
	speakingWindow       = 150 * time.Millisecond
	netStatsInterval     = 2 * time.Second
	netStatsLogInterval  = 10 * time.Second
)

func (a *App) audioMonitorLoop() {
	ticker := time.NewTicker(audioMonitorInterval)
	defer ticker.Stop()
	var audible []string
	var lastStats, lastLog time.Time
	var prevStats, prevLog map[string]audio.JitterStats
	for now := range ticker.C {
		engine := a.engine()
		if engine == nil {
			prevStats, prevLog = nil, nil
			continue
		}
		audible = engine.AudibleStreams(audible[:0], speakingWindow)
		for _, id := range audible {
			a.markPeerSpeaking(id, "")
		}
		a.sweepSpeakingPeers()

		if now.Sub(lastStats) < netStatsInterval {
			continue
		}
		lastStats = now
		report := engine.JitterReport()
		a.updatePeerNetStats(intervalNetStats(report, prevStats))
		prevStats = report
		if now.Sub(lastLog) >= netStatsLogInterval {
			lastLog = now
			if logging.IsControl() {
				a.logJitterReport(report, prevLog)
			}
			prevLog = report
		}
	}
}

// intervalDelta resta los contadores del intervalo anterior. Si bajaron, el stream se recreó
// (purga por silencio, reset del Engine) y cuenta desde cero.
func intervalDelta(current, previous audio.JitterStats) audio.JitterStats {
	if current.Total() < previous.Total() || current.Late < previous.Late {
		return current
	}
	current.Played -= previous.Played
	current.Recovered -= previous.Recovered
	current.Concealed -= previous.Concealed
	current.Late -= previous.Late
	current.Dropped -= previous.Dropped
	current.Resets -= previous.Resets
	return current
}

// intervalNetStats calcula pérdida y jitter del último intervalo para los emisores que mandaron
// audio en él (los que no, conservan el último valor en el chip).
func intervalNetStats(report, previous map[string]audio.JitterStats) map[string]peerNetStats {
	stats := make(map[string]peerNetStats, len(report))
	for id, current := range report {
		delta := intervalDelta(current, previous[id])
		if delta.Total() == 0 {
			continue
		}
		stats[id] = peerNetStats{
			known:    true,
			lossPct:  100 * float64(delta.Lost()) / float64(delta.Total()),
			jitterMS: current.JitterMS,
		}
	}
	return stats
}

// logJitterReport deja en el log (nivel 1, control) una línea por emisor: totales, pérdida del
// intervalo y estado del colchón. Es la línea base para comparar entre versiones.
func (a *App) logJitterReport(report, previous map[string]audio.JitterStats) {
	for id, current := range report {
		delta := intervalDelta(current, previous[id])
		lossPct := 0.0
		if delta.Total() > 0 {
			lossPct = 100 * float64(delta.Lost()) / float64(delta.Total())
		}
		logging.Infof("Audio de %s: reproducidos=%d recuperados=%d ocultados=%d tardíos=%d descartados=%d resets=%d | últimos %s: frames=%d pérdida=%.1f%% tardíos=%d | buffer=%d frames objetivo=%d ms jitter=%.1f ms p95=%d ms",
			a.peerDisplayName(id), current.Played, current.Recovered, current.Concealed, current.Late, current.Dropped, current.Resets,
			netStatsLogInterval, delta.Total(), lossPct, delta.Late,
			current.BufferedFrames, current.TargetMS, current.JitterMS, current.DelayP95MS)
	}
}

// peerDisplayName devuelve "nombre (id)" si el participante está en la lista, o el id.
func (a *App) peerDisplayName(id string) string {
	a.peerMu.RLock()
	defer a.peerMu.RUnlock()
	for _, peer := range a.peerNames {
		if peer.id == id {
			return peer.name + " (" + id + ")"
		}
	}
	return id
}
