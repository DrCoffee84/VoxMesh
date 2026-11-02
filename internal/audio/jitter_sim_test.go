package audio

import (
	"bufio"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Simulación determinista de una charla (sin reloj real ni CGO): un emisor con frases y pausas,
// una red con picos de lag como los del Wi-Fi (el enlace se traba y después entrega todo junto) y
// un receptor que lee el jitter buffer de a 10 ms, como el callback de malgo. Mide los cortes
// (Underruns) y la latencia de lo que suena.

type simPacket struct {
	seq       uint32
	talkspurt uint8
	sentAt    time.Duration
	arriveAt  time.Duration
	lost      bool
}

type simConfig struct {
	duration time.Duration
	phrase   [2]time.Duration // largo de cada frase (mín, máx)
	pause    [2]time.Duration // pausa entre frases (mín, máx): el VAD cierra y no sale nada
	// captureLoss es la proporción de frames que la captura del emisor pierde (PC saturada,
	// Bluetooth). Sin advanceSeq la secuencia no avanza: el receptor recibe menos audio que tiempo.
	captureLoss float64
	advanceSeq  bool
	netLoss     float64
	spikeEvery  time.Duration    // un pico de lag cada ~spikeEvery (0 = sin picos)
	spike       [2]time.Duration // duración del pico (mín, máx)
	noise       time.Duration    // jitter chico, uniforme entre 0 y noise
}

type simResult struct {
	stats       JitterStats
	cutsPerMin  float64
	meanLatency time.Duration // del envío a que el frame empieza a sonar
}

func between(rng *rand.Rand, r [2]time.Duration) time.Duration {
	if r[1] <= r[0] {
		return r[0]
	}
	return r[0] + time.Duration(rng.Int63n(int64(r[1]-r[0])))
}

// simulateSender arma los paquetes como sendOutgoingPCM: la secuencia solo avanza con lo que se
// manda, al abrir el VAD el pre-roll (2 frames) sale junto con el primer frame, y la frase cambia
// si pasaron más de 60 ms desde el último paquete.
func simulateSender(cfg simConfig, rng *rand.Rand) []simPacket {
	var packets []simPacket
	var seq uint32
	var spurt uint8
	lastSent := -time.Hour
	for start := time.Duration(0); start < cfg.duration; {
		frames := int(between(rng, cfg.phrase) / jitterFrameDuration)
		if start-lastSent > 60*time.Millisecond {
			spurt++
		}
		for f := 0; f < frames+2; f++ {
			sentAt := start
			if f >= 2 {
				sentAt = start + time.Duration(f-2)*jitterFrameDuration
			}
			if rng.Float64() < cfg.captureLoss {
				if cfg.advanceSeq {
					seq++
				}
				continue
			}
			seq++
			packets = append(packets, simPacket{seq: seq, talkspurt: spurt, sentAt: sentAt})
			lastSent = sentAt
		}
		start += time.Duration(frames)*jitterFrameDuration + between(rng, cfg.pause)
	}
	return packets
}

// simulateNetwork asigna la llegada: retardo base + jitter chico, picos donde el enlace se traba y
// entrega lo encolado al final, sin reordenar (FIFO), y pérdida al azar.
func simulateNetwork(packets []simPacket, cfg simConfig, rng *rand.Rand) {
	const base = 30 * time.Millisecond
	var spikes [][2]time.Duration
	if cfg.spikeEvery > 0 {
		for at := between(rng, [2]time.Duration{0, cfg.spikeEvery}); at < cfg.duration+time.Second; at += cfg.spikeEvery/2 + between(rng, [2]time.Duration{0, cfg.spikeEvery}) {
			spikes = append(spikes, [2]time.Duration{at, at + between(rng, cfg.spike)})
		}
	}
	var prev time.Duration
	for i := range packets {
		p := &packets[i]
		at := p.sentAt + base + time.Duration(rng.Int63n(int64(cfg.noise)+1))
		for _, s := range spikes {
			if at >= s[0] && at < s[1] {
				at = s[1]
			}
		}
		at = max(at, prev)
		prev = at
		p.arriveAt = at
		p.lost = rng.Float64() < cfg.netLoss
	}
}

// simulateReceiver entrega cada paquete a su hora de llegada y lee 10 ms por vuelta.
func simulateReceiver(packets []simPacket, tuning JitterTuning, end time.Duration) simResult {
	j := newJitterBufferTuned(tuning, &fakeDecoder{})
	frame := fakePacket(sineFrame(0, 8000), nil) // el contenido no importa
	sentAt := make(map[uint32]time.Duration, len(packets))
	arrivals := make([]simPacket, 0, len(packets))
	for _, p := range packets {
		sentAt[p.seq] = p.sentAt
		if !p.lost {
			arrivals = append(arrivals, p)
		}
	}
	sort.SliceStable(arrivals, func(a, b int) bool { return arrivals[a].arriveAt < arrivals[b].arriveAt })

	buf := make([]int16, FrameSamples/2)
	next := 0
	var latencySum time.Duration
	latencyCount := 0
	for t := time.Duration(0); t < end; t += 10 * time.Millisecond {
		for next < len(arrivals) && arrivals[next].arriveAt <= t {
			p := arrivals[next]
			j.Push(p.seq, p.talkspurt, frame, jitterT0.Add(p.arriveAt))
			next++
		}
		played := j.stats.Played
		j.Read(buf, jitterT0.Add(t))
		if j.stats.Played > played {
			if sent, ok := sentAt[j.playSeq-1]; ok {
				latencySum += t - sent
				latencyCount++
			}
		}
	}
	result := simResult{stats: j.Stats()}
	result.cutsPerMin = float64(result.stats.Underruns) / end.Minutes()
	if latencyCount > 0 {
		result.meanLatency = latencySum / time.Duration(latencyCount)
	}
	return result
}

func runSim(t *testing.T, name string, cfg simConfig) simResult {
	t.Helper()
	rng := rand.New(rand.NewSource(7))
	packets := simulateSender(cfg, rng)
	simulateNetwork(packets, cfg, rng)
	result := simulateReceiver(packets, DefaultJitterTuning(), cfg.duration+time.Second)
	s := result.stats
	t.Logf("%s: cortes=%d (%.1f/min, %v) frases=%d reproducidos=%d perdidos=%d descartados=%d latencia media=%v objetivo=%d ms jitter=%.1f ms p95=%d ms",
		name, s.Underruns, result.cutsPerMin, s.UnderrunTime.Round(time.Millisecond), s.Phrases, s.Played, s.Lost(), s.Dropped,
		result.meanLatency.Round(time.Millisecond), s.TargetMS, s.JitterMS, s.DelayP95MS)
	return result
}

// Charla con pausas cortas y picos de lag de Wi-Fi.
var simWiFi = simConfig{
	duration:   2 * time.Minute,
	phrase:     [2]time.Duration{time.Second, 4 * time.Second},
	pause:      [2]time.Duration{100 * time.Millisecond, 400 * time.Millisecond},
	netLoss:    0.003,
	spikeEvery: 3 * time.Second,
	spike:      [2]time.Duration{60 * time.Millisecond, 180 * time.Millisecond},
	noise:      10 * time.Millisecond,
}

// Red estable, pausas normales: no tiene que haber cortes ni latencia de más.
var simStable = simConfig{
	duration: 2 * time.Minute,
	phrase:   [2]time.Duration{time.Second, 4 * time.Second},
	pause:    [2]time.Duration{300 * time.Millisecond, 1500 * time.Millisecond},
	noise:    5 * time.Millisecond,
}

// Captura que pierde frames (PC saturada o Bluetooth), con red buena y frases largas.
var simCaptureLoss = simConfig{
	duration:    2 * time.Minute,
	phrase:      [2]time.Duration{5 * time.Second, 15 * time.Second},
	pause:       [2]time.Duration{200 * time.Millisecond, 500 * time.Millisecond},
	captureLoss: 0.02,
	noise:       5 * time.Millisecond,
}

// Los topes de estos tests son los valores medidos con esta semilla más un margen; la referencia
// es lo que daba el jitter buffer antes de esperar el colchón en cada frase nueva y de cubrir los
// picos repetidos (promedio de 10 semillas: Wi-Fi 16 cortes/min, captura 25.9; ahora 3.5 y 7.1).

func TestJitterSim_WiFiSpikes(t *testing.T) {
	result := runSim(t, "wifi", simWiFi) // antes: 34 cortes (16.9/min, 2.2 s)
	if result.cutsPerMin > 5 {
		t.Fatalf("demasiados cortes con picos de Wi-Fi: %.1f/min", result.cutsPerMin)
	}
}

func TestJitterSim_StableNetworkHasNoCuts(t *testing.T) {
	result := runSim(t, "estable", simStable)
	if result.stats.Underruns != 0 {
		t.Fatalf("red estable con %d cortes", result.stats.Underruns)
	}
	// Sin picos el colchón queda en el piso: la latencia no puede crecer por las mejoras para Wi-Fi.
	if result.meanLatency > 120*time.Millisecond {
		t.Fatalf("latencia de más con red estable: %v", result.meanLatency)
	}
}

func TestJitterSim_CaptureLoss(t *testing.T) {
	// El receptor no puede reponer audio que el emisor nunca mandó: solo lo amortigua con el
	// colchón de cada frase (antes: 48 cortes, 23.8/min).
	result := runSim(t, "captura pierde frames", simCaptureLoss)
	if result.cutsPerMin > 8 {
		t.Fatalf("demasiados cortes con captura que pierde frames: %.1f/min", result.cutsPerMin)
	}
	// Si el emisor avanza la secuencia por cada frame perdido, el receptor lo ve como pérdida
	// (PLC/FEC) y la línea de tiempo no se corre: ningún corte.
	advance := simCaptureLoss
	advance.advanceSeq = true
	result = runSim(t, "captura pierde frames (secuencia avanza)", advance)
	if result.stats.Underruns != 0 || result.stats.Lost() == 0 {
		t.Fatalf("con la secuencia avanzando no debería haber cortes, solo pérdidas: %+v", result.stats)
	}
}

// TestJitterReplayArrivals reproduce una grabación real de llegadas (el CSV que escribe
// ArrivalRecorder mientras el nivel de log es 2) por el jitter buffer con los parámetros de
// fábrica, emisor por emisor:
//
//	VOXMESH_ARRIVALS=%AppData%\VoxMesh\llegadas-....csv go test -run TestJitterReplayArrivals -v ./internal/audio/
func TestJitterReplayArrivals(t *testing.T) {
	path := os.Getenv("VOXMESH_ARRIVALS")
	if path == "" {
		t.Skip("sin VOXMESH_ARRIVALS")
	}
	bySender, err := readArrivals(path)
	if err != nil {
		t.Fatal(err)
	}
	for sender, packets := range bySender {
		start := packets[0].arriveAt
		end := packets[len(packets)-1].arriveAt - start + time.Second
		for i := range packets {
			packets[i].arriveAt -= start
		}
		result := simulateReceiver(packets, DefaultJitterTuning(), end)
		s := result.stats
		t.Logf("%s: %v de audio, cortes=%d (%.1f/min, %v) frases=%d reproducidos=%d perdidos=%d tardíos=%d descartados=%d objetivo=%d ms p95=%d ms",
			sender, end.Round(time.Second), s.Underruns, result.cutsPerMin, s.UnderrunTime.Round(time.Millisecond), s.Phrases,
			s.Played, s.Lost(), s.Late, s.Dropped, s.TargetMS, s.DelayP95MS)
	}
}

// readArrivals lee el CSV de ArrivalRecorder: emisor,seq,frase,ns desde el inicio.
func readArrivals(path string) (map[string][]simPacket, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	bySender := map[string][]simPacket{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ",")
		if len(fields) != 4 {
			continue
		}
		seq, errSeq := strconv.ParseUint(fields[1], 10, 32)
		spurt, errSpurt := strconv.ParseUint(fields[2], 10, 8)
		at, errAt := strconv.ParseInt(fields[3], 10, 64)
		if errSeq != nil || errSpurt != nil || errAt != nil {
			continue // encabezado o línea cortada
		}
		bySender[fields[0]] = append(bySender[fields[0]], simPacket{seq: uint32(seq), talkspurt: uint8(spurt), arriveAt: time.Duration(at)})
	}
	return bySender, scanner.Err()
}
