package audio

import (
	"bufio"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// ArrivalRecorder graba la llegada de cada paquete de audio de red (emisor, secuencia, frase y
// momento) en un CSV, para reproducir después la red real por el jitter buffer
// (TestJitterReplayArrivals en jitter_sim_test.go). Record nunca bloquea al receptor: si el
// escritor se atrasa, la llegada se pierde y se cuenta.
type ArrivalRecorder struct {
	path      string
	start     time.Time
	lines     chan arrivalRecord
	stop      chan struct{}
	done      chan error
	dropped   atomic.Uint64
	closeOnce sync.Once
	closeErr  error
}

type arrivalRecord struct {
	sender    string
	seq       uint32
	talkspurt uint8
	at        time.Time
}

// arrivalQueue son las llegadas que pueden esperar al escritor (~10 s con 3 emisores).
const arrivalQueue = 1500

// NewArrivalRecorder crea (o vacía) el archivo y arranca el escritor.
func NewArrivalRecorder(path string) (*ArrivalRecorder, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	r := &ArrivalRecorder{
		path:  path,
		start: time.Now(),
		lines: make(chan arrivalRecord, arrivalQueue),
		stop:  make(chan struct{}),
		done:  make(chan error, 1),
	}
	go r.write(file)
	return r, nil
}

// Path es el archivo donde se graba.
func (r *ArrivalRecorder) Path() string { return r.path }

// Record anota una llegada. Se puede llamar desde cualquier goroutine, también después de Close
// (se ignora): nunca bloquea.
func (r *ArrivalRecorder) Record(sender string, seq uint32, talkspurt uint8, at time.Time) {
	select {
	case r.lines <- arrivalRecord{sender: sender, seq: seq, talkspurt: talkspurt, at: at}:
	default:
		r.dropped.Add(1)
	}
}

// Dropped devuelve las llegadas que no se llegaron a anotar.
func (r *ArrivalRecorder) Dropped() uint64 { return r.dropped.Load() }

// Close escribe lo pendiente y cierra el archivo.
func (r *ArrivalRecorder) Close() error {
	r.closeOnce.Do(func() {
		close(r.stop)
		r.closeErr = <-r.done
	})
	return r.closeErr
}

// write es el único dueño del archivo. El momento va en ns desde el inicio de la grabación
// (reloj monotónico: un ajuste de hora del sistema no deforma la red grabada).
func (r *ArrivalRecorder) write(file *os.File) {
	w := bufio.NewWriter(file)
	_, err := w.WriteString("emisor,seq,frase,ns\n")
	keep := func(e error) {
		if err == nil {
			err = e
		}
	}
	line := make([]byte, 0, 64)
	writeLine := func(rec arrivalRecord) {
		line = append(line[:0], rec.sender...)
		line = append(line, ',')
		line = strconv.AppendUint(line, uint64(rec.seq), 10)
		line = append(line, ',')
		line = strconv.AppendUint(line, uint64(rec.talkspurt), 10)
		line = append(line, ',')
		line = strconv.AppendInt(line, rec.at.Sub(r.start).Nanoseconds(), 10)
		line = append(line, '\n')
		_, e := w.Write(line)
		keep(e)
	}
	flush := time.NewTicker(time.Second)
	defer flush.Stop()
	for {
		select {
		case rec := <-r.lines:
			writeLine(rec)
		case <-flush.C:
			keep(w.Flush())
		case <-r.stop:
			for len(r.lines) > 0 { // único lector: no bloquea
				writeLine(<-r.lines)
			}
			keep(w.Flush())
			keep(file.Close())
			r.done <- err
			return
		}
	}
}
