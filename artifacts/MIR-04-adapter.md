# MIR-04 Caddy adapter artifact

The hermetic adapter harness is `go test ./internal/adapters/outbound/caddy`. It uses `httptest.NewServer` as a controllable Caddy Admin API and verifies:

- generated `@id` values use `mirage-route-<link-id>` and reverse-proxy only a loopback upstream;
- add/remove are idempotent;
- list filters to valid owned routes;
- reconciliation repairs missing/damaged desired owned routes, deletes owned orphans, and leaves third-party route JSON untouched;
- timeout, retry, malformed JSON, conflict, and rejection error translation; and
- managed child readiness/shutdown ownership versus external mode.

Run: `mise run test` (or the package command above).
