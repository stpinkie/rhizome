# Rhizome Test Report

## Environment

| Field | Value |
|---|---|
| Agent | devin (Devin Cloud, app.devin.ai session) |
| OS | linux (Ubuntu, kernel on /dev/vda1 124 GB disk, 31 GB RAM, 8 vCPU) |
| Architecture | amd64 |
| Go version | go1.26.6 linux/amd64 |
| Node version | v22.23.2 |
| pnpm version | 10.33.0 |
| Commit | 8aae5cb0 (branch `devin/1789246308-linux-validation-fixes`, on base 1d8dc6fd) |
| Date | 2026-09-12T20:05:00Z |
| Duration | ~1900 seconds (total session) |

## Commands Run

| Step | Command | Exit Code | Duration | Notes |
|---|---|---|---|---|
| 1 | `go version` | 0 | 0s | go1.26.6 linux/amd64 |
| 2 | `go mod download` | 0 | 6s | |
| 3 | `make build-launcher-frontend` | 0 | 8s | vite build to web/backend/dist |
| 4 | `make test` (run 1, parallel) | 2 | 130s | flake: `TestSwarmOfferRetryThenDeadLetter` (60s condition timeout) |
| 5 | `go test -run TestSwarmOfferRetryThenDeadLetter ./pkg/rhizome/swarm` | 0 | 39s | passes in isolation — parallel-load flake |
| 6 | `make build` | 0 | 2s | build/rhizome-linux-amd64 |
| 7 | `CGO_ENABLED=0 make build-all` | 0 | 330s | all 15 cross-compile targets |
| 8 | `bash ./scripts/run-integration-tests.sh` | 0 | 82s | Docker-backed MCP integration suite |
| 9 | `CGO_ENABLED=0 bash ./scripts/integration-mesh.sh` | 0 | 19s | ping, bidirectional sync, reconnect catch-up |
| 10 | `bash ./scripts/integration-swarm.sh` (run 1-2) | 1 | 3s | real bug: `swarm members` printed roster to stderr, `grep` on stdout failed |
| 11 | `make vet` | 0 | 11s | |
| 12 | `make lint` (run 1) | 2 | 13s | real bug: `unconvert` in pkg/browser/diskspace_linux.go |
| 13 | `make lint` (run 2, post-fix) | 0 | 3s | 0 issues |
| 14 | mesh-focused suite (6 packages, parallel) | 1 | 86s | flake: `TestSyncerConflictingEditsConverge` (peers did not converge, dial backoff) |
| 15 | `go test -run TestSyncerConflictingEditsConverge ./pkg/rhizome/sync` | 0 | 1s | passes in isolation |
| 16 | `go test -p 1 -race ./pkg/rhizome/mesh ./pkg/rhizome/stream ./pkg/rhizome/swarm` (run 1) | 1 | 175s | real data race: `Mesh.cfg.SkillShare` read by announceLoop vs test write |
| 17 | same race suite (run 2, post-fix) | 0 | 69s | |
| 18 | `bash ./scripts/integration-swarm.sh` (post-fix run 3) | 1 | 64s | flake: daemon B roster re-establish after restart timed out once |
| 19 | `bash ./scripts/integration-swarm.sh` (post-fix run 4) | 0 | 3s | join, roster exchange, presence, restart persistence all pass |
| 20 | `go test -count=1 ./pkg/rhizome/swarm` | 0 | 48s | package green in isolation |
| 21 | `cd web && make test` (run 1) | 2 | 35s | real failure: `prettier --check` flagged 11 unformatted frontend files on main |
| 22 | `pnpm exec prettier --write .` then `cd web && make test` | 0 | 10s | eslint 0 errors / 2 warnings, prettier clean |
| 23 | `make test` (run 2, parallel) | 2 | 139s | flakes: `TestMeshFanoutAll`, `TestSwarmOfferCancel`, `TestSwarmOfferRetryThenDeadLetter` |
| 24 | `bash ./scripts/validate-small-vm.sh` (run concurrent w/ #23) | 1 | 220s | cross-suite libp2p interference (shared test peer IDs on loopback) — rerun alone |
| 25 | `bash ./scripts/validate-small-vm.sh` (clean run) | 0 | 583s | all 5 stages: build, vet, unit tests, lint-slim, core cross-compile |
| 26 | `go test -p 1 -tags goolm,stdjson -count=1 ./...` (excl. web) | 0 | 278s | **full suite green sequentially** |

## Results

- Passed packages: 102 (`ok` lines in the sequential full-suite run)
- Failed packages: 0 in sequential run; mesh/swarm/sync each flaked once under parallel load
- Skipped packages: 22 packages have no test files

### Failed packages

None in the final sequential run. Under default parallel `go test` (and `make test`), these timing-sensitive tests flake on Linux as they did on Windows:

- `pkg/rhizome/swarm`: `TestSwarmOfferRetryThenDeadLetter`, `TestSwarmOfferCancel`
- `pkg/rhizome/mesh`: `TestMeshFanoutAll`
- `pkg/rhizome/sync`: `TestSyncerConflictingEditsConverge`

All pass in isolation and under `go test -p 1`. This reproduces the Windows flake pattern on Linux: parallel package execution starves libp2p dial/heartbeat timing. Root cause is scheduling/timing contention, not Windows Firewall — the previous "re-verify on Linux" hypothesis is confirmed as load-related, not platform-related.

### Skipped packages / known platform skips

- `pkg/rhizome/p2putil`, `scripts`, `examples/pico-echo-server`, `integration`, etc. report `[no test files]`.
- `validate-small-vm.sh` must not run concurrently with another `go test` pass over the mesh packages: tests derive identical libp2p peer IDs from the shared `testMnemonic`, so two suites on one host dial each other's listeners (`connection refused`, `forbidden: remote delegate is disabled for peer …`).

## Summary

PASS — release gate green on Linux amd64 in sequential mode: `go test -p 1` (102 packages; the default parallel `make test` flaked twice on known timing-sensitive tests — see Results), `make vet`, `make lint` (0 issues), race suite (mesh/stream/swarm), Docker integration suite, mesh integration, swarm integration, web backend/frontend tests, 15-target `make build-all`, and `validate-small-vm.sh` (pending v0.8.4 item — now confirmed clean).

## Notes

Four real defects found and fixed during this validation:

1. **`swarm members` (and `swarm list`/`status`/`offers`) wrote results to stderr**, breaking `cmd | grep` in `scripts/integration-swarm.sh` (works on PowerShell because stderr merges differently). Replaced cobra `cmd.Print*` with `fmt.Fprint*(cmd.OutOrStdout(), …)` in `cmd/rhizome/internal/swarm/status.go` and `offer.go`, honoring `SetOut` redirection.
2. **Data race**: `Mesh.cfg.SkillShare` was mutated by tests post-`Start` while `shareableSkills()` read it from the announce loop. Added `Mesh.skillShareMu` + `Mesh.SetSkillShare`; test now uses the setter.
3. **Lint failure**: redundant `uint64(st.Bavail)` conversion in `pkg/browser/diskspace_linux.go` (unconvert).
4. **`pnpm format` gate red on main**: 11 frontend files unformatted; ran `prettier --write`.

Remaining flakiness is timing-only: mesh/swarm/sync integration tests can exceed their `require.Eventually` budgets under `go test`'s default parallel package execution; all pass with `-p 1` (recommended for small/contended runners) or in isolation.
