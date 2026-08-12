# MIR-07 HTTP API Code Review (Attempt 2)

Reviewer: Ollem Luna (high)  
Revision: `d77d814` (`fix: harden HTTP API drain and server composition`)  
Scope: `/root/mirage-worktrees/MIR-07`; comparison with `docs/plan.md` and `docs/usage.md`; no source edits.

## Verdict: GO

All previously identified MIR-07 blockers are resolved at the checked-in gate level. API conformance, race testing, package coverage, aggregate coverage, formatting/lint/build/smoke checks, and the supporting MIR-05/MIR-06 artifacts pass. The HTTP package is above its required threshold and listener composition plus synchronized drain admission are now present.

## Evidence executed

| Check | Result |
|---|---|
| `mise run api-conformance` | PASS |
| `mise run coverage-mir07` | PASS; HTTP API package **91.3%** |
| `mise run test-race` | PASS; all packages |
| `mise run coverage` | PASS; aggregate **85.8%** (required >=85%) |
| `mise run check` | PASS; format, vet, tests, coverage, build, smoke, MIR-05/MIR-06 checks |
| MIR-05 process artifact | PASS on this revision’s check run |
| MIR-05 package coverage | PASS (health 90.0%, process 91.0%, logs 90.8%) |
| MIR-06 package artifact | PASS (compensation/consistency 100%) |

No unrelated pre-existing process-test flake was observed. Normal tests, race tests, process artifact, and full check were green.

## Review findings

### Routes, content types, methods, body limits, and errors
- `/healthz` is unauthenticated, GET-only, readiness-only, and returns 503 when not ready/draining.
- Versioned routes cover spaces, links, logs/follow, restart, and deletes; unsupported methods return 405 with `Allow`; unknown paths return stable JSON 404.
- JSON mutations reject missing/non-JSON media types, now using `mime.ParseMediaType` for strict `application/json` matching; unknown fields, malformed/trailing JSON, and oversized bodies are rejected. `http.MaxBytesReader` enforces the configured limit (default 1 MiB).
- Error responses consistently use `{code,message,details?}` and map validation/unauthorized/not-found/conflict/timeout/internal failures without returning internal error text.

### Authentication, scoping, and token secrecy
- Link operations require a non-empty Bearer token. Token resolution obtains the owning space before link calls and passes the resolved alias/token, preserving cross-space scoping through the application service.
- Trusted-local private space list/get/create behavior matches usage; link endpoints remain bearer-protected.
- Space creation returns the one-time token only. Space/link list and inspection DTOs do not expose token or token hash. Existing tests explicitly assert no `SECRET_HASH` or previously returned token leakage.
- Force deletion remains private-interface behavior and carries an audit reason to the application layer.

### Public/private listener isolation
- `httpapi.NewServers` uses distinct private and public `http.Server` handlers; the public nil handler is `NotFoundHandler`, preventing management route exposure.
- New `internal/composition/server.go` provides normalized listener defaults (`:9955`, `127.0.0.1:9956`), binds both sockets, closes the private socket if public bind fails, and serves isolated handlers. Composition tests cover startup and shutdown.
- The command remains a bootstrap CLI in this milestone; actual service dependency construction is outside this adapter task. The checked-in composition boundary now exposes the required production wiring (`NewHTTPAPI`, `StartHTTP`, `ShutdownHTTP`) for the eventual server command.

### Logs, streaming, drain, and shutdown
- Tail is validated as a non-negative integer. Follow uses `application/x-ndjson`, flushes chunks, closes the source, and exits on request cancellation/stream end.
- Bounded ring, stream labels, and redaction are covered by MIR-05 artifacts and package gates.
- Drain now uses a mutex-protected admission state (`draining`, `activeMutations`, `drained` channel). It atomically closes admission before waiting, and mutation completion closes the drain channel at zero active calls. This removes the prior unsafe `WaitGroup.Add`/`Wait` race window; `go test -race ./...` passes.
- Server shutdown drains before shutting down both listeners and respects context cancellation/deadlines.

### Hexagonal architecture
- HTTP adapter depends on application interfaces/ports and domain types; no domain/application-to-adapter dependency was found.
- Composition owns adapter construction and listener binding, preserving the intended boundary.

## Residual note (non-blocking)

The executable’s current command surface is still the MIR-01 bootstrap CLI and does not itself launch the full service; that is a later composition/startup integration concern rather than a failing MIR-07 adapter gate. The new composition API and tests establish listener isolation without introducing a dependency inversion violation.
