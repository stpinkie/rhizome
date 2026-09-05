# Rhizome Test Report

## Environment

| Field | Value |
|---|---|
| Agent | devin |
| OS | windows |
| Architecture | amd64 |
| Go version | go1.26.6 windows/amd64 |
| Node version | v24.16.0 |
| pnpm version | 10.33.0 (via `npx pnpm`) |
| Commit | 164de417 (+ uncommitted v0.5.0 hardening changes in working tree) |
| Date | 2026-09-05T23:55:39+08:00 |
| Duration | ~1500 seconds |

## Commands Run

| Step | Command | Exit Code | Duration | Notes |
|---|---|---|---|---|
| Build (full) | `go build -tags goolm,stdjson ./...` | 0 | ~90s | CGO_ENABLED=0, caches under D:\tmp |
| Unit tests (full) | `go test -tags goolm,stdjson ./...` | 0 | ~6m | 97 packages ok, 24 no-test-files, 0 failures |
| Mesh-focused suite | `go test -tags goolm,stdjson -count=1 ./pkg/rhizome/network/ ./pkg/rhizome/mesh/ ./pkg/rhizome/sync/ ./pkg/rhizome/stream/ ./pkg/rhizome/agentrpc/ ./pkg/rhizome/agenttask/` | 0 | ~60s | all ok; agentrpc/agenttask have no test files |
| Mesh integration | `powershell -File scripts\integration-mesh.ps1` | 0* | ~90s | all steps passed: 3× onboard, daemon A, ping, A→B sync, B→A sync, B restart + post-reconnect catch-up. *Outer wrapper surfaced harmless mDNS stderr warnings as error records; script printed the success line. |
| Web deps | `npx pnpm install --frozen-lockfile` | 0 | ~2s | lockfile clean |
| Web build | `npx pnpm build:backend` | 0 | ~2.5s | built into web/backend/dist |
| Web backend tests | `go test -tags goolm,stdjson ./web/backend/...` | 0 | (in full suite) | all ok |
| Web lint | `npx pnpm lint` | 0 | ~10s | 0 errors, 1 pre-existing warning (channel-config-page.tsx exhaustive-deps) |
| Web format | `npx pnpm format` | 1 | ~10s | 18 files flagged by prettier --check — all pre-existing, on files untouched by this change set |
| Lint/vet spot check | `gofmt -l` + `go vet -tags goolm,stdjson` on changed packages | 0 | ~30s | clean |

## Results

- Passed packages: 97
- Failed packages: 0
- Skipped packages: 24 (no test files)

### Failed packages

None.

### Skipped packages / known platform skips

Packages with no test files: `pkg/devices/events`, `pkg/devices/sources`, `pkg/providers/messageutil`, `pkg/providers/protocoltypes`, `pkg/rhizome/agentrpc`, `pkg/rhizome/agenttask`, `pkg/tokenizer`, `scripts`, `web/backend/dashboardauth`, `web/backend/model`, plus other `[no test files]` entries in the log.

## Summary

PASS — full Windows runbook green: build, unit suite (97 pkgs), mesh-focused suite, two-daemon mesh integration (ping + bidirectional sync + reconnect catch-up), web install/build/lint. `pnpm format` reports 18 pre-existing prettier issues on untouched files.

## Notes

- This run covered the working tree containing the uncommitted v0.5.0 hardening changes on top of `164de417`: `require_signed_caps` default flipped to `true`, nonce/timestamp now required on `/rhizome/agent/1.0.0`, tests updated, README/AGENTS/release-notes refreshed, `stale.yml` cron re-enabled, CLI banner rebranded to RHIZOME.
- **Bug found and fixed during validation**: `scripts/integration-mesh.ps1` crashed at the daemon-B restart step — `$processes | Where-Object` unwraps to a scalar when one element remains, breaking `$processes += $bJob`. Fixed with an `@(...)` wrap; the script then passed end-to-end.
- Windows Firewall was left enabled (SKILL.md's disable step requires admin elevation); mDNS multicast warnings appear in logs but do not affect results — the integration script uses explicit bootstrap addresses, not mDNS.
- `pnpm format` (prettier --check) flags 18 files; all are pre-existing style issues on files not touched by this change set. Left as-is to keep the diff clean — candidate for a separate formatting pass.
- `internal.Logo` is still `🦞` (pkg/env.go) and appears in onboarding/version output; the ASCII banner is now RHIZOME but the mascot emoji decision is left to the maintainer.
