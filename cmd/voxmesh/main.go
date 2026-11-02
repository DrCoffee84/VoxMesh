package main

import (
	"os"

	"voxmesh/internal/ui"
	"voxmesh/internal/updater"
)

func main() {
	// Después de actualizar: el proceso viejo todavía cierra la sala, guarda la config y suelta
	// los puertos. Hasta que termine no se lee nada ni se borra su .old.
	updater.WaitForParent(os.Args[1:])
	updater.CleanupOldVersions()
	ui.New(os.Args[0]).Run()
}
