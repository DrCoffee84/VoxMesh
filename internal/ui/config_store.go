//go:build cgo

package ui

import (
	"voxmesh/internal/config"
	"voxmesh/internal/logging"
)

// La configuración es copy-on-write: a.cfg apunta a una instantánea inmutable. Los lectores
// (callbacks de malgo, receiveLoop, UI) hacen una sola carga atómica y nunca esperan; los
// escritores clonan, modifican el clon y lo publican con CompareAndSwap. No hay mutex en
// ningún lado, así que un ajuste de la UI no puede frenar un frame de audio.

// conf devuelve la instantánea vigente. Es de SOLO LECTURA: mutarla (incluido SoundBinds) es
// una data race con todos los demás lectores. Para cambiar algo, updateConfig.
// En código caliente leerla una vez por frame y usar esa copia: todos los campos quedan
// coherentes entre sí aunque la UI publique otra en el medio.
func (a *App) conf() *config.Config {
	return a.cfg.Load()
}

// updateConfig aplica mutate sobre un clon y lo publica. Si otro escritor publicó en el medio,
// el CAS falla y se reintenta sobre la versión nueva (no se pierden cambios concurrentes de la
// UI y de receiveLoop). mutate puede correr más de una vez: solo debe asignar campos, sin I/O
// ni efectos secundarios. Devuelve la instantánea publicada.
func (a *App) updateConfig(mutate func(c *config.Config)) *config.Config {
	for {
		current := a.cfg.Load()
		next := current.Clone()
		mutate(&next)
		if a.cfg.CompareAndSwap(current, &next) {
			return &next
		}
	}
}

// saveConfig pide guardar la configuración en disco sin bloquear al que llama: el pedido se
// encola (capacidad 1, los pedidos repetidos se fusionan) y configSaver escribe la última
// instantánea. Antes config.Save corría en receiveLoop y en callbacks de la UI.
func (a *App) saveConfig() {
	select {
	case a.cfgSave <- struct{}{}:
	default:
	}
}

func (a *App) configSaver() {
	for range a.cfgSave {
		if err := a.writeConfig(); err != nil {
			logging.Errorf("guardar configuración: %s", logging.FormatError(err))
			a.setStatus("No se pudo guardar la configuración.")
		}
	}
}

// writeConfig escribe la instantánea vigente. cfgSaveMu evita que dos escrituras de
// voxmesh.json se intercalen (configSaver y flushConfig al salir).
// Con la vista de ajustes abierta, LiveSettings en memoria es un borrador sin confirmar: se
// escribe la base tomada al abrirla, junto con los campos de segundo plano vigentes (LastPeer,
// token del celular...). Así un guardado de receiveLoop, o cerrar la app, no persiste el borrador.
func (a *App) writeConfig() error {
	a.cfgSaveMu.Lock()
	defer a.cfgSaveMu.Unlock()
	cfg := *a.cfg.Load()
	if baseline := a.cfgBaseline.Load(); baseline != nil {
		cfg.LiveSettings = *baseline
	}
	return config.Save(a.cfgPath, cfg)
}

// flushConfig guarda de forma sincrónica (al cerrar la app, para no perder un pedido encolado).
func (a *App) flushConfig() {
	if err := a.writeConfig(); err != nil {
		logging.Errorf("guardar configuración al salir: %s", logging.FormatError(err))
	}
}
