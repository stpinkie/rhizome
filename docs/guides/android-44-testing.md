# Android 4.4 (API 19) Compatibility Test Plan

> Back to [Guides](README.md) | See also [Hardware Compatibility](hardware-compatibility.md)

Purpose: verify whether Rhizome can actually run on Android 4.4 devices,
and produce evidence for either keeping or dropping the "Android 4.4+"
claim in the README and hardware-compatibility docs.

## Background

- NDK r26+ removed API <= 20 toolchains. **NDK r25c is the last release
  shipping `androideabi19-clang`** — current builds use `ANDROID_API=21`.
- An API-21-linked binary can still run on older Android *if* it never
  references post-19 bionic symbols — so test the released binary first.
- The universal zip's `librhizome.so` files are PIE executables and run
  directly from `adb shell`.
- Go 1.26.0–1.26.2 had a seccomp regression on 32-bit Android <= 10
  (fixed in 1.26.3+). Build with Go >= 1.26.3.

## Prerequisites

- Android 4.4 device with USB debugging enabled
  (Settings → About → tap "Build number" 7× → Developer options).
- `adb` on the test machine (platform-tools).
- Same LAN for the device and a second Rhizome node.
- `rhizome-android-universal.zip` from the v0.5.0 release.

## Step 0 — Probe the released binary (no build needed)

1. Identify the ABI:

   ```
   adb shell getprop ro.product.cpu.abi
   ```

   - `armeabi-v7a` → use `armeabi-v7a/librhizome.so` (most phones)
   - `x86` → use `x86/librhizome.so` (some Intel tablets)
   - `armeabi` (no `-v7a`) → ARMv5/6, **unsupported**, stop here.

2. Push and run:

   ```
   adb push librhizome.so /data/local/tmp/rhizome
   adb shell chmod 755 /data/local/tmp/rhizome
   adb shell /data/local/tmp/rhizome version
   ```

3. Record the outcome:

   | Result | Meaning | Next step |
   |---|---|---|
   | Prints version | API-21 build is 4.4-compatible | → Step 2 (functional test) |
   | `cannot locate symbol "X"` | 4.4 bionic lacks symbol X | Record symbol(s), → Step 1 |
   | `SIGSYS` | seccomp-blocked syscall | Note kernel/Android version; recheck Go >= 1.26.3 |
   | `SIGILL` / `not executable` | Wrong ABI or non-PIE | Verify `ro.product.cpu.abi` |
   | `cannot execute` / magic error | Corrupt push or wrong file | Re-push, verify checksum |

## Step 1 — Only if symbols are missing: rebuild at API 19

Download NDK **r25c** from the Google NDK archive
(developer.android.com/ndk/downloads/older_releases), then:

```
ANDROID_NDK=<path-to-android-ndk-r25c> ANDROID_API=19 make build-android-arm
# or build-android-386 / build-android-amd64 to match the device ABI
```

- **Link failure** → the error names the exact missing bionic functions.
  That list is the evidence for whether a compat shim is feasible or the
  API floor is real.
- **Links cleanly** → push the new binary and retry Step 0, then continue.

## Step 2 — Functional smoke test

```
adb shell
export RHIZOME_HOME=/data/local/tmp/rhizome-home
/data/local/tmp/rhizome network onboard --generate --encrypt none --yes --non-interactive
/data/local/tmp/rhizome daemon --no-dht
```

Then from a second node on the LAN:

- [ ] `rhizome network ping <4.4-node-multiaddr>` succeeds
- [ ] Trust the peer (`network trust` / config `mesh.trusted_peers`)
- [ ] `rhizome sync` pulls/pushes a workspace file both directions
- [ ] (Stretch) `rhizome network delegate` remote agent call works
- [ ] Daemon stays alive; note free RAM (needs ~256 MB for full daemon)

## Known limitations to expect on 4.4 (independent of build)

| Area | Expected behavior |
|---|---|
| TLS / HTTPS | System CA store predates ISRG Root X1 etc. — outbound calls to modern endpoints (LLM APIs, GitHub) may fail `certificate signed by unknown authority`. Mesh peer traffic uses Noise, not TLS — test LAN and internet paths separately. |
| mDNS | Usually broken on Android Wi-Fi; use `--no-dht` and static `mesh.bootstrap_peers`. |
| Web console | WebView is Chrome 30-era; `librhizome-web.so` ships arm64-only. 4.4 = headless daemon only. |
| Backgrounding | No JobScheduler; keep the adb shell attached for testing. |

## Step 3 — Record and decide

Write the result into `docs/guides/hardware-compatibility.md`:

- **Works** → add "Verified: \<device\>, Android 4.4.x, \<date\>" and tick the
  real-device smoke-test item in `.todo.md`. Then decide: dedicated CI lane
  (job pinned to `ndk-version: r25c` + `ANDROID_API: 19`) vs. documented
  manual recipe.
- **Fails** → record the exact failure (symbol / syscall / signal). That
  evidence determines whether a compat shim is worth it or the claim
  becomes "Android 5.0+ (API 21)".

## Decision matrix

| Outcome | Action |
|---|---|
| API-21 binary runs + mesh works | Keep claim; no build changes needed |
| API-19 rebuild needed but works | Document recipe; consider CI lane |
| Fails on missing symbols | Evaluate shim cost; else floor = API 21 |
| Runs but unusable (TLS/RAM/stability) | Document limits; likely floor = API 21 |
