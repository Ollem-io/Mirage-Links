# MIR-10 Code/Release Review

## Decision: APPROVE

Reviewed checkout `/root/mirage-worktrees/MIR-10` without source edits. The implementation and release evidence satisfy the MIR-10 definition-of-done review areas.

## Executed evidence

- `mise run ci`: **PASS** (format check, vet, race suite, aggregate coverage, MIR-05/06/07/08 package gates, real libSQL artifact, real Caddy 2.10.2 harness, process artifact, API conformance, dashboard artifact, CLI artifact, lifecycle scenario, embedded asset reproducibility, release build).
- `mise run check`: **PASS** (including build/smoke and configured coverage/artifact gates).
- `mise exec -- go test -race ./...`: **PASS** (also included in `mise run ci`).
- `mise run e2e-release`: **PASS on three consecutive reruns**. It built `dist/mirage`, generated `dist/checksums.txt`, ran the compiled source-free CLI matrix, dashboard/API conformance, and process/health/log artifact.

The first direct e2e invocation encountered a transient process-artifact harness failure (`noisy.out` missing after the SIGKILL process-group case); the exact same release harness passed three consecutive `mise run e2e-release` reruns. This did not reproduce and is recorded as a test-harness flake rather than a product gap.

Observed release output included `release candidate: dist/mirage (dev)` and all artifact PASS markers. Coverage gates reported aggregate 86.0%, HTTP API 91.0%, dashboard 91.0%, process 90.8%, health 90.0%, logs 90.8%; all meet their configured thresholds.

## Audit against requested behavior

- **Periodic/startup reconciliation:** `application.Service.Reconcile` performs expiry cleanup, rejects partial/dead/unhealthy records, restores only active live loopback-healthy links, and calls namespace-scoped proxy reconciliation. Composition runs it before readiness and starts a one-minute worker.
- **Readiness:** private HTTP is not bound until storage migration, Caddy child readiness (managed mode), and startup reconciliation complete; the API readiness gate is set only when listeners are served.
- **Crash recovery:** persisted process identities are checked and stale groups are destroyed; only healthy active links become routes. Caddy reconciliation removes stale Mirage-owned routes while preserving unrelated routes.
- **Shutdown/resources:** HTTP drain, worker cancellation/join, route/process cleanup, managed-Caddy stop, and libSQL close are bounded/idempotent. External Caddy mode does not own or terminate the Caddy process. Process artifact checks process groups, ports, goroutines, FDs, logs, escalation, and never-bind/collision cases.
- **Real integrations:** embedded go-libsql migrations/reopen/concurrency/foreign keys/token-hash non-leakage are exercised; pinned real Caddy 2.10.2 admin/public traffic and managed-child behavior are exercised; compiled CLI/dashboard/API artifacts run against real listeners and a source-free binary.
- **Security:** private listener defaults to loopback; public and private handlers are isolated; health checks enforce loopback authority and redirect safety; bearer tokens are hashed/persisted as BLOB only, redacted from logs/JSON diagnostics, and token files require exact permissions; dashboard mutations require CSRF/reason handling; Caddy writes are namespace-scoped; path/command/host validation and architecture boundaries are tested.
- **Hexagonal architecture:** architecture tests pass and core domain/application packages do not import inbound/outbound adapters; composition is the wiring boundary.
- **Release/runbook:** `scripts/release.sh` performs trimpath stripped build, emits SHA-256 manifest, and executes binary from a source-free temporary directory. `docs/runbook.md` documents startup/readiness, recovery, shutdown, external-vs-managed Caddy ownership, checksum retention, and token handling.

## Gaps

No blocking implementation or release gap found. The only observation is the non-reproduced first-run process-artifact flake noted above; consider hardening that shell harness before relying on repeated CI retries as the sole mitigation.


## Attempt 2 re-review (commit 11e2acf)

Reviewed the validation/version diff against the prior approved baseline. Changes correctly validate resolved public/private bind addresses before invoking composition, make explicitly selected missing config files fail closed, and set the stable release version to `v1.0.0` in both build metadata and `scripts/release.sh`. No regressions or architecture/security concerns found.

Additional evidence:

- `mise run ci`: **PASS**, aggregate coverage 86.2%; release output `mirage v1.0.0`.
- `mise run e2e-release`: **PASS** twice consecutively, including compiled CLI, API/dashboard, process/health/log artifacts and source-free checksummed release.

**Final verdict: APPROVE.**
