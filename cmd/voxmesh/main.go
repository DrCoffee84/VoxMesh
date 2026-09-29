package main

import (
	"os"

	"voxmesh/internal/ui"
	"voxmesh/internal/updater"
)

func main() {
	updater.CleanupOldVersions()
	ui.New(os.Args[0]).Run()
}
