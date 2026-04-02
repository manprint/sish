#!/usr/bin/env bash
set -euo pipefail

# V2 scenario requested by user:
# - 3 minutes
# - 15 workers
# - same SISH_ID + same subdomain
# - hold each successful tunnel 5-10s before interrupt

BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAIN_SCRIPT="${BASE_DIR}/main-e2e-validation-stale-listener.sh"

if [[ ! -x "${MAIN_SCRIPT}" ]]; then
  echo "ERROR: main script not executable: ${MAIN_SCRIPT}" >&2
  exit 1
fi

export DURATION_SEC="${DURATION_SEC:-180}"
export WORKERS="${WORKERS:-15}"
export HOLD_MIN_MS="${HOLD_MIN_MS:-5000}"
export HOLD_MAX_MS="${HOLD_MAX_MS:-10000}"
export CHAOS_EARLY_PCT="${CHAOS_EARLY_PCT:-0}"
export READY_WAIT_MIN_MS="${READY_WAIT_MIN_MS:-1000}"
export READY_WAIT_MAX_MS="${READY_WAIT_MAX_MS:-10000}"
export PAUSE_MIN_MS="${PAUSE_MIN_MS:-2000}"
export PAUSE_MAX_MS="${PAUSE_MAX_MS:-5000}"
export OUT_DIR="${OUT_DIR:-${BASE_DIR}/e2e-logs-v2}"

exec "${MAIN_SCRIPT}"
