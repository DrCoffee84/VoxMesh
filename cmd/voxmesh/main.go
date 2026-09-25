package main

import (
	"os"

	"voxmesh/internal/ui"
)

func main() {
	ui.New(os.Args[0]).Run()
}
