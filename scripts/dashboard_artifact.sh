#!/usr/bin/env sh
# Seeded httptest HTTP/DOM artifact: no listener, DNS, browser, or secrets needed.
set -eu
mise exec go@1.26 -- go test -count=1 -run 'TestDashboard(PrivateFragmentsAndEscaping|MutationFragmentsAndCookies|CSRF)' ./internal/adapters/inbound/httpapi
