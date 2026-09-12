#!/usr/bin/env bash
set -euo pipefail

# Build the rhizome binary for the core cross-compile targets. This script is
# designed for small Linux VMs; it reuses a large-volume Go cache when one is
# provided and removes cross-compile artifacts when the build directory is on
# the small overlay.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

source "${repo_root}/scripts/rhizome-small-vm-helpers.sh"

build_dir="${RHIZOME_BUILD_DIR:-${BUILD_DIR:-build}}"
export BUILD_DIR="$build_dir"
ldflags="${LDFLAGS:-}"

mkdir -p "$build_dir"

rhizome_report_disk

for target in \
    "linux/amd64" \
    "linux/386" \
    "linux/arm:GOARM=7" \
    "linux/arm64" \
    "darwin/amd64" \
    "darwin/arm64" \
    "windows/amd64" \
    "windows/386" \
    "freebsd/amd64" \
    "openbsd/amd64" \
    "netbsd/amd64" \
    "netbsd/arm64"; do

    goos="${target%%/*}"
    rest="${target#*/}"
    goarch="${rest%%:*}"
    extra_env="${rest#*:}"
    if [ "$extra_env" = "$rest" ]; then
        extra_env=""
    fi

    ext=""
    if [ "$goos" = "windows" ]; then
        ext=".exe"
    fi

    out="$build_dir/rhizome-$goos-$goarch$ext"
    echo "=== building $goos/$goarch -> $out ==="

    env_vars="CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch"
    if [ -n "$extra_env" ]; then
        env_vars="$env_vars $extra_env"
    fi

    # shellcheck disable=SC2086
    env $env_vars go build -tags goolm,stdjson -ldflags "$ldflags" -o "$out" ./cmd/rhizome

    if rhizome_should_clean_builds; then
        echo "Removing $out to keep the small overlay from filling"
        rm -f "$out"
    fi

    rhizome_maybe_clean_cache
    rhizome_report_disk
done
