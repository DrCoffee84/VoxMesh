//go:build cgo

package ui

import (
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
			logging.Errorf("hilo de UI bloqueado más de %v; stack del hilo principal:\n%s", uiStallThreshold, mainGoroutineStack())
			<-done
			logging.Errorf("hilo de UI liberado tras %v", time.Since(sent))
		}
	}()
}

func mainGoroutineStack() string {
	buf := make([]byte, 1<<20)
	all := string(buf[:runtime.Stack(buf, true)])
	for _, goroutine := range strings.Split(all, "\n\n") {
		if strings.HasPrefix(goroutine, "goroutine 1 ") {
			return goroutine
		}
	}
	return all
}
