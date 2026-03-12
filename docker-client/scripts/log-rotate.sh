#!/usr/bin/env sh
set -eu

if [ "$#" -lt 4 ]; then
  echo "usage: $0 <file> <max_size_mb> <max_age_hours> <max_files>" >&2
  exit 1
fi

file="$1"
max_size_mb="$2"
max_age_hours="$3"
max_files="$4"

[ -f "$file" ] || exit 0

now_epoch="$(date +%s)"
rotate_needed="0"

size_bytes="$(wc -c < "$file" | tr -d ' ')"
max_size_bytes=$((max_size_mb * 1024 * 1024))
if [ "$max_size_bytes" -gt 0 ] && [ "$size_bytes" -ge "$max_size_bytes" ]; then
  rotate_needed="1"
fi

mod_epoch="$(stat -c %Y "$file" 2>/dev/null || date +%s)"
age_hours=$(((now_epoch - mod_epoch) / 3600))
if [ "$max_age_hours" -gt 0 ] && [ "$age_hours" -ge "$max_age_hours" ]; then
  rotate_needed="1"
fi

if [ "$rotate_needed" = "1" ]; then
  ts="$(date +%Y%m%d-%H%M%S)"
  mv "$file" "${file}.${ts}"
  : > "$file"
fi

count=0
for rotated in $(ls -1t "${file}."* 2>/dev/null || true); do
  count=$((count + 1))
  if [ "$count" -gt "$max_files" ]; then
    rm -f "$rotated"
  fi
done
