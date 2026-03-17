#!/usr/bin/env sh
set -eu

trim() {
  echo "$1" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//'
}

print_arg() {
  printf '%s\n' "$1"
}

append_forwards() {
  list="$1"
  flag="$2"

  old_ifs="$IFS"
  IFS=';'
  for item in $list; do
    clean="$(trim "$item")"
    [ -n "$clean" ] || continue
    print_arg "$flag"
    print_arg "$clean"
  done
  IFS="$old_ifs"
}

SSH_HOST="${SSH_HOST:-tuns.0912345.xyz}"
SSH_PORT="${SSH_PORT:-2222}"
SSH_USER="${SSH_USER:-}"
SSH_TARGET="${SSH_TARGET:-}"
SSH_AUTH_MODE="${SSH_AUTH_MODE:-auto}"
SSH_PRIVATE_KEY_PATH="${SSH_PRIVATE_KEY_PATH:-/app/ssh/id_ed25519}"
SSH_IDENTITY_ONLY="${SSH_IDENTITY_ONLY:-yes}"
SSH_EXIT_ON_FORWARD_FAILURE="${SSH_EXIT_ON_FORWARD_FAILURE:-yes}"
SSH_SERVER_ALIVE_INTERVAL="${SSH_SERVER_ALIVE_INTERVAL:-30}"
SSH_SERVER_ALIVE_COUNT_MAX="${SSH_SERVER_ALIVE_COUNT_MAX:-3}"
SSH_CONNECT_TIMEOUT="${SSH_CONNECT_TIMEOUT:-10}"
SSH_STRICT_HOST_KEY_CHECKING="${SSH_STRICT_HOST_KEY_CHECKING:-no}"
# Keep default on a writable path; /app/ssh is often mounted read-only from host keys.
SSH_USER_KNOWN_HOSTS_FILE="${SSH_USER_KNOWN_HOSTS_FILE:-/tmp/ssh_known_hosts}"
SSH_COMPRESSION="${SSH_COMPRESSION:-no}"
SSH_LOG_LEVEL="${SSH_LOG_LEVEL:-INFO}"
SSH_REMOTE_FORWARDS="${SSH_REMOTE_FORWARDS:-}"
SSH_LOCAL_FORWARDS="${SSH_LOCAL_FORWARDS:-}"
SSH_DYNAMIC_FORWARDS="${SSH_DYNAMIC_FORWARDS:-}"
SSH_SEND_ENV="${SSH_SEND_ENV:-SISH_PROXY_PROTOCOL,SISH_PROXYPROTO,SISH_HOST_HEADER,SISH_STRIP_PATH,SISH_SNI_PROXY,SISH_TCP_ADDRESS,SISH_TCP_ALIAS,SISH_LOCAL_FORWARD,SISH_AUTO_CLOSE,SISH_FORCE_HTTPS,SISH_TCP_ALIASES_ALLOWED_USERS,SISH_DEADLINE,SISH_ID,SISH_NOTE,SISH_NOTE64,SISH_FORCE_CONNECT}"
SSH_OPTIONS="${SSH_OPTIONS:-}"
SSH_EXTRA_ARGS="${SSH_EXTRA_ARGS:-}"
SISH_REMOTE_COMMAND="${SISH_REMOTE_COMMAND:-}"

if [ -z "$SSH_TARGET" ]; then
  if [ -n "$SSH_USER" ]; then
    SSH_TARGET="${SSH_USER}@${SSH_HOST}"
  else
    SSH_TARGET="$SSH_HOST"
  fi
fi

print_arg ssh

print_arg -p
print_arg "$SSH_PORT"

print_arg -o
print_arg "StrictHostKeyChecking=${SSH_STRICT_HOST_KEY_CHECKING}"
print_arg -o
print_arg "UserKnownHostsFile=${SSH_USER_KNOWN_HOSTS_FILE}"
print_arg -o
print_arg "ServerAliveInterval=${SSH_SERVER_ALIVE_INTERVAL}"
print_arg -o
print_arg "ServerAliveCountMax=${SSH_SERVER_ALIVE_COUNT_MAX}"
print_arg -o
print_arg "ConnectTimeout=${SSH_CONNECT_TIMEOUT}"
print_arg -o
print_arg "ExitOnForwardFailure=${SSH_EXIT_ON_FORWARD_FAILURE}"
print_arg -o
print_arg "Compression=${SSH_COMPRESSION}"
print_arg -o
print_arg "LogLevel=${SSH_LOG_LEVEL}"

use_key="no"
if [ "$SSH_AUTH_MODE" = "key" ]; then
  use_key="yes"
elif [ "$SSH_AUTH_MODE" = "auto" ] && [ -f "$SSH_PRIVATE_KEY_PATH" ]; then
  use_key="yes"
fi

if [ "$use_key" = "yes" ]; then
  print_arg -i
  print_arg "$SSH_PRIVATE_KEY_PATH"
  print_arg -o
  print_arg "IdentitiesOnly=${SSH_IDENTITY_ONLY}"
fi

append_forwards "$SSH_REMOTE_FORWARDS" -R
append_forwards "$SSH_LOCAL_FORWARDS" -L
append_forwards "$SSH_DYNAMIC_FORWARDS" -D

old_ifs="$IFS"
IFS=','
for send_var in $SSH_SEND_ENV; do
  clean="$(trim "$send_var")"
  [ -n "$clean" ] || continue

  value="$(printenv "$clean" 2>/dev/null || true)"
  [ -n "$value" ] || continue

  print_arg -o
  print_arg "SendEnv=${clean}"
done
IFS="$old_ifs"

old_ifs="$IFS"
IFS=';'
for opt in $SSH_OPTIONS; do
  clean="$(trim "$opt")"
  [ -n "$clean" ] || continue
  print_arg -o
  print_arg "$clean"
done
IFS="$old_ifs"

if [ -n "$SSH_EXTRA_ARGS" ]; then
  for extra in $SSH_EXTRA_ARGS; do
    print_arg "$extra"
  done
fi

print_arg "$SSH_TARGET"

if [ -n "$SISH_REMOTE_COMMAND" ]; then
  print_arg "$SISH_REMOTE_COMMAND"
fi
