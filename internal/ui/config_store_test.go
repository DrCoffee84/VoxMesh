//go:build cgo

package ui

import (
	"path/filepath"
	"sync"
	"testing"

	"voxmesh/internal/config"
)

// La UI y receiveLoop escriben campos distintos a la vez mientras el "audio" lee en bucle:
// ningún cambio se pierde (CAS) y con -race no hay accesos sin sincronizar.
func TestUpdateConfigConcurrent(t *testing.T) {
	a := &App{}
	cfg := config.Default()
	a.cfg.Store(&cfg)

	const writes = 500
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() { // lector del camino crítico
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				c := a.conf()
				_ = c.InputGainDB + c.OutputGainDB
				_ = len(c.SoundBinds)
			}
		}
	}()
	var writers sync.WaitGroup
	writers.Add(3)
	go func() {
		defer writers.Done()
		for i := 0; i < writes; i++ {
			a.updateConfig(func(c *config.Config) { c.JitterBufferMS++ })
		}
	}()
	go func() {
		defer writers.Done()
		for i := 0; i < writes; i++ {
			a.updateConfig(func(c *config.Config) { c.VADHoldMS++ })
		}
	}()
	go func() {
		defer writers.Done()
		for i := 0; i < writes; i++ {
			a.updateConfig(func(c *config.Config) { c.SoundBinds["x"] = "F1"; delete(c.SoundBinds, "x") })
		}
	}()
	writers.Wait()
	close(stop)
	wg.Wait()

	got := a.conf()
	if got.JitterBufferMS != cfg.JitterBufferMS+writes || got.VADHoldMS != cfg.VADHoldMS+writes {
		t.Fatalf("se perdieron escrituras: jitter=%d vadHold=%d", got.JitterBufferMS, got.VADHoldMS)
	}
}

// Con la vista de ajustes abierta, un guardado (de receiveLoop o al salir) persiste la base y no
// el borrador en vivo, pero sí los campos de segundo plano que cambiaron en el medio.
func TestWriteConfigPersistsBaselineWhileSettingsOpen(t *testing.T) {
	a := &App{cfgPath: filepath.Join(t.TempDir(), "voxmesh.json")}
	cfg := config.Default()
	a.cfg.Store(&cfg)
	baseline := cfg.LiveSettings
	a.cfgBaseline.Store(&baseline)
	a.updateConfig(func(c *config.Config) {
		c.VADThreshold = 0.9
		c.PhoneMicToken = "nuevo"
	})
	if err := a.writeConfig(); err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load(a.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.VADThreshold != baseline.VADThreshold {
		t.Fatalf("se persistió el borrador: vad_threshold=%v", saved.VADThreshold)
	}
	if saved.PhoneMicToken != "nuevo" {
		t.Fatalf("no se persistió el campo de segundo plano: phone_mic_token=%q", saved.PhoneMicToken)
	}

	a.cfgBaseline.Store(nil) // Guardar
	if err := a.writeConfig(); err != nil {
		t.Fatal(err)
	}
	if saved, _ = config.Load(a.cfgPath); saved.VADThreshold != 0.9 {
		t.Fatalf("Guardar no persistió el ajuste: vad_threshold=%v", saved.VADThreshold)
	}
}
