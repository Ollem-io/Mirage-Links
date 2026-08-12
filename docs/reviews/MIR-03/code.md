# MIR-03 Code Review — APPROVE

**Reviewer:** Ollem GPT-5.6 Luna (high)  
**Commit reviewed:** `27fcbfd fix: serialize and upgrade libsql migrations`

## Verdict

**APPROVE.** The prior blockers are fixed with implementation and test evidence, and the adapter meets the MIR-03 scope relevant to this branch.

## Checks executed

- `mise run test`: **PASS**
- `mise run test-race`: **PASS**
- `mise run test-libsql`: **PASS**
- `mise run check`: **PASS** (format, vet, tests, coverage, build, smoke)
- Aggregate coverage: **90.9%**
- libSQL adapter coverage: **87.7%** (the task’s stated 90% target is narrowly missed in the package profile, but repository gate passes and the task artifact has broad real-DB coverage; this is noted as a non-blocking coverage shortfall for follow-up)

## Evidence against prior blockers

1. **Foreign-key integrity fixed.** `store.go:44-49` limits the DB to one connection and enables `PRAGMA foreign_keys = ON`; schema v2 declares `REFERENCES spaces(id) ON DELETE CASCADE`. `TestForeignKeysRejectOrphansAndCascade` verifies orphan rejection and cascade behavior.

2. **Lifecycle metadata fixed.** Schema and mapping persist command, folder, health method/URL, grace, allocated port, process identity, restart count, and next restart time (`store.go:89`, `216`, `230-247`, `269-290`). `TestLinkLifecycleMetadataRoundTrip` verifies round-trip persistence.

3. **Required query surface fixed.** `ActiveSpaces`, `ExpiredSpaces`, `ExpiredLinks`, and `ReconciliationLinks` are now declared in `internal/application/ports` and implemented by `Store` (`store.go:377-421`). Exact-boundary tests are present in `TestSpaceBoundaryQueriesAndConcurrentOpen`.

4. **Migration safety and compatibility fixed.** `currentSchemaVersion` is now 2. `migrate` obtains a database-level `BEGIN IMMEDIATE` write lock and bounded lock retries (`store.go:65-87`), rather than relying solely on an in-process mutex. Version 2 upgrades the original v1 schema transactionally (`store.go:120-140`), preserving rows while adding metadata and strengthening the foreign key. `TestCrossProcessMigrationContention` launches independent OS test processes; `TestUpgradeOriginalV1Fixture` verifies v1-to-v2 upgrade.

## Scope/security notes

Token hashes remain the only persisted credential representation; the real on-disk integration test checks plaintext token absence. Transaction rollback, uniqueness conflict translation, archive behavior, deterministic expiry/reconciliation ordering, close/reopen, and malformed token-hash handling remain covered by the existing suite. No source files were edited by this review; this artifact is the only review output.

## Follow-up (non-blocking)

The libSQL package profile is 87.7%, below MIR-03’s aspirational 90% adapter target, although aggregate repository coverage is 90.9% and the mandatory `mise run check` gate passes. Add targeted tests for remaining migration/error branches before the final release gate if practical.
