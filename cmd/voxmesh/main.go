package main

import (
	"os"

	"voxmesh/internal/ui"
	"voxmesh/internal/updater"
	"voxmesh/internal/winproc"
)

func main() {
	// Después de actualizar: el proceso viejo todavía cierra la sala, guarda la config y suelta
	// los puertos. Hasta que termine no se lee nada ni se borra su .old.
	updater.WaitForParent(os.Args[1:])
	// Un solo VoxMesh por carpeta (voxmesh.json está al lado del exe; con la ventana oculta en el
	// área de notificaciones es fácil abrir otro): el nuevo muestra el que estaba y se va sin leer
	// ni escribir nada. Después de una actualización sigue igual: el viejo ya terminó o lleva 15 s
	// colgado, como antes.
	instance, first := winproc.SingleInstance(os.Args[0])
	if !first && !updater.Relaunched(os.Args[1:]) {
		instance.RequestShow()
		return
	}
	updater.CleanupOldVersions()
	ui.New(os.Args[0]).Run(instance.OnShowRequest)
}
