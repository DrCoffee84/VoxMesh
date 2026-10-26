//go:build cgo

package ui

import (
	"os"
	"path/filepath"
	"time"

	"voxmesh/internal/audio"
	"voxmesh/internal/config"
	"voxmesh/internal/logging"
)

// Monitoreo del audio, en una goroutine propia (nunca desde el callback de malgo):
//   - indicador "hablando": se prende con lo que el Engine efectivamente reprodujo, no con la
//     llegada del paquete (que suena un colchón después);
//   - estadísticas de red por emisor (pérdida, jitter, cortes) para el chip del avatar y el log;
//   - del lado que envía, si la captura entrega frames a ritmo real (línea "Envío" del log);
//   - con nivel de log 2, la grabación de llegadas para reproducir la red real en un test.
const (
	audioMonitorInterval = 100 * time.Millisecond
	speakingWindow       = 150 * time.Millisecond
	netStatsInterval     = 2 * time.Second
	netStatsLogInterval  = 10 * time.Second
	// arrivalFilesMaxAge es cuánto se guardan las grabaciones de llegadas (como los logs).
	arrivalFilesMaxAge = 48 * time.Hour
)

// captureSnapshot es la medición de la captura y lo enviado en un momento dado.
type captureSnapshot struct {
	at     time.Time
	engine *audio.Engine
	stats  audio.CaptureStats
	sent   uint64 // a.audioSent
}

// audioMonitorLoop vive lo que la app. Un panic en una vuelta queda en el log y el monitor vuelve
// a arrancar (con las mediciones de cero): sin él no hay recuperación del audio ni indicadores.
func (a *App) audioMonitorLoop() {
	for !logging.Guard("monitor de audio", a.runAudioMonitor) {
		time.Sleep(time.Second)
	}
}

func (a *App) runAudioMonitor() {
	ticker := time.NewTicker(audioMonitorInterval)
	defer ticker.Stop()
	var audible []string
	var lastStats, lastLog time.Time
	var prevStats, prevLog map[string]audio.JitterStats
	var prevCapture captureSnapshot
	var lastHealth time.Time
	for now := range ticker.C {
		a.sweepVoiceIndicator(now)
		if now.Sub(lastHealth) >= audioHealthInterval {
			lastHealth = now
			a.checkAudioHealth(now)
		}
		engine := a.engine()
		if engine == nil {
			prevStats, prevLog = nil, nil
			prevCapture = captureSnapshot{}
			a.stopArrivalRecorder()
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
		a.syncArrivalRecorder(engine)
		report := engine.JitterReport()
		a.updatePeerNetStats(intervalNetStats(report, prevStats))
		prevStats = report
		if now.Sub(lastLog) >= netStatsLogInterval {
			lastLog = now
			capture := captureSnapshot{at: now, engine: engine, stats: engine.TakeCaptureStats(), sent: a.audioSent.Load()}
			if logging.IsControl() {
				a.logJitterReport(report, prevLog)
				logCaptureStats(capture, prevCapture)
			}
			prevLog = report
			prevCapture = capture
		}
	}
}

// intervalDelta resta los contadores del intervalo anterior. Si bajaron, el stream se recreó
// (purga por silencio, reset del Engine) y cuenta desde cero.
func intervalDelta(current, previous audio.JitterStats) audio.JitterStats {
	if current.Total() < previous.Total() || current.Late < previous.Late ||
		current.Underruns < previous.Underruns || current.Phrases < previous.Phrases {
		return current
	}
	current.Played -= previous.Played
	current.Recovered -= previous.Recovered
	current.Concealed -= previous.Concealed
	current.Late -= previous.Late
	current.Dropped -= previous.Dropped
	current.Resets -= previous.Resets
	current.Underruns -= previous.Underruns
	current.UnderrunTime -= previous.UnderrunTime
	current.Phrases -= previous.Phrases
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

// logJitterReport deja en el log (nivel 1, control) una línea por emisor que mandó algo en el
// intervalo: totales, pérdida y cortes del intervalo, y estado del colchón. Es la línea base para
// comparar entre versiones. Cortes = el audio se interrumpió en medio de una frase (el emisor
// seguía mandando); frases = arranques después de una pausa del emisor, no son cortes.
func (a *App) logJitterReport(report, previous map[string]audio.JitterStats) {
	for id, current := range report {
		delta := intervalDelta(current, previous[id])
		if delta.Total() == 0 && delta.Late == 0 && delta.Underruns == 0 {
			continue // callado: el stream sigue vivo para no perder lo medido de su red
		}
		lossPct := 0.0
		if delta.Total() > 0 {
			lossPct = 100 * float64(delta.Lost()) / float64(delta.Total())
		}
		logging.Infof("Audio de %s: reproducidos=%d recuperados=%d ocultados=%d tardíos=%d descartados=%d resets=%d cortes=%d | últimos %s: frames=%d pérdida=%.1f%% tardíos=%d cortes=%d (%d ms) frases=%d | buffer=%d frames objetivo=%d ms jitter=%.1f ms p95=%d ms",
			a.peerDisplayName(id), current.Played, current.Recovered, current.Concealed, current.Late, current.Dropped, current.Resets, current.Underruns,
			netStatsLogInterval, delta.Total(), lossPct, delta.Late, delta.Underruns, delta.UnderrunTime.Milliseconds(), delta.Phrases,
			current.BufferedFrames, current.TargetMS, current.JitterMS, current.DelayP95MS)
	}
}

// logCaptureStats deja la línea del lado que envía. A ritmo real la captura arma 50 frames por
// segundo y la cola queda en 0; menos frames, frames descartados o una cola que crece son audio que
// los demás reciben tarde o nunca. Los descartados se mandan como pérdida (los demás los ocultan);
// la cola atrasada llega tarde y en ráfaga.
func logCaptureStats(current, previous captureSnapshot) {
	if previous.engine != current.engine || previous.at.IsZero() {
		// Engine nuevo: todavía no hay intervalo para comparar.
		logging.Infof("Envío: hilo de envío con %s", current.engine.SendThreadPriority())
		return
	}
	elapsed := current.at.Sub(previous.at).Seconds()
	frames := current.stats.Frames - previous.stats.Frames
	logging.Infof("Envío: capturados=%d (%.1f/s) descartados=%d enviados=%d | cola máx=%d frames proc máx=%.1f ms",
		frames, float64(frames)/elapsed, current.stats.Dropped-previous.stats.Dropped, current.sent-previous.sent,
		current.stats.MaxQueue, float64(current.stats.MaxProc)/float64(time.Millisecond))
}

// syncArrivalRecorder graba las llegadas de audio mientras el nivel de log sea 2 (trace): un CSV
// por conexión en la carpeta de datos, para reproducir la red real con TestJitterReplayArrivals.
func (a *App) syncArrivalRecorder(engine *audio.Engine) {
	if !logging.IsTrace() {
		a.stopArrivalRecorder()
		return
	}
	if a.arrivalRec == nil {
		root, err := config.AppDir()
		if err != nil {
			return
		}
		pruneArrivalFiles(root, time.Now().Add(-arrivalFilesMaxAge))
		path := filepath.Join(root, "llegadas-"+time.Now().Format("2006-01-02-150405")+".csv")
		rec, err := audio.NewArrivalRecorder(path)
		if err != nil {
			logging.Errorf("grabar llegadas de audio: %s", logging.FormatError(err))
			return
		}
		a.arrivalRec = rec
		logging.Infof("Grabando llegadas de audio en %s", path)
	}
	if a.arrivalEngine != engine {
		if a.arrivalEngine != nil {
			a.arrivalEngine.SetArrivalRecorder(nil)
		}
		engine.SetArrivalRecorder(a.arrivalRec)
		a.arrivalEngine = engine
	}
}

// stopArrivalRecorder cierra la grabación de llegadas, si hay una.
func (a *App) stopArrivalRecorder() {
	if a.arrivalRec == nil {
		return
	}
	if a.arrivalEngine != nil {
		a.arrivalEngine.SetArrivalRecorder(nil)
		a.arrivalEngine = nil
	}
	if err := a.arrivalRec.Close(); err != nil {
		logging.Errorf("cerrar grabación de llegadas: %s", logging.FormatError(err))
	}
	logging.Infof("Grabación de llegadas terminada: %s (sin anotar: %d)", a.arrivalRec.Path(), a.arrivalRec.Dropped())
	a.arrivalRec = nil
}

// pruneArrivalFiles borra las grabaciones viejas (~7 MB por hora y por persona que habla).
func pruneArrivalFiles(root string, before time.Time) {
	paths, _ := filepath.Glob(filepath.Join(root, "llegadas-*.csv"))
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && info.ModTime().Before(before) {
			_ = os.Remove(path)
		}
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
