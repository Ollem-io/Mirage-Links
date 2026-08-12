#!/bin/sh
# MIR-07 conformance is intentionally standalone: servers bind random ports in Go tests.
set -eu
mise exec go@1.26 -- go test ./internal/adapters/inbound/httpapi -run 'TestAPIConformance|TestListenerIsolation' -count=1
