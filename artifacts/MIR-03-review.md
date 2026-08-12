# MIR-03 implementation evidence

## Scope delivered

- `internal/adapters/outbound/libsql` implements existing repository, unit-of-work, and audit ports with actual embedded `github.com/tursodatabase/go-libsql` files.
- Versioned, transactional migrations create `spaces`, `links`, and bounded `audit_events`; reopen is idempotent, an unsupported newer version is rejected, and simultaneous in-process `Open` calls are serialized/tested.
- `PRAGMA foreign_keys = ON` is enabled before any work. The adapter uses one owned connection (`MaxOpenConns(1)`) so the connection-scoped pragma cannot be bypassed by a pool connection. Link foreign keys have `ON DELETE CASCADE`; real-DB tests prove orphan rejection and physical cascading.
- Token persistence is BLOB `token_hash` only. The real DB test searches raw database bytes and cannot find the issued bearer token.
- Link desired-state and recovery metadata are persisted/mapped: command, execution folder, health method/URL, grace, allocated port, process identity, restart count, next restart timestamp, status, and expiry.
- Unique `(space_id, link_name)` writes yield typed conflicts, with bounded retry around SQLite's single-writer lock. Adapter deletion archives links transactionally for lifecycle history.
- Deterministic active/expired space, expired-link, and reconciliation-link queries have exact-boundary tests.

## Black-box adapter artifact

Run `mise run test-libsql`. The executable script creates temporary on-disk `.db` files and runs the complete real-driver integration suite: migration/reopen/concurrent-open, CRUD/mapping, raw token non-leakage, uniqueness, archive/rollback, foreign-key integrity/cascade, and cleanup boundary selection.

## Validation (attempt 2)

```text
mise run test-libsql  PASS
mise run test-race    PASS
mise run check        PASS
aggregate coverage: 91.7%
libSQL adapter coverage: 88.8%
```

`mise.toml` sets `CGO_ENABLED=1`, as required by go-libsql native bindings; a C compiler is therefore a build prerequisite.
