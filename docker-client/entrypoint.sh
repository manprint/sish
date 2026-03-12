#!/usr/bin/env sh
set -eu

SISH_CLIENT_UID="${SISH_CLIENT_UID:-1000}"
SISH_CLIENT_GID="${SISH_CLIENT_GID:-1000}"
SISH_CLIENT_USER="${SISH_CLIENT_USER:-sishclient}"
SISH_CLIENT_GROUP="${SISH_CLIENT_GROUP:-sishclient}"
SISH_CLIENT_HOME="${SISH_CLIENT_HOME:-/home/${SISH_CLIENT_USER}}"

ensure_runtime_paths() {
  mkdir -p /app/log/forwarder /app/log/stats /app/ssh "$SISH_CLIENT_HOME/.ssh"
}

safe_chown_dir() {
  target="$1"

  if [ -d "$target" ]; then
    # Never fail startup if host-mounted paths are read-only or restricted.
    chown -R "$SISH_CLIENT_UID:$SISH_CLIENT_GID" "$target" >/dev/null 2>&1 || true
  fi
}

if [ "$(id -u)" = "0" ]; then
  if ! getent group "$SISH_CLIENT_GROUP" >/dev/null 2>&1; then
    addgroup -S -g "$SISH_CLIENT_GID" "$SISH_CLIENT_GROUP" >/dev/null 2>&1 || true
  fi

  if ! id "$SISH_CLIENT_USER" >/dev/null 2>&1; then
    adduser -S -D -H -h "$SISH_CLIENT_HOME" -u "$SISH_CLIENT_UID" -G "$SISH_CLIENT_GROUP" "$SISH_CLIENT_USER" >/dev/null 2>&1 || true
  fi

  ensure_runtime_paths
  # Do not chown /app/ssh, it is commonly mounted read-only with user keys.
  safe_chown_dir /app/log
  safe_chown_dir "$SISH_CLIENT_HOME"

  exec su-exec "$SISH_CLIENT_UID:$SISH_CLIENT_GID" /app/scripts/run-client.sh
fi

ensure_runtime_paths
exec /app/scripts/run-client.sh
