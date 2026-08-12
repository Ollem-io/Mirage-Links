#!/bin/sh
set -eu
for pkg in health process logs; do
 out=$(go test -cover ./internal/adapters/outbound/$pkg); printf '%s\n' "$out"
 value=$(printf '%s\n' "$out" | sed -n 's/.*coverage: \([0-9.]*\)%.*/\1/p')
 awk -v v="$value" -v p="$pkg" 'BEGIN { if (v < 90) { printf "%s coverage %.1f%% below 90%%\n", p, v > "/dev/stderr"; exit 1 } }'
done
