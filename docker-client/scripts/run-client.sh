#!/usr/bin/env sh
set -eu

FORWARDER_LOG_FILE="${FORWARDER_LOG_FILE:-/app/log/forwarder/outputs.log}"
STATS_LOG_FILE="${STATS_LOG_FILE:-/app/log/stats/stats.log}"
FORWARDER_LOG_MAX_SIZE_MB="${FORWARDER_LOG_MAX_SIZE_MB:-100}"
FORWARDER_LOG_MAX_AGE_HOURS="${FORWARDER_LOG_MAX_AGE_HOURS:-168}"
FORWARDER_LOG_MAX_FILES="${FORWARDER_LOG_MAX_FILES:-30}"
STATS_LOG_MAX_SIZE_MB="${STATS_LOG_MAX_SIZE_MB:-20}"
STATS_LOG_MAX_AGE_HOURS="${STATS_LOG_MAX_AGE_HOURS:-720}"
STATS_LOG_MAX_FILES="${STATS_LOG_MAX_FILES:-60}"
SISH_CLIENT_AUTORESTART="${SISH_CLIENT_AUTORESTART:-true}"
SISH_CLIENT_RESTART_DELAY_SECONDS="${SISH_CLIENT_RESTART_DELAY_SECONDS:-3}"
SISH_CLIENT_MAX_RETRIES="${SISH_CLIENT_MAX_RETRIES:-0}"
SSH_AUTH_MODE="${SSH_AUTH_MODE:-auto}"
SSH_PASSWORD="${SSH_PASSWORD:-}"

mkdir -p "$(dirname "$FORWARDER_LOG_FILE")" "$(dirname "$STATS_LOG_FILE")"
touch "$FORWARDER_LOG_FILE" "$STATS_LOG_FILE"

rotate_logs() {
  /app/scripts/log-rotate.sh "$FORWARDER_LOG_FILE" "$FORWARDER_LOG_MAX_SIZE_MB" "$FORWARDER_LOG_MAX_AGE_HOURS" "$FORWARDER_LOG_MAX_FILES"
  /app/scripts/log-rotate.sh "$STATS_LOG_FILE" "$STATS_LOG_MAX_SIZE_MB" "$STATS_LOG_MAX_AGE_HOURS" "$STATS_LOG_MAX_FILES"
}

log_stats() {
  ts="$(date '+%Y/%m/%d - %H:%M:%S')"
  printf '%s | %s\n' "$ts" "$1" | tee -a "$STATS_LOG_FILE"
}

log_forwarder_line() {
  ts="$(date '+%Y/%m/%d - %H:%M:%S')"
  clean_line="$(printf '%s' "$1" | sed "s/$(printf '\033')\[[0-9;]*[[:alpha:]]//g" | tr -d '\r')"
  printf '%s | %s\n' "$ts" "$clean_line" | tee -a "$FORWARDER_LOG_FILE"
}

format_cmd_for_log() {
  out=""
  for arg in "$@"; do
    escaped="$(printf '%s' "$arg" | sed "s/'/'\\\\''/g")"
    out="$out '$escaped'"
  done

  printf '%s' "$out"
}

log_ssh_env_inputs() {
  ssh_vars="SSH_HOST SSH_PORT SSH_USER SSH_TARGET SSH_AUTH_MODE SSH_PRIVATE_KEY_PATH SSH_IDENTITY_ONLY SSH_EXIT_ON_FORWARD_FAILURE SSH_SERVER_ALIVE_INTERVAL SSH_SERVER_ALIVE_COUNT_MAX SSH_CONNECT_TIMEOUT SSH_STRICT_HOST_KEY_CHECKING SSH_USER_KNOWN_HOSTS_FILE SSH_COMPRESSION SSH_LOG_LEVEL SSH_REMOTE_FORWARDS SSH_LOCAL_FORWARDS SSH_DYNAMIC_FORWARDS SSH_OPTIONS SSH_EXTRA_ARGS SSH_SEND_ENV SISH_REMOTE_COMMAND"

  log_forwarder_line "SSH input environment variables (effective):"
  for key in $ssh_vars; do
    value="$(printenv "$key" 2>/dev/null || true)"
    if [ "$key" = "SSH_PASSWORD" ]; then
      value="[REDACTED]"
    fi
    log_forwarder_line "  ${key}=${value}"
  done

  log_forwarder_line "SendEnv variables forwarded to SSH (effective):"
  send_list="${SSH_SEND_ENV:-SISH_PROXY_PROTOCOL,SISH_PROXYPROTO,SISH_HOST_HEADER,SISH_STRIP_PATH,SISH_SNI_PROXY,SISH_TCP_ADDRESS,SISH_TCP_ALIAS,SISH_LOCAL_FORWARD,SISH_AUTO_CLOSE,SISH_FORCE_HTTPS,SISH_TCP_ALIASES_ALLOWED_USERS,SISH_DEADLINE,SISH_ID,SISH_NOTE,SISH_NOTE64,SISH_FORCE_CONNECT}"
  old_ifs="$IFS"
  IFS=','
  for var_name in $send_list; do
    clean="$(echo "$var_name" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
    [ -n "$clean" ] || continue
    value="$(printenv "$clean" 2>/dev/null || true)"
    [ -n "$value" ] || continue
    log_forwarder_line "  ${clean}=${value}"
  done
  IFS="$old_ifs"
}

log_output_file() {
  input_file="$1"
  while IFS= read -r line || [ -n "$line" ]; do
    ts="$(date '+%Y/%m/%d - %H:%M:%S')"
    printf '%s | %s\n' "$ts" "$line" | tee -a "$FORWARDER_LOG_FILE"
  done < "$input_file"
}

log_output_stream() {
  input_pipe="$1"
  raw_file="$2"
  : > "$raw_file"

  while IFS= read -r line || [ -n "$line" ]; do
    printf '%s\n' "$line" >> "$raw_file"
    log_forwarder_line "$line"
  done < "$input_pipe"
}

build_cmd() {
  set --
  while IFS= read -r arg || [ -n "$arg" ]; do
    set -- "$@" "$arg"
  done <<EOF
$(/app/scripts/build-ssh-command.sh)
EOF

  printf '%s\n' "$@"
}

execute_ssh() {
  tmp_out="/tmp/sish-client-ssh-output.$$"
  tmp_pipe="/tmp/sish-client-ssh-output-pipe.$$"
  cmd_file="/tmp/sish-client-cmd.$$"

  build_cmd > "$cmd_file"

  set --
  while IFS= read -r arg || [ -n "$arg" ]; do
    set -- "$@" "$arg"
  done < "$cmd_file"

  target="${SSH_TARGET:-}"
  if [ -z "$target" ]; then
    if [ -n "${SSH_USER:-}" ]; then
      target="${SSH_USER}@${SSH_HOST:-}"
    else
      target="${SSH_HOST:-}"
    fi
  fi

  log_stats "CONNECTING | target=${target} | mode=${SSH_AUTH_MODE}"
  log_ssh_env_inputs
  log_forwarder_line "Executing SSH command:$(format_cmd_for_log "$@")"

  rm -f "$tmp_pipe"
  mkfifo "$tmp_pipe"
  log_output_stream "$tmp_pipe" "$tmp_out" &
  stream_pid="$!"

  set +e
  if [ "$SSH_AUTH_MODE" = "password" ] || { [ "$SSH_AUTH_MODE" = "auto" ] && [ -n "$SSH_PASSWORD" ] && [ ! -f "${SSH_PRIVATE_KEY_PATH:-/app/ssh/id_ed25519}" ]; }; then
    if [ -z "$SSH_PASSWORD" ]; then
      log_stats "ERROR | SSH_AUTH_MODE requires password but SSH_PASSWORD is empty"
      echo "SSH_PASSWORD is required for password authentication mode" >&2
      kill "$stream_pid" >/dev/null 2>&1 || true
      rm -f "$tmp_out" "$tmp_pipe" "$cmd_file"
      return 10
    fi

    SSHPASS="$SSH_PASSWORD" sshpass -e "$@" > "$tmp_pipe" 2>&1
    rc="$?"
  else
    "$@" > "$tmp_pipe" 2>&1
    rc="$?"
  fi
  set -e

  wait "$stream_pid" >/dev/null 2>&1 || true

  if grep -q "remote port forwarding failed for listen port" "$tmp_out"; then
    hint="Forward request rejected by server (likely already in use). Consider changing SSH_REMOTE_FORWARDS or enabling SISH_FORCE_CONNECT=true (requires server --enable-force-connect=true)."
    log_forwarder_line "$hint"
    log_stats "HINT | $hint"
  fi

  rm -f "$tmp_out" "$tmp_pipe" "$cmd_file"

  return "$rc"
}

attempt=0
while true; do
  rotate_logs

  attempt=$((attempt + 1))
  start_epoch="$(date +%s)"

  if execute_ssh; then
    rc=0
  else
    rc=$?
  fi

  end_epoch="$(date +%s)"
  duration=$((end_epoch - start_epoch))
  log_stats "DISCONNECTED | rc=${rc} | session_seconds=${duration} | attempt=${attempt}"

  rotate_logs

  if [ "$SISH_CLIENT_AUTORESTART" != "true" ]; then
    exit "$rc"
  fi

  if [ "$SISH_CLIENT_MAX_RETRIES" -gt 0 ] && [ "$attempt" -ge "$SISH_CLIENT_MAX_RETRIES" ]; then
    log_stats "STOP | max retries reached (${SISH_CLIENT_MAX_RETRIES})"
    exit "$rc"
  fi

  log_stats "RESTARTING | sleep_seconds=${SISH_CLIENT_RESTART_DELAY_SECONDS}"
  sleep "$SISH_CLIENT_RESTART_DELAY_SECONDS"
done
