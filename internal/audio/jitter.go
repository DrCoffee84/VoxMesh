package audio

import (
	"math"
	"time"

	"voxmesh/internal/config"
)

// jitterRingFrames es la capacidad del ring de frames (1.28 s a 20 ms por frame).
const jitterRingFrames = 64

// jitterHistogramBuckets cubre hasta 640 ms de variación de retardo (buckets de 5 ms).
const jitterHistogramBuckets = 128

// jitterPeakEvents es cuántos picos recientes recuerda arrivalStats (sobra para los que entran en
// config.JitterPeakMemory en cualquier red usable).
const jitterPeakEvents = 16

const jitterFrameDuration = 20 * time.Millisecond

// frameDecoder decodifica los paquetes de un emisor: Opus en producción (codec.go), un fake en
// los tests (así jitter_test corre sin CGO). Las tres usan cap(pcm) como duración a generar, así
// que siempre se les pasa exactamente un frame.
type frameDecoder interface {
	Decode(packet []byte, pcm []int16) (int, error)
	DecodeFEC(packet []byte, pcm []int16) error
	DecodePLC(pcm []int16) error
}

type jitterFrameKind int

const (
	jitterFrameSilence jitterFrameKind = iota
	jitterFrameDecoded                 // salida del decoder (real, FEC o PLC): continua con la anterior
	jitterFrameFade                    // PLC que se va apagando porque el buffer se vació
)

type jitterSlot struct {
	seq       uint32
	talkspurt uint8 // frase del emisor (Packet.Talkspurt)
	present   bool
	data      []byte // paquete codificado tal como llegó
}

// JitterStats cuenta lo que hizo el jitter buffer de un emisor y lo que mide de la red.
type JitterStats struct {
	Played    uint64 // frames decodificados de su propio paquete
	Recovered uint64 // frames perdidos rearmados con el FEC del paquete siguiente (o su PLC si no traía)
	Concealed uint64 // frames perdidos ocultados con PLC o silencio (sin paquete siguiente)
	Late      uint64 // paquetes que llegaron después de su turno y se descartaron
	Dropped   uint64 // frames descartados para corregir deriva (buffer demasiado lleno)
	Resets    uint64 // saltos de secuencia enormes (emisor reiniciado)

	// Underruns son los cortes: el buffer se vació en medio de una frase (el siguiente frame que
	// llegó es de la misma frase, o sea que el emisor seguía mandando) y el audio se interrumpió
	// hasta que llegó. UnderrunTime suma lo que duraron. Phrases cuenta los arranques de frase
	// nueva (pausas del emisor, no son cortes).
	Underruns    uint64
	UnderrunTime time.Duration
	Phrases      uint64

	BufferedFrames int     // frames esperando en el buffer
	TargetMS       int     // colchón objetivo vigente (adaptativo, tope JitterBufferMS)
	JitterMS       float64 // jitter de llegada RFC 3550: J += (|D| - J) / 16
	DelayP95MS     int     // percentil de variación de retardo que cubre el colchón; -1 sin datos
}

// Lost es la cantidad de frames que no llegaron a tiempo (recuperados u ocultados).
func (s JitterStats) Lost() uint64 { return s.Recovered + s.Concealed }

// Total es la cantidad de frames que tocaba reproducir.
func (s JitterStats) Total() uint64 { return s.Played + s.Lost() }

// jitterBuffer reordena los paquetes de un emisor por su secuencia de audio, los decodifica al
// momento de reproducirlos y los entrega a ritmo constante. Si falta un frame:
//   - con frames posteriores ya recibidos es una pérdida: se recupera con el FEC del paquete
//     siguiente, o con el PLC del decoder, y se avanza la secuencia;
//   - con el buffer vacío es un pico de lag o el fin de una frase: PLC que se apaga en ~60 ms y
//     NO se avanza la secuencia, así el frame demorado se reproduce cuando llegue. Si es de la
//     misma frase (corte) se reanuda enseguida; si es de una frase nueva se espera el colchón.
//
// Todo empalme no contiguo (arranque, descarte por deriva) se suaviza con un crossfade corto:
// para eso la salida retiene las últimas JitterSpliceSamples muestras de cada frame.
// No es seguro para uso concurrente: el Engine lo protege con playbackMu. Corre dentro del
// callback de malgo, así que no asigna memoria en Read (buffers preasignados).
type jitterBuffer struct {
	slots        [jitterRingFrames]jitterSlot
	dec          frameDecoder
	started      bool
	playing      bool   // false = esperando colchón (arranque de frase o underrun)
	playSeq      uint32 // próximo seq a reproducir
	highestSeq   uint32 // seq más alto recibido
	tuning       JitterTuning
	maxFrames    int // tope del colchón (tuning.MaxMS)
	minFrames    int // piso del colchón (tuning.MinMS)
	targetFrames int // colchón vigente: solo cambia con !playing para que no haya saltos

	firstArrival  time.Time // llegada del primer frame mientras se espera colchón
	lastRealAt    time.Time // cuándo arrancó el último frame real reproducido
	lastTalkspurt uint8     // frase del último frame real reproducido
	emptyAt       time.Time // cuándo se vació el buffer (mide la duración del corte)

	lossRun       int
	dropCooldown  int
	discontinuity bool // hubo descarte o reset: el próximo frame no empalma con el anterior
	prevKind      jitterFrameKind
	fadePos       int // muestras de PLC ya generadas desde que se vació el buffer

	decodeBuf  [FrameSamples]int16
	silence    [FrameSamples]int16
	frame      []int16 // frame de trabajo armado por nextFrame
	tail       []int16 // cola retenida del frame anterior para el crossfade
	pending    []int16 // muestras listas para salir
	pendingPos int

	arrival arrivalStats
	stats   JitterStats
}

// newJitterBuffer crea el buffer de un emisor con los parámetros de fábrica y tope maxMS.
func newJitterBuffer(maxMS int, dec frameDecoder) *jitterBuffer {
	tuning := DefaultJitterTuning()
	tuning.MaxMS = maxMS
	return newJitterBufferTuned(tuning, dec)
}

func newJitterBufferTuned(tuning JitterTuning, dec frameDecoder) *jitterBuffer {
	j := &jitterBuffer{
		dec:     dec,
		tail:    make([]int16, 0, config.JitterSpliceSamples),
		pending: make([]int16, 0, FrameSamples+config.JitterSpliceSamples),
	}
	j.setTuning(tuning)
	return j
}

// setTuning aplica los parámetros (el Engine lo llama en cada callback; sin cambios no hace
// nada). El colchón real lo elige adaptiveTarget al arrancar cada frase, entre el piso y el tope.
func (j *jitterBuffer) setTuning(tuning JitterTuning) {
	if tuning == j.tuning {
		return
	}
	j.tuning = tuning
	j.maxFrames = min(max(tuning.MaxMS/20, 1), jitterRingFrames/2)
	j.minFrames = min(max(tuning.MinMS/20, 1), j.maxFrames)
	if j.targetFrames == 0 || j.targetFrames > j.maxFrames {
		j.targetFrames = j.maxFrames
	}
}

// reset vacía el ring y vuelve a esperar colchón desde seq. No toca la cola retenida, así la
// salida se empalma sin clic.
func (j *jitterBuffer) reset(seq uint32) {
	for i := range j.slots {
		j.slots[i].present = false
	}
	j.started = true
	j.playing = false
	j.playSeq = seq
	j.highestSeq = seq - 1
	j.firstArrival = time.Time{}
	j.lastRealAt = time.Time{} // lo que venga después de un reinicio es una frase nueva
	j.lossRun = 0
	j.dropCooldown = 0
	j.discontinuity = true
}

// Push guarda un paquete recibido con su secuencia de audio y su número de frase (talkspurt).
func (j *jitterBuffer) Push(seq uint32, talkspurt uint8, packet []byte, now time.Time) {
	if len(packet) == 0 {
		return
	}
	j.arrival.observe(seq, talkspurt, now)
	if !j.started {
		j.reset(seq)
	}
	diff := int32(seq - j.playSeq)
	if diff < -jitterRingFrames || diff >= jitterRingFrames {
		// Salto enorme de secuencia: el emisor se reinició o hubo un corte muy largo.
		j.reset(seq)
		j.stats.Resets++
		diff = 0
	}
	if diff < 0 {
		j.stats.Late++
		return
	}
	slot := &j.slots[seq%jitterRingFrames]
	if slot.present && slot.seq == seq {
		return // duplicado
	}
	slot.seq = seq
	slot.talkspurt = talkspurt
	slot.present = true
	slot.data = append(slot.data[:0], packet...)
	if int32(seq-j.highestSeq) > 0 {
		j.highestSeq = seq
	}
	if !j.playing && j.firstArrival.IsZero() {
		j.firstArrival = now
	}
}

// Read llena dst con el audio del emisor y devuelve si hubo alguna muestra distinta de cero.
func (j *jitterBuffer) Read(dst []int16, now time.Time) bool {
	active := false
	for i := range dst {
		if j.pendingPos >= len(j.pending) {
			j.advance(now)
		}
		sample := j.pending[j.pendingPos]
		j.pendingPos++
		if sample != 0 {
			active = true
		}
		dst[i] = sample
	}
	return active
}

// Stats devuelve los contadores acumulados y el estado de la medición de red.
func (j *jitterBuffer) Stats() JitterStats {
	stats := j.stats
	if j.started {
		for seq := j.playSeq; int32(j.highestSeq-seq) >= 0; seq++ {
			if slot := &j.slots[seq%jitterRingFrames]; slot.present && slot.seq == seq {
				stats.BufferedFrames++
			}
		}
	}
	stats.TargetMS = j.targetFrames * 20
	stats.JitterMS = j.arrival.jitter / float64(time.Millisecond)
	stats.DelayP95MS = j.arrival.percentileMS(j.tuning.Percentile)
	return stats
}

// adaptiveTarget elige el colchón para la próxima frase: lo justo para cubrir el percentil de
// variación de retardo medido, más un margen, entre el piso y el tope. Sin datos suficientes,
// el tope (conservador).
func (j *jitterBuffer) adaptiveTarget() int {
	if j.arrival.windowCount < config.JitterAdaptiveMinSamples {
		return j.maxFrames
	}
	p := j.arrival.percentileMS(j.tuning.Percentile)
	// Picos repetidos (Wi-Fi): el percentil los deja afuera porque cada uno toca pocos paquetes,
	// pero cada uno que el colchón no cubre es un corte. Si se repiten, se cubre el más alto.
	if peak, ok := j.arrival.recentPeakMS(); ok {
		p = max(p, peak)
	}
	frames := (p+19)/20 + j.tuning.MarginFrames
	return min(max(frames, j.minFrames), j.maxFrames)
}

// advance arma el próximo frame y lo pasa a pending, empalmándolo con la cola del anterior.
// Un frame contiguo sale intacto; uno no contiguo arranca con un crossfade desde la cola
// retenida (y por eso sale JitterSpliceSamples más corto).
func (j *jitterBuffer) advance(now time.Time) {
	kind := j.nextFrame(now)
	if kind == jitterFrameDecoded {
		j.fadePos = 0
	}
	frame := j.frame
	splice := config.JitterSpliceSamples
	// Lo que sale del decoder (real, FEC, PLC y el PLC del fade) es continuo con lo anterior del
	// decoder, y el fade es continuo consigo mismo (la ganancia baja de corrido entre frames). El
	// silencio es continuo con el fade (que termina en cero) y con otro silencio. Un frame real
	// después del fade lleva crossfade: el fade quedó atenuado y el decoder sale a nivel normal.
	contiguous := !j.discontinuity &&
		((kind == jitterFrameDecoded && j.prevKind == jitterFrameDecoded) ||
			(kind == jitterFrameFade && j.prevKind != jitterFrameSilence) ||
			(kind == jitterFrameSilence && j.prevKind != jitterFrameDecoded))
	j.discontinuity = false

	j.pending = j.pending[:0]
	j.pendingPos = 0
	if len(frame) < 2*splice {
		// Frame demasiado corto para retener cola: sale tal cual.
		j.pending = append(j.pending, j.tail...)
		j.pending = append(j.pending, frame...)
		j.tail = j.tail[:0]
	} else {
		if contiguous || len(j.tail) == 0 {
			j.pending = append(j.pending, j.tail...)
			j.pending = append(j.pending, frame[:len(frame)-splice]...)
		} else {
			n := len(j.tail)
			for i := 0; i < n; i++ {
				w := float64(i+1) / float64(n+1)
				j.pending = append(j.pending, int16(float64(j.tail[i])*(1-w)+float64(frame[i])*w))
			}
			j.pending = append(j.pending, frame[n:len(frame)-splice]...)
		}
		j.tail = append(j.tail[:0], frame[len(frame)-splice:]...)
	}
	j.prevKind = kind
}

// nextFrame decide qué suena en los próximos 20 ms y lo deja en j.frame.
func (j *jitterBuffer) nextFrame(now time.Time) jitterFrameKind {
	if !j.started {
		return j.silenceOrFade()
	}
	if !j.playing {
		// El colchón cambia solo acá (arranque de frase o underrun): sin saltos audibles.
		j.targetFrames = j.adaptiveTarget()
		first, ok := j.firstPresent()
		if !ok || !j.readyToPlay(first, now) {
			return j.silenceOrFade()
		}
		j.countRestart(first, now)
		j.playSeq = first
		j.playing = true
		j.firstArrival = time.Time{}
	}
	return j.playFrame(now, true)
}

// playFrame reproduce playSeq. allowDrop es false en la segunda pasada después de un descarte,
// así hay como mucho un descarte por frame.
func (j *jitterBuffer) playFrame(now time.Time, allowDrop bool) jitterFrameKind {
	if allowDrop && j.dropCooldown > 0 {
		j.dropCooldown--
	}
	// Deriva: si el buffer quedó por encima del objetivo (ráfaga, o el objetivo bajó), hay que
	// descartar un frame. Se posterga hasta uno de baja energía, salvo que el exceso sea grande.
	span := int(int32(j.highestSeq-j.playSeq)) + 1
	over := allowDrop && j.dropCooldown == 0 && span > j.targetFrames+j.tuning.DriftSlackFrames
	force := span > j.targetFrames+j.tuning.DriftSlackFrames+j.tuning.DriftForceFrames

	slot := &j.slots[j.playSeq%jitterRingFrames]
	if slot.present && slot.seq == j.playSeq {
		slot.present = false
		if j.decode(slot.data) {
			if over && (force || frameRMS(j.frame) < config.JitterDriftQuietRMS) {
				// Se decodificó igual (el decoder no pierde continuidad) y se tira en un momento
				// de baja energía, donde el salto no se nota.
				j.playSeq++
				j.stats.Dropped++
				j.dropCooldown = config.JitterDriftCooldownFrames
				j.discontinuity = true
				return j.playFrame(now, false)
			}
			j.playSeq++
			j.lossRun = 0
			j.lastRealAt = now
			j.lastTalkspurt = slot.talkspurt
			j.stats.Played++
			return jitterFrameDecoded
		}
		// Paquete corrupto: se trata como perdido.
	}

	if int32(j.highestSeq-j.playSeq) > 0 {
		// Hay frames posteriores: este se perdió (o llegará tarde).
		if over {
			// Con el buffer pasado del objetivo, el hueco no se oculta: se saltea y la latencia
			// baja sin cortar nada que se haya recibido.
			j.playSeq++
			j.stats.Concealed++
			j.stats.Dropped++
			j.dropCooldown = config.JitterDriftCooldownFrames
			j.discontinuity = true
			return j.playFrame(now, false)
		}
		j.playSeq++
		j.lossRun++
		if j.lossRun <= j.tuning.PLCMaxFrames {
			pcm := j.decodeBuf[:]
			if next := &j.slots[j.playSeq%jitterRingFrames]; next.present && next.seq == j.playSeq {
				if j.dec.DecodeFEC(next.data, pcm) == nil {
					j.frame = pcm
					j.stats.Recovered++
					return jitterFrameDecoded
				}
			}
			if j.dec.DecodePLC(pcm) == nil {
				j.frame = pcm
				j.stats.Concealed++
				return jitterFrameDecoded
			}
		}
		j.stats.Concealed++
		j.frame = j.silence[:]
		return jitterFrameSilence
	}

	// Buffer vacío: pico de lag o fin de frase. Esperamos sin avanzar playSeq.
	j.playing = false
	j.firstArrival = time.Time{}
	j.emptyAt = now
	return j.silenceOrFade()
}

// countRestart clasifica cada vuelta a reproducir: si el primer frame disponible es de la misma
// frase que el último reproducido, el emisor nunca dejó de mandar y el buffer se vació porque el
// audio no llegó a tiempo (un corte); si no, el emisor volvió a hablar después de una pausa.
func (j *jitterBuffer) countRestart(first uint32, now time.Time) {
	if j.samePhrase(first) {
		j.stats.Underruns++
		j.stats.UnderrunTime += now.Sub(j.emptyAt)
		return
	}
	j.stats.Phrases++
}

// samePhrase indica si el frame first es de la frase que se venía reproduciendo. El emisor
// cambia de frase cada vez que deja de mandar más de 60 ms (talkspurtGap en ui), así que misma
// frase = no hubo pausa del emisor.
func (j *jitterBuffer) samePhrase(first uint32) bool {
	return !j.lastRealAt.IsZero() && j.slots[first%jitterRingFrames].talkspurt == j.lastTalkspurt
}

// decode decodifica un paquete en decodeBuf y lo deja en j.frame.
func (j *jitterBuffer) decode(packet []byte) bool {
	pcm := j.decodeBuf[:]
	n, err := j.dec.Decode(packet, pcm)
	if err != nil || n <= 0 {
		return false
	}
	j.frame = pcm[:n]
	return true
}

// readyToPlay decide si ya hay colchón suficiente para (re)arrancar.
func (j *jitterBuffer) readyToPlay(first uint32, now time.Time) bool {
	// Corte en medio de una frase (el emisor siguió mandando): reanudamos apenas llega el frame
	// demorado. Lo atrasado suele llegar todo junto y hace de colchón; la deriva recorta el
	// exceso después. Un corte más largo que TalkspurtGap vuelve a esperar el colchón.
	if j.samePhrase(first) && now.Sub(j.lastRealAt) < j.tuning.TalkspurtGap {
		return true
	}
	// Frase nueva (el emisor hizo una pausa, aunque sea corta): esperamos el colchón completo, o
	// su duración si la frase es más corta. La espera cae en la pausa, donde no se nota. Sin
	// esto cada frase arrancaba solo con el pre-roll (~40 ms) y el primer pico la cortaba.
	span := int(int32(j.highestSeq-first)) + 1
	return span >= j.targetFrames || now.Sub(j.firstArrival) >= time.Duration(j.targetFrames)*jitterFrameDuration
}

// firstPresent busca el primer frame recibido a partir de playSeq.
func (j *jitterBuffer) firstPresent() (uint32, bool) {
	for seq := j.playSeq; int32(j.highestSeq-seq) >= 0; seq++ {
		slot := &j.slots[seq%jitterRingFrames]
		if slot.present && slot.seq == seq {
			return seq, true
		}
	}
	return 0, false
}

// silenceOrFade: después de audio decodificado, el PLC del decoder sigue la voz bajando de a poco
// hasta silencio en JitterFadeOutSamples (puede abarcar varios frames); después, silencio. Si el
// frame demorado llega en el medio, la voz vuelve con un crossfade y el pico casi no se nota.
func (j *jitterBuffer) silenceOrFade() jitterFrameKind {
	fade := config.JitterFadeOutSamples
	if j.prevKind == jitterFrameSilence || j.fadePos >= fade {
		j.frame = j.silence[:]
		return jitterFrameSilence
	}
	pcm := j.decodeBuf[:]
	if j.dec.DecodePLC(pcm) != nil {
		clear(pcm)
	}
	for i := range pcm {
		pos := j.fadePos + i
		if pos >= fade {
			pcm[i] = 0
			continue
		}
		pcm[i] = int16(float64(pcm[i]) * (1 - float64(pos)/float64(fade)))
	}
	j.fadePos += len(pcm)
	j.frame = pcm
	return jitterFrameFade
}

// frameRMS es el nivel RMS del frame en escala S16.
func frameRMS(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// arrivalStats mide la red a partir de los tiempos de llegada de un emisor.
//   - jitter: estimador RFC 3550 sobre llegadas consecutivas (D = llegada real − esperada).
//   - Variación de retardo: dentro de cada frase, offset = llegada − seq·20 ms (relativo al
//     primer paquete de la frase) menos el mínimo visto en la frase. Es lo que el colchón tiene
//     que absorber. Va a un histograma sobre una ventana deslizante para sacar el percentil.
//
// La frase se reinicia cuando cambia el talkspurt del paquete: el emisor no manda nada con la
// compuerta cerrada pero la secuencia sigue contigua, y sin ese corte el silencio se mediría
// como retardo.
//
// Además agrupa en picos las llegadas consecutivas con variación ≥ JitterPeakThresholdMS, para
// que el colchón cubra los picos que se repiten aunque el percentil no los vea.
type arrivalStats struct {
	have      bool
	talkspurt uint8
	lastAt    time.Time
	lastSeq   uint32
	baseAt    time.Time
	baseSeq   uint32
	minOffset time.Duration
	jitter    float64 // ns

	hist        [jitterHistogramBuckets]uint16
	window      [config.JitterAdaptiveWindow]uint8
	windowPos   int
	windowCount int

	samples uint64 // llegadas registradas: fecha los picos (JitterPeakMemory)
	inPeak  bool
	peaks   [jitterPeakEvents]jitterPeak
	peakPos int
}

// jitterPeak es un pico de retardo: cuándo empezó (número de llegada) y su altura.
type jitterPeak struct {
	sample uint64
	height time.Duration
}

func (s *arrivalStats) observe(seq uint32, talkspurt uint8, now time.Time) {
	if !s.have || talkspurt != s.talkspurt || now.Sub(s.lastAt) > time.Second {
		s.have = true
		s.talkspurt = talkspurt
		s.baseAt, s.baseSeq, s.minOffset = now, seq, 0
		s.lastAt, s.lastSeq = now, seq
		return
	}
	if dseq := int32(seq - s.lastSeq); dseq != 0 {
		d := now.Sub(s.lastAt) - time.Duration(dseq)*jitterFrameDuration
		if d < 0 {
			d = -d
		}
		s.jitter += (float64(d) - s.jitter) / 16
	}
	s.lastAt, s.lastSeq = now, seq

	offset := now.Sub(s.baseAt) - time.Duration(int32(seq-s.baseSeq))*jitterFrameDuration
	if offset < s.minOffset {
		s.minOffset = offset
	}
	s.record(offset - s.minOffset)
}

func (s *arrivalStats) record(delay time.Duration) {
	bucket := int(delay / (config.JitterHistogramBucketMS * time.Millisecond))
	if bucket < 0 {
		bucket = 0
	} else if bucket >= jitterHistogramBuckets {
		bucket = jitterHistogramBuckets - 1
	}
	if s.windowCount == len(s.window) {
		s.hist[s.window[s.windowPos]]--
	} else {
		s.windowCount++
	}
	s.window[s.windowPos] = uint8(bucket)
	s.hist[bucket]++
	s.windowPos = (s.windowPos + 1) % len(s.window)
	s.samples++
	s.trackPeak(delay)
}

// trackPeak junta las llegadas consecutivas por encima del umbral en un solo pico y guarda su
// altura máxima.
func (s *arrivalStats) trackPeak(delay time.Duration) {
	if delay < config.JitterPeakThresholdMS*time.Millisecond {
		s.inPeak = false
		return
	}
	if !s.inPeak {
		s.inPeak = true
		s.peakPos = (s.peakPos + 1) % len(s.peaks)
		s.peaks[s.peakPos] = jitterPeak{sample: s.samples, height: delay}
		return
	}
	if peak := &s.peaks[s.peakPos]; delay > peak.height {
		peak.height = delay
	}
}

// recentPeakMS devuelve la altura en ms del pico más alto de las últimas JitterPeakMemory
// llegadas, si en ellas hubo al menos JitterPeakMinCount picos.
func (s *arrivalStats) recentPeakMS() (int, bool) {
	count := 0
	var highest time.Duration
	for _, peak := range s.peaks {
		if peak.sample == 0 || s.samples-peak.sample >= config.JitterPeakMemory {
			continue
		}
		count++
		highest = max(highest, peak.height)
	}
	if count < config.JitterPeakMinCount {
		return 0, false
	}
	return int(highest / time.Millisecond), true
}

// percentileMS devuelve el borde superior (ms) del bucket donde cae el percentil p, o -1 sin datos.
func (s *arrivalStats) percentileMS(p float64) int {
	if s.windowCount == 0 {
		return -1
	}
	need := int(math.Ceil(p * float64(s.windowCount)))
	acc := 0
	for i, count := range s.hist {
		acc += int(count)
		if acc >= need {
			return (i + 1) * config.JitterHistogramBucketMS
		}
	}
	return jitterHistogramBuckets * config.JitterHistogramBucketMS
}
