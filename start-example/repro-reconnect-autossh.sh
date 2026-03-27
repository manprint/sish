#!/usr/bin/env bash
set -euo pipefail

# Repro script for rapid autossh reconnect/disconnect race scenarios.
# It tries to mimic manual behavior:
# 1) start autossh
# 2) wait until the tunnel is actually up (or timeout)
# 3) interrupt with SIGINT (Ctrl+C-like)
#
# Usage:
#   ./start-example/repro-reconnect-autossh.sh
#   HOST=tuns.0912345.xyz SUBDOMAIN=xiaomi-sdufs LOCAL_PORT=5000 ITERATIONS=80 ./start-example/repro-reconnect-autossh.sh
#
# Optional env vars:
#   HOST                (default: tuns.0912345.xyz)
#   SSH_PORT            (default: 443)
#   SUBDOMAIN           (default: xiaomi-sdufs)
#   REMOTE_PORT         (default: 80)
#   LOCAL_PORT          (default: 5000)
#   SISH_ID             (default: test02)
#   ITERATIONS          (default: 60)
#   RUN_MIN_MS          (default: 80)     # keepalive after tunnel-up (min)
#   RUN_MAX_MS          (default: 240)    # keepalive after tunnel-up (max)
#   PAUSE_MIN_MS        (default: 20)     # pause after kill min
#   PAUSE_MAX_MS        (default: 180)    # pause after kill max
#   WAIT_READY_MS       (default: 7000)   # max wait for tunnel-up evidence
#   FORCE_HTTPS         (default: true)
#   FORCE_CONNECT       (default: true)
#   SSH_BIN             (default: autossh)
#   LOG_FILE            (default: ./repro-reconnect-autossh.log)
#   CONNECT_TIMEOUT_SEC (default: 5)

HOST="${HOST:-tuns.0912345.xyz}"
SSH_PORT="${SSH_PORT:-443}"
SUBDOMAIN="${SUBDOMAIN:-xiaomi-sdufs}"
REMOTE_PORT="${REMOTE_PORT:-80}"
LOCAL_PORT="${LOCAL_PORT:-5000}"
SISH_ID="${SISH_ID:-test02}"
ITERATIONS="${ITERATIONS:-60}"
RUN_MIN_MS="${RUN_MIN_MS:-80}"
RUN_MAX_MS="${RUN_MAX_MS:-240}"
PAUSE_MIN_MS="${PAUSE_MIN_MS:-20}"
PAUSE_MAX_MS="${PAUSE_MAX_MS:-180}"
WAIT_READY_MS="${WAIT_READY_MS:-7000}"
FORCE_HTTPS="${FORCE_HTTPS:-true}"
FORCE_CONNECT="${FORCE_CONNECT:-true}"
SSH_BIN="${SSH_BIN:-autossh}"
LOG_FILE="${LOG_FILE:-./repro-reconnect-autossh.log}"
CONNECT_TIMEOUT_SEC="${CONNECT_TIMEOUT_SEC:-5}"

if ! command -v "${SSH_BIN}" >/dev/null 2>&1; then
  echo "ERROR: ${SSH_BIN} not found in PATH" >&2
  exit 1
fi

if [[ "${RUN_MIN_MS}" -gt "${RUN_MAX_MS}" ]]; then
  echo "ERROR: RUN_MIN_MS must be <= RUN_MAX_MS" >&2
  exit 1
fi

if [[ "${PAUSE_MIN_MS}" -gt "${PAUSE_MAX_MS}" ]]; then
  echo "ERROR: PAUSE_MIN_MS must be <= PAUSE_MAX_MS" >&2
  exit 1
fi

if [[ "${WAIT_READY_MS}" -lt 1 ]]; then
  echo "ERROR: WAIT_READY_MS must be >= 1" >&2
  exit 1
fi

rand_ms() {
  local min="$1"
  local max="$2"
  if [[ "${min}" -eq "${max}" ]]; then
    echo "${min}"
    return
  fi
  echo $(( min + RANDOM % (max - min + 1) ))
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
  local waited=0
  local step=100

  # Evidence patterns observed with ssh/autossh verbose output once remote forward is active.
  local ready_regex='remote forward success|remote forwarding listening on'

  while (( waited < WAIT_READY_MS )); do
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

cleanup() {
  if [[ -n "${CURRENT_PID:-}" ]]; then
    kill "${CURRENT_PID}" >/dev/null 2>&1 || true
    wait "${CURRENT_PID}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

{
  echo "=== reconnect repro start $(date '+%Y-%m-%d %H:%M:%S') ==="
  echo "host=${HOST} ssh_port=${SSH_PORT} subdomain=${SUBDOMAIN} remote_port=${REMOTE_PORT} local_port=${LOCAL_PORT} sish_id=${SISH_ID}"
  echo "iterations=${ITERATIONS} run_ms=[${RUN_MIN_MS},${RUN_MAX_MS}] pause_ms=[${PAUSE_MIN_MS},${PAUSE_MAX_MS}] wait_ready_ms=${WAIT_READY_MS}"
  echo
} | tee "${LOG_FILE}"

ok_ready=0
timeout_ready=0
dead_before_ready=0

for i in $(seq 1 "${ITERATIONS}"); do
  run_ms="$(rand_ms "${RUN_MIN_MS}" "${RUN_MAX_MS}")"
  pause_ms="$(rand_ms "${PAUSE_MIN_MS}" "${PAUSE_MAX_MS}")"
  iter_log="$(mktemp -t repro-reconnect-${i}-XXXX.log)"

  echo "[${i}/${ITERATIONS}] start autossh (run=${run_ms}ms pause=${pause_ms}ms)" | tee -a "${LOG_FILE}"

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

  CURRENT_PID="$!"

  if wait_ready "${CURRENT_PID}" "${iter_log}"; then
    ok_ready=$((ok_ready + 1))
    echo "  -> tunnel ready, sending SIGINT (Ctrl+C simulation)" | tee -a "${LOG_FILE}"
    sleep_ms "${run_ms}"
  else
    rc="$?"
    if [[ "${rc}" -eq 1 ]]; then
      timeout_ready=$((timeout_ready + 1))
      echo "  -> ready timeout (${WAIT_READY_MS}ms), still sending SIGINT" | tee -a "${LOG_FILE}"
    else
      dead_before_ready=$((dead_before_ready + 1))
      echo "  -> autossh exited before ready, continuing" | tee -a "${LOG_FILE}"
    fi
  fi

  kill -INT "${CURRENT_PID}" >/dev/null 2>&1 || true
  sleep_ms 120
  kill -0 "${CURRENT_PID}" >/dev/null 2>&1 && kill -TERM "${CURRENT_PID}" >/dev/null 2>&1 || true
  sleep_ms 120
  kill -0 "${CURRENT_PID}" >/dev/null 2>&1 && kill -KILL "${CURRENT_PID}" >/dev/null 2>&1 || true
  wait "${CURRENT_PID}" >/dev/null 2>&1 || true
  CURRENT_PID=""

  {
    echo "----- iter ${i} begin -----"
    cat "${iter_log}"
    echo "----- iter ${i} end -----"
  } >>"${LOG_FILE}"
  rm -f "${iter_log}"

  sleep_ms "${pause_ms}"
done

{
  echo
  echo "=== reconnect repro end $(date '+%Y-%m-%d %H:%M:%S') ==="
  echo "log_file=${LOG_FILE}"
  echo "ready_ok=${ok_ready} ready_timeout=${timeout_ready} exited_before_ready=${dead_before_ready}"
  echo
  echo "Quick grep:"
  echo "  grep -E 'unavailable|failed for listen port|unable to bind|Connection id set to|Force connect|Closed SSH connection' '${LOG_FILE}'"
} | tee -a "${LOG_FILE}"
