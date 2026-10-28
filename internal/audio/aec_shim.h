// Shim C sobre WebRTC Audio Processing (AEC3), para cgo. Ver aec_windows.go.
#ifndef VOXMESH_AEC_SHIM_H
#define VOXMESH_AEC_SHIM_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct voxmesh_aec voxmesh_aec;

// Crea el procesador a 48 kHz mono con solo el cancelador de eco (AEC3) y su pasa-altos.
// NULL si no se pudo.
voxmesh_aec *voxmesh_aec_new(void);
void voxmesh_aec_free(voxmesh_aec *aec);

// Procesa un frame capturado: primero render_blocks bloques de 10 ms (480 muestras) de lo que
// sonó por los parlantes (render, contiguos), después capture_blocks bloques de captura en el
// lugar. delay_ms es la estimación inicial del retardo de eco. Devuelve 0 o el error de WebRTC.
int voxmesh_aec_process(voxmesh_aec *aec, const int16_t *render, int render_blocks,
                        int16_t *capture, int capture_blocks, int delay_ms);

// Lo que mide AEC3: ERLE (dB) y retardo estimado (ms). has_* = 0 si todavía no hay medida.
void voxmesh_aec_stats(voxmesh_aec *aec, double *erle_db, int *has_erle, int *delay_ms, int *has_delay);

#ifdef __cplusplus
}
#endif

#endif
