//go:build cgo

package ui

import (
	"fmt"
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
//
// Log: cada netStatsLogInterval se mide un intervalo, pero con nivel 1 solo se escribe si hubo un
// problema (pérdida, cortes, captura lenta...); cada netStatsSummaryInterval, al irse un emisor o
// al cerrar el audio queda una línea "Resumen" con lo acumulado. Con nivel 2 se escriben todos los
// intervalos (TRACE).
const (
	audioMonitorInterval    = 100 * time.Millisecond
	speakingWindow          = 150 * time.Millisecond
	netStatsInterval        = 2 * time.Second
	netStatsLogInterval     = 10 * time.Second
	netStatsSummaryInterval = 5 * time.Minute
	// Umbrales de la línea "Envío": menos frames por segundo que esto es audio que no llega a ritmo
	// real; una cola o un frame más lento que esto (el período es de 20 ms) es la PC atrasada.
	captureMinRate  = 48.0
	captureMaxQueue = 1
	captureMaxProc  = 10 * time.Millisecond
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

// audioLogState es lo que el log del monitor arrastra entre intervalos.
type audioLogState struct {
	prev    map[string]audio.JitterStats // reporte del intervalo anterior
	capture captureSnapshot              // captura del intervalo anterior
	senders map[string]*senderSummary    // resumen en curso por emisor
	sending captureInterval              // resumen en curso del envío
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
	var prevStats map[string]audio.JitterStats
	var logState audioLogState
	var lastHealth time.Time
	for now := range ticker.C {
		a.sweepVoiceIndicator(now)
		if now.Sub(lastHealth) >= audioHealthInterval {
			lastHealth = now
			a.checkAudioHealth(now)
		}
		engine := a.engine()
		if engine == nil {
			prevStats = nil
			flushAudioLog(&logState, now)
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
			a.logJitterReport(&logState, now, report)
			logCaptureStats(&logState, capture)
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
			lossPct:  lossPercent(delta.Lost(), delta.Total()),
			jitterMS: current.JitterMS,
		}
	}
	return stats
}

func lossPercent(lost, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(lost) / float64(total)
}

// receiveProblem dice si en el intervalo de un emisor pasó algo que vale una línea con nivel 1:
// frames perdidos, tardíos, descartados por deriva, reinicios o cortes.
func receiveProblem(delta audio.JitterStats) bool {
	return delta.Lost() > 0 || delta.Late > 0 || delta.Dropped > 0 || delta.Resets > 0 || delta.Underruns > 0
}

// senderSummary acumula lo que se escuchó de un emisor entre resúmenes.
type senderSummary struct {
	name                                      string
	since                                     time.Time
	frames, lost                              uint64
	late, dropped, resets, underruns, phrases uint64
	underrunTime                              time.Duration
	maxJitterMS                               float64
	maxP95MS, maxTargetMS                     int
}

func (s *senderSummary) add(delta, current audio.JitterStats) {
	s.frames += delta.Total()
	s.lost += delta.Lost()
	s.late += delta.Late
	s.dropped += delta.Dropped
	s.resets += delta.Resets
	s.underruns += delta.Underruns
	s.underrunTime += delta.UnderrunTime
	s.phrases += delta.Phrases
	s.maxJitterMS = max(s.maxJitterMS, current.JitterMS)
	s.maxP95MS = max(s.maxP95MS, current.DelayP95MS)
	s.maxTargetMS = max(s.maxTargetMS, current.TargetMS)
}

func (s *senderSummary) log(now time.Time) {
	audioTime := time.Duration(s.frames) * time.Second * audio.FrameSamples / audio.SampleRate
	logging.Infof("Resumen audio de %s (%s): frames=%d (%s de voz) pérdida=%.1f%% tardíos=%d descartados=%d resets=%d cortes=%d (%d ms) frases=%d | jitter máx=%.1f ms p95 máx=%d ms objetivo máx=%d ms",
		s.name, now.Sub(s.since).Round(time.Second), s.frames, audioTime.Round(time.Second), lossPercent(s.lost, s.frames),
		s.late, s.dropped, s.resets, s.underruns, s.underrunTime.Milliseconds(), s.phrases, s.maxJitterMS, s.maxP95MS, s.maxTargetMS)
}

// logJitterReport mide el intervalo de cada emisor que mandó algo: lo suma a su resumen y lo deja
// en el log si tuvo un problema (con nivel 2, siempre). Cortes = el audio se interrumpió en medio
// de una frase (el emisor seguía mandando); frases = arranques después de una pausa del emisor, no
// son cortes. El resumen sale cada netStatsSummaryInterval o cuando el stream del emisor desaparece
// (se fue o estuvo callado más que JitterStreamIdleEvict).
func (a *App) logJitterReport(st *audioLogState, now time.Time, report map[string]audio.JitterStats) {
	if st.senders == nil {
		st.senders = make(map[string]*senderSummary)
	}
	for id, current := range report {
		delta := intervalDelta(current, st.prev[id])
		if delta.Total() == 0 && delta.Late == 0 && delta.Underruns == 0 {
			continue // callado: el stream sigue vivo para no perder lo medido de su red
		}
		name := a.peerDisplayName(id)
		summary := st.senders[id]
		if summary == nil {
			summary = &senderSummary{since: now.Add(-netStatsLogInterval)}
			st.senders[id] = summary
		}
		summary.name = name
		summary.add(delta, current)
		logf := logging.Tracef
		if receiveProblem(delta) {
			logf = logging.Infof
		}
		logf("Audio de %s: últimos %s: frames=%d pérdida=%.1f%% (recuperados=%d ocultados=%d) tardíos=%d descartados=%d resets=%d cortes=%d (%d ms) frases=%d | buffer=%d frames objetivo=%d ms jitter=%.1f ms p95=%d ms",
			name, netStatsLogInterval, delta.Total(), lossPercent(delta.Lost(), delta.Total()), delta.Recovered, delta.Concealed,
			delta.Late, delta.Dropped, delta.Resets, delta.Underruns, delta.UnderrunTime.Milliseconds(), delta.Phrases,
			current.BufferedFrames, current.TargetMS, current.JitterMS, current.DelayP95MS)
	}
	for id, summary := range st.senders {
		if _, alive := report[id]; !alive || now.Sub(summary.since) >= netStatsSummaryInterval {
			summary.log(now)
			delete(st.senders, id)
		}
	}
	st.prev = report
}

// captureInterval es lo que hizo la captura en un intervalo (o lo acumulado para el resumen).
type captureInterval struct {
	elapsed  time.Duration
	frames   uint64
	dropped  uint64
	sent     uint64
	maxQueue int
	maxProc  time.Duration
}

func (c captureInterval) rate() float64 {
	if c.elapsed <= 0 {
		return 0
	}
	return float64(c.frames) / c.elapsed.Seconds()
}

// problem dice si el intervalo vale una línea con nivel 1. A ritmo real la captura arma 50 frames
// por segundo y la cola queda en 0; menos frames, frames descartados, una cola que crece o un frame
// lento son audio que los demás reciben tarde o nunca. Los descartados se mandan como pérdida (los
// demás los ocultan); la cola atrasada llega tarde y en ráfaga.
func (c captureInterval) problem() bool {
	return c.rate() < captureMinRate || c.dropped > 0 || c.maxQueue > captureMaxQueue || c.maxProc > captureMaxProc
}

func (c *captureInterval) add(o captureInterval) {
	c.elapsed += o.elapsed
	c.frames += o.frames
	c.dropped += o.dropped
	c.sent += o.sent
	c.maxQueue = max(c.maxQueue, o.maxQueue)
	c.maxProc = max(c.maxProc, o.maxProc)
}

func (c captureInterval) String() string {
	return fmt.Sprintf("capturados=%d (%.1f/s) descartados=%d enviados=%d | cola máx=%d frames proc máx=%.1f ms",
		c.frames, c.rate(), c.dropped, c.sent, c.maxQueue, float64(c.maxProc)/float64(time.Millisecond))
}

// logCaptureStats mide el intervalo del lado que envía: lo suma al resumen y lo deja en el log si
// tuvo un problema (con nivel 2, siempre).
func logCaptureStats(st *audioLogState, current captureSnapshot) {
	previous := st.capture
	st.capture = current
	if previous.engine != current.engine || previous.at.IsZero() {
		// Engine nuevo: todavía no hay intervalo para comparar.
		flushSendSummary(st)
		logging.Infof("Envío: hilo de envío con %s", current.engine.SendThreadPriority())
		return
	}
	interval := captureInterval{
		elapsed:  current.at.Sub(previous.at),
		frames:   current.stats.Frames - previous.stats.Frames,
		dropped:  current.stats.Dropped - previous.stats.Dropped,
		sent:     current.sent - previous.sent,
		maxQueue: current.stats.MaxQueue,
		maxProc:  current.stats.MaxProc,
	}
	logf := logging.Tracef
	if interval.problem() {
		logf = logging.Infof
	}
	logf("Envío: %s", interval)
	st.sending.add(interval)
	if st.sending.elapsed >= netStatsSummaryInterval {
		flushSendSummary(st)
	}
}

func flushSendSummary(st *audioLogState) {
	if st.sending.elapsed > 0 {
		logging.Infof("Resumen envío (%s): %s", st.sending.elapsed.Round(time.Second), st.sending)
	}
	st.sending = captureInterval{}
}

// flushAudioLog deja los resúmenes pendientes cuando se cierra el audio (salir de la sala).
func flushAudioLog(st *audioLogState, now time.Time) {
	for _, summary := range st.senders {
		summary.log(now)
	}
	flushSendSummary(st)
	*st = audioLogState{}
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
