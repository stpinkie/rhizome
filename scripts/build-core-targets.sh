#!/usr/bin/env bash
set -euo pipefail

# Build the rhizome binary for the core cross-compile targets. This script is
# designed for small Linux VMs; it frees the Go build cache after each target.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

build_dir="${BUILD_DIR:-build}"
ldflags="${LDFLAGS:-}"

mkdir -p "$build_dir"

for target in \
    "linux/amd64" \
    "linux/arm64" \
    "darwin/amd64" \
    "darwin/arm64" \
    "windows/amd64" \
    "freebsd/amd64" \
    "openbsd/amd64"; do
    goos="${target%%/*}"
    goarch="${target#*/}"
    ext=""
    if [ "$goos" = "windows" ]; then
        ext=".exe"
    fi
    out="$build_dir/rhizome-$goos-$goarch$ext"
    echo "=== building $goos/$goarch -> $out ==="
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
        go build -tags goolm,stdjson -ldflags "$ldflags" -o "$out" ./cmd/rhizome
    # Free the build cache between targets to bound disk usage on small VMs.
    go clean -cache
done
