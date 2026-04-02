#!/usr/bin/env bash
set -euo pipefail

# Worker process for E2E reconnect/lifecycle validation.
# It loops until DEADLINE_EPOCH and repeatedly:
# - starts autossh
# - waits for tunnel readiness (bounded timeout)
# - keeps session alive for random time
# - interrupts with SIGINT (Ctrl+C-like), then TERM/KILL fallback

HOST="${HOST:-tuns.0912345.xyz}"
SSH_PORT="${SSH_PORT:-443}"
SUBDOMAIN="${SUBDOMAIN:-xiaomi-sdufs}"
REMOTE_PORT="${REMOTE_PORT:-80}"
LOCAL_PORT="${LOCAL_PORT:-5000}"
SISH_ID="${SISH_ID:-test02}"
FORCE_CONNECT="${FORCE_CONNECT:-true}"
FORCE_HTTPS="${FORCE_HTTPS:-true}"
SSH_BIN="${SSH_BIN:-autossh}"
CONNECT_TIMEOUT_SEC="${CONNECT_TIMEOUT_SEC:-5}"
READY_WAIT_MIN_MS="${READY_WAIT_MIN_MS:-1000}"
READY_WAIT_MAX_MS="${READY_WAIT_MAX_MS:-10000}"
HOLD_MIN_MS="${HOLD_MIN_MS:-300}"
HOLD_MAX_MS="${HOLD_MAX_MS:-8000}"
PAUSE_MIN_MS="${PAUSE_MIN_MS:-80}"
PAUSE_MAX_MS="${PAUSE_MAX_MS:-1000}"
CHAOS_EARLY_PCT="${CHAOS_EARLY_PCT:-20}"
EARLY_MIN_MS="${EARLY_MIN_MS:-120}"
EARLY_MAX_MS="${EARLY_MAX_MS:-900}"
WORKER_ID="${WORKER_ID:-w0}"
DEADLINE_EPOCH="${DEADLINE_EPOCH:-0}"
LOG_FILE="${LOG_FILE:-./e2e-worker-${WORKER_ID}.log}"

if ! command -v "${SSH_BIN}" >/dev/null 2>&1; then
  echo "ERROR: ${SSH_BIN} not found" >&2
  exit 1
fi

if [[ "${DEADLINE_EPOCH}" -le 0 ]]; then
  echo "ERROR: DEADLINE_EPOCH must be provided and > 0" >&2
  exit 1
fi

check_range() {
  local min="$1"
  local max="$2"
  local name="$3"
  if [[ "${min}" -gt "${max}" ]]; then
    echo "ERROR: ${name} min > max" >&2
    exit 1
  fi
}

check_range "${READY_WAIT_MIN_MS}" "${READY_WAIT_MAX_MS}" "READY_WAIT"
check_range "${HOLD_MIN_MS}" "${HOLD_MAX_MS}" "HOLD"
check_range "${PAUSE_MIN_MS}" "${PAUSE_MAX_MS}" "PAUSE"
check_range "${EARLY_MIN_MS}" "${EARLY_MAX_MS}" "EARLY"

if [[ "${READY_WAIT_MAX_MS}" -gt 10000 ]]; then
  echo "ERROR: READY_WAIT_MAX_MS must be <= 10000" >&2
  exit 1
fi

rand_ms() {
  local min="$1"
  local max="$2"
  if [[ "${min}" -eq "${max}" ]]; then
    echo "${min}"
  else
    echo $(( min + RANDOM % (max - min + 1) ))
  fi
}

sleep_ms() {
  local ms="$1"
  python3 - <<PY
import time
time.sleep(${ms}/1000.0)
PY
}

wait_ready() {
  local pid="$1"
  local iter_log="$2"
  local timeout_ms="$3"
  local waited=0
  local step=100
  local ready_regex='remote forward success|remote forwarding listening on'

  while (( waited < timeout_ms )); do
    if ! kill -0 "${pid}" >/dev/null 2>&1; then
      return 2
    fi
    if [[ -f "${iter_log}" ]] && grep -Eiq "${ready_regex}" "${iter_log}"; then
      return 0
    fi
    sleep_ms "${step}"
    waited=$((waited + step))
  done
  return 1
}

shutdown_pid() {
  local pid="$1"
  kill -INT "${pid}" >/dev/null 2>&1 || true
  sleep_ms 120
  kill -0 "${pid}" >/dev/null 2>&1 && kill -TERM "${pid}" >/dev/null 2>&1 || true
  sleep_ms 120
  kill -0 "${pid}" >/dev/null 2>&1 && kill -KILL "${pid}" >/dev/null 2>&1 || true
  wait "${pid}" >/dev/null 2>&1 || true
}

current_pid=""
cleanup() {
  if [[ -n "${current_pid}" ]]; then
    shutdown_pid "${current_pid}"
  fi
}
trap cleanup EXIT INT TERM

iterations=0
ready_ok=0
ready_timeout=0
exited_before_ready=0
early_interrupts=0

{
  echo "=== worker ${WORKER_ID} start $(date '+%Y-%m-%d %H:%M:%S') ==="
  echo "host=${HOST} port=${SSH_PORT} subdomain=${SUBDOMAIN} remote_port=${REMOTE_PORT} local_port=${LOCAL_PORT} sish_id=${SISH_ID}"
  echo "deadline_epoch=${DEADLINE_EPOCH} hold_ms=[${HOLD_MIN_MS},${HOLD_MAX_MS}] ready_wait_ms=[${READY_WAIT_MIN_MS},${READY_WAIT_MAX_MS}]"
} >"${LOG_FILE}"

while [[ "$(date +%s)" -lt "${DEADLINE_EPOCH}" ]]; do
  iterations=$((iterations + 1))
  iter_log="$(mktemp -t e2e-worker-${WORKER_ID}-${iterations}-XXXX.log)"
  hold_ms="$(rand_ms "${HOLD_MIN_MS}" "${HOLD_MAX_MS}")"
  pause_ms="$(rand_ms "${PAUSE_MIN_MS}" "${PAUSE_MAX_MS}")"
  ready_wait_ms="$(rand_ms "${READY_WAIT_MIN_MS}" "${READY_WAIT_MAX_MS}")"

  AUTOSSH_GATETIME=0 \
  SISH_FORCE_CONNECT="${FORCE_CONNECT}" \
  SISH_FORCE_HTTPS="${FORCE_HTTPS}" \
  SISH_ID="${SISH_ID}" \
  "${SSH_BIN}" -M0 \
    -v \
    -o ConnectTimeout="${CONNECT_TIMEOUT_SEC}" \
    -o ExitOnForwardFailure=yes \
    -o ServerAliveInterval=2 \
    -o ServerAliveCountMax=1 \
    -o StrictHostKeyChecking=accept-new \
    -o SendEnv=SISH_FORCE_CONNECT \
    -o SendEnv=SISH_FORCE_HTTPS \
    -o SendEnv=SISH_ID \
    -p "${SSH_PORT}" \
    -R "${SUBDOMAIN}:${REMOTE_PORT}:localhost:${LOCAL_PORT}" \
    "${HOST}" >"${iter_log}" 2>&1 &
  current_pid="$!"

  forced_early=0
  if [[ $((RANDOM % 100)) -lt "${CHAOS_EARLY_PCT}" ]]; then
    forced_early=1
    early_interrupts=$((early_interrupts + 1))
    sleep_ms "$(rand_ms "${EARLY_MIN_MS}" "${EARLY_MAX_MS}")"
  else
    if wait_ready "${current_pid}" "${iter_log}" "${ready_wait_ms}"; then
      ready_ok=$((ready_ok + 1))
      sleep_ms "${hold_ms}"
    else
      rc="$?"
      if [[ "${rc}" -eq 1 ]]; then
        ready_timeout=$((ready_timeout + 1))
      else
        exited_before_ready=$((exited_before_ready + 1))
      fi
    fi
  fi

  shutdown_pid "${current_pid}"
  current_pid=""

  {
    echo "----- iter ${iterations} begin -----"
    echo "iter=${iterations} hold_ms=${hold_ms} pause_ms=${pause_ms} ready_wait_ms=${ready_wait_ms} forced_early=${forced_early}"
    cat "${iter_log}"
    echo "----- iter ${iterations} end -----"
  } >>"${LOG_FILE}"
  rm -f "${iter_log}"

  sleep_ms "${pause_ms}"
done

{
  echo
  echo "=== worker ${WORKER_ID} end $(date '+%Y-%m-%d %H:%M:%S') ==="
  echo "SUMMARY worker=${WORKER_ID} iterations=${iterations} ready_ok=${ready_ok} ready_timeout=${ready_timeout} exited_before_ready=${exited_before_ready} early_interrupts=${early_interrupts}"
} >>"${LOG_FILE}"

echo "SUMMARY worker=${WORKER_ID} iterations=${iterations} ready_ok=${ready_ok} ready_timeout=${ready_timeout} exited_before_ready=${exited_before_ready} early_interrupts=${early_interrupts}"
