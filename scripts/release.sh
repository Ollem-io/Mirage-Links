#!/bin/sh
# Hermetic release candidate: build a versioned executable, checksum it, then
# run it from a directory containing no source tree.
set -eu
version=${VERSION:-dev}
out=${1:-dist}
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
rm -rf "$out"
mkdir -p "$out"
GOFLAGS="${GOFLAGS:-}" go build -trimpath -ldflags "-s -w -X github.com/primeintellect/mirage/internal/buildinfo.Version=$version" -o "$out/mirage" "$root/cmd/mirage"
( cd "$out" && sha256sum mirage > checksums.txt )
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
cp "$out/mirage" "$tmp/"
( cd "$tmp"; ./mirage version; ./mirage --help >/dev/null )
printf 'release candidate: %s/mirage (%s)\n' "$out" "$version"
