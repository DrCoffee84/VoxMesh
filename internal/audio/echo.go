package audio

import "sync/atomic"

// Referencia de la cancelación de eco: lo que sonó por los parlantes (la salida de Engine.mix ya
// limitada), copiado por el callback de playback y leído por quien procesa la captura. Sin cgo:
// la parte que corre en el callback y la alineación se prueban sin WebRTC.

// echoBlock es lo que procesa AEC3 de una vez: 10 ms.
const echoBlock = SampleRate / 100

const (
	// echoRefSamples es el tamaño del ring (~1.36 s, potencia de 2).
	echoRefSamples = 1 << 16
	// echoRefMarks son los callbacks de playback que se recuerdan con su hora (medio ring cubre
	// 1.5 s aun con períodos de 3 ms).
	echoRefMarks = 1024
	// echoMaxRenderBlocks es lo máximo de referencia que se le pasa al AEC antes de un frame
	// (320 ms). Más es que la goroutine de envío se atrasó mucho: lo viejo se saltea.
	echoMaxRenderBlocks = 32
)

// echoReference es un ring SPSC: escribe solo el callback de playback (put + publish, sin
// asignaciones ni locks) y lee solo el echoReader del que cancela el eco. Cada publish deja una
// marca (fin, hora) para que el lector tome lo que ya había sonado cuando se capturó un frame.
type echoReference struct {
	samples [echoRefSamples]int16
	markEnd [echoRefMarks]atomic.Uint64 // muestras escritas al terminar ese callback
	markAt  [echoRefMarks]atomic.Int64  // hora del callback (UnixNano)
	marks   atomic.Uint64               // marcas publicadas
	// released es hasta dónde leyó el lector. El escritor lo carga al empezar cada callback
	// (begin): así reusar un lugar del ring viene después de que el lector lo soltó (modelo de
	// memoria de Go). El escritor nunca espera: si el lector se atrasa más que el ring, lo saltea
	// él (take).
	released atomic.Uint64
	written  uint64 // solo el escritor
}

// begin empieza un callback del escritor, antes del primer put.
func (r *echoReference) begin() {
	r.released.Load()
}

// put escribe la muestra i del callback en curso (todavía no visible para el lector).
func (r *echoReference) put(i int, sample int16) {
	r.samples[(r.written+uint64(i))&(echoRefSamples-1)] = sample
}

// publish hace visibles las n muestras escritas con put en el callback de la hora at.
func (r *echoReference) publish(n int, at int64) {
	r.written += uint64(n)
	m := r.marks.Load()
	r.markEnd[m%echoRefMarks].Store(r.written)
	r.markAt[m%echoRefMarks].Store(at)
	r.marks.Store(m + 1)
}

// echoReader lee el ring del lado de la captura. Todo lo suyo es preasignado.
type echoReader struct {
	pos     uint64 // próxima muestra a pasarle al AEC
	mark    uint64 // próxima marca a mirar
	limit   uint64 // fin de lo que había sonado hasta la última captura
	render  [echoMaxRenderBlocks * echoBlock]int16
	skipped uint64 // muestras salteadas por atraso (acumulado)
}

// reset arranca desde lo último publicado: la referencia vieja no corresponde a esta captura.
func (r *echoReader) reset(ref *echoReference) {
	m := ref.marks.Load()
	r.mark, r.pos = m, 0
	if m > 0 {
		r.pos = ref.markEnd[(m-1)%echoRefMarks].Load()
	}
	r.limit = r.pos
	ref.released.Store(r.pos)
}

// take devuelve los bloques enteros de 10 ms de referencia que habían sonado (callback con hora
// ≤ at) y que el AEC todavía no recibió. Lo que no completa un bloque queda para la próxima.
// El slice vale hasta la próxima llamada.
func (r *echoReader) take(ref *echoReference, at int64) []int16 {
	m := ref.marks.Load()
	if m-r.mark > echoRefMarks/2 {
		// Atrasado más de medio ring de marcas: las más viejas se están por pisar.
		r.mark = m - echoRefMarks/2
	}
	for ; r.mark < m; r.mark++ {
		i := r.mark % echoRefMarks
		if ref.markAt[i].Load() > at {
			break
		}
		r.limit = ref.markEnd[i].Load()
	}
	if r.pos > r.limit {
		r.pos = r.limit
	}
	n := r.limit - r.pos
	if n > uint64(len(r.render)) {
		r.skipped += n - uint64(len(r.render))
		r.pos = r.limit - uint64(len(r.render))
		n = uint64(len(r.render))
	}
	n -= n % echoBlock
	for i := range n {
		r.render[i] = ref.samples[(r.pos+i)&(echoRefSamples-1)]
	}
	r.pos += n
	ref.released.Store(r.pos)
	return r.render[:n]
}
