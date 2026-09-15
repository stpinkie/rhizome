#!/usr/bin/env bash
set -euo pipefail

# Footprint measurement for the v0.10.0 worker-tier work.
#
# Builds a stripped rhizome binary (or uses a prebuilt one) and measures three
# profiles on Linux:
#
#   cli     — one-shot `agent -m hi` (or `version` when no model is configured):
#             wall-clock cold start + peak RSS via /usr/bin/time -v when present.
#   worker  — `daemon --no-dht --no-gateway` with mesh.role=worker.
#   full    — `daemon` with mesh enabled (DHT + relay + NAT infra + gateway).
#
# For daemon profiles the script samples /proc/<pid>/status VmRSS every
# SAMPLE_INTERVAL seconds for SAMPLE_SECONDS total, records the cold-start
# time (spawn → process alive; → gateway port answering for `full`), then
# emits a single JSON document on stdout. Requires /proc (Linux only — WSL2
# works).
#
# Environment:
#   RHIZOME_BINARY   prebuilt binary path (skips the build step)
#   RHIZOME_HOME     scratch home for the daemon profiles (default: mktemp dir)
#   RHIZOME_CONFIG   config file for the runs (default: generated minimal one
#                    under RHIZOME_HOME; needs a real model only for cli=agent)
#   SAMPLE_SECONDS   steady-state window, default 60
#   SAMPLE_INTERVAL  seconds between VmRSS samples, default 2
#   GATEWAY_PORT     port for the full-daemon profile, default 18695
#   SKIP_CLI=1       skip the one-shot CLI profile (e.g. no model configured)
#
# Usage:  ./scripts/measure-footprint.sh > footprint.json

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

SAMPLE_SECONDS="${SAMPLE_SECONDS:-60}"
SAMPLE_INTERVAL="${SAMPLE_INTERVAL:-2}"
GATEWAY_PORT="${GATEWAY_PORT:-18695}"

command -v python3 >/dev/null || { echo "error: python3 not found" >&2; exit 1; }
[[ -d /proc ]] || { echo "error: /proc required (Linux/WSL2 only)" >&2; exit 1; }

# --- build -----------------------------------------------------------------

BIN="${RHIZOME_BINARY:-}"
BUILD_META="prebuilt"
if [[ -z "$BIN" ]]; then
    command -v go >/dev/null || { echo "error: go not found (or set RHIZOME_BINARY)" >&2; exit 1; }
    BUILD_DIR="$(mktemp -d)"
    BIN="$BUILD_DIR/rhizome"
    echo "==> building stripped binary" >&2
    (cd "$repo_root" && CGO_ENABLED=0 go build -tags goolm,stdjson \
        -ldflags="-s -w" -o "$BIN" ./cmd/rhizome)
    BUILD_META="stripped -s -w, tags goolm,stdjson, CGO_ENABLED=0"
fi
BIN_BYTES="$(stat -c%s "$BIN")"
BIN_VERSION="$("$BIN" version 2>/dev/null | tr -d '\033' | sed 's/\[[0-9;]*m//g' | grep -m1 -iE "rhizome" | sed 's/^[^a-zA-Z]*//' || true)"

# --- scratch home/config ----------------------------------------------------

HOME_DIR="${RHIZOME_HOME:-$(mktemp -d /tmp/rhizome-footprint.XXXXXX)}"
mkdir -p "$HOME_DIR"

CFG="${RHIZOME_CONFIG:-$HOME_DIR/config.json}"
GENERATED_CONFIG=0
if [[ ! -f "$CFG" ]]; then
    GENERATED_CONFIG=1
    # A stub model entry lets the gateway provider initialize; the endpoint is
    # never called during the measurement window.
    cat > "$CFG" <<EOF
{
  "version": 3,
  "model_list": [
    {
      "model_name": "footprint-stub",
      "provider": "openai",
      "model": "gpt-5.4",
      "api_base": "http://127.0.0.1:9/unreachable",
      "api_keys": ["sk-footprint-dummy"]
    }
  ],
  "agents": { "defaults": { "model_name": "footprint-stub", "provider": "openai" } },
  "mesh": { "enabled": true, "role": "full" },
  "gateway": { "host": "127.0.0.1", "port": ${GATEWAY_PORT} }
}
EOF
fi

export RHIZOME_HOME="$HOME_DIR" RHIZOME_CONFIG="$CFG"

# Onboard once so daemon profiles have an identity; harmless if it exists.
if [[ ! -f "$HOME_DIR/identity/node.json" ]]; then
    "$BIN" network onboard --generate --encrypt none --non-interactive >"$HOME_DIR/onboard.log" 2>&1 || {
        echo "warning: network onboard failed; daemon profiles may exit early" >&2
        tail -5 "$HOME_DIR/onboard.log" >&2
    }
fi

# --- helpers -----------------------------------------------------------------

# Portable millisecond clock. EPOCHREALTIME (bash 5+) avoids `date` quirks —
# uutils coreutils ignores the %3N precision modifier and prints full nanos.
if [[ -n "${EPOCHREALTIME:-}" ]]; then
    ms() {
        local t="$EPOCHREALTIME"
        echo $(( ${t%.*} * 1000 + 10#${t#*.} / 1000 ))
    }
else
    ms() { python3 -c 'import time; print(int(time.time() * 1000))'; }
fi

vmrss_kb() {  # $1=pid → VmRSS in KiB, or empty
    awk '/^VmRSS:/{print $2}' "/proc/$1/status" 2>/dev/null || true
}

# process_alive: kill -0 alone lies — a zombie child still owns its pid.
process_alive() {
    [[ -d "/proc/$1" ]] || return 1
    [[ "$(ps -o stat= -p "$1" 2>/dev/null)" != *Z* ]]
}

# sample_daemon <pid> <ready_probe_fn> → prints JSON {cold_start_ms, samples_kb[], peak_kb, steady_kb}
sample_daemon() {
    local pid="$1" probe="$2"
    local t0 cold_ms="" samples=() v
    t0="$(ms)"

    # cold start: wait until the readiness probe succeeds (bounded)
    local deadline=$(( $(ms) + 30000 ))
    while (( $(ms) < deadline )); do
        if ! process_alive "$pid"; then echo '{"error":"process exited during startup"}'; return; fi
        if [[ -n "$(vmrss_kb "$pid")" ]] && "$probe"; then cold_ms=$(( $(ms) - t0 )); break; fi
        sleep 0.05
    done
    [[ -n "$cold_ms" ]] || cold_ms=-1

    # steady-state sampling
    local end=$(( $(ms) + SAMPLE_SECONDS * 1000 ))
    while (( $(ms) < end )); do
        process_alive "$pid" || break
        v="$(vmrss_kb "$pid")"
        [[ -n "$v" ]] && samples+=("$v")
        sleep "$SAMPLE_INTERVAL"
    done

    local peak=0 last=0
    for v in "${samples[@]:-0}"; do
        (( v > peak )) && peak=$v
        last=$v
    done
    python3 - "$cold_ms" "$peak" "$last" "${samples[@]:-}" <<'PY'
import json, sys
cold, peak, last = int(sys.argv[1]), int(sys.argv[2]), int(sys.argv[3])
samples = [int(x) for x in sys.argv[4:] if x]
print(json.dumps({
    "cold_start_ms": cold,
    "peak_rss_kb": peak,
    "steady_rss_kb": last,
    "sample_count": len(samples),
    "samples_kb": samples,
}))
PY
}

proc_alive() { true; }  # worker profile: process existence is the only signal
port_ready() { (exec 3<>"/dev/tcp/127.0.0.1/${GATEWAY_PORT}") 2>/dev/null && exec 3>&- 3<&-; }

run_daemon_profile() {  # $1=name $2=config-role $3...=daemon args
    local name="$1" role="$2"; shift 2

    local cfg="$CFG"
    if [[ "$GENERATED_CONFIG" == "1" ]]; then
        cfg="$HOME_DIR/config-$name.json"
        python3 - "$CFG" "$cfg" "$role" <<'PY'
import json, sys
src, dst, role = sys.argv[1], sys.argv[2], sys.argv[3]
cfg = json.load(open(src))
cfg.setdefault("mesh", {})["role"] = role
json.dump(cfg, open(dst, "w"), indent=2)
PY
    fi

    local logf="$HOME_DIR/daemon-$name.log"
    RHIZOME_CONFIG="$cfg" "$BIN" daemon "$@" >"$logf" 2>&1 &
    local pid=$!
    echo "==> profile $name (pid $pid, role=$role, args: $*)" >&2

    local result
    if [[ "$name" == "full" ]]; then
        result="$(sample_daemon "$pid" port_ready)"
    else
        result="$(sample_daemon "$pid" proc_alive)"
    fi

    kill "$pid" 2>/dev/null || true
    for _ in $(seq 1 40); do process_alive "$pid" || break; sleep 0.25; done
    kill -9 "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true

    if [[ "$result" == *'"error"'* ]]; then
        # Attach the daemon's last log lines so failures are diagnosable.
        local tail
        tail="$(tail -n 5 "$logf" 2>/dev/null | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))')"
        result="{\"error\":\"daemon failed\",\"log_tail\":${tail:-\"\"}}"
    fi
    echo "$result"
}

# --- profiles ----------------------------------------------------------------

CLI_JSON='{"skipped": true, "reason": "SKIP_CLI=1"}'
if [[ "${SKIP_CLI:-0}" != "1" ]]; then
    echo "==> profile cli (one-shot agent -m hi)" >&2
    cli_cmd=("$BIN" agent -m hi)
    if [[ -n "${CLI_CMD:-}" ]]; then
        # shellcheck disable=SC2206 # intentional word splitting for CLI_CMD
        cli_cmd=($CLI_CMD)
    fi
    t0="$(ms)"
    if command -v /usr/bin/time >/dev/null; then
        rc=0
        timeout 45 /usr/bin/time -v "${cli_cmd[@]}" </dev/null >"$HOME_DIR/cli.out" 2>"$HOME_DIR/cli.time" || rc=$?
        wall=$(( $(ms) - t0 ))
        peak="$(awk '/Maximum resident set size/{print $NF}' "$HOME_DIR/cli.time" || true)"
        CLI_JSON="$(python3 - "$wall" "${peak:-0}" "$rc" <<'PY'
import json, sys
print(json.dumps({
    "wall_ms": int(sys.argv[1]),
    "peak_rss_kb": int(sys.argv[2]),
    "exit_code": int(sys.argv[3]),
    "timed_out": int(sys.argv[3]) == 124,
}))
PY
)"
    else
        rc=0
        timeout 45 "${cli_cmd[@]}" </dev/null >"$HOME_DIR/cli.out" 2>&1 || rc=$?
        wall=$(( $(ms) - t0 ))
        CLI_JSON="$(python3 - "$wall" "$rc" <<'PY'
import json, sys
rc = int(sys.argv[2])
print(json.dumps({"wall_ms": int(sys.argv[1]), "peak_rss_kb": None,
                  "exit_code": rc, "timed_out": rc == 124,
                  "note": "usr/bin/time -v unavailable"}))
PY
)"
    fi
fi

echo "==> profile worker (daemon --no-dht --no-gateway, role=worker)" >&2
WORKER_JSON="$(run_daemon_profile worker worker --no-dht --no-gateway)"

echo "==> profile full (daemon, role=full)" >&2
FULL_JSON="$(run_daemon_profile full full)"

# --- emit ---------------------------------------------------------------------

python3 - "$BIN_BYTES" "$BIN_VERSION" "$BUILD_META" "$CLI_JSON" "$WORKER_JSON" "$FULL_JSON" <<'PY'
import json, sys
out = {
    "binary_bytes": int(sys.argv[1]),
    "binary_version": sys.argv[2],
    "build": sys.argv[3],
    "profiles": {
        "cli":    json.loads(sys.argv[4]),
        "worker": json.loads(sys.argv[5]),
        "full":   json.loads(sys.argv[6]),
    },
}
print(json.dumps(out, indent=2))
PY
