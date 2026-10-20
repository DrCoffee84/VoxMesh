package audio

import (
	"encoding/binary"
	"time"

	"voxmesh/internal/config"
)

// jitterRingFrames es la capacidad del ring de frames (1.28 s a 20 ms por frame).
const jitterRingFrames = 64

type jitterFrameKind int

const (
	jitterFrameSilence jitterFrameKind = iota
	jitterFrameReal
	jitterFramePLC
)

type jitterSlot struct {
	seq     uint32
	present bool
	pcm     []int16
}

// JitterStats cuenta lo que hizo el jitter buffer de un emisor.
type JitterStats struct {
	Played    uint64 // frames reales reproducidos
	Concealed uint64 // frames perdidos ocultados con PLC o silencio
	Late      uint64 // paquetes que llegaron después de su turno y se descartaron
	Dropped   uint64 // frames descartados para corregir deriva (buffer demasiado lleno)
	Resets    uint64 // saltos de secuencia enormes (emisor reiniciado)
}

// jitterBuffer reordena los frames de un emisor por su secuencia de audio y los
// entrega a ritmo constante. Si falta un frame:
//   - con frames posteriores ya recibidos es una pérdida: se oculta repitiendo el
//     último frame real con atenuación progresiva (PLC) y se avanza la secuencia;
//   - con el buffer vacío es un pico de lag o el fin de una frase: se hace un fade a
//     silencio y NO se avanza la secuencia, así el frame demorado se reproduce cuando llegue.
//
// Todo empalme entre frames no contiguos se suaviza con un crossfade corto: para eso
// la salida retiene las últimas JitterSpliceSamples muestras de cada frame.
// No es seguro para uso concurrente: el Engine lo protege con playbackMu.
type jitterBuffer struct {
	slots        [jitterRingFrames]jitterSlot
	started      bool
	playing      bool   // false = esperando colchón (arranque de frase o underrun)
	playSeq      uint32 // próximo seq a reproducir
	highestSeq   uint32 // seq más alto recibido
	targetFrames int

	firstArrival time.Time // llegada del primer frame mientras se espera colchón
	lastRealAt   time.Time // cuándo arrancó el último frame real reproducido

	last         []int16 // último frame real, base del PLC
	plcGain      float64 // ganancia con la que terminó el último frame emitido
	lossRun      int
	fadedOut     bool
	dropCooldown int

	prevKind jitterFrameKind
	prevSeq  uint32

	frame      []int16 // frame de trabajo armado por nextFrame
	tail       []int16 // cola retenida del frame anterior para el crossfade
	pending    []int16 // muestras listas para salir
	pendingPos int

	stats JitterStats
}

func newJitterBuffer(targetMS int) *jitterBuffer {
	j := &jitterBuffer{plcGain: 1}
	j.setTargetMS(targetMS)
	return j
}

func (j *jitterBuffer) setTargetMS(ms int) {
	frames := ms / 20
	if frames < 2 {
		frames = 2
	}
	if frames > jitterRingFrames/2 {
		frames = jitterRingFrames / 2
	}
	j.targetFrames = frames
}

// reset vacía el ring y vuelve a esperar colchón desde seq. No toca la cola ni el
// último frame, así la salida se empalma sin clic.
func (j *jitterBuffer) reset(seq uint32) {
	for i := range j.slots {
		j.slots[i].present = false
	}
	j.started = true
	j.playing = false
	j.playSeq = seq
	j.highestSeq = seq - 1
	j.firstArrival = time.Time{}
	j.lossRun = 0
	j.dropCooldown = 0
}

// Push guarda un frame S16LE recibido con su secuencia de audio.
func (j *jitterBuffer) Push(seq uint32, pcm []byte, now time.Time) {
	if len(pcm) < 2 {
		return
	}
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
	slot.present = true
	slot.pcm = slot.pcm[:0]
	for i := 0; i+1 < len(pcm); i += 2 {
		slot.pcm = append(slot.pcm, int16(binary.LittleEndian.Uint16(pcm[i:])))
	}
	if int32(seq-j.highestSeq) > 0 {
		j.highestSeq = seq
	}
	if !j.playing && j.firstArrival.IsZero() {
		j.firstArrival = now
	}
}

// Read llena dst (S16LE) con el audio del emisor y devuelve si hubo alguna muestra
// distinta de cero.
func (j *jitterBuffer) Read(dst []byte, now time.Time) bool {
	active := false
	for i := 0; i+1 < len(dst); i += 2 {
		if j.pendingPos >= len(j.pending) {
			j.advance(now)
		}
		sample := j.pending[j.pendingPos]
		j.pendingPos++
		if sample != 0 {
			active = true
		}
		binary.LittleEndian.PutUint16(dst[i:], uint16(sample))
	}
	return active
}

// Stats devuelve los contadores acumulados.
func (j *jitterBuffer) Stats() JitterStats {
	return j.stats
}

// advance arma el próximo frame y lo pasa a pending, empalmándolo con la cola del
// anterior. Un frame contiguo sale intacto; uno no contiguo arranca con un crossfade
// desde la cola retenida (y por eso sale JitterSpliceSamples más corto).
func (j *jitterBuffer) advance(now time.Time) {
	kind, seq := j.nextFrame(now)
	frame := j.frame
	splice := config.JitterSpliceSamples
	contiguous := (kind == jitterFrameReal && j.prevKind == jitterFrameReal && seq == j.prevSeq+1) ||
		(kind == jitterFrameSilence && j.prevKind == jitterFrameSilence)

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
	j.prevKind, j.prevSeq = kind, seq
}

// nextFrame decide qué suena en los próximos 20 ms y lo deja en j.frame.
func (j *jitterBuffer) nextFrame(now time.Time) (jitterFrameKind, uint32) {
	if !j.started {
		return j.silenceOrFade()
	}
	if !j.playing {
		first, ok := j.firstPresent()
		if !ok || !j.readyToPlay(first, now) {
			return j.silenceOrFade()
		}
		j.playSeq = first
		j.playing = true
		j.firstArrival = time.Time{}
	}

	// Deriva: si el buffer quedó muy por encima del objetivo (por ejemplo tras una
	// ráfaga), descartamos un frame cada tanto en lugar de cortar de golpe.
	if j.dropCooldown > 0 {
		j.dropCooldown--
	}
	if span := int(int32(j.highestSeq-j.playSeq)) + 1; span > j.targetFrames+config.JitterDriftSlackFrames && j.dropCooldown == 0 {
		if slot := &j.slots[j.playSeq%jitterRingFrames]; slot.present && slot.seq == j.playSeq {
			slot.present = false
			j.stats.Dropped++
		}
		j.playSeq++
		j.dropCooldown = config.JitterDriftCooldownFrames
	}

	slot := &j.slots[j.playSeq%jitterRingFrames]
	if slot.present && slot.seq == j.playSeq {
		slot.present = false
		j.frame = append(j.frame[:0], slot.pcm...)
		j.last = append(j.last[:0], slot.pcm...)
		seq := j.playSeq
		j.playSeq++
		j.lossRun = 0
		j.fadedOut = false
		j.plcGain = 1
		j.lastRealAt = now
		j.stats.Played++
		return jitterFrameReal, seq
	}

	if int32(j.highestSeq-j.playSeq) > 0 {
		// Hay frames posteriores: este se perdió (o llegará tarde). Lo ocultamos y seguimos.
		j.playSeq++
		j.lossRun++
		j.stats.Concealed++
		if j.lossRun <= config.JitterPLCMaxFrames && len(j.last) > 0 {
			from := j.plcGain
			j.plcGain *= config.JitterPLCAttenuation
			j.fillFromLast(from, j.plcGain, len(j.last))
			return jitterFramePLC, 0
		}
		j.plcGain = 0
		j.fillSilence()
		return jitterFrameSilence, 0
	}

	// Buffer vacío: pico de lag o fin de frase. Esperamos sin avanzar playSeq.
	j.playing = false
	j.firstArrival = time.Time{}
	return j.silenceOrFade()
}

// readyToPlay decide si ya hay colchón suficiente para (re)arrancar.
func (j *jitterBuffer) readyToPlay(first uint32, now time.Time) bool {
	// Underrun en medio de una frase: reanudamos apenas llega el frame demorado.
	// La latencia crece lo que duró el pico y la deriva la recorta después.
	if !j.lastRealAt.IsZero() && now.Sub(j.lastRealAt) < config.JitterTalkspurtGapMS*time.Millisecond {
		return true
	}
	// Frase nueva: esperamos el colchón completo, o su duración si la frase es más corta.
	span := int(int32(j.highestSeq-first)) + 1
	return span >= j.targetFrames || now.Sub(j.firstArrival) >= time.Duration(j.targetFrames)*20*time.Millisecond
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

// silenceOrFade hace un único fade a silencio desde el último frame y después silencio.
func (j *jitterBuffer) silenceOrFade() (jitterFrameKind, uint32) {
	if !j.fadedOut && len(j.last) > 0 && j.prevKind != jitterFrameSilence && j.plcGain > 0 {
		j.fadedOut = true
		j.fillFromLast(j.plcGain, 0, config.JitterFadeOutSamples)
		j.plcGain = 0
		return jitterFramePLC, 0
	}
	j.fillSilence()
	return jitterFrameSilence, 0
}

// fillFromLast copia el último frame real con una rampa de ganancia de from a to
// en las primeras rampLen muestras (el resto queda en to).
func (j *jitterBuffer) fillFromLast(from, to float64, rampLen int) {
	j.frame = j.frame[:0]
	for i, sample := range j.last {
		gain := to
		if i < rampLen {
			gain = from + (to-from)*float64(i)/float64(rampLen)
		}
		j.frame = append(j.frame, int16(float64(sample)*gain))
	}
}

func (j *jitterBuffer) fillSilence() {
	frameLen := len(j.last)
	if frameLen == 0 {
		frameLen = FrameSamples
	}
	j.frame = j.frame[:0]
	for i := 0; i < frameLen; i++ {
		j.frame = append(j.frame, 0)
	}
}
