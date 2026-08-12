#!/usr/bin/env sh
# MIR-03 real embedded-libSQL adapter artifact. It intentionally uses a temp on-disk DB.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
: "${CGO_ENABLED:=1}"
export CGO_ENABLED
go test -count=1 -v ./internal/adapters/outbound/libsql
