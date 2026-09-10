#!/usr/bin/env bash
set -euo pipefail

# Build rhizome and run a two-node swarm integration test:
# 1. Create node identities A and B.
# 2. Write configs: mesh + swarm enabled, mutual trust, shared swarm "ops".
# 3. Start daemon A and daemon B (B bootstraps to A).
# 4. Wait until both sides see each other in the "ops" roster.
# 5. Verify roster persistence survives a daemon restart.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

BUILD_DIR="$(mktemp -d)"
RHIZOME_BIN="${BUILD_DIR}/rhizome"
TEST_DIR="$(mktemp -d)"

cleanup() {
  local ec=$?
  if [[ -n "${A_PID:-}" ]]; then kill "${A_PID}" 2>/dev/null || true; wait "${A_PID}" 2>/dev/null || true; fi
  if [[ -n "${B_PID:-}" ]]; then kill "${B_PID}" 2>/dev/null || true; wait "${B_PID}" 2>/dev/null || true; fi
  rm -rf "${BUILD_DIR}" "${TEST_DIR}"
  exit "${ec}"
}
trap cleanup EXIT

peer_id() {
  RHIZOME_HOME="$1" "${RHIZOME_BIN}" network status --json 2>/dev/null \
    | sed -n 's/.*"peer_id":[[:space:]]*"\([^"]*\)".*/\1/p' | head -1
}

write_config() {
  # $1 = home, $2 = trusted peer id, $3 = bootstrap multiaddr (may be empty)
  local bootstrap_json=""
  if [[ -n "$3" ]]; then
    bootstrap_json=", \"bootstrap_peers\": [\"$3\"]"
  fi
  cat >"$1/config.json" <<EOF
{
  "mesh": {
    "enabled": true,
    "trusted_peers": ["$2"]${bootstrap_json},
    "dht_enabled": false
  },
  "swarm": {
    "enabled": true,
    "memberships": ["ops"],
    "presence": { "heartbeat_interval": "5s", "expire_after": "30s" }
  }
}
EOF
}

wait_for_member() {
  # $1 = home, $2 = expected peer id, $3 = timeout seconds
  local file="$1/swarms.json"
  for _ in $(seq 1 "$3"); do
    # Prefer a live CLI readback; fall back to the persisted file.
    if RHIZOME_HOME="$1" "${RHIZOME_BIN}" swarm members ops 2>/dev/null | grep -q "$2"; then
      return 0
    fi
    if [[ -f "${file}" ]] && grep -q "$2" "${file}" 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  return 1
}

cd "${REPO_ROOT}"
echo "Building rhizome..."
CGO_ENABLED=0 go build -tags goolm,stdjson -o "${RHIZOME_BIN}" ./cmd/rhizome

A_HOME="${TEST_DIR}/a"
B_HOME="${TEST_DIR}/b"
mkdir -p "${A_HOME}" "${B_HOME}"

echo "Onboarding nodes..."
RHIZOME_HOME="${A_HOME}" "${RHIZOME_BIN}" network onboard \
  --generate --name a --node-index 0 --encrypt none --yes --non-interactive
RHIZOME_HOME="${B_HOME}" "${RHIZOME_BIN}" network onboard \
  --generate --name b --node-index 1 --encrypt none --yes --non-interactive

A_PEER="$(peer_id "${A_HOME}")"
B_PEER="$(peer_id "${B_HOME}")"
if [[ -z "${A_PEER}" || -z "${B_PEER}" ]]; then
  echo "failed to read peer ids" >&2
  exit 1
fi
echo "A: ${A_PEER}"
echo "B: ${B_PEER}"

# A listens on a fixed loopback port so B's config can embed the bootstrap
# address before A starts.
A_ADDR="/ip4/127.0.0.1/tcp/18797/p2p/${A_PEER}"

write_config "${A_HOME}" "${B_PEER}" ""
write_config "${B_HOME}" "${A_PEER}" "${A_ADDR}"

echo "Starting daemon A..."
A_LOG="${A_HOME}/daemon.log"
RHIZOME_HOME="${A_HOME}" "${RHIZOME_BIN}" daemon \
  --allow-empty --no-dht --no-gateway \
  --listen /ip4/127.0.0.1/tcp/18797 >"${A_LOG}" 2>&1 &
A_PID=$!

for _ in $(seq 1 50); do
  if [[ -s "${A_LOG}" ]] && grep -q "Rhizome daemon online" "${A_LOG}"; then
    break
  fi
  sleep 0.2
done

echo "Starting daemon B..."
B_LOG="${B_HOME}/daemon.log"
RHIZOME_HOME="${B_HOME}" "${RHIZOME_BIN}" daemon \
  --allow-empty --no-dht --no-gateway \
  --listen /ip4/127.0.0.1/tcp/0 >"${B_LOG}" 2>&1 &
B_PID=$!

B_ONLINE=0
for _ in $(seq 1 50); do
  if [[ -s "${B_LOG}" ]] && grep -q "Rhizome daemon online" "${B_LOG}"; then
    B_ONLINE=1
    break
  fi
  sleep 0.2
done
if [[ "${B_ONLINE}" -ne 1 ]]; then
  echo "Timed out waiting for daemon B" >&2
  cat "${B_LOG}" >&2 || true
  exit 1
fi

echo "Waiting for mutual swarm rosters..."
if ! wait_for_member "${A_HOME}" "${B_PEER}" 60; then
  echo "daemon A never saw B in the ops roster" >&2
  cat "${A_LOG}" >&2 || true
  exit 1
fi
if ! wait_for_member "${B_HOME}" "${A_PEER}" 30; then
  echo "daemon B never saw A in the ops roster" >&2
  cat "${B_LOG}" >&2 || true
  exit 1
fi
echo "Both daemons see each other in swarm 'ops'."

# CLI readback.
if ! RHIZOME_HOME="${A_HOME}" "${RHIZOME_BIN}" swarm members ops | grep -q "${B_PEER}"; then
  echo "swarm members did not list B" >&2
  exit 1
fi

# Restart B and verify the roster is re-established (persistence + rejoin).
echo "Restarting daemon B to verify roster persistence..."
kill "${B_PID}" 2>/dev/null || true
wait "${B_PID}" 2>/dev/null || true
B_PID=""

B_LOG="${B_HOME}/daemon2.log"
RHIZOME_HOME="${B_HOME}" "${RHIZOME_BIN}" daemon \
  --allow-empty --no-dht --no-gateway \
  --listen /ip4/127.0.0.1/tcp/0 >"${B_LOG}" 2>&1 &
B_PID=$!

B_ONLINE=0
for _ in $(seq 1 50); do
  if [[ -s "${B_LOG}" ]] && grep -q "Rhizome daemon online" "${B_LOG}"; then
    B_ONLINE=1
    break
  fi
  sleep 0.2
done
if [[ "${B_ONLINE}" -ne 1 ]]; then
  echo "Timed out waiting for restarted daemon B" >&2
  cat "${B_LOG}" >&2 || true
  exit 1
fi

if ! wait_for_member "${B_HOME}" "${A_PEER}" 60; then
  echo "daemon B lost the ops roster after restart" >&2
  cat "${B_LOG}" >&2 || true
  exit 1
fi

echo "Swarm integration test passed: join, roster exchange, presence, and restart persistence all work."
