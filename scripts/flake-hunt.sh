#!/usr/bin/env bash
set -uo pipefail

# scripts/flake-hunt.sh — repeat the networked test packages under go test's
# default package parallelism and report per-test pass rates.
#
# Package-level parallelism is where the libp2p timing flake family lived:
# multiple test binaries sharing the scheduler and the LAN. `-p 1` hides it;
# this script deliberately does NOT use it.
#
# Usage:
#   scripts/flake-hunt.sh [RUNS] [-- PKG ...]
#
#   RUNS  iterations over the package set (default: 5)
#   PKG   package list override (default: the networked set)
#
# Env:  GO_TAGS overrides the build tags (default: goolm,stdjson).
# Exit: 0 if every test passed in every run, 1 otherwise.
#       Run logs are kept under a printed directory when anything fails.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

runs=5
if [[ "${1:-}" =~ ^[0-9]+$ ]]; then runs="$1"; shift; fi
[[ "${1:-}" == "--" ]] && shift

if [[ $# -gt 0 ]]; then
  packages=("$@")
else
  packages=(
    ./pkg/rhizome/mesh ./pkg/rhizome/swarm ./pkg/rhizome/sync
    ./pkg/rhizome/network ./pkg/rhizome/stream ./pkg/rhizome/pair
    ./pkg/rhizome/blob ./pkg/rhizome/agentrpc ./pkg/rhizome/agenttask
    ./pkg/rhizome/blackboard ./pkg/gateway
  )
fi

tags="${GO_TAGS:-goolm,stdjson}"
log_dir="$(mktemp -d "${TMPDIR:-/tmp}/flake-hunt-XXXXXX")"
clean=1
cleanup() { [[ "$clean" == 1 ]] && rm -rf "$log_dir"; }
trap cleanup EXIT

echo "flake-hunt: $runs run(s), tags=$tags"
echo "packages: ${packages[*]}"

for i in $(seq 1 "$runs"); do
  log="$log_dir/run-$i.log"
  printf 'run %d/%d ... ' "$i" "$runs"
  if CGO_ENABLED=0 go test -count=1 -tags "$tags" -v "${packages[@]}" >"$log" 2>&1; then
    echo "ok"
  else
    echo "FAILED"
  fi
done

# Run-level failures: a package/binary that died mid-run (panic, timeout,
# build break) leaves a package FAIL line or a runtime panic; tests in flight
# never report --- FAIL individually, so surface these separately.
echo
echo "=== package/binary failures ==="
panics="$(grep -hE '^(panic|fatal error)' "$log_dir"/run-*.log 2>/dev/null | sed -E 's/0x[0-9a-f]+/0x.../g' | sort | uniq -c)"
pkg_fails="$(grep -hE '^FAIL[[:space:]]+github\.com/' "$log_dir"/run-*.log 2>/dev/null | sort | uniq -c)"
if [[ -n "$panics" ]]; then echo "$panics"; fi
if [[ -n "$pkg_fails" ]]; then echo "$pkg_fails"; fi
if [[ -z "$panics" && -z "$pkg_fails" ]]; then
  echo "(none)"
fi

# Per-test tallies: each run prints one '--- PASS|FAIL|SKIP: Name' line per
# test (subtests included). sort|uniq -c then pivot to a per-test row.
echo
echo "=== per-test results (failures first) ==="
printf '%6s %6s %6s %6s  %s\n' "FAIL" "PASS" "SKIP" "RUNS" "TEST"

tallies="$log_dir/tallies.tsv"
grep -hoE '^[[:space:]]*--- (PASS|FAIL|SKIP): [^[:space:]]+' "$log_dir"/run-*.log 2>/dev/null \
  | sed -E 's/^[[:space:]]*--- (PASS|FAIL|SKIP): ([^[:space:]]+).*/\2\t\1/' \
  | sort | uniq -c \
  | awk '{
      name=$2; outcome=$3; n=$1
      total[name]+=n
      if (outcome=="PASS") pass[name]+=n
      else if (outcome=="FAIL") fail[name]+=n
      else skip[name]+=n
    }
    END {
      for (t in total)
        printf "%d\t%d\t%d\t%d\t%s\n", fail[t]+0, pass[t]+0, skip[t]+0, total[t], t
    }' > "$tallies"

flaky=0
while IFS=$'\t' read -r f p s n name; do
  [[ "$f" -gt 0 ]] && flaky=$((flaky + 1))
  printf '%6d %6d %6d %6d  %s\n' "$f" "$p" "$s" "$n" "$name"
done < <(sort -t$'\t' -k1,1rn -k5,5 "$tallies")

tests=$(wc -l < "$tallies" | tr -d ' ')
echo
echo "=== summary: $tests test(s), $flaky with at least one failure, $runs run(s) ==="

if [[ "$flaky" -gt 0 || -n "$pkg_fails" ]]; then
  clean=0
  echo "logs kept: $log_dir"
  exit 1
fi
