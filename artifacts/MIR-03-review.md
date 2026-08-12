# MIR-03 implementation evidence

## Scope delivered

- `internal/adapters/outbound/libsql` is a real embedded libSQL (`github.com/tursodatabase/go-libsql`) implementation of the existing repository, unit-of-work, and audit ports.
- Versioned transactional migrations create `spaces`, `links`, and bounded `audit_events`; reopen is idempotent and a newer database version is rejected.
- Space credentials are stored only in a BLOB `token_hash` column. The integration suite searches raw database bytes for the issued token.
- Link uniqueness is enforced by SQLite/libSQL `UNIQUE(space_id, name)` and translated to typed domain conflicts. Space archive/deletion atomically archives child links.
- Repository reads exclude archived data. Exact-boundary expiry and deterministic reconciliation queries are provided by `ExpiredLinks` and `ReconciliationLinks`.

## Black-box adapter artifact

Run `mise run test-libsql`. It uses temporary on-disk `.db` files and the actual libSQL driver, exercising migrations/reopen, CRUD, token non-leakage, concurrent uniqueness, archive cascade, rollback, and exact expiry/reconciliation selection.

## Validation

```text
mise run test      PASS
mise run test-libsql PASS
- `mise run coverage` PASS: **92.0% aggregate** (libSQL adapter **88.8%**; repository mapping/migration logic has isolated real-DB integration coverage)
```

`mise.toml` sets `CGO_ENABLED=1`, required by go-libsql. A C compiler is required by the driver’s upstream native bindings.
