# MIR-06 Code Review — Ollem Luna (high)

## Verdict: APPROVE

## Verification evidence

- `mise run check`: PASS (rc 0), including fmt-check, vet, full tests, aggregate coverage gate (>=85%), MIR-05/MIR-06 coverage gates, scenario, build, and smoke.
- `mise run test-race`: PASS (rc 0), including application concurrency tests.
- `mise run mir06-scenario`: PASS (rc 0), exact trace: `reserve-port -> start-process -> health-ok -> add-route -> active -> remove-route -> stop-process -> release-port -> deleted`.
- `mise run test-libsql`: PASS (rc 0), including migrations/reopen, token-hash-only storage, uniqueness/concurrency, expiry/reconcile, lifecycle metadata, tombstone query across reopen, and transactional behavior.
- MIR-06 compensation coverage: 100.0%, above required 95%.

## Re-review of prior blockers

1. **Compensation error handling: fixed.** `destroy` and `destroyForRestart` now retain compensation errors, persist `StatusFailed`, and return an internal error instead of deleting/resetting state. `failStart` also preserves failure state and reports compensation/persistence failures. This prevents silent route/process/port leaks from being represented as successful terminal state.

2. **Delete idempotency: fixed.** `LinkRepository.LinkDeleted` is a durable tombstone query. `DeleteLink` checks it after authenticated not-found, so duplicate known deletes work across Service instances/restarts while unknown names remain not-found. LibSQL includes `TestDeletedLinkTombstoneQuerySurvivesReopen`; application includes `TestDeleteIdempotencyAcrossServiceInstances`.

3. **Scheduled restart failure handling: fixed.** `runScheduledRestart` persists failed state and calls `rescheduleFailed`; failed starts are reloaded and rescheduled with bounded exponential backoff, with TTL checks. Application includes `TestScheduledFailureReschedulesDurableFailed`.

## Semantic plan checks

- Health probe completes before route publication; TTL is checked before publication and link expiry is bounded by space expiry.
- Deletion compensation ordering remains exact: route removal, process stop, port release, then terminal persistence/deletion.
- Manual restart does not extend TTL; automatic restart uses 1s-to-1m bounded exponential backoff and TTL wins.
- Authorization verifies bearer hashes, rejects invalid credentials, and preserves not-found behavior for unknown links; force deletion requires a non-empty audit reason.
- Token hash is persisted, plaintext token is returned only from create, and representations omit credentials.
- Concurrent same-name mutations are serialized/uniqueness-protected; race suite passes.
- Hexagonal boundaries remain port-based; MIR-06 application and compensation coverage gates pass.

No remaining blocking semantic or security/auth/lifecycle findings observed. MIR-06 is complete and approved.
