# Black-box Behavior Review — MIR-03 / MIR-04

## Verdict: APPROVE

Reviewed only the supplied compiled artifacts (`/tmp/mirage-MIR-03-artifact/adapter.test` and `/tmp/mirage-MIR-04-artifact/adapter.test`) and their TASK/USAGE documents; no source/worktrees were inspected.

### MIR-03 (libSQL)
- `./adapter.test -test.v`: all 8 tests passed, including migrations/reopen, token hash-only behavior, concurrent scoped uniqueness, transactional archive cascade, rollback/save/delete, expiry/reconciliation/audit, unsupported migration/closed DB, and constraint/SQL shape.
- Full suite repeated 20 times (`-test.count=1` in separate invocations): 20/20 passed.
- Concurrency target `TestLinksConcurrentUniqueScope` repeated in 10 batches of `-test.count=10`: all batches passed.
- Targeted negative/error paths (`TestUnsupportedMigrationAndClosedDB`, `TestWithinRollbackAndSaveDelete`, `TestConstraintAndSQLShape`) passed. An unmatched selector (`-test.run NoSuchTest`) cleanly produced Go's expected “no tests to run” warning and exit 0.

These runs provide black-box evidence for real on-disk libSQL migration/reopen behavior, non-leaking token storage, uniqueness under concurrency, atomic child-link archival, rollback semantics, and exact expiry/reconciliation selection.

### MIR-04 (Caddy adapter)
- `./adapter.test -test.v`: all 10 tests passed, covering namespaced `@id` ownership, loopback reverse-proxy validation, add/remove idempotency, matching-owned updates, reconciliation repair and orphan deletion, preservation of third-party route JSON, route validation, retries/timeouts/malformed JSON/conflict/rejection translation, constructor/decode boundaries, external child ownership, managed readiness/shutdown, and managed failure/readiness timeout.
- Full suite repeated 10 times: 10/10 passed.
- Targeted robustness tests repeated 10 times each for error/retry/malformed handling, reconciliation byte preservation, idempotent add/remove, validation, and managed readiness failure: every invocation passed.

No observable failures, flaky behavior, isolation violations, or security regressions were found in the supplied black-box acceptance surfaces. APPROVE.


## Attempt 2 rebuild verification

The rebuilt artifact was rerun. Verbose suite now contains 11 tests (including metadata round-trip, foreign-key orphan rejection/cascade, and space boundary queries with concurrent open): **11/11 passed**. Focused repetitions with `-test.count=20` passed for each of `TestForeignKeysRejectOrphansAndCascade`, `TestLinkLifecycleMetadataRoundTrip`, `TestSpaceBoundaryQueriesAndConcurrentOpen`, and `TestOpenMigratesAndReopens` (20/20 each). Verdict remains **APPROVE**; no regression observed.


## Sol escalation rebuild verification

The latest rebuilt artifact was rerun source-free. Verbose execution passed all executed tests, including `TestSpaceBoundaryQueriesAndConcurrentOpen`, `TestCrossProcessMigrationContention`, and `TestUpgradeOriginalV1Fixture` (legacy V1 upgrade). `TestMigrationProcessHelper` was explicitly skipped by the artifact as a helper-process test. Focused `-test.count=20` repetitions passed 20/20 for multi-open/boundary, cross-process migration contention, legacy upgrade fixture, and migration/reopen. Final verdict remains **APPROVE**; no behavioral regression observed.
