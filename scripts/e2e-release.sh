#!/bin/sh
# Release candidate harness entrypoint. Existing artifacts are deliberately run
# against compiled fixtures/real adapters; release.sh verifies source-free CLI.
set -eu
./scripts/release.sh "${1:-dist}"
./scripts/api-conformance.sh
./scripts/cli-artifact.sh
./scripts/dashboard_artifact.sh
./scripts/process_artifact.sh
