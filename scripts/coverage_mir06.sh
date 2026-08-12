#!/bin/sh
set -eu
profile="${TMPDIR:-/tmp}/mirage-mir06-coverage.out"
mise exec go@1.26 -- go test -coverprofile="$profile" ./internal/application/compensation
coverage=$(mise exec go@1.26 -- go tool cover -func="$profile" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')
printf 'MIR-06 compensation/consistency module coverage: %s%%\n' "$coverage"
awk -v coverage="$coverage" 'BEGIN { if (coverage < 95) { printf "MIR-06 compensation/consistency module coverage %.1f%% is below 95%%\n", coverage > "/dev/stderr"; exit 1 } }'
