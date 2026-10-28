//go:build cgo && !windows

package audio

import "errors"

// Fuera de Windows no se compila WebRTC AEC3: la cancelación de eco no está disponible.
type echoCanceller struct{}

func newEchoCanceller() (*echoCanceller, error) {
	return nil, errors.New("la cancelación de eco solo está disponible en Windows")
}

func (c *echoCanceller) process(render, capture []int16, delayMS int) error { return nil }

func (c *echoCanceller) stats() (erleDB float64, hasERLE bool, delayMS int, hasDelay bool) {
	return 0, false, 0, false
}

func (c *echoCanceller) close() {}
