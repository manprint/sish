#!/usr/bin/env bash
set -euo pipefail

# Main orchestrator for production-like E2E validation of stale-listener/reconnect paths.
# It launches multiple autossh workers with random timing but ALWAYS with:
# - same SISH_ID
# - same subdomain
# This is intentionally aligned to the manual scenario that exposed the bug.

BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKER_SCRIPT="${BASE_DIR}/e2e-worker-autossh.sh"

if [[ ! -x "${WORKER_SCRIPT}" ]]; then
  echo "ERROR: worker script not executable: ${WORKER_SCRIPT}" >&2
  exit 1
fi

DURATION_SEC="${DURATION_SEC:-300}"         # 5 minutes default
WORKERS="${WORKERS:-3}"
HOST="${HOST:-tuns.0912345.xyz}"
SSH_PORT="${SSH_PORT:-443}"
SUBDOMAIN="${SUBDOMAIN:-xiaomi-sdufs}"
REMOTE_PORT="${REMOTE_PORT:-80}"
LOCAL_PORT="${LOCAL_PORT:-5000}"
SISH_ID="${SISH_ID:-test02}"
FORCE_CONNECT="${FORCE_CONNECT:-true}"
FORCE_HTTPS="${FORCE_HTTPS:-true}"
SSH_BIN="${SSH_BIN:-autossh}"
OUT_DIR="${OUT_DIR:-${BASE_DIR}/e2e-logs}"

# Per-worker behavior defaults (bounded, max wait 10s as requested).
READY_WAIT_MIN_MS="${READY_WAIT_MIN_MS:-1000}"
READY_WAIT_MAX_MS="${READY_WAIT_MAX_MS:-10000}"
HOLD_MIN_MS="${HOLD_MIN_MS:-300}"
HOLD_MAX_MS="${HOLD_MAX_MS:-8000}"
PAUSE_MIN_MS="${PAUSE_MIN_MS:-80}"
PAUSE_MAX_MS="${PAUSE_MAX_MS:-1000}"
CHAOS_EARLY_PCT="${CHAOS_EARLY_PCT:-25}"
EARLY_MIN_MS="${EARLY_MIN_MS:-120}"
EARLY_MAX_MS="${EARLY_MAX_MS:-900}"
CONNECT_TIMEOUT_SEC="${CONNECT_TIMEOUT_SEC:-5}"

if [[ "${DURATION_SEC}" -lt 30 ]]; then
  echo "ERROR: DURATION_SEC must be >= 30" >&2
  exit 1
fi
if [[ "${WORKERS}" -lt 1 ]]; then
  echo "ERROR: WORKERS must be >= 1" >&2
  exit 1
fi
if [[ "${READY_WAIT_MAX_MS}" -gt 10000 ]]; then
  echo "ERROR: READY_WAIT_MAX_MS must be <= 10000" >&2
  exit 1
fi

mkdir -p "${OUT_DIR}"
RUN_ID="$(date '+%Y%m%d-%H%M%S')"
RUN_DIR="${OUT_DIR}/run-${RUN_ID}"
mkdir -p "${RUN_DIR}"

deadline_epoch=$(( $(date +%s) + DURATION_SEC ))
pids=()

cleanup() {
  for pid in "${pids[@]:-}"; do
    kill "${pid}" >/dev/null 2>&1 || true
  done
  for pid in "${pids[@]:-}"; do
    wait "${pid}" >/dev/null 2>&1 || true
  done
}
trap cleanup EXIT INT TERM

echo "=== main e2e start $(date '+%Y-%m-%d %H:%M:%S') ==="
echo "duration_sec=${DURATION_SEC} workers=${WORKERS} run_dir=${RUN_DIR}"
echo "host=${HOST} ssh_port=${SSH_PORT} local_port=${LOCAL_PORT} remote_port=${REMOTE_PORT}"
echo "subdomain=${SUBDOMAIN} sish_id=${SISH_ID} (fixed for all workers)"
echo

for w in $(seq 1 "${WORKERS}"); do
  worker_id="w${w}"
  worker_log="${RUN_DIR}/${worker_id}.log"

  DEADLINE_EPOCH="${deadline_epoch}" \
  WORKER_ID="${worker_id}" \
  LOG_FILE="${worker_log}" \
  HOST="${HOST}" \
  SSH_PORT="${SSH_PORT}" \
  SUBDOMAIN="${SUBDOMAIN}" \
  REMOTE_PORT="${REMOTE_PORT}" \
  LOCAL_PORT="${LOCAL_PORT}" \
  SISH_ID="${SISH_ID}" \
  FORCE_CONNECT="${FORCE_CONNECT}" \
  FORCE_HTTPS="${FORCE_HTTPS}" \
  SSH_BIN="${SSH_BIN}" \
  READY_WAIT_MIN_MS="${READY_WAIT_MIN_MS}" \
  READY_WAIT_MAX_MS="${READY_WAIT_MAX_MS}" \
  HOLD_MIN_MS="${HOLD_MIN_MS}" \
  HOLD_MAX_MS="${HOLD_MAX_MS}" \
  PAUSE_MIN_MS="${PAUSE_MIN_MS}" \
  PAUSE_MAX_MS="${PAUSE_MAX_MS}" \
  CHAOS_EARLY_PCT="${CHAOS_EARLY_PCT}" \
  EARLY_MIN_MS="${EARLY_MIN_MS}" \
  EARLY_MAX_MS="${EARLY_MAX_MS}" \
  CONNECT_TIMEOUT_SEC="${CONNECT_TIMEOUT_SEC}" \
  "${WORKER_SCRIPT}" >"${RUN_DIR}/${worker_id}.stdout.log" 2>&1 &

  pids+=("$!")
  echo "started ${worker_id}: subdomain=${SUBDOMAIN} sish_id=${SISH_ID} pid=${pids[-1]}"
done

rc=0
for pid in "${pids[@]}"; do
  if ! wait "${pid}"; then
    rc=1
  fi
done

echo
echo "=== main e2e end $(date '+%Y-%m-%d %H:%M:%S') ==="
echo "run_dir=${RUN_DIR}"
echo
echo "Worker summaries:"
grep -h '^SUMMARY worker=' "${RUN_DIR}"/w*.stdout.log || true
echo
echo "Potential server-side symptoms (from worker logs):"
grep -Eh 'unavailable|failed for listen port|unable to bind|administratively prohibited|channel_setup_fwd_listener_tcpip: cannot listen' "${RUN_DIR}"/w*.log || true
echo
echo "Shared-mode conflict lens:"
grep -Eh 'Address already in use|cannot listen|administratively prohibited|remote port forwarding failed|failed for listen port' "${RUN_DIR}"/w*.log || true
echo
echo "Next checks on sish internal metrics:"
echo "  - debug_bind_conflict_total + by_type"
echo "  - debug_stale_holder_purged_total + by_type"
echo "  - debug_force_disconnect_noop_total"
echo "  - debug_target_release_timeout_total"
echo "  - forward_cleanup_errors_total / forward_cleanup_socket_remove_errors_total"

exit "${rc}"
