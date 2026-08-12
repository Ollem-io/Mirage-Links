#!/bin/sh
# Hermetic Linux amd64 release candidate: build a versioned executable,
# checksum it, and run it from a directory containing no source tree.
set -eu
version=${VERSION:-v0.1.0}
out=${1:-dist}
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
artifact="mirage-${version}-linux-amd64"
rm -rf "$out"
mkdir -p "$out"
GOFLAGS="${GOFLAGS:-}" GOOS=linux GOARCH=amd64 go build -trimpath   -ldflags "-s -w -X github.com/primeintellect/mirage/internal/buildinfo.Version=$version"   -o "$out/$artifact" "$root/cmd/mirage"
( cd "$out" && sha256sum "$artifact" > checksums.txt )
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cp "$out/$artifact" "$tmp/"
( cd "$tmp"; "./$artifact" version; "./$artifact" --help >/dev/null )
printf 'release candidate: %s/%s (%s)
' "$out" "$artifact" "$version"
