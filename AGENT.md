# AGENT.md - VoxMesh Technical Architecture & Project Map

> **Context for AI Agents & Developers**: This document is a token-efficient, high-density map of the entire VoxMesh codebase. Read this first to understand the architecture, data structures, threading rules, and pipelines without scanning multiple source files.

---

## 1. System Overview

**VoxMesh** is a low-latency, decentralized peer-to-peer and star-topology voice & text communications app built in Go with native CGO audio filters and a Fyne GUI.

* **Language**: Go 1.24+ (Requires `CGO_ENABLED=1`).
* **GUI Toolkit**: Fyne v2.6.3.
* **Audio Engine**: `malgo` (miniaudio wrapper) + `rnnoise-go` (AI neural noise suppression).
* **Audio Spec**: 48,000 Hz, 16-bit S16LE, Mono, 20 ms frames (960 samples = 1920 bytes).
* **Network**: UDP-only direct/mesh with host election and room migration.
* **Platform**: Primary Windows 10/11 x64, with Android client (`apk/`) for phone mic.

---

## 2. Directory Structure & Key Components

```
VoxMesh/
├── cmd/voxmesh/main.go            # Entrypoint. Cleans up old update binaries & starts UI.
├── internal/
│   ├── audio/                     # Real-time DSP and sound capture/playback
│   │   ├── engine.go              # malgo capture & playback. Multi-Stream Mixer (PlayStream).
│   │   ├── engine_test.go         # Unit tests for mixer and softClip limiter.
│   │   ├── effects.go             # Noise gate, high-pass, low-pass, notch, compressor, expander.
│   │   ├── rnnoise.go             # RNNoise neural network model integration.
│   │   ├── pcm.go                 # S16LE byte/slice encoding, volume scaling, VU meter.
│   │   ├── player.go              # Standalone SFX/preview audio player (used when engine idle).
│   │   ├── sfx.go                 # Embedded WAV sound cues (connect, disconnect, pop).
│   │   ├── devices.go             # Enumeration of WASAPI/DirectSound input/output devices.
│   │   ├── gate.go & monitor.go   # Voice Activity Detection (VAD) and level monitoring.
│   │   └── sound.go               # Soundboard clip structures and serialization.
│   ├── config/config.go           # Persistent configuration struct (voxmesh.json).
│   ├── history/history.go         # Message store for chat and sync between peers.
│   ├── logging/logging.go         # File and console logger.
│   ├── netinfo/netinfo.go         # Local IP resolution and UPnP port mapping.
│   ├── phonemic/server.go         # Phone-as-Mic server (UDP discovery + WebSocket/HTTP).
│   ├── room/room.go               # Room state, participant list, host migration election.
│   ├── transport/
│   │   ├── udp.go                 # UDP socket management, broadcast, packet framing.
│   │   ├── udp_windows.go         # Windows socket buffer tuning (SO_RCVBUF, SO_SNDBUF).
│   │   └── udp_other.go           # Fallback for non-Windows platforms.
│   ├── ui/
│   │   ├── ui.go                  # Main UI, event loop, message handling, auto-updater UI.
│   │   ├── file_windows.go        # Windows native OpenFileDialog / Explorer integration.
│   │   ├── hotkey_windows.go      # Windows RegisterHotKey listener and async key poller.
│   │   └── hotkey_other.go        # Non-Windows hotkey stubs.
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

### A. Audio Multi-Stream Mixer (`internal/audio/engine.go`)
* **Problem**: If multiple peers speak simultaneously, sequential single-queue playback chops audio like a machine gun and drops packets.
* **Architecture**:
  * `Engine` maintains `streams map[string]*audioStream` protected by `playbackMu`.
  * `PlayStream(senderID string, data []byte)` routes packets into that sender's private buffer.
  * In the `malgo` output callback: samples are algebraically summed across all active streams.
  * `softClip(sample int32) int16`: Soft-knee saturation limiter kicks in above 30,000 to prevent harsh digital clipping while clamping strictly within `[-32768, 32767]`.
  * Streams inactive for > 2 seconds are evicted from memory.

### B. Unique Participant Names (`_PUTO` Suffix)
* In `internal/ui/ui.go` (`PacketRoomHello` handler on Host):
  * When a participant connects, if their username collides (case-insensitive) with an already connected peer:
  * The host appends `_PUTO` iteratively (`Pepe` -> `Pepe_PUTO` -> `Pepe_PUTO_PUTO`).
  * Propagated via `PacketRoomState`. The client detects the change, updates local config, header label (`selfNameLabel`), and status log.

### C. Self-Updating on Windows (`internal/updater/updater.go`)
* **Windows Lock Workaround**: Windows blocks modifying/deleting running `.exe` files, but **allows renaming** them on NTFS.
* **Process**:
  1. Downloads `voxmesh.exe.new` into the application directory.
  2. Renames running `voxmesh.exe` -> `voxmesh.exe.old`.
  3. Renames `voxmesh.exe.new` -> `voxmesh.exe`.
  4. Launches new `voxmesh.exe` via `exec.Command` and calls `os.Exit(0)`.
  5. At startup (`main.go`), `updater.CleanupOldVersions()` deletes leftover `.old` and `.new` files.
* **Trigger**: Background goroutine on startup checks GitHub Releases API (`api.github.com/repos/DrCoffee84/VoxMesh/releases/latest`). A popup asks the user for confirmation. Also triggered manually in Settings.

### D. UI Threading & Fyne Concurrency
* **NEVER block the UI goroutine**: Heavy tasks (network calls, audio initialization, file I/O) MUST run inside goroutines (`go func() { ... }()`).
* **UI mutations from goroutines**: Use `fyne.Do(func() { ... })` when modifying widgets, labels, or dialogs from background goroutines.

---

## 4. Build & Release Commands

* **Local Windows Build**:
  ```powershell
  .\build.ps1
  # Produces: dist/voxmesh.exe (with static CGO linking)
  ```
* **Run Tests**:
  ```powershell
  go test ./...
  ```
* **Create a Release**:
  ```powershell
  .\release.ps1 v0.9.1 "Notas del release"
  # Creates git tag, pushes to GitHub, triggers .github/workflows/release.yml
  # Automatically builds and publishes voxmesh.exe + voxmesh-mic.apk to GitHub Releases.
  ```
