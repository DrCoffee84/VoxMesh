//go:build !cgo

package ui

// App is a headless placeholder used by go test when CGO is unavailable.
type App struct{}

func New(string) *App { return &App{} }

func (*App) Run() { showHeadlessWarning() }
