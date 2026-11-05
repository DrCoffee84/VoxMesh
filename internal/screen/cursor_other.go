//go:build !windows

package screen

type CursorTracker struct{}

func OpenCursorTracker() (*CursorTracker, error) { return nil, errUnsupported }
func (t *CursorTracker) Poll() CursorState       { return CursorState{} }
func (t *CursorTracker) Close()                  {}
