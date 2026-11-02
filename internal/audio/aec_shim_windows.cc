//go:build cgo

// Shim C sobre WebRTC Audio Processing 1.3 (AEC3). Ver aec_shim.h y aec_windows.go.
#include "aec_shim.h"

#include <modules/audio_processing/include/audio_processing.h>

namespace {
constexpr int kRate = 48000;
constexpr int kBlock = kRate / 100; // 10 ms
} // namespace

struct voxmesh_aec {
  rtc::scoped_refptr<webrtc::AudioProcessing> apm;
  webrtc::StreamConfig stream{kRate, 1};
  int16_t render_out[kBlock]; // ProcessReverseStream pide destino aunque no se use
};

extern "C" voxmesh_aec *voxmesh_aec_new(void) {
  rtc::scoped_refptr<webrtc::AudioProcessing> apm = webrtc::AudioProcessingBuilder().Create();
  if (!apm) {
    return nullptr;
  }
  webrtc::AudioProcessing::Config config;
  config.echo_canceller.enabled = true;
  config.echo_canceller.mobile_mode = false; // AEC3, no AECM
  config.high_pass_filter.enabled = true;    // AEC3 lo fuerza igual (enforce_high_pass_filtering)
  config.noise_suppression.enabled = false;  // RNNoise
  config.gain_controller1.enabled = false;
  config.gain_controller2.enabled = false;
  apm->ApplyConfig(config);
  if (apm->Initialize() != webrtc::AudioProcessing::kNoError) {
    return nullptr;
  }
  voxmesh_aec *aec = new voxmesh_aec();
  aec->apm = apm;
  return aec;
}

extern "C" void voxmesh_aec_free(voxmesh_aec *aec) { delete aec; }

extern "C" int voxmesh_aec_process(voxmesh_aec *aec, const int16_t *render, int render_blocks,
                                   int16_t *capture, int capture_blocks, int delay_ms) {
  for (int i = 0; i < render_blocks; i++) {
    int err = aec->apm->ProcessReverseStream(render + i * kBlock, aec->stream, aec->stream, aec->render_out);
    if (err != webrtc::AudioProcessing::kNoError) {
      return err;
    }
  }
  for (int i = 0; i < capture_blocks; i++) {
    aec->apm->set_stream_delay_ms(delay_ms);
    int16_t *block = capture + i * kBlock;
    int err = aec->apm->ProcessStream(block, aec->stream, aec->stream, block);
    if (err != webrtc::AudioProcessing::kNoError) {
      return err;
    }
  }
  return 0;
}

extern "C" void voxmesh_aec_stats(voxmesh_aec *aec, double *erle_db, int *has_erle, int *delay_ms, int *has_delay) {
  webrtc::AudioProcessingStats stats = aec->apm->GetStatistics();
  *has_erle = stats.echo_return_loss_enhancement.has_value();
  *erle_db = stats.echo_return_loss_enhancement.value_or(0);
  *has_delay = stats.delay_ms.has_value();
  *delay_ms = stats.delay_ms.value_or(0);
}
