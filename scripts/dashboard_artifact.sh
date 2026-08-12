#!/usr/bin/env sh
# Headless black-box HTTP/DOM journey against production private/public servers
# on random loopback listeners with seeded temporary in-memory data.
set -eu
mise exec go@1.26 -- go test -count=1 -run '^TestDashboardRunningListenerArtifact$' ./internal/adapters/inbound/httpapi
