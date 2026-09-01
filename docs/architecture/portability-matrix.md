# Rhizome Portability Matrix

This document tracks the build and runtime status of `rhizome` across the supported Go `GOOS/GOARCH` combinations. It is used to decide which targets are built in `make build-all`, released via `.goreleaser.yaml`, and which features are available on each platform.

## Legend

- **Build**: whether `go build -tags goolm,stdjson ./cmd/rhizome` compiles for the target.
- **Matrix**: whether the Matrix channel (`pkg/channels/matrix`) is compiled into the binary.
- **P2P**: whether the libp2p mesh/DHT is expected to work.
- **Release**: whether the target is built by `.goreleaser.yaml` or included in the release artifacts.
- **Notes**: relevant caveats or prerequisites.

## Core targets

| Target        | Build | Matrix | P2P | Release | Notes |
|---------------|-------|--------|-----|---------|-------|
| linux/amd64   | Yes   | Yes    | Yes | Yes     | Primary desktop/server target. |
| linux/386     | Yes   | Yes    | Yes | Yes     | Added for 32-bit x86 boards. |
| linux/arm     | Yes   | Yes    | Yes | Yes     | ARMv7 (GOARM=7); used on Raspberry Pi 2/3 32-bit and Termux/proot. |
| linux/arm64   | Yes   | Yes    | Yes | Yes     | ARMv8; Raspberry Pi 3/4 64-bit. |
| linux/loong64 | Yes   | Yes    | Yes | Yes     | Requires the loong64 pty type patch in `Makefile`. |
| linux/riscv64 | Yes   | Yes    | Yes | Yes     | RISC-V 64-bit boards. |
| linux/mipsle  | Yes   | No     | Yes | Yes     | `goolm` tag removed and Matrix excluded due to `modernc.org/sqlite` / `modernc.org/libc` softfloat issues. |
| windows/amd64 | Yes   | Yes    | Yes | Yes     | Primary Windows desktop. |
| windows/386   | Yes   | Yes    | Yes | Yes     | Added for 32-bit Windows. |
| darwin/arm64  | Yes   | Yes    | Yes | Yes     | Apple Silicon. |
| netbsd/amd64  | Yes   | No     | Yes | Yes     | Matrix excluded due to `modernc.org/sqlite` generated mutex code. |
| netbsd/arm64  | Yes   | No     | Yes | Yes     | Matrix excluded for the same reason. |
| freebsd/amd64 | Yes   | Yes    | Yes | Yes     | FreeBSD x86_64. |
| freebsd/arm64 | Yes   | Yes    | Yes | Yes     | FreeBSD ARM64. |
| freebsd/arm   | Yes   | No     | Yes | No      | 32-bit FreeBSD excluded from Matrix due to `modernc.org/libc` type mismatches. |

## Android targets

All Android builds produce PIE executables. Go 1.23+ requires `-checklinkname=0` for the `wlynxg/anet` package used by libp2p's WebRTC stack, so `ANDROID_LDFLAGS` includes this flag.

| Target         | Build | Matrix | P2P | Release | Notes |
|----------------|-------|--------|-----|---------|-------|
| android/arm64  | Yes   | Yes    | Yes | Yes     | No cgo required; builds with `CGO_ENABLED=0` and `-checklinkname=0`. |
| android/arm    | N/A   | N/A    | N/A | Bundle  | Requires `CGO_ENABLED=1` and `ANDROID_NDK`. Built by `make build-android-bundle` when `ANDROID_NDK` is set. |
| android/386    | N/A   | N/A    | N/A | Bundle  | Requires `CGO_ENABLED=1` and `ANDROID_NDK`. Built by `make build-android-bundle` when `ANDROID_NDK` is set. |
| android/amd64  | N/A   | N/A    | N/A | Bundle  | Requires `CGO_ENABLED=1` and `ANDROID_NDK`. Built by `make build-android-bundle` when `ANDROID_NDK` is set. |

`android/arm64` does not require cgo, but the 32-bit/x86 Android targets need the NDK because Go's linker does not support internal PIE for those ABIs. The universal zip is produced by `make build-android-bundle` and is now built in GitHub Actions for `build.yml`, `release.yml`, and `nightly.yml` (via `nttld/setup-ndk`).

## Build commands

```bash
# Linux i386 (no cgo)
CGO_ENABLED=0 GOOS=linux GOARCH=386 go build -tags goolm,stdjson -ldflags '-s -w' -o rhizome-linux-386 ./cmd/rhizome

# Windows i386 (no cgo)
CGO_ENABLED=0 GOOS=windows GOARCH=386 go build -tags goolm,stdjson -ldflags '-s -w' -o rhizome-windows-386.exe ./cmd/rhizome

# Linux ARMv7 (Termux/proot)
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -tags goolm,stdjson -ldflags '-s -w' -o rhizome-linux-arm ./cmd/rhizome

# Android ARM64 (no cgo)
CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -tags goolm,stdjson -ldflags '-s -w -checklinkname=0' -o rhizome-android-arm64 ./cmd/rhizome
```

## P2P transport notes

- `pkg/rhizome/network/host.go` explicitly uses **TCP with `DisableReuseport()`** and **QUIC**. This avoids the `SO_REUSEPORT` requirement on kernels older than 3.9 and the WebRTC/UDP-GSO paths that can fail on Android 4.4.
- **mDNS** is now best-effort. If it cannot start (e.g., no multicast permission on Android), the daemon logs a warning and continues.
- The full libp2p DHT is available but should be disabled on mobile/old hardware with `rhizome daemon --no-dht` when Internet/DHT connectivity is not available.

## Feature availability

- The **Matrix** channel is available on every target where it compiles. It is currently disabled on `linux/mipsle`, `netbsd/*`, and `freebsd/arm` due to `modernc.org/sqlite` / `mautrix` portability issues.
- The **Feishu** channel is an **optional build** (`-tags feishu`) and is only compiled in on 64-bit targets. Without the build tag it is a stub that returns an error at runtime.
- The **rhizome-launcher** (web UI) is optional and is only guaranteed for desktop targets.
