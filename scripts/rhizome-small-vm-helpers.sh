#!/usr/bin/env bash
# Shared helpers for small-VM validation scripts.

# Return 0 if the given path's filesystem has less than $2 MB free.
rhizome_fs_free_mb() {
    local path="${1:-.}"
    local min_free="${2:-500}"
    local free_mb
    free_mb=$(df -P "$path" 2>/dev/null | awk 'NR==2 {print int($4/1024)}')
    if [ -z "$free_mb" ]; then
        return 0
    fi
    [ "$free_mb" -lt "$min_free" ]
}

# Return 0 if Go caches are on a path that looks like small-VM overlay storage.
rhizome_cache_is_small() {
    case "${GOCACHE:-}" in
        /tmp/*|/var/tmp/*) return 0 ;;
    esac
    if rhizome_fs_free_mb "${GOCACHE:-/tmp}" 1024; then
        return 0
    fi
    return 1
}

# Return 0 if build output directory is on a small overlay.
rhizome_build_dir_is_small() {
    local build_dir="${1:-build}"
    case "$build_dir" in
        /*) ;;
        *) build_dir="$(pwd)/$build_dir" ;;
    esac
    case "$build_dir" in
        /tmp/*|/var/tmp/*) return 0 ;;
    esac
    rhizome_fs_free_mb "$build_dir" 1024
}

# Return 0 if cross-compile artifacts should be removed after each build.
rhizome_should_clean_builds() {
    if [ "${RHIZOME_BUILD_CLEAN:-}" = "1" ]; then
        return 0
    fi
    if [ "${RHIZOME_BUILD_CLEAN:-}" = "0" ]; then
        return 1
    fi
    rhizome_build_dir_is_small "${BUILD_DIR:-build}"
}

# Optionally clean the Go build cache.
rhizome_maybe_clean_cache() {
    if rhizome_cache_is_small; then
        go clean -cache
    else
        echo "Keeping Go build cache on large volume ($GOCACHE)"
    fi
}

# Show current disk usage for the build dir and the Go cache.
rhizome_report_disk() {
    echo "Disk: build=$(df -h "${BUILD_DIR:-build}" 2>/dev/null | awk 'NR==2 {print $4}') root=$(df -h / 2>/dev/null | awk 'NR==2 {print $4}')"
}
