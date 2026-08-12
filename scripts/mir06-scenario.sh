#!/bin/sh
set -eu
mise exec go@1.26 -- go run ./cmd/mirage-scenario
