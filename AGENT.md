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
│   │   ├── engine.go              # [cgo] malgo capture & playback. Mixer (PlayStreamSeq for network, PlayStream FIFO for SFX/local), per-stream volume + output chain, JitterReport, AudibleStreams.
│   │   ├── codec.go               # [cgo] Opus: OpusEncoder (send) and newOpusDecoder (one per sender, used by the jitter buffer).
│   │   ├── jitter.go              # Per-sender jitter buffer: stores Opus packets, decodes on playback (FEC → PLC), adaptive target, drift, splice crossfades, arrival stats.
│   │   ├── limiter.go             # peakLimiter: envelope peak limiter (int32 mix bus before softClip; int16 at the end of the send chain).
│   │   ├── effects.go             # Filters, compressor/expander, tanh limiter, send-chain state (envelope limiter, VAD pre-roll), ApplyOutput (skips a no-op chain).
│   │   ├── rnnoise.go             # RNNoise integration (float32 API + floatToInt16 clamp) and outgoing pipeline (PrepareOutgoingFrames).
│   │   ├── pcm.go                 # S16LE byte/slice encoding, frame constants (SampleRate, FrameSamples).
│   │   ├── player.go              # [cgo] Standalone SFX/preview audio player (used when engine idle).
│   │   ├── sfx.go                 # Embedded WAV sound cues (connect, disconnect, pop).
│   │   ├── devices.go             # Enumeration of WASAPI/DirectSound input/output devices.
│   │   ├── gate.go & monitor.go   # RMS noise gate and level monitoring.
│   │   ├── sound.go               # Soundboard clip structures and serialization.
│   │   └── *_test.go              # engine_test needs cgo (Opus); jitter_test uses a fake decoder and runs without it.
│   ├── config/
│   │   ├── config.go              # Persistent configuration struct (voxmesh.json), defaults. Embeds LiveSettings (see §3.I).
│   │   └── audio.go               # Audio pipeline constants: send limiter/pre-roll, softClip, bus limiter, Opus, jitter buffer (adaptive/drift), phone drift.
│   ├── history/history.go         # Message store for chat and sync between peers.
│   ├── logging/logging.go         # File and console logger.
│   ├── netinfo/netinfo.go         # Local IP resolution and UPnP port mapping.
│   ├── phonemic/server.go         # Phone-as-Mic server (UDP discovery + WebSocket/HTTP).
│   ├── room/
│   │   ├── state.go               # Room state, participant list, host migration election (NextHost).
│   │   └── protocol.go            # Envelope encoding for Hello/State/Leave messages.
│   ├── transport/
│   │   ├── udp.go                 # UDP socket, peers, broadcast, 16-byte packet header (see §3.C).
│   │   ├── udp_windows.go         # SO_REUSEADDR on the socket.
│   │   └── udp_other.go           # Fallback for non-Windows platforms.
│   ├── ui/                        # [cgo] package ui — split by responsibility (see §3.D)
│   │   ├── ui_main.go             # App struct, New, Run, setStatus, quit (Hide + Quit), auto-updater dialogs.
│   │   ├── ui_components.go       # Main layout (content), widgets, panels, lightbox, peer avatars.
│   │   ├── ui_settings.go         # Settings view (renderSettingsView: live draft + baseline, save/discard dialogs), levelMonitor, mic/output tests.
│   │   ├── ui_chat.go             # Chat UI, images, activity log (batched, max 200), room history view.
│   │   ├── ui_chat_thumbnail.go   # Async chat image thumbnails (decode + downscale off-thread, cached by path).
│   │   ├── ui_soundboard.go       # Soundboard panel (search filter, edit dialog: rename / replace audio / delete), waveform, trim dialog (max ~87 s = assets.MaxBytes), pickSoundFile, updateSoundBinds (copy-on-write).
│   │   ├── ui_connection.go       # Create/open host, connect client, stopConnection/stopConnectionAsync (drives buttons).
│   │   ├── watchdog.go            # UI-thread watchdog: logs goroutine 1 stack when the UI thread stalls > 750 ms.
│   │   ├── network.go             # receiveLoop (packet dispatch), room state, ping, host monitor/migration.
│   │   ├── network_audio.go       # Audio over the network: handleAudioPacket, sendOutgoingPCM (Opus encode, talkspurt), output filter provider.
│   │   ├── audio_monitor.go       # audioMonitorLoop: speaking indicator from what the Engine played + per-sender loss/jitter (chip + log).
│   │   ├── network_sync.go        # Chat, asset chunks and history sync over the network.
│   │   ├── peers.go               # Peer list state (lag, speaking indicator, net stats) and per-participant volume (peerVolumes).
│   │   ├── config_store.go        # Copy-on-write config: conf() / updateConfig (CAS) / saveConfig (async, coalesced).
│   │   ├── connection_state.go    # Atomic transport / endpoint (pre-resolved) / Engine: connected, setEndpoint, publishEngine, stopEngine.
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
1. malgo capture callback re-frames into exact 1920-byte frames (`captureFrames` chan, drops if full). Phone mic frames enter the same function.
2. `Effects.ApplySamples` (gain, optional filters). The tanh limiter here only runs if `InputGainDB > 0` (protection; it distorts what RNNoise sees).
3. **RNNoise always runs** when enabled — there is NO bypass for loud frames. RNNoise has a structural 20 ms delay (`Denoiser.Delay() = 2*480`); skipping it frame-by-frame reorders/duplicates audio. Uses `denoiser.Process` (float32) + `floatToInt16` clamp because `ProcessInt16` wraps around on overflow (the original "cirkci" on shouts). Processes in place.
4. VAD/gate hold (`KeepVoiceOpen`).
5. **Single envelope limiter** (`Effects.limitSend`, `peakLimiter` with instant attack / 60 ms release) on EVERY frame, sent or not. Ceiling = `AudioSendPeakCeiling` (27000) or the user's `LimiterThresholdDB` if enabled and lower. No per-frame static gain anymore.
6. `Effects.gateFrames`: closed gate → nothing sent (DTX), frame kept as **pre-roll** (last `AudioPreRollFrames` = 2). Gate opening → pre-roll frames first, then the current one, with the 3 ms fade-in on the first of them (the first syllable is no longer clipped; no extra latency, the receiver waits for its cushion anyway).
7. Mute drops the frames after the chain (§3.E2). `LiveMonitoring` plays the PCM (not encoded) via `engine.Play`.
8. Opus encode once per frame (`a.encoder`, recreated in `startAudio`; `AppVoIP`, 40 kbps, complexity 9, in-band FEC on, 8 % expected loss, Opus DTX off). Payload = `[idLen=8][participant ID as 8 raw bytes][opus]`, `Sequence = a.audioSequence.Add(1)`, `Flags = FlagAudioSequence`, `Talkspurt = a.talkspurt` (incremented when > 60 ms passed since the last audio packet sent). `sendMu` serializes the send (mic and phone may overlap while switching input).

**Incoming** (`ui/network_audio.go` `handleAudioPacket`, the single audio entry point for both host and client branches of `receiveLoop`):
* The host **relays the packet first** (`udp.Broadcast` before any processing).
* Requires `FlagAudioSequence`; the ID bytes are hex-encoded back to the participant ID; `Engine.PlayStreamSeq(senderID, seq, talkspurt, opus)` → per-sender jitter buffer. No PCM work in `receiveLoop`.

**Mixer** (`audio/engine.go` `Engine.mix`, malgo playback callback):
* `Engine.streams map[string]*audioStream` protected by `playbackMu`. Short lock: pull each stream's chunk (int16 `scratchBuffer`); everything else outside the lock into `mixBuffer []int32` (no integer overflow possible).
* Network streams read from their jitter buffer (decode + fades/PLC happen there, under the lock). `__sfx__` / `__local__` use the old PCM FIFO with prebuffer/fade-in/decay (`PlayStream`, only for those two).
* **Output chain per sender**: each network stream has its own `Effects` (`stream.fx`) applied to the decoded audio in sequence order, with the settings from `Engine.SetOutputFilter(a.outputFilter)` (one `a.conf()` load per callback). `ApplyOutput` skips the chain when it would be a no-op (no gain/filters and block peak ≤ limiter threshold).
* Per-stream `volume` is applied while summing. `Engine.volumes` keeps the per-sender volume even before the stream exists or after it is evicted (`SetStreamVolume`, clamped 0–3).
* `stream.lastAudible` is set when the jitter buffer returned non-zero audio: `AudibleStreams` feeds the speaking indicator (§3.G).
* `mixLimiter.Process` (peak detector with instant attack + gain smoothing 1 ms attack / 80 ms release, threshold 29000) then `softClip` (knee 30000, asymptote 32700) as a safety net for attack overshoot.
* Streams inactive for > 2 seconds are evicted.
* All thresholds live in `internal/config/audio.go` — do not hardcode new ones in `audio/`.

**Measurement** (`ui/audio_monitor.go`, goroutine started in `Run`): every 100 ms marks speaking peers from `Engine.AudibleStreams`; every 2 s reads `Engine.JitterReport()` and publishes per-peer loss % (interval delta) and jitter to the avatar chip (`🟢 45 ms · 2% · ±6 ms`); every 10 s logs one `Audio de <name>: ...` line per sender at level 1 (control). Compare those lines before/after changes.

### B. Per-Sender Jitter Buffer (`internal/audio/jitter.go`)
* Ring of 64 slots indexed by `seq % 64` holding the **Opus packets as received**; `playSeq` = next frame to play. Decoding happens in `Read` (inside the malgo callback) into preallocated buffers: no allocations in `Read`. Not concurrent-safe: only touched under `Engine.playbackMu`.
* The decoder is an interface (`frameDecoder`: `Decode` / `DecodeFEC` / `DecodePLC`); production uses `*opus.Decoder`, `jitter_test.go` a fake (runs without CGO). Each call gets exactly one frame (`cap(pcm)` = duration).
* **Push**: late (`seq < playSeq`) → dropped (`Stats.Late`); duplicate → ignored; jump > 64 frames either way → `reset` (sender restarted, `Stats.Resets`). Every arrival also feeds `arrivalStats` (below).
* **Missing frame with later frames buffered** = loss: if packet N+1 is there → `DecodeFEC(N+1)` recovers N (`Stats.Recovered`); otherwise `DecodePLC` (`Stats.Concealed`), up to `JitterPLCMaxFrames` (5, 100 ms), then silence. `playSeq` advances.
* **Buffer empty** = lag spike or end of phrase → one PLC frame faded to silence over 10 ms and **`playSeq` does NOT advance**, so the delayed packet still plays. Resume rule: if the last real frame was < 300 ms ago (`JitterTalkspurtGapMS`) resume as soon as a frame arrives; otherwise (new phrase) wait for `targetFrames` or that much time.
* **Adaptive target** (`adaptiveTarget`, recomputed only while `!playing`, so never mid-phrase): `clamp(ceil(p95 / 20 ms) + 1, 2, JitterBufferMS/20)`, where p95 is the 95th percentile of the delay variation over the last 500 arrivals (~10 s of speech; histogram of 5 ms buckets). Fewer than 50 samples → the max. `JitterBufferMS` (settings slider) is the **maximum**.
* **Arrival stats**: per phrase (`Packet.Talkspurt` changes, or > 1 s without packets) `offset = arrival − seq·20 ms`; delay variation = offset − min offset of the phrase. Without the talkspurt split the DTX silence (contiguous seq, late arrival) would be measured as delay. Also RFC 3550 jitter `J += (|D| − J)/16`. Exposed in `Stats()` (`JitterMS`, `DelayP95MS`, `TargetMS`, `BufferedFrames`).
* **Drift**: if the buffered span > target + 2, one frame has to go (at most every 5 frames). The frame is decoded anyway (decoder continuity) and dropped only if its RMS < `JitterDriftQuietRMS` (~ −40 dBFS); otherwise the drop is postponed to the next quiet frame, unless the span exceeds target + 2 + `JitterDriftForceFrames`. A *missing* frame while over target is skipped instead of concealed.
* **Splice**: output holds back the last 120 samples (2.5 ms) of each frame. Decoder output (real, FEC, PLC and the fade) is continuous and passes bit-exact; a discontinuity (phrase start, drift drop, reset) crossfades the held tail into the new frame (output becomes 120 samples shorter — intentional).
* The sender's `audioSequence` only advances on sent audio, so it stays contiguous across VAD silence: a gap in seq means real loss.

### C. Packet Header (`internal/transport/udp.go`)
* 16 bytes: `[0:4]` magic `VM01`, `[4]` Kind, `[5]` Flags, `[6]` Talkspurt (audio only), `[7]` unused, `[8:12]` Sequence (BE), `[12:16]` payload length (BE).
* `FlagAudioSequence` (bit 0): `Sequence` is the sender's own contiguous audio counter. Without it, `Sequence` is the global `a.sequence` shared by ping/chat/assets/room packets (has gaps — never use it for loss detection). Audio without the flag is ignored.
* `Talkspurt`: phrase number of the audio sender (§3.A step 8, §3.B arrival stats).
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
* **Send cut** (`sendOutgoingPCM`): the filter chain still runs (RNNoise 20 ms delay + VAD hold stay coherent, no stale audio on unmute); the frame is dropped like DTX — no packet, no live monitoring, no speaking indicator, `audioSequence` does not advance. On unmute the > 60 ms gap starts a new talkspurt.
* **Signaling**: `room.Participant.Muted` (`json:"muted,omitempty"`). Client → host: `PacketRoomMute` with `Envelope{Kind: "mute", Participant: {ID, Muted}}`. Host applies it with `State.SetMuted(id, addr, muted)` (only from the address registered in Hello; copy-on-write slice) and broadcasts `PacketRoomState` to everyone (sender included = ack); unchanged → state sent back to the sender only. Host's own mute: `SetMuted` + broadcast. Hello (`connectClient`, `sendRoomHello`), `createHost` and `becomeHost` carry the current value.
* **Loss**: `monitorHost` resends every 2 s while the host's state differs from the local value (`selfMuteSynced`). Hosts < this version ignore kind 12, so clients keep resending (tiny packet).
* **Avatars**: `peerView.muted` (self from `isMuted`, others from `Participant.Muted`). `newMuteBadge` = red `theme.VolumeMuteIcon` on a dark circle, bottom-right of the avatar, 3rd object of the avatar Stack; `refreshPeerAvatars` shows/hides it inside its `fyne.Do`.

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
  * **Settings view = live draft**: controls publish `LiveSettings` fields instantly (so filters can be heard with "Escuchar micrófono"), but nothing reaches disk until **Guardar**. On open, `renderSettingsView` stores `a.cfgBaseline` (copy of `LiveSettings`); while it is non-nil `writeConfig` writes the baseline + current background fields, so a `saveConfig` from `receiveLoop`/phone server or `flushConfig` on quit never persists the draft (closing the app with settings open = discard). Text entries (username, ports, phone buffer) are only committed by Guardar. Volver with changes → dialog Guardar / Descartar / Seguir editando; Cancelar with changes → confirm discard. `discardSettings` restores `c.LiveSettings = baseline` via `updateConfig` and undoes side effects (`logging.SetLevel`, `SetJitterBufferMS`, phone-mic server started/stopped during the session). A new callback with a side effect outside the config must be undone there too.
  * `a.transport`, `a.endpoint`, `a.audioEngine` are atomic pointers. Load once into a local (`if engine := a.engine(); engine != nil { ... }`): a double read can see nil the second time. Connect publishes with `transport.CompareAndSwap(nil, udp)`; disconnect takes it with `Swap(nil)` (exactly once) **before** `stopEngine()`. `startAudio` publishes via `publishEngine`, which drops the engine if the connection ended while malgo was opening. `endpointAddr()` is resolved at `setEndpoint` time: don't call `ResolveUDPAddr` in send paths.
  * `transport.UDP` peers are a copy-on-write slice: `Broadcast`/`AddPeer` (every packet on the host) read with no lock; only real list changes take `peersMu` (writers only).
* **Shared maps**: `cfg.SoundBinds` belongs to the config snapshot, so it is never mutated in place. Use `updateSoundBinds` (goes through `updateConfig`, then `publishHotkeys` + `saveConfig`). After changing `StopSoundBind`, call `publishHotkeys()`. `SoundBinds` is keyed by sound **ID**, not name: renaming (`renameSound`, propagated as `sound_add` with the same ID; `addSound` persists it with `UpdateSoundMeta`) never touches it; replacing the audio (`replaceSoundAudio`) saves under a new ID (peers already cached the old asset) and moves the bind old ID → new ID.
* **Soundboard filter**: `a.soundFilter` (atomic, lowercase) is read by `rebuildSoundboard` from any goroutine; its `fyne.Do` drops a build made with a stale filter. A concurrent map write is a fatal runtime error: no `recover`, and with `-H windowsgui` nothing reaches the log.
* **Shutdown**: never `window.Close()`. Fyne 2.6.3 sets `closing` (so `view()` returns nil) while events keep arriving; a mouse move then panics in `processMouseMoved → SetInputMode` on goroutine 1. Use `a.quit(cleanup)`: Hide + `fyne.CurrentApp().Quit()` after `stopConnectionAsync()` finishes (max 3 s) so the Leave packet/UPnP unmap are sent.
* **Diagnosing freezes**: `watchdog.go` logs `hilo de UI bloqueado` with the goroutine 1 stack (ERROR level, always on) — check the log before guessing.
* **Never block the malgo callbacks**: no allocations-heavy work, no I/O, keep `playbackMu` held only to pull chunks (that includes the Opus decode of each stream, ~tens of µs). Never `fyne.Do` from them: UI state derived from playback (speaking indicator) is polled by `audioMonitorLoop` through `Engine.AudibleStreams`.
* **Per-participant volume**: `a.peerVolumes` (by participant ID, `peerVolumeMu` RWMutex) is the source; `setPeerVolume` updates it + `Engine.SetStreamVolume`, and on slider release persists `config.PeerVolumes[username]` (IDs change per connection). `startAudio` calls `applyPeerVolumes`; `refreshPeersFromRoom` → `loadSavedPeerVolumes` applies saved values to new participants. Avatar chips (`peerChip`, UI-thread only in `a.peerChips`) are updated in place while the participants (IDs) and their order are unchanged, so a slider drag is not interrupted.

### H. Known Audio/Network Debt (not yet fixed)
* `effects.go`: high-pass and notch store the raw input as `x[n-1]` instead of the filter's own input → huge low-frequency gain when `GainDB != 0` or filters are chained. Compressor/expander act per sample with no envelope (waveshaping). All off by default.
* The Opus decoder is not reset when a sender restarts (`jitterBuffer.reset`); Opus recovers on its own, but the first frame after a restart may carry state from the previous stream.
* `DecodeFEC` silently falls back to PLC when the next packet has no FEC data, so `Stats.Recovered` counts "FEC attempted".
* UI: `a.roomState` is still read/written from several goroutines without sync (`receiveLoop`, `pingPeers`, `monitorHost`, `configureHostNetwork`, `saveSettings`); the host branch of `receiveLoop` touches it on every packet (LastSeenUTC). `participantID`, `activeRoomName`, `pingStop`/`monitorStop` are plain fields written by connect goroutines. Next step: a single owner goroutine for room state (messages via channel), same copy-on-write snapshot for readers. `thumbCache` is unbounded (~600 KB per image). `refreshChat` still rebuilds all rows on the UI thread (cheap now that thumbnails are async).

### I. Configurable Options Map (`voxmesh.json`, `internal/config/config.go`)
`Config` embeds `LiveSettings` (no JSON tag, so keys stay flat and old files load unchanged). **LiveSettings** = applied live by the settings view and reverted on discard. **Other** = committed by Guardar (text entries) or written in the background; the settings view never reverts them. Every new field must go in one of the two groups. "Per frame" = read with `a.conf()` on every audio frame, so it takes effect immediately.

**Input processing** (Settings → Entrada de Audio → Configuración avanzada; consumed per frame in `prepareOutgoingPCM`, `ui/network_audio.go`). Reset button ↺ restores all of these to `config.Default()` except `MicMode` and `LiveMonitoring`.

| Field (json key) | Default | UI range | Notes |
|---|---|---|---|
| `MicMode` (`mic_mode`) | `voice` | radio voice / ptt | Group: LiveSettings. **Stored only, no consumer: push-to-talk is not implemented.** |
| `AudioFilterEnabled` (`audio_filter_enabled`) | true | check | Master switch for the whole outgoing chain. |
| `RNNoiseEnabled` (`rnnoise_enabled`) | true | check | See §3.A (no bypass). |
| `VADEnabled` / `VADThreshold` / `VADHoldMS` | true / 0.5 / 250 | check / 0–1 step 0.05 / 0–500 ms | RNNoise voice probability gate + hold. |
| `NoiseGateEnabled` / `ThresholdDB` / `GateHoldMS` | true / -42 / 250 | check / -60…-5 dBFS / 0–500 ms | RMS gate (`audio/gate.go`). |
| `InputGainDB` | 0 | -24…24 dB | |
| `HighPassEnabled` / `HighPassHz` | false / 80 | check / 20–500 Hz | See §3.H filter bug. |
| `LowPassEnabled` / `LowPassHz` | false / 12000 | check / 1000–18000 Hz | |
| `NotchEnabled` / `NotchHz` | false / 50 | check / 40–1000 Hz | |
| `CompressorEnabled` / `CompressorThresholdDB` / `CompressorRatio` | false / -18 / 3 | check / -50…0 dB / 1–12 | Per-sample, no envelope (§3.H). |
| `ExpanderEnabled` / `ExpanderThresholdDB` / `ExpanderRatio` | false / -50 / 2 | check / -70…-10 dB / 1–12 | Per-sample, no envelope (§3.H). |
| `LimiterEnabled` / `LimiterThresholdDB` | true / -1 | check / -12…0 dB | Ceiling of the final envelope limiter when lower than 27000 (§3.A step 5); the input tanh only acts when `InputGainDB > 0`. |
| `LiveMonitoring` (`live_monitoring`) | false | check | Hear own processed voice while in a room (`network_audio.go`). |

**Output processing** (Settings → Salida de Audio → Configuración avanzada; read once per playback callback via `a.outputFilter`; group LiveSettings; ↺ resets them).

| Field | Default | UI range | Notes |
|---|---|---|---|
| `OutputFilterEnabled` | true | check | Master switch. Applied per sender inside the Engine (`stream.fx`, decoded audio, sequence order); skipped when it would be a no-op. |
| `OutputGainDB` | 0 | -24…24 dB | |
| `OutputHighPassEnabled` / `OutputHighPassHz` | false / 80 | check / 20–500 Hz | |
| `OutputLowPassEnabled` / `OutputLowPassHz` | false / 12000 | check / 1000–18000 Hz | |
| `OutputLimiterEnabled` / `OutputLimiterDB` | true / -1 | check / -12…0 dB | |

**Devices, network, system** (group in brackets).

| Field | Default | UI | When it takes effect |
|---|---|---|---|
| `InputDevice` [Live] | `Sistema predeterminado` | Select (also `phoneMicInput`) | Settings level meter: immediately. In a room: Guardar calls `restartAudioInRoom`. Choosing the phone starts its server immediately. |
| `OutputDevice` [Live] | `Sistema predeterminado` | Select | Same as above; also used by `NewPlayer` (SFX/preview). |
| `PhoneMicBufferEnabled` / `PhoneMicBufferMS` [Live] | true / 40 | check + entry 40–1000 + ✓ | Read in `phonemic.Start`: needs a phone server restart. ✓ applies in memory only. The room jitter buffer already absorbs Wi-Fi jitter; `forwardPCM` drops a (preferably silent) frame when the queue exceeds prebuffer + 2 (phone clock drift). Existing `voxmesh.json` files keep their saved value. |
| `JitterBufferMS` [Live] | 120 | slider 40–500 step 10, "Buffer de recepción máximo (adaptativo)" | **Maximum** of the adaptive cushion (§3.B). Immediately (`engine.SetJitterBufferMS`) + on `startAudio`. `Load` clamps out-of-range to 120. |
| `AllowHostMigration` [Live] | true | check | `CanBeHost` sent in Hello/room state; Guardar updates own participant. |
| `ShowStatusBar` [Live] | true | check | On Guardar (`bottomBar` show/hide) and when building the main layout. |
| `EventSoundsEnabled` [Live] | true | check | Immediately (`audio_bridge.go`, SFX). |
| `LogLevel` [Live] | 0 | select 0 errors / 1 control / 2 trace | Immediately (`logging.SetLevel`) + startup. `Load` clamps to 0–2. |
| `Username` [Other] | `Usuario` | entry, Guardar | Header label; participant ID on next connect. Host may rename it with `_PUTO` (§3.E). Empty → `Usuario`. |
| `ListenAddress` [Other] | `:47830` | entry "Puerto UDP de la sala", Guardar | Next create/connect. No validation of the port value. |
| `PhoneMicPort` [Other] | 47831 | entry, Guardar (1–65535) | Next phone server start; overwritten with the actually bound port. |
| `RoomName` [Other] | `Mi sala` | connection panel | On create/join; updated from host state. |
| `LastPeer` [Other] | "" | connection panel | Written on connect and on host migration (background). |
| `PhoneMicToken` [Other] | "" | none | Generated by the phone server, background. |
| `SoundboardMuted` / `SoundboardVolume` [Other] | false / 1.0 | soundboard panel | Immediately; `Load` resets volume ≤ 0 to 1.0. |
| `SoundBinds` / `StopSoundBind` [Other] | {} / "" | soundboard bind dialogs | Via `updateSoundBinds` / `publishHotkeys` (§3.G). |
| `PeerVolumes` (`peer_volumes`) [Other] | {} | slider 0–300 % under each avatar | Keyed by username; written on slider release (1.0 is removed). Applied immediately (`setPeerVolume`, §3.G). Copied by `Config.Clone`. |
| `WindowWidth` / `WindowHeight` / `WindowMaximized` [Other] | 900 / 620 / false | window itself | Captured in `quit` (`rememberWindowState`, before Hide); applied in `Run` (Resize, then `maximizeWindow` after Show, Windows only). Size = last normal size in Fyne units (kept when closed maximized/minimized). `Load` resets < 400×300 to default. |

**Hardcoded tunables not exposed in the UI** are centralized in `internal/config/audio.go` (send ceiling/limiter/pre-roll, softClip knee/range/curve, bus limiter, Opus bitrate/complexity/FEC, jitter splice/PLC/fade/talkspurt gap/drift/adaptive window & percentile, phone drift). Candidates for a future "advanced" section; other magic numbers may still live in `audio/` and `ui/`.

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
