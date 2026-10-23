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

// maxBusyGoroutines limita cuántas goroutines ocupadas se registran por bloqueo.
const maxBusyGoroutines = 20

// startUIWatchdog encola un fyne.Do vacío por segundo y mide cuánto tarda en ejecutarse. Si el
// hilo de UI no lo atiende a tiempo, registra el stack de la goroutine 1 (el hilo de Fyne) en el
// momento del bloqueo: dice exactamente qué función lo está trabando.
func (a *App) startUIWatchdog() {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			sent := time.Now()
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
