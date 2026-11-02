//go:build cgo

package ui

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2"

	"voxmesh/internal/logging"
)

// uiStallThreshold: a partir de acá un bloqueo del hilo de UI se nota como congelamiento.
// Windows marca la ventana como "No responde" (y la reemplaza por una ventana fantasma en la
// barra de tareas) recién a los 5 s sin bombear mensajes.
const uiStallThreshold = 750 * time.Millisecond

// processPauseThreshold: entre dos ticks del watchdog (1 s) pasó tanto → el proceso estuvo pausado.
const processPauseThreshold = 5 * time.Second

// maxBusyGoroutines limita cuántas goroutines ocupadas se registran por bloqueo.
const maxBusyGoroutines = 20

// startUIWatchdog encola un fyne.Do vacío por segundo y mide cuánto tarda en ejecutarse. Si el
// hilo de UI no lo atiende a tiempo, registra el stack de la goroutine 1 (el hilo de Fyne) en el
// momento del bloqueo: dice exactamente qué función lo está trabando.
func (a *App) startUIWatchdog() {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		lastTick := time.Now()
		for range ticker.C {
			sent := time.Now()
			// Un tick de 1 s que llega mucho después: el proceso entero estuvo frenado (suspensión
			// de la PC, Windows lo pausó). No es un bloqueo de la UI; el monitor de audio reabre
			// los dispositivos si quedaron invalidados.
			if paused := sent.Sub(lastTick); paused >= processPauseThreshold {
				logging.Errorf("proceso pausado %v por el sistema (suspensión o Windows lo frenó; %s)", paused.Round(time.Millisecond), runtimeSummary())
			}
			lastTick = sent
			done := make(chan struct{})
			fyne.Do(func() { close(done) })
			select {
			case <-done:
				continue
			case <-time.After(uiStallThreshold):
			}
			main, busy := stallStacks()
			logging.Errorf("hilo de UI bloqueado más de %v (%s); stack del hilo principal:\n%s", uiStallThreshold, runtimeSummary(), main)
			// goroutine 1 "runnable" = la llamada (p. ej. a OpenGL) ya volvió y espera un P libre:
			// el bloqueo no es del driver sino de lo que tiene ocupados los P en ese momento.
			if busy != "" {
				logging.Errorf("goroutines ocupando la CPU durante el bloqueo:\n%s", busy)
			}
			<-done
			logging.Errorf("hilo de UI liberado tras %v", time.Since(sent))
			lastTick = time.Now() // la espera de arriba no es una pausa del proceso
		}
	}()
}

// stallStacks devuelve el stack de la goroutine 1 y los de las goroutines que estaban
// corriendo o listas para correr (las que compiten con el hilo de UI por la CPU).
func stallStacks() (main, busy string) {
	buf := make([]byte, 4<<20)
	all := string(buf[:runtime.Stack(buf, true)])
	var busyStacks []string
	for _, goroutine := range strings.Split(all, "\n\n") {
		if strings.HasPrefix(goroutine, "goroutine 1 ") {
			main = goroutine
			continue
		}
		header, _, _ := strings.Cut(goroutine, "\n")
		if !strings.Contains(header, "[running") && !strings.Contains(header, "[runnable") {
			continue
		}
		if strings.Contains(goroutine, "ui.stallStacks") || len(busyStacks) >= maxBusyGoroutines {
			continue
		}
		busyStacks = append(busyStacks, goroutine)
	}
	if main == "" {
		main = all
	}
	return main, strings.Join(busyStacks, "\n\n")
}

// runtimeSummary resume CPU, goroutines y memoria: con poca RAM o pocos núcleos el hilo de UI
// espera al GC o a otras goroutines aunque ninguna función propia sea lenta.
func runtimeSummary() string {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	lastPause := time.Duration(mem.PauseNs[(mem.NumGC+255)%256])
	return fmt.Sprintf("cpus=%d gomaxprocs=%d goroutines=%d heap=%dMB sys=%dMB gc=%d última pausa=%v",
		runtime.NumCPU(), runtime.GOMAXPROCS(0), runtime.NumGoroutine(),
		mem.HeapAlloc>>20, mem.Sys>>20, mem.NumGC, lastPause)
}
