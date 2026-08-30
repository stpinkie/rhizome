#!/usr/bin/env bash
set -euo pipefail

# Build rhizome and run a two-node mesh integration test:
# 1. Create three node identities (A, B, C).
# 2. Start daemon A and daemon B (B bootstraps to A).
# 3. Ping A from C.
# 4. Write a file in A's workspace and wait for B to converge.

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

cd "${REPO_ROOT}"
echo "Building rhizome..."
CGO_ENABLED=0 go build -tags goolm,stdjson -o "${RHIZOME_BIN}" ./cmd/rhizome

A_HOME="${TEST_DIR}/a"
B_HOME="${TEST_DIR}/b"
C_HOME="${TEST_DIR}/c"
mkdir -p "${A_HOME}" "${B_HOME}" "${C_HOME}"

# Onboard A, B and C with distinct mnemonics.
echo "Onboarding nodes..."
RHIZOME_HOME="${A_HOME}" "${RHIZOME_BIN}" network onboard \
  --generate --name a --node-index 0 --encrypt none --yes --non-interactive
RHIZOME_HOME="${B_HOME}" "${RHIZOME_BIN}" network onboard \
  --generate --name b --node-index 1 --encrypt none --yes --non-interactive
RHIZOME_HOME="${C_HOME}" "${RHIZOME_BIN}" network onboard \
  --generate --name c --node-index 2 --encrypt none --yes --non-interactive

# Start daemon A.
echo "Starting daemon A..."
A_LOG="${A_HOME}/daemon.log"
RHIZOME_HOME="${A_HOME}" "${RHIZOME_BIN}" daemon \
  --allow-empty --no-dht --no-gateway \
  --listen /ip4/127.0.0.1/tcp/0 \
  --sync-commit-interval 1s \
  --sync-announce-interval 1s >"${A_LOG}" 2>&1 &
A_PID=$!

# Wait for the daemon to print its address.
A_ADDR=""
for _ in $(seq 1 50); do
  if [[ -s "${A_LOG}" ]] && grep -q "Addrs:" "${A_LOG}"; then
    A_ADDR=$(grep "Addrs:" "${A_LOG}" | head -1 | sed 's/.*Addrs:[[:space:]]*//')
    break
  fi
  sleep 0.2
done

if [[ -z "${A_ADDR}" ]]; then
  echo "Timed out waiting for daemon A" >&2
  cat "${A_LOG}" >&2 || true
  exit 1
fi

echo "Daemon A is listening on: ${A_ADDR}"

# Ping A from C.
echo "Pinging A from C..."
if ! RHIZOME_HOME="${C_HOME}" "${RHIZOME_BIN}" network ping "${A_ADDR}"; then
  echo "Ping from C to A failed" >&2
  exit 1
fi

# Start daemon B, bootstrapping to A.
echo "Starting daemon B..."
B_LOG="${B_HOME}/daemon.log"
RHIZOME_HOME="${B_HOME}" "${RHIZOME_BIN}" daemon \
  --allow-empty --no-dht --no-gateway \
  --listen /ip4/127.0.0.1/tcp/0 \
  --bootstrap "${A_ADDR}" \
  --sync-commit-interval 1s \
  --sync-announce-interval 1s >"${B_LOG}" 2>&1 &
B_PID=$!

# Wait until B is online.
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

# Give B a moment to connect to A.
sleep 2

# Write a file on A and wait for it to appear on B.
A_WORKSPACE="${A_HOME}/workspace"
B_WORKSPACE="${B_HOME}/workspace"
mkdir -p "${A_WORKSPACE}"
echo "hello from A" >"${A_WORKSPACE}/AGENT.md"

echo "Waiting for workspace sync from A to B..."
SYNCED=0
for _ in $(seq 1 60); do
  if [[ -f "${B_WORKSPACE}/AGENT.md" ]]; then
    CONTENT=$(cat "${B_WORKSPACE}/AGENT.md" 2>/dev/null || true)
    if [[ "${CONTENT}" == "hello from A" ]]; then
      SYNCED=1
      break
    fi
  fi
  sleep 1
done

if [[ "${SYNCED}" -ne 1 ]]; then
  echo "Workspace sync from A to B failed" >&2
  echo "A log:" >&2
  cat "${A_LOG}" >&2 || true
  echo "B log:" >&2
  cat "${B_LOG}" >&2 || true
  exit 1
fi

echo "Integration test passed: ping and workspace sync both work."
