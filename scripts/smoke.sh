#!/usr/bin/env sh
# Black-box bootstrap smoke check. It is intentionally shell-only: no source
# package is imported after the binary has been built.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/mirage-smoke.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

cd "$root"
mise run fmt-check
mise run test
mise run build
cp bin/mirage "$tmp/mirage"

run_case() {
    name=$1
    expected=$2
    shift 2
    set +e
    "$tmp/mirage" "$@" >"$tmp/$name.stdout" 2>"$tmp/$name.stderr"
    actual=$?
    set -e
    if [ "$actual" -ne "$expected" ]; then
        echo "$name: expected exit $expected, got $actual" >&2
        cat "$tmp/$name.stdout" "$tmp/$name.stderr" >&2
        exit 1
    fi
}

run_case help 0 --help
run_case version 0 version
run_case unknown 2 not-a-command

grep -q '^Usage:' "$tmp/help.stdout"
grep -q '^mirage ' "$tmp/version.stdout"
grep -q 'unknown command' "$tmp/unknown.stderr"
printf '%s\n' "smoke passed: help=0 version=0 unknown=2"
