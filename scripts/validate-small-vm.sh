#!/usr/bin/env bash
set -euo pipefail

# Validation path for Linux VMs with ~4 GB root FS and limited memory.
# This is a pre-flight subset, NOT the full release gate.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

export CGO_ENABLED=0
export GOCACHE=/tmp/rhizome-gocache
export GOMODCACHE=/tmp/rhizome-gomodcache
export TEMP=/tmp
export TMP=/tmp
export GOGC=30
export GOMEMLIMIT="2GiB"
export PATH="$PATH:$(go env GOPATH)/bin"

echo "=== build rhizome binary ==="
go build -tags goolm,stdjson -o build/rhizome ./cmd/rhizome

echo "=== go vet (in-scope + evolution) ==="
go vet -tags goolm,stdjson \
    ./pkg/evolution/... ./pkg/rhizome/... ./pkg/media/... ./pkg/tools/fs/... ./cmd/rhizome

echo "=== unit tests (evolution + in-scope packages) ==="
go test -p 1 -tags goolm,stdjson -count=1 \
    ./pkg/evolution/... ./pkg/rhizome/... ./pkg/media/... ./pkg/tools/fs/...

echo "=== slim lint ==="
make lint-slim

# golangci-lint's analysis cache can be large; drop it before the cross-compile
# loop so the 4 GB root FS does not fill.
go clean -cache

echo "=== core cross-compile ==="
make build-core

echo "Small-VM validation passed."
