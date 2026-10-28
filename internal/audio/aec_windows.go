//go:build cgo

package audio

// WebRTC Audio Processing 1.3 (AEC3) linkeada estática desde third_party/webrtc-apm/out (la
// compila third_party/webrtc-apm/build.ps1; build.ps1 la llama si falta).

/*
#cgo CXXFLAGS: -std=c++17 -O2 -I${SRCDIR}/../../third_party/webrtc-apm/out/include/webrtc-audio-processing-1
#cgo CXXFLAGS: -DWEBRTC_LIBRARY_IMPL -DWEBRTC_WIN -D_WIN32 -D__STDC_FORMAT_MACROS=1 -DNOMINMAX -D_USE_MATH_DEFINES
#cgo LDFLAGS: -L${SRCDIR}/../../third_party/webrtc-apm/out/lib -lwebrtc-audio-processing-1 -lwinmm -lstdc++
#include "aec_shim.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// echoCanceller es una instancia de AEC3. No es concurrente: la usa una goroutine por vez.
type echoCanceller struct {
	aec *C.voxmesh_aec
}

func newEchoCanceller() (*echoCanceller, error) {
	aec := C.voxmesh_aec_new()
	if aec == nil {
		return nil, errors.New("inicializar la cancelación de eco (WebRTC AEC3)")
	}
	return &echoCanceller{aec: aec}, nil
}

// process le pasa al AEC la referencia (bloques enteros de 10 ms, puede estar vacía) y después
// saca el eco de capture (bloques enteros de 10 ms) en el lugar. delayMS es la estimación inicial
// del retardo; AEC3 busca el real.
func (c *echoCanceller) process(render, capture []int16, delayMS int) error {
	var renderPtr *C.int16_t
	if len(render) > 0 {
		renderPtr = (*C.int16_t)(unsafe.Pointer(&render[0]))
	}
	code := C.voxmesh_aec_process(c.aec, renderPtr, C.int(len(render)/echoBlock),
		(*C.int16_t)(unsafe.Pointer(&capture[0])), C.int(len(capture)/echoBlock), C.int(delayMS))
	if code != 0 {
		return fmt.Errorf("cancelación de eco: error %d de WebRTC", int(code))
	}
	return nil
}

// stats devuelve lo que mide AEC3: ERLE (cuánto eco saca, dB) y el retardo que encontró (ms).
func (c *echoCanceller) stats() (erleDB float64, hasERLE bool, delayMS int, hasDelay bool) {
	var erle C.double
	var hasE, delay, hasD C.int
	C.voxmesh_aec_stats(c.aec, &erle, &hasE, &delay, &hasD)
	return float64(erle), hasE != 0, int(delay), hasD != 0
}

func (c *echoCanceller) close() {
	if c != nil && c.aec != nil {
		C.voxmesh_aec_free(c.aec)
		c.aec = nil
	}
}
