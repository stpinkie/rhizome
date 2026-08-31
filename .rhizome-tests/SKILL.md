---
name: rhizome-tester
description: "Run the Rhizome test suite for the current operating system and write a structured Markdown + JSON report. Use when asked to test the repository, validate a PR, or check cross-platform builds. Supports Windows, Linux, and macOS."
---

# Rhizome Tester

Run the full Rhizome test suite for the current operating system and write a Markdown and JSON report to `.rhizome-tests/reports/`.

## Output

Produce two files:

- `.rhizome-tests/reports/rhizome-test-report-<os>-<arch>-<agent>-<YYYYMMDD-HHMMSS>.md`
- `.rhizome-tests/reports/rhizome-test-report-<os>-<arch>-<agent>-<YYYYMMDD-HHMMSS>.json`

Use `<os>` = `windows`, `linux`, or `darwin`. Use `<arch>` = `amd64` or `arm64`. Use `<agent>` from the `RHIZOME_TEST_AGENT_NAME` environment variable, or `unknown` if not set.

Use `.rhizome-tests/TEMPLATE.md` as the Markdown structure and `.rhizome-tests/report-schema.json` for the JSON structure.

## Prerequisites

- Go 1.25.13 (check with `go version`)
- pnpm 10.33.0 and Node 22 (only for web tests)
- Docker (only on Linux/macOS for integration and mesh tests)
- bash (Linux/macOS) or PowerShell (Windows)

## Build flags

Use these for every Go command:

```bash
export CGO_ENABLED=0
# -tags goolm,stdjson must be passed to go build, go test, go vet, etc.
```

## Operating-system runbooks

### Linux (recommended for full coverage)

1. `go version` — must be 1.25.13+.
2. `make build-launcher-frontend` — build the frontend into `web/backend/dist`.
3. `make test` — run core Go tests and web frontend/backend tests.
4. `make build` — build the current-platform `rhizome` binary.
5. `CGO_ENABLED=0 make build-all` — cross-compile all platforms.
6. `bash ./scripts/run-integration-tests.sh` — Docker-backed integration suites.
7. `CGO_ENABLED=0 bash ./scripts/integration-mesh.sh` — P2P mesh integration.
8. Write the report.

### Windows

1. Run in PowerShell.
2. `go version` — must be 1.25.13+.
3. Disable Windows Firewall for the test run:
   ```powershell
   netsh advfirewall set allprofiles state off
   ```
4. `go build -tags goolm,stdjson ./...`
5. `go test -tags goolm,stdjson ./...`
6. If pnpm/Node are available, build the frontend and run web checks:
   ```powershell
   cd web/frontend
   pnpm install --frozen-lockfile
   pnpm build:backend
   cd ../backend
   $env:CGO_ENABLED='0'
   go test -tags goolm,stdjson ./...
   cd ../frontend
   pnpm lint
   pnpm format
   ```
7. Do **not** run `make build-all` or the Docker integration/mesh scripts on Windows.
8. Write the report.

### macOS

1. `go version` — must be 1.25.13+.
2. `make build-launcher-frontend` — build the frontend.
3. `make test` — core and web tests.
4. `make build` — build the current-platform binary.
5. If Docker is available, optionally run `bash ./scripts/run-integration-tests.sh` and `CGO_ENABLED=0 bash ./scripts/integration-mesh.sh`; otherwise skip them and note this in the report.
6. Write the report.

## Report fields

At minimum, record:

- Agent name, OS, architecture.
- Go, Node, and pnpm versions.
- Commit SHA and timestamp.
- Each command run, its exit code, duration, and a short output excerpt.
- Lists of passed, failed, and skipped packages.
- Any known platform-specific skips or failures.
- A one-line `summary` (`PASS`, `FAIL`, or `FAIL with blockers`) and free-form `notes`.

## Committing the report

After writing the report, if the repo is in a state where a commit is safe and you have write access, stage and commit the new `.rhizome-tests/reports/*` files with a message such as:

```
test: add report from <agent> on <os>/<arch> <date>
```

If you do not have commit access or are unsure, leave the files in `.rhizome-tests/reports/` for the user to review.
