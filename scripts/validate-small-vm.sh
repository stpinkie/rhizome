#!/usr/bin/env bash
set -euo pipefail

# Validation path for Linux VMs with ~4 GB root FS and limited memory.
# This is a pre-flight subset, NOT the full release gate.
#
# Optional environment variables to place heavy caches/output on a large host
# mount:
#   GOCACHE, GOMODCACHE        Go build and module caches
#   RHIZOME_BUILD_DIR          Where build/ and cross-compile binaries go
#   RHIZOME_BUILD_CLEAN=1      Force removal of cross-compile binaries
#
# The script will automatically keep the Go build cache when GOCACHE is on a
# large volume and will remove cross-compile artifacts when BUILD_DIR is on the
# small overlay.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

source "${repo_root}/scripts/rhizome-small-vm-helpers.sh"

export CGO_ENABLED=0
export GOCACHE="${GOCACHE:-/tmp/rhizome-gocache}"
export GOMODCACHE="${GOMODCACHE:-/tmp/rhizome-gomodcache}"
export TEMP="${GOCACHE}/tmp"
export TMP="${GOCACHE}/tmp"
export GOGC=30
export GOMEMLIMIT="2GiB"
export PATH="$PATH:$(go env GOPATH)/bin"

mkdir -p "$GOCACHE" "$GOMODCACHE" "$TEMP"

build_dir="${RHIZOME_BUILD_DIR:-build}"
export BUILD_DIR="$build_dir"
mkdir -p "$build_dir"

echo "=== build rhizome binary ==="
go build -tags goolm,stdjson -o "$build_dir/rhizome" ./cmd/rhizome

echo "=== go vet (in-scope + evolution) ==="
go vet -tags goolm,stdjson \
    ./pkg/evolution/... ./pkg/rhizome/... ./pkg/media/... ./pkg/tools/fs/... ./cmd/rhizome

echo "=== unit tests (evolution + in-scope packages) ==="
go test -p 1 -tags goolm,stdjson -count=1 \
    ./pkg/evolution/... ./pkg/rhizome/... ./pkg/media/... ./pkg/tools/fs/...

echo "=== slim lint ==="
make lint-slim

# Free the build cache before cross-compile only if it is on the small overlay.
rhizome_maybe_clean_cache

echo "=== core cross-compile ==="
make build-core

echo "Small-VM validation passed."
