# AGENT.md - VoxMesh Technical Architecture & Project Map

> **Context for AI Agents & Developers**: This document is a token-efficient, high-density map of the entire VoxMesh codebase. Read this first to understand the architecture, data structures, threading rules, and pipelines without scanning multiple source files.

---

## 1. System Overview

**VoxMesh** is a low-latency, decentralized peer-to-peer and star-topology voice & text communications app built in Go with native CGO audio filters and a Fyne GUI.

* **Language**: Go 1.24+ (Requires `CGO_ENABLED=1`, see §4 build trap).
* **GUI Toolkit**: Fyne v2.6.3.
* **Audio Engine**: `malgo` (miniaudio wrapper) + `rnnoise-go` (AI neural noise suppression) + `gopkg.in/hraban/opus.v2` (cgo over libopus, build tag `nolibopusfile`, see §4).
* **Audio Spec**: 48,000 Hz, 16-bit S16LE, Mono, 20 ms frames (960 samples = 1920 bytes) inside the app. **Network audio is always Opus** (one packet per 20 ms frame, ~100–130 bytes). Phone mic frames are PCM of the same size over the LAN; the PC encodes them like its own mic.
* **Network**: UDP-only direct/mesh with host election and room migration.
* **Platform**: Primary Windows 10/11 x64, with Android client (`apk/`) for phone mic.
* **Current release line**: 0.9.7 (first release with the per-sender jitter buffer, see §3.B). The Opus switch breaks the network audio format: **no backward compatibility**, every peer must run the same build.

---

## 2. Directory Structure & Key Components

```
VoxMesh/
├── cmd/voxmesh/main.go            # Entrypoint. Cleans up old update binaries & starts UI.
├── internal/
│   ├── audio/                     # Real-time DSP and sound capture/playback
│   │   ├── engine.go              # [cgo] malgo capture & playback. Capture pool (captureInput → CapturedFrame, no allocations), send goroutine with audio priority, TakeCaptureStats. Mixer (PlayStreamSeq for network, PlayStream FIFO for SFX/local), per-stream volume + output chain, JitterReport, AudibleStreams.
│   │   ├── priority_windows.go    # LockAudioThread: locks the goroutine to an OS thread registered in MMCSS "Pro Audio" (fallback THREAD_PRIORITY_HIGHEST). priority_other.go: no-op.
│   │   ├── codec.go               # [cgo] Opus: OpusEncoder (send) and newOpusDecoder (one per sender, used by the jitter buffer).
│   │   ├── jitter.go              # Per-sender jitter buffer: stores Opus packets, decodes on playback (FEC → PLC), adaptive target + repeated-peak detector, talkspurt-aware restart, drift, splice crossfades, arrival stats, cut counters.
│   │   ├── arrivals.go            # ArrivalRecorder: CSV of every network audio arrival (sender, seq, talkspurt, ns), written while log level = 2; replayed by TestJitterReplayArrivals.
│   │   ├── limiter.go             # peakLimiter: envelope peak limiter (int32 mix bus before softClip; int16 at the end of the send chain).
│   │   ├── effects.go             # FilterSettings, filters, compressor/expander (envelope + attack/release), tanh limiter, send-chain state (envelope limiter, pre-roll), ApplyOutput (skips a no-op chain).
│   │   ├── settings.go            # Config → pipeline: InputSettings, OutputSettings (PlaybackSettings + JitterTuning), EncoderSettings (OpusSettings). The only place that knows both sides.
│   │   ├── rnnoise.go             # RNNoise integration (float32 API + floatToInt16 clamp) and outgoing pipeline (PrepareOutgoingFrames).
│   │   ├── shout.go               # Dry delay line aligned with RNNoise (Delay() = 960), RNNoise intensity mix and shout mode.
│   │   ├── pcm.go                 # S16LE byte/slice encoding, frame constants (SampleRate, FrameSamples).
│   │   ├── player.go              # [cgo] Standalone SFX/preview audio player (used when engine idle).
│   │   ├── sfx.go                 # Embedded WAV sound cues (connect, disconnect, pop).
│   │   ├── devices.go             # Enumeration of WASAPI/DirectSound input/output devices.
│   │   ├── gate.go & monitor.go   # RMS noise gate and level monitoring.
│   │   ├── sound.go               # Soundboard clip structures and serialization.
│   │   └── *_test.go              # engine_test needs cgo (Opus); jitter_test uses a fake decoder and runs without it. jitter_sim_test: deterministic talk/network simulation (Wi-Fi spikes, capture loss) that measures cuts/min and latency, plus replay of a recorded arrivals CSV (VOXMESH_ARRIVALS).
│   ├── config/
│   │   ├── config.go              # Persistent configuration struct (voxmesh.json), Default(), Load, AppDir(), default ports/names. Embeds LiveSettings (see §3.I).
│   │   ├── params.go              # Params: THE table of every numeric/bool setting (default, range, step, unit, label, profile). See §3.I.
│   │   ├── profiles.go            # Audio profiles: one JSON per profile in <AppDir>/perfiles/<entrada|salida>/, Default built in, migration.
│   │   └── audio.go               # Internal (not user-tunable) audio constants: send attack, softClip, bus attack, Opus max packet, jitter splice/fade/cooldown/quiet RMS/window/histogram/peaks, network stream eviction.
│   ├── history/history.go         # Message store for chat and sync between peers; soundboard files, manifest and tombstones.
│   ├── logging/logging.go         # File and console logger.
│   ├── netinfo/netinfo.go         # Local IP resolution, public IP, UPnP: MapUDP(candidate external ports → listen port), UnmapUDP(external port).
│   ├── phonemic/server.go         # Phone-as-Mic server (UDP discovery + WebSocket/HTTP). forwardPCM: fixed 20 ms schedule that catches up (never skips), audio-priority thread, frames stamped with their scheduled time.
│   ├── room/
│   │   ├── state.go               # Room state, participant list, host candidates in room order (Candidates, NextHost).
│   │   ├── failover.go            # Failover: pure state machine for losing the host (reconnect / elect / switch / step down), see §3.E4.
│   │   └── protocol.go            # Envelope encoding for Hello/State/Leave messages.
│   ├── transport/
│   │   ├── udp.go                 # UDP socket, peers, broadcast, 16-byte packet header, paced bulk queue for assets (see §3.C).
│   │   ├── udp_windows.go         # SO_REUSEADDR on the socket.
│   │   └── udp_other.go           # Fallback for non-Windows platforms.
│   ├── ui/                        # [cgo] package ui — split by responsibility (see §3.D)
│   │   ├── ui_main.go             # App struct, New, Run, setStatus, quit (Hide + Quit), auto-updater dialogs.
│   │   ├── ui_components.go       # Main layout (content), widgets, panels, lightbox, peer avatars.
│   │   ├── ui_settings.go         # Settings view (renderSettingsView: live draft + baseline, save/discard dialogs), levelMonitor, mic/output tests.
│   │   ├── ui_audio_params.go     # Controls generated from config.Params (paramControls), accordion sections (inputSections/outputSections), profile bar.
│   │   ├── ui_chat.go             # Chat UI, images, activity log (batched, max 200), room history view.
│   │   ├── ui_chat_thumbnail.go   # Async chat image thumbnails (decode + downscale off-thread, cached by path).
│   │   ├── ui_soundboard.go       # Soundboard panel (search filter, edit dialog: rename / replace audio / delete), waveform, trim dialog (max ~87 s = assets.MaxBytes), pickSoundFile, updateSoundBinds (copy-on-write).
│   │   ├── ui_connection.go       # Create/open host, connect client, stopConnection/stopConnectionAsync (drives buttons).
│   │   ├── watchdog.go            # UI-thread watchdog: logs goroutine 1 stack when the UI thread stalls > 750 ms.
│   │   ├── network.go             # receiveLoop → handleHostPacket / handleClientPacket, RoomHello (idempotent), leaves, room state, pings.
│   │   ├── network_failover.go    # failoverLoop: ticks room.Failover and runs its actions (takeOverHost, switchHost, stepDown) in one goroutine.
│   │   ├── network_audio.go       # Audio over the network: handleAudioPacket, sendOutgoingPCM (Opus encode, talkspurt), playbackSettings provider.
│   │   ├── audio_monitor.go       # audioMonitorLoop: speaking indicator from what the Engine played + per-sender loss/jitter/cuts (chip + log), "Envío" capture line, arrival recording at log level 2.
│   │   ├── network_sync.go        # Chat, asset chunks and history sync over the network.
│   │   ├── sound_sync.go          # Soundboard sync on connect (list + tombstones), on-demand audio download (§3.J).
│   │   ├── peers.go               # Peer list state (lag, speaking indicator, net stats) and per-participant volume (peerVolumes).
│   │   ├── config_store.go        # Copy-on-write config: conf() / updateConfig (CAS) / saveConfig (async, coalesced).
│   │   ├── connection_state.go    # Atomic transport / endpoint (pre-resolved) / Engine: connected, setEndpoint, publishEngine, stopEngine. stopSignal (connLife, hostRole).
│   │   ├── audio_bridge.go        # UI ↔ Engine wiring: startAudio, phone mic, mic preview (start/detach), SFX/soundboard players.
│   │   ├── file_windows.go        # Native GetOpenFileNameW (off UI thread, COM STA, owner HWND), clipboard images.
│   │   ├── file_other.go          # Non-Windows file dialog stubs.
│   │   ├── hotkey_windows.go      # GetAsyncKeyState poller over a pre-parsed snapshot (publishHotkeys), bind dialogs.
│   │   ├── hotkey_other.go        # Non-Windows hotkey stubs.
│   │   ├── window_state_windows.go # GetWindowPlacement / ShowWindow on the HWND: maximized state + maximize at startup.
│   │   ├── window_state_other.go  # Non-Windows stubs (size only, no maximize).
│   │   └── headless*.go           # !cgo placeholder App (lets go test build the package without CGO).
│   ├── updater/
│   │   ├── updater.go             # GitHub Releases update checker, rename-and-replace, restart.
│   │   └── updater_test.go        # Version comparison unit tests (IsNewer).
│   └── version/version.go         # Current build version string (injected at build time).
├── apk/                           # Android mini-app (VoxMesh Mic)
│   ├── app/src/main/              # Android foreground service, UDP streamer, QR scanner.
│   └── build.gradle               # Gradle build file.
├── .github/workflows/
│   ├── release.yml                # Tag trigger ('v*'): builds Windows .exe & Android .apk, publishes GitHub Release.
│   └── build-apk.yml              # CI trigger for Android APK.
├── build.ps1                      # Local build script for Windows (Go + CGO + UCRT64).
└── release.ps1                    # Helper script: git tag + git push to trigger release pipeline.
```

---

## 3. Core Architectural Patterns & Rules

### A. Audio Pipeline (send → network → mix)

**Outgoing** (`ui/network_audio.go` `sendOutgoingPCM` → `audio/rnnoise.go` `PrepareOutgoingFrames`, int16 in place):
1. malgo capture callback (`Engine.captureInput`, real-time thread) re-frames into exact 1920-byte frames using a pool of `AudioCaptureQueueFrames` (25 = 500 ms) preallocated buffers: no allocations, never waits. The send goroutine gets `CapturedFrame{PCM, At, Dropped}` and returns the buffer after `onFrame`; it runs on an OS thread with audio priority (`LockAudioThread`: MMCSS "Pro Audio"), so a busy PC (a game) doesn't starve it. Only if all 25 buffers are busy is a frame dropped, and the next one reports it (`Dropped`). Phone mic frames enter the same function with `At` = their slot in the phone server's schedule.
2. `Effects.ApplySamples` (gain, optional filters). The tanh limiter here only runs if `InputGainDB > 0` (protection; it distorts what RNNoise sees).
3. **RNNoise always runs** when enabled — there is NO bypass for loud frames. RNNoise has a structural 20 ms delay (`Denoiser.Delay() = 2*480`); skipping it frame-by-frame reorders/duplicates audio. Uses `denoiser.Process` (float32) + `floatToInt16` clamp because `ProcessInt16` wraps around on overflow (the original "cirkci" on shouts). Processes in place.
   * **Dry mix / shout mode** (`shout.go`, only with `Effects`): before RNNoise the input goes into a delay line of `Denoiser.Delay()` samples (960 = one frame), so the original is sample-aligned with the denoised output (test `TestShoutMix_DryIsAlignedWithRNNoiseDelay`). Shout mode: frame RMS (same dBFS as the settings meter) ≥ `ShoutThresholdDB`, or (`ShoutOnClip`) any input peak ≥ `config.AudioClipPeak` (~ -1 dBFS: a loud talker saturates before reaching the RMS threshold) → active for this frame (ramp) + the next (the one that carries the shout, RNNoise delay) + `ShoutHoldMS`; while active the frame counts as speech (forces VAD) and the mix uses `ShoutDryMix`. Otherwise the mix is `RNNoiseDryMix` (= 1 − `RNNoiseMix`). Mix ramps linearly across the frame; 0 → 0 leaves RNNoise output untouched.
4. VAD/gate hold (`KeepVoiceOpen`).
5. **Single envelope limiter** (`Effects.limitSend`, `peakLimiter` with instant attack / `send_limiter_release_ms` release) on EVERY frame, sent or not. Ceiling = `send_ceiling_db` (-1.7 dBFS) or the user's `limiter_threshold_db` if enabled and lower. No per-frame static gain anymore.
6. `Effects.gateFrames`: closed gate → nothing sent (DTX), frame kept as **pre-roll** (last `preroll_ms`/20 frames, max `AudioMaxPreRollFrames`). Gate opening → pre-roll frames first, then the current one, with the `fade_in_ms` fade-in on the first of them (the first syllable is no longer clipped; no extra latency, the receiver waits for its cushion anyway).
7. Mute drops the frames after the chain (§3.E2). `LiveMonitoring` plays the PCM (not encoded) via `engine.Play`.
8. Opus encode once per frame (`a.encoder`, recreated in `startAudio`; `Encode(samples, audio.EncoderSettings(cfg), send)` reapplies bitrate/complexity/FEC/loss live when they change and recreates the libopus encoder only when VoIP ↔ music mode changes; defaults VoIP, 40 kbps, complexity 9, FEC on, 8 %, Opus DTX off). Encoder settings are sender-only: receivers decode anything. Payload = `[idLen=8][participant ID as 8 raw bytes][opus]`, `Flags = FlagAudioSequence`. `Sequence` and `Talkspurt` come from `a.timeline` (`audioTimeline.next`, `network_audio.go`), measured in **capture time** (`CapturedFrame.At`), not send time: a new talkspurt when more than `talkspurtGap` (60 ms) of captured audio went unsent (gate closed, mute, phone gap), so a processing delay doesn't split a phrase; frames the capture dropped mid-phrase advance the sequence, so receivers conceal them as loss instead of getting less audio than time (their buffer drained and cut; simulation: 7.1 → 0.5 cuts/min). `sendMu` serializes the send (mic and phone may overlap while switching input). `a.audioSent` counts packets for the log.

**Incoming** (`ui/network_audio.go` `handleAudioPacket`, the single audio entry point for both host and client branches of `receiveLoop`):
* The host **relays the packet first** (`udp.Broadcast` before any processing).
* Requires `FlagAudioSequence`; the ID bytes are hex-encoded back to the participant ID; `Engine.PlayStreamSeq(senderID, seq, talkspurt, opus)` → per-sender jitter buffer. No PCM work in `receiveLoop`.

**Mixer** (`audio/engine.go` `Engine.mix`, malgo playback callback):
* `Engine.streams map[string]*audioStream` protected by `playbackMu`. Short lock: pull each stream's chunk (int16 `scratchBuffer`); everything else outside the lock into `mixBuffer []int32` (no integer overflow possible).
* Network streams read from their jitter buffer (decode + fades/PLC happen there, under the lock). `__sfx__` / `__local__` use the old PCM FIFO with prebuffer/fade-in/decay (`PlayStream`, only for those two).
* **Output chain per sender**: each network stream has its own `Effects` (`stream.fx`) applied to the decoded audio in sequence order. Everything the callback needs (output filter, `JitterTuning`, bus limiter) comes from `Engine.SetPlaybackSettings(a.playbackSettings)` = `audio.OutputSettings(a.conf())`, called once per callback **before** taking `playbackMu`; without a provider the factory defaults apply. `ApplyOutput` skips the chain when it would be a no-op (no gain/filters and block peak ≤ limiter threshold).
* Per-stream `volume` is applied while summing. `Engine.volumes` keeps the per-sender volume even before the stream exists or after it is evicted (`SetStreamVolume`, clamped 0–3).
* `stream.lastAudible` is set when the jitter buffer returned non-zero audio: `AudibleStreams` feeds the speaking indicator (§3.G).
* `mixLimiter.Process` (peak detector with instant attack + gain smoothing 1 ms attack / `bus_limiter_release_ms` release, threshold `bus_limiter_db`, max -0.8 dBFS so it stays under the knee) then `softClip` (knee 30000, asymptote 32700) as a safety net for attack overshoot.
* Network streams inactive for > `JitterStreamIdleEvict` (60 s) are evicted, so a sender's measured network (target, peaks) survives the pauses of a conversation; `__sfx__` / `__local__` FIFOs after 2 s.
* User-tunable values live in the `config.Params` table (§3.I) and reach `audio/` through `settings.go`; fixed internals live in `internal/config/audio.go`. Do not hardcode new ones in `audio/`.

**Measurement** (`ui/audio_monitor.go`, goroutine started in `Run`): every 100 ms marks speaking peers from `Engine.AudibleStreams`; every 2 s reads `Engine.JitterReport()` and publishes per-peer loss % (interval delta) and jitter to the avatar chip (`🟢 45 ms · 2% · ±6 ms`); every 10 s, at level 1 (control):
* one `Audio de <name>: ...` line per sender that sent something in the interval, with `cortes=N (X ms)` (audio interrupted in the middle of a phrase: what "se corta" means) and `frases=N` (restarts after a sender pause, not cuts). Compare those lines before/after changes.
* one `Envío: capturados=N (50.0/s) descartados=N enviados=N | cola máx=N frames proc máx=X ms` line from `Engine.TakeCaptureStats` (plus `Envío: hilo de envío con MMCSS Pro Audio` once per Engine): below 50 frames/s means the mic/driver delivers less audio than time (e.g. Bluetooth: not fixable by the queue); dropped frames or a growing queue mean the PC is too busy (dropped ones go out as loss, see §3.A step 8).
* At level 2 (trace) every arrival is also recorded to `%AppData%\VoxMesh\llegadas-<fecha>.csv` (one file per connection, pruned after 48 h). Replay it through the jitter buffer: `VOXMESH_ARRIVALS=<csv> go test -run TestJitterReplayArrivals -v ./internal/audio/`.

### B. Per-Sender Jitter Buffer (`internal/audio/jitter.go`)
* Ring of 64 slots indexed by `seq % 64` holding the **Opus packets as received**; `playSeq` = next frame to play. Decoding happens in `Read` (inside the malgo callback) into preallocated buffers: no allocations in `Read`. Not concurrent-safe: only touched under `Engine.playbackMu`.
* The decoder is an interface (`frameDecoder`: `Decode` / `DecodeFEC` / `DecodePLC`); production uses `*opus.Decoder`, `jitter_test.go` a fake (runs without CGO). Each call gets exactly one frame (`cap(pcm)` = duration).
* **Push**: late (`seq < playSeq`) → dropped (`Stats.Late`); duplicate → ignored; jump > 64 frames either way → `reset` (sender restarted, `Stats.Resets`; the next audio counts as a new phrase). Each slot keeps the packet's talkspurt. Every arrival also feeds `arrivalStats` (below).
* **Tuning**: every parameter below that is not a fixed internal comes from `JitterTuning` (`jitter_*` keys of the output profile). The Engine calls `setTuning` on each stream every callback (no-op when unchanged), so changes apply live; `newJitterBuffer(maxMS, dec)` (tests) uses the factory defaults.
* **Missing frame with later frames buffered** = loss: if packet N+1 is there → `DecodeFEC(N+1)` recovers N (`Stats.Recovered`); otherwise `DecodePLC` (`Stats.Concealed`), up to `PLCMaxFrames` (`jitter_plc_max_ms`, 100 ms), then silence. `playSeq` advances.
* **Buffer empty** = lag spike or end of phrase → Opus PLC keeps going, ramping down to silence over `JitterFadeOutSamples` (60 ms, may span several frames; was 10 ms and every spike sounded like a hole), and **`playSeq` does NOT advance**, so the delayed packet still plays (crossfaded into the fade).
* **Resume rule** (`readyToPlay`), decided by the talkspurt of the first buffered frame vs the last real frame played:
  * **Same phrase** (the sender kept sending: a *cut*) and the last real frame < `jitter_talkspurt_gap_ms` (300 ms) ago → resume as soon as it arrives (what was delayed usually arrives together and becomes the cushion). Counted in `Stats.Underruns` / `UnderrunTime` (the log's `cortes`).
  * **New phrase** (the sender paused > 60 ms, even briefly) or a longer cut → wait for `targetFrames` or that much time; the wait falls in the pause. Counted in `Stats.Phrases`. Before this, every phrase after a short pause restarted with only the pre-roll (~40 ms) as cushion and the first spike cut it (simulation: Wi-Fi 16 → 3.5 cuts/min).
* **Adaptive target** (`adaptiveTarget`, recomputed only while `!playing`, so never mid-phrase): `clamp(ceil(p / 20 ms) + margin, jitter_min_ms/20, jitter_buffer_ms/20)`, where p is the `jitter_percentile` (95) percentile of the delay variation over the last 500 arrivals (~10 s of speech; histogram of 5 ms buckets), margin `jitter_margin_ms`/20 (1). **Repeated peaks**: consecutive arrivals with variation ≥ `JitterPeakThresholdMS` (60) form a peak; if ≥ `JitterPeakMinCount` (2) peaks happened in the last `JitterPeakMemory` (1000 arrivals, ~20 s), p = max(p, highest peak). The percentile alone misses Wi-Fi stalls (each touches few packets) and each uncovered one is a cut. Fewer than 50 samples → the max. `jitter_buffer_ms` is the **maximum**.
* **Arrival stats**: per phrase (`Packet.Talkspurt` changes, or > 1 s without packets) `offset = arrival − seq·20 ms`; delay variation = offset − min offset of the phrase. Without the talkspurt split the DTX silence (contiguous seq, late arrival) would be measured as delay. Also RFC 3550 jitter `J += (|D| − J)/16`. Exposed in `Stats()` (`JitterMS`, `DelayP95MS`, `TargetMS`, `BufferedFrames`).
* **Drift**: if the buffered span > target + slack (`jitter_drift_slack_ms`/20 = 2), one frame has to go (at most every 5 frames). The frame is decoded anyway (decoder continuity) and dropped only if its RMS < `JitterDriftQuietRMS` (~ −40 dBFS); otherwise the drop is postponed to the next quiet frame, unless the span exceeds target + slack + force (`jitter_drift_force_ms`/20 = 4). A *missing* frame while over target is skipped instead of concealed.
* **Splice**: output holds back the last 120 samples (2.5 ms) of each frame. Decoder output (real, FEC, PLC and the fade) is continuous and passes bit-exact; a discontinuity (phrase start, drift drop, reset) crossfades the held tail into the new frame (output becomes 120 samples shorter — intentional).
* The sender's sequence only advances on sent audio and on frames its capture dropped mid-phrase, so it stays contiguous across VAD silence: a gap in seq means real loss (network or capture).

### C. Packet Header (`internal/transport/udp.go`)
* 16 bytes: `[0:4]` magic `VM01`, `[4]` Kind, `[5]` Flags, `[6]` Talkspurt (audio only), `[7]` unused, `[8:12]` Sequence (BE), `[12:16]` payload length (BE).
* `FlagInputClipped` (bit 1): the sender's captured frame saturated (`audio.FrameLevel`, peak ≥ `config.AudioClipPeak`); receivers ring that avatar in red (`markPeerClipping`). Informative only, old receivers ignore it.
* `FlagAudioSequence` (bit 0): `Sequence` is the sender's own contiguous audio counter. Without it, `Sequence` is the global `a.sequence` shared by ping/chat/assets/room packets (has gaps — never use it for loss detection). Audio without the flag is ignored.
* `Talkspurt`: phrase number of the audio sender (§3.A step 8, §3.B arrival stats and resume rule).
* **Bulk (assets)**: `SendBulk` / `BroadcastBulk` (block for a queue slot, own goroutines) and `TryBroadcastBulk` (host relay from `receiveLoop`, drops if the queue is full; receivers re-request with `AssetMissing`) go through one queue paced by a token bucket at `BulkRateBytes` (250 KB/s ≈ 2 Mbps total, 16 KB burst). Before, `sendAsset` pushed ~20 Mbps per destination and `handleAssetMissing` had no limit: an 8 MB sound saturated the host's uplink for seconds and delayed the voice it relays. Audio and control (`Send`, `Broadcast`) bypass it. `Close` unblocks waiting senders.
* No backward compatibility for audio (Opus only). Unknown kinds are still ignored by `receiveLoop`.
* Kinds: 1 Audio, 2 Hello, 3 Ping, 4 Pong, 5 RoomHello, 6 RoomState, 7 Chat, 8 ChatSync, 9 AssetChunk, 10 AssetMissing, 11 RoomLeave, 12 RoomMute (§3.E2).
* Socket: `Listen` asks for 4 MB read/write buffers (`SocketBufferBytes`, best effort). `Receive` reuses one 64 KB read buffer (single reader: `receiveLoop`) and returns an exact-size copy (handlers keep payloads). `receiveLoop` computes `addr.String()` once per packet and only formats the trace line when `logging.IsTrace()`.

### D. `internal/ui` File Layout Rule
* Same `package ui`, split by responsibility. **Prefix `ui_` only for files that build or mutate widgets**; logic without UI uses a descriptive name (`network*.go`, `peers.go`, `audio_bridge.go`).
* Network audio work (jitter, codecs, FEC) goes in `network_audio.go` / `audio_monitor.go` / `internal/audio`, never in `receiveLoop` branches.
* Known coupling debt: `handleRoomState` / `PacketRoomHello` (`network.go`) still set `selfNameLabel`/`endpointEntry` (wrapped in `fyne.Do`), and `ui_connection.go` mixes network lifecycle with buttons. Fully extracting them needs a new package (e.g. `internal/session`) with interfaces.

### E. Unique Participant Names (`_PUTO` Suffix)
* In `internal/ui/network.go` (`PacketRoomHello` handler on Host):
  * When a participant connects, if their username collides (case-insensitive) with an already connected peer:
  * The host appends `_PUTO` iteratively (`Pepe` -> `Pepe_PUTO` -> `Pepe_PUTO_PUTO`).
  * Propagated via `PacketRoomState`. The client detects the change in `handleRoomState`, updates local config, header label (`selfNameLabel`), and status log.

### E2. Mic Mute (`isMuted`, `PacketRoomMute = 12`)
* `App.isMuted atomic.Bool`, runtime only (not persisted), survives reconnects/migration. Toggle button left of "Refresh Audio" (`toggleMute` in `ui_components.go`: flips the atomic + icon on the UI thread, `go publishMuteState()`).
* **Send cut** (`sendOutgoingPCM`): the filter chain still runs (RNNoise 20 ms delay + VAD hold stay coherent, no stale audio on unmute); the frame is dropped like DTX — no packet, no live monitoring, no speaking indicator, the sequence does not advance. On unmute the > 60 ms gap (in capture time) starts a new talkspurt.
* **Signaling**: `room.Participant.Muted` (`json:"muted,omitempty"`). Client → host: `PacketRoomMute` with `Envelope{Kind: "mute", Participant: {ID, Muted}}`. Host applies it with `State.SetMuted(id, addr, muted)` (only from the address registered in Hello; copy-on-write slice) and broadcasts `PacketRoomState` to everyone (sender included = ack); unchanged → state sent back to the sender only. Host's own mute: `SetMuted` + broadcast. Hello (`connectClient`, `sendRoomHello`), `createHost` and `takeOverHost` carry the current value.
* **Loss**: `failoverLoop` resends every 2 s while the host's state differs from the local value (`selfMuteSynced`). Hosts < this version ignore kind 12, so clients keep resending (tiny packet).
* **Avatars**: `peerView.muted` (self from `isMuted`, others from `Participant.Muted`). `newMuteBadge` = red `theme.VolumeMuteIcon` on a dark circle, bottom-right of the avatar, 3rd object of the avatar Stack; `refreshPeerAvatars` shows/hides it inside its `fyne.Do`.

### E3. Hosting: UPnP and public address (`ui_connection.go` `configureHostNetwork`)
* The socket listens on the configured port (`listen_address`). `netinfo.MapUDP` tries external ports listenPort..listenPort+5, **all pointing to listenPort** (a port taken by another router rule, `ConflictInMappingEntry` 718, moves to the next), and the room advertises `publicIP:<external port that worked>`. The old code retried with listenPort+1 → listenPort+1, where nothing listened, and advertised a dead port.
* Lease `upnpLeaseSeconds` (1 h), renewed at half (`renewHostPort`) until the room closes or the host steps down (`a.hostRole`). `a.upnpPort` keeps the mapped external port: `stopConnectionAsync` unmaps that one.
* Without UPnP the status asks to open the UDP port by hand and the room advertises `publicIP:listenPort`.

### E4. Disconnects and host migration (`room/failover.go`, `ui/network_failover.go`)
* **Lifecycle**: `a.connLife` (stopSignal) stops the connection's goroutines (`failoverLoop`, `sweepAssetLoop`) and is stopped **first thing** in `stopConnectionAsync`; `a.hostRole` stops the host-role ones (`pingPeers`, UPnP renewal) on disconnect or step-down. Before, the client's `monitorHost` was never stopped and, seconds after leaving, became host of a ghost room on the closed socket (UPnP mapping, mic opened, fake messages).
* **`room.Failover`** is pure (no I/O): events in (`JoinedAsClient`, `Hosting`, `Seen`, `HostState`, `HostLeft`, `StateFrom`, `HelloFrom`, `Tick`), actions out (`ActPing`, `ActHello`, `ActSendState`, `ActBecomeHost`, `ActSwitchHost`, `ActStepDown`, `ActGiveUp`, `ActStatus`). One per connection, passed to `receiveLoop` and `failoverLoop`; `failoverLoop` ticks it every 250 ms and runs the actions in order (role changes never race). Tested with a fake clock in `failover_test.go` (every scenario below).
* **Client only trusts its host**: `Seen(addr)` says whether a packet comes from the host. Audio, chat, assets, `RoomState` and `RoomLeave` only count from the host; pings from anyone get a Pong (it opens NATs during a migration) but don't keep the host "alive". The first `RoomState` binds the host address (the reply may come from the host's LAN address if you dialed its public IP). `handleAudioPacket` drops our own ID (echo when two hosts relay to each other).
* **Brief cut**: host silent > 6 s → "Reconectando…", ping + `RoomHello` every 2 s; host back before 20 s (`Grace`) → nothing migrates. The host drops a silent participant after `participantTimeout` (20 s, same grace). `RoomHello` from someone already connected at the same address only gets the state back (no message/sound/broadcast), so retries are free.
* **Migration** (host `RoomLeave`, sent ×3, or silent > 20 s): candidates = `State.Candidates()` of the last state received from the host (connected, `CanBeHost`, room order, without the old host): everyone computes the same list. Each one sends `RoomHello` to the current candidate every 500 ms (opens its NAT towards it) for up to 10 s, then the next candidate. The candidate becomes host immediately only if the host left on purpose and it is first; otherwise when someone asks (a `RoomHello`): if nobody asks, maybe it was the one offline, so it never hosts an empty room. While waiting it pings the others (opens its NAT). New host: epoch+1, old host disconnected, `LastSeenUTC` reset, `ActSendState` to whoever hasn't joined every 500 ms for 10 s then every 2 s up to 2 min (also to the old host if it only went silent). Nobody reachable → `ActGiveUp` ("No se pudo seguir la sala sin el host") and disconnect.
* **Never two hosts**: a host that gets a `RoomState` from a known participant with higher epoch (or equal epoch and lower host ID) steps down (`stepDown`: forwards that state to its own clients, stops the host role, unmaps UPnP, joins as client). A client follows any known room with a higher epoch. A client whose host forwards a state naming another host migrates to it.
* **No audio restart**: `sendOutgoingPCM` reads `hostMode` / `endpointAddr()` per frame, so `takeOverHost` / `switchHost` / `stepDown` don't reopen devices and every sender's jitter buffer keeps its measurements.
* **Limits**: hole punching works with usual home routers (cone NAT); with symmetric NAT/CGNAT on both sides the app gives up with a message instead of leaving broken rooms. A participant on the old host's LAN is stored with a private address that remote peers can't reach.

### F. Self-Updating on Windows (`internal/updater/updater.go`)
* **Windows Lock Workaround**: Windows blocks modifying/deleting running `.exe` files, but **allows renaming** them on NTFS.
* **Process**:
  1. Downloads `voxmesh.exe.new` into the application directory.
  2. Renames running `voxmesh.exe` -> `voxmesh.exe.old`.
  3. Renames `voxmesh.exe.new` -> `voxmesh.exe`.
  4. Launches new `voxmesh.exe` via `exec.Command` and calls `os.Exit(0)`.
  5. At startup (`main.go`), `updater.CleanupOldVersions()` deletes leftover `.old` and `.new` files.
* **Trigger**: Background goroutine on startup checks GitHub Releases API (`api.github.com/repos/DrCoffee84/VoxMesh/releases/latest`). A popup asks the user for confirmation. Also triggered manually in Settings.

### G. UI Threading & Fyne Concurrency
* **How the Fyne 2.6.3 loop works**: goroutine 1 (locked to the main OS thread) runs widget callbacks, every queued `fyne.Do` (one per `select` iteration) and `glfw.PollEvents` at 60 Hz. Anything slow in any of them stops repainting. ≥ 5 s without pumping messages → Windows swaps in a "Not Responding" ghost window (the taskbar button looks like the app restarted).
* **NEVER block the UI goroutine**: Heavy tasks (network calls, audio initialization, file I/O) MUST run inside goroutines (`go func() { ... }()`). Specifically never in a callback or `fyne.Do`:
  * malgo device open/close (`audio.NewEngine`, `NewPlayer`, `StartMonitor`, `Engine.Stop`) — WASAPI init takes 100s of ms, seconds with Bluetooth. Settings uses `levelMonitor` (generation counter discards stale starts) and `startMicPreview` / `detachPreviewAudio` (detach under lock on the UI thread, `Stop` in a goroutine).
  * Native dialogs (`selectSoundFile`/`selectImageFile`): read the owner HWND on the UI thread with `a.dialogOwner()`, then call the dialog from a goroutine. `selectFile` locks the OS thread and inits COM STA itself. Owner HWND keeps it modal and out of the taskbar.
  * Phone-mic server start/stop (`ensurePhoneMicServer`, `stopPhoneMic`), image decoding (use `newChatThumbnail`, never `canvas.Image{File: ...}` for chat: Fyne decodes + scales the full file on the UI thread on every Refresh).
* **UI mutations from goroutines**: Use `fyne.Do(func() { ... })` when modifying widgets, labels, or dialogs from background goroutines. Building new (not yet attached) widgets off-thread is fine; swap them in inside `fyne.Do` (see `rebuildSoundboard`, `addActivityBatch`). Reading widget state (`Entry.Text`, `Select.Selected`, `Canvas().Focused()`) also belongs on the UI thread — use `fyne.DoAndWait` from goroutines if needed (`isTypingInApp`).
* **Don't flood the queue**: one `fyne.Do` per batch, not per item (`addActivityBatch`), throttle high-rate sources (level meter ≤ 15 fps via `levelPaintInterval`), skip refreshes when state is unchanged (`refreshPhoneStatus`). Each `SetText`/`Refresh` can relayout the whole view.
* **Audio never waits for the UI (lock-free shared state)**: the malgo callbacks and `receiveLoop` must never block on something the UI holds.
  * `a.cfg` is an `atomic.Pointer[config.Config]` to an **immutable** snapshot. Read with `cfg := a.conf()` once per frame/function and use the local (consistent fields, one atomic load). Never assign through it. Write with `a.updateConfig(func(c *config.Config) { c.X = v })`: clone + CAS retry loop, no mutex; the closure may run twice, so only assign fields (no I/O). Persist with `a.saveConfig()` (non-blocking, coalesced, written by `configSaver`); never call `config.Save` directly. `quit` calls `flushConfig`.
  * **Settings view = live draft**: controls publish `LiveSettings` fields instantly (so filters can be heard with "Escuchar micrófono"), but nothing reaches disk until **Guardar**. On open, `renderSettingsView` stores `a.cfgBaseline` (copy of `LiveSettings`); while it is non-nil `writeConfig` writes the baseline + current background fields, so a `saveConfig` from `receiveLoop`/phone server or `flushConfig` on quit never persists the draft (closing the app with settings open = discard). Text entries (username, ports) are only committed by Guardar. Volver with changes → dialog Guardar / Descartar / Seguir editando; Cancelar with changes → confirm discard. `discardSettings` restores `c.LiveSettings = baseline` via `updateConfig` (which also reverts the active profile names) and undoes side effects (`logging.SetLevel`, phone-mic server started/stopped during the session). Profile files written during the session (Guardar / Guardar como / Renombrar / Borrar perfil) stay: those are explicit disk actions. A new callback with a side effect outside the config must be undone there too.
  * `a.transport`, `a.endpoint`, `a.audioEngine` are atomic pointers. Load once into a local (`if engine := a.engine(); engine != nil { ... }`): a double read can see nil the second time. Connect publishes with `transport.CompareAndSwap(nil, udp)`; disconnect takes it with `Swap(nil)` (exactly once) **before** `stopEngine()`. `startAudio` publishes via `publishEngine`, which drops the engine if the connection ended while malgo was opening. `endpointAddr()` is resolved at `setEndpoint` time: don't call `ResolveUDPAddr` in send paths.
  * `transport.UDP` peers are a copy-on-write slice: `Broadcast`/`AddPeer` (every packet on the host) read with no lock; only real list changes take `peersMu` (writers only).
* **Shared maps**: `cfg.SoundBinds` belongs to the config snapshot, so it is never mutated in place. Use `updateSoundBinds` (goes through `updateConfig`, then `publishHotkeys` + `saveConfig`). After changing `StopSoundBind`, call `publishHotkeys()`. `SoundBinds` is keyed by sound **ID**, not name: renaming (`renameSound`, propagated as `sound_add` with the same ID; `addSound` persists it with `UpdateSoundMeta`) never touches it; replacing the audio (`replaceSoundAudio`) saves under a new ID (peers already cached the old asset) and moves the bind old ID → new ID.
* **Soundboard filter**: `a.soundFilter` (atomic, lowercase) is read by `rebuildSoundboard` from any goroutine; its `fyne.Do` drops a build made with a stale filter. A concurrent map write is a fatal runtime error: no `recover`, and with `-H windowsgui` nothing reaches the log.
* **Shutdown**: never `window.Close()`. Fyne 2.6.3 sets `closing` (so `view()` returns nil) while events keep arriving; a mouse move then panics in `processMouseMoved → SetInputMode` on goroutine 1. Use `a.quit(cleanup)`: Hide + `fyne.CurrentApp().Quit()` after `stopConnectionAsync()` finishes (max 3 s) so the Leave packet/UPnP unmap are sent.
* **Diagnosing freezes**: `watchdog.go` logs `hilo de UI bloqueado` with the goroutine 1 stack (ERROR level, always on) — check the log before guessing.
* **Never block the malgo callbacks**: no allocations-heavy work, no I/O, keep `playbackMu` held only to pull chunks (that includes the Opus decode of each stream, ~tens of µs). Never `fyne.Do` from them: UI state derived from playback (speaking indicator) is polled by `audioMonitorLoop` through `Engine.AudibleStreams`. The own-voice indicator works the same way: `flashVoiceIndicator` (per sent frame) only stores the time and turns it on once; `sweepVoiceIndicator` (monitor loop) turns it off after `voiceHold`. No timers per frame.
* **No forced GC**: `debug.SetMemoryLimit(512 MiB)` at startup; never `runtime.GC()` / `debug.FreeOSMemory()` (the old one every 30 s forced a full GC that slowed the goroutines that allocate, audio included). The runtime returns free memory to the OS by itself.
* **Per-participant volume**: `a.peerVolumes` (by participant ID, `peerVolumeMu` RWMutex) is the source; `setPeerVolume` updates it + `Engine.SetStreamVolume`, and on slider release persists `config.PeerVolumes[username]` (IDs change per connection). `startAudio` calls `applyPeerVolumes`; `refreshPeersFromRoom` → `loadSavedPeerVolumes` applies saved values to new participants. Avatar chips (`peerChip`, UI-thread only in `a.peerChips`) are updated in place while the participants (IDs) and their order are unchanged, so a slider drag is not interrupted.

### H. Known Audio/Network Debt (not yet fixed)
* The Opus decoder is not reset when a sender restarts (`jitterBuffer.reset`); Opus recovers on its own, but the first frame after a restart may carry state from the previous stream.
* `DecodeFEC` silently falls back to PLC when the next packet has no FEC data, so `Stats.Recovered` counts "FEC attempted".
* UI: `a.roomState` is still read/written from several goroutines without sync (`receiveLoop`, `pingPeers`, `failoverLoop`, `configureHostNetwork`, `saveSettings`); the host branch of `receiveLoop` touches it on every packet (LastSeenUTC). `participantID` and `activeRoomName` are plain fields written by connect goroutines. Next step: a single owner goroutine for room state (messages via channel), same copy-on-write snapshot for readers. `thumbCache` is unbounded (~600 KB per image). `refreshChat` still rebuilds all rows on the UI thread (cheap now that thumbnails are async).

### J. Soundboard Sync (`ui/sound_sync.go`, `history.go`)
* **Storage** (per room, `salas/<sala>/sounds/`): `<id>.pcm` files + append-only `manifest` (last entry per ID wins; entries without a file are dropped by `ListSounds`) + `tombstones` (one deleted ID per line; `DeleteSound` always appends; trimmed to the newest `MaxSoundTombstones` = 500 because the list travels in one UDP packet). `a.sounds` (under `soundMu`) can hold **metadata-only** entries whose audio was never downloaded; they are not persisted and come back on the next sync.
* **Live changes** (peers connected at that moment): `sound_add` / `sound_remove` / `sound_play` over `PacketChat` (host relays). Adding a sound still pushes its audio to everyone (`sendAsset`). A received `sound_remove` goes through `dropSounds` (no re-broadcast).
* **Sync on connect, once**: `requestSoundSync` in `connectClient` and on reconnect after migration; `maybeSendSoundSync` sends `sound_sync_request` (`PacketChatSync`: on-disk sounds + tombstones) once the host's room state arrived, retried every 2 s from `failoverLoop` until the `sound_sync_response`. Host (`handleSoundSyncRequest`): applies the client's tombstones (broadcasts `sound_remove` to the others), adds the sounds only the client has (broadcasts `sound_add`, records the client in `soundHolders`), replies with the full list + its tombstones. Client (`handleSoundSyncResponse`): drops tombstoned sounds, takes the host's list (and order) as the soundboard, keeps local leftovers at the end, applies renames to the manifest. Name conflicts: host wins. Idempotent (a resent request announces nothing new).
* **Audio on demand**: `playOrFetchSound` (own click and incoming `sound_play`) plays from disk or marks `pendingPlays[id]` and calls `fetchSound`: a client asks the host (`AssetMissing`); a host without the file asks one of `soundHolders[id]` (rotating, never the requester). Chunks reaching the host are relayed to the whole room, so everyone who got the `sound_play` receives them. Max one request per sound every 3 s; a pending play older than 10 s is saved but not played. `handleAssetChunk` only saves sounds present in `a.sounds` (no nameless/resurrected manifest entries).

### I. Configurable Options (`voxmesh.json`, `internal/config/`)
`Config` embeds `LiveSettings` (no JSON tag, so keys stay flat and old files load unchanged). **LiveSettings** = applied live by the settings view and reverted on discard. **Other** = committed by Guardar (text entries) or written in the background; the settings view never reverts them. Every new field must go in one of the two groups.

**Single table: `config.Params` (`params.go`).** Every numeric/bool field of `LiveSettings` has exactly one row: json key, label, help, profile (`entrada` / `salida` / none), default, min, max, step, unit, display scale (mixes 0–1 shown as %). From it come `Default()`, `Load`'s range check (out of range → default; `ResetOutOfRange`), profile load/save and every slider/check in Settings (`paramControls`, `ui_audio_params.go`). `TestParamsMatchLiveSettings` fails if a field is missing from the table or a row has no field. **Adding a setting** = field in `LiveSettings` + row in `Params` + (if audio) mapping in `audio/settings.go` + key in one of `inputSections`/`outputSections` (`TestParamSectionsCoverProfiles`). Default values are not written anywhere else.

Strings stay outside the table: `MicMode` (stored only, push-to-talk not implemented), `InputDevice`/`OutputDevice` (`config.DefaultDevice`), `ActiveInputProfile`/`ActiveOutputProfile` ("" = Default). Default ports/names: `config.DefaultListenPort` / `DefaultListenAddress` / `DefaultPhoneMicPort` / `DefaultUsername`. Data folder: `config.AppDir()` (`%AppData%\VoxMesh`: salas, perfiles, logs, phone cert).

**When values take effect.** Input chain: per frame (`audio.InputSettings(a.conf())` in `prepareOutgoingFrames`). Opus: next encoded frame. Output chain, jitter buffer, bus limiter: next playback callback (`playbackSettings` provider). Phone buffer/drift: next phone server start. `peer_volume_max`: avatar sliders built afterwards (the Engine clamps at the table max, 5). Devices: settings meter immediately; in a room, Guardar calls `restartAudioInRoom`. `log_level`: immediately (`logging.SetLevel`). `show_status_bar`, `allow_host_migration`: on Guardar.

**Factory defaults** (Default profile): input = voice filter + RNNoise (100 %) + VAD (0.5, 250 ms) + shout mode (-18 dBFS RMS or saturation, 20 % original, 300 ms); gate, gain, EQ, compressor, expander and user limiter off; send ceiling -1.7 dBFS; Opus VoIP 40 kbps / 9 / FEC / 8 %; phone buffer 100 ms. Output = filter off (gain +5 dB and limiter -1 dB stored but inactive), jitter max 300 / min 40 ms. **Default is never persisted as values**: at startup `ui.New` runs `config.ApplyDefaultProfiles` after `MigrateToProfiles`, so whoever has Default active (input and/or output) gets this version's factory values; changing a default in the table reaches them on the next update. Users on a named profile keep their values.

**Profiles** (`profiles.go`, UI in `ui_audio_params.go`): input and output are separate, unlimited, one file each in `<AppDir>/perfiles/entrada|salida/<sanitized name>.json` = `{"version":1,"name","kind", key: value...}` with only that kind's params (never devices, so they can be copied to another PC). Loading = defaults + file on top (missing/out-of-range keys → default), so old profiles survive new params. "Default" is built in (not a file, can't be saved over, renamed or deleted); the ↺ button loads it. Changes made while Default is active are not lost: on Guardar (settings) `saveDefaultChangesAsProfile` writes them to a new profile "Personalizado" ("Personalizado 2"..., `config.NextCustomProfileName`) and makes it active. Profile bar above each advanced section: select (loads into the live draft: audible, discardable), Guardar (overwrites the active one; on Default acts as Guardar como), Guardar como…, Renombrar, Borrar, and "(modificado)" when current values differ from the loaded profile (`SameProfileValues`). Profile file I/O runs off the UI thread (`showSettings` reads the lists and the active profile before rendering). **Migration**: on startup, if `<AppDir>/perfiles` does not exist and a `voxmesh.json` did, the current settings are saved as "Mi configuración" (input and output) and made active (`MigrateToProfiles`, `ui.New`).

**Saturation indicator** (red = input peak ≥ `config.AudioClipPeak`, held `clipHold` = 1 s): settings level meter (bar + "SATURA" text; fed by the device monitor outside a room, and by `reportInputLevel` → `a.levelListener` from the room mic, the mic preview and the phone) and a red ring on the avatar (`peerView.clipUntil`, own from `sendOutgoingPCM`, others from `FlagInputClipped`; cleared by `sweepSpeakingPeers`).

**Settings layout.** Input card: device, level meter (RMS dBFS, same scale as the shout threshold; red when saturating), mode, "Aplicar filtro de voz" + Configuración avanzada + ↺ → profile bar + accordion: Modo gritos · Supresión de ruido y voz · Ecualización · Dinámica · Envío · Codec Opus · Micrófono del celular. Output card: same pattern with Filtros · Buffer de recepción · Mezcla. Network card: ports + host migration. Interface card: status bar, event sounds, log level.

**Fixed internals** (not exposed on purpose, `internal/config/audio.go`): send limiter attack 0, softClip knee/range/curve, bus limiter attack, Opus max packet, jitter splice / fade-out / drift cooldown / quiet RMS / window / min samples / histogram bucket / peak threshold, count and memory / network stream eviction (60 s); also `antiClickSamples`, FIFO decay 0.8, FIFO stream eviction 2 s (`engine.go`), `talkspurtGap` 60 ms (protocol, `network_audio.go`), speaking-indicator timings.

---

## 4. Build & Release Commands

* **Build trap**: `engine.go`, `codec.go`, `engine_test.go`, `player.go` and all of `internal/ui` are `//go:build cgo`. With `CGO_ENABLED=0` (or no gcc on PATH) `go build`/`go test` still succeed but silently produce the headless build. Always confirm `go env CGO_ENABLED` = `1`.
* **Opus**: needs libopus + pkg-config from msys2 (`pacman -S mingw-w64-ucrt-x86_64-opus mingw-w64-ucrt-x86_64-pkgconf`) and **`-tags nolibopusfile` on every cgo build/test/vet** (otherwise `hraban/opus` also wants libopusfile). `-extldflags -static` links `libopus.a`: `objdump -p dist/voxmesh.exe | grep "DLL Name"` must list only system DLLs. For gopls/VS Code: `"go.buildTags": "nolibopusfile"` (or `GOFLAGS=-tags=nolibopusfile`).
* **Local Windows Build**:
  ```powershell
  .\build.ps1
  # Produces: dist/voxmesh.exe (with static CGO linking, gcc from C:\msys64\ucrt64 or mingw64)
  ```
* **Manual build / tests with CGO**:
  ```powershell
  $env:CGO_ENABLED="1"; $env:CC="C:\msys64\ucrt64\bin\gcc.exe"; $env:Path="C:\msys64\ucrt64\bin;$env:Path"
  go build -tags nolibopusfile -o bin/voxmesh.exe ./cmd/voxmesh
  go test -tags nolibopusfile ./...
  go test -race -tags nolibopusfile ./internal/audio/
  ```
  First cgo build/vet takes several minutes (Fyne + malgo).
* **Create a Release**:
  ```powershell
  .\release.ps1 v0.9.7 "Notas del release"
  # Creates git tag, pushes to GitHub, triggers .github/workflows/release.yml
  # Automatically builds and publishes voxmesh.exe + voxmesh-mic.apk to GitHub Releases.
  ```
