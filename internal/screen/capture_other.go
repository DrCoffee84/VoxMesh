//go:build !windows

package screen

import (
	"errors"
	"image"
)

var errUnsupported = errors.New("compartir pantalla solo funciona en Windows")

func Monitors() ([]Monitor, error) { return nil, errUnsupported }

type Capturer struct{}

func OpenCapturer(string) (*Capturer, error)                 { return nil, errUnsupported }
func (c *Capturer) Monitor() Monitor                         { return Monitor{} }
func (c *Capturer) DPIAwareness() int                        { return -1 }
func (c *Capturer) Capture(*image.RGBA) (*image.RGBA, error) { return nil, errUnsupported }
func (c *Capturer) Close()                                   {}
