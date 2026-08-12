# MIR-04 Caddy adapter artifact

## Hermetic real-Caddy integration

Run `mise run caddy-integration`. This launches the pinned Caddy 2.10.2 binary on temporary unprivileged loopback admin/public ports using a temporary JSON config and a `httptest` loopback upstream. It demonstrates all of:

1. an Admin API-owned `mirage-route-real` reverse-proxy route handles real public listener traffic;
2. removing that route removes it without changing the byte representation of a preconfigured third-party route; and
3. managed startup waits for admin readiness and terminates only its child, while external mode owns no process and cannot terminate Caddy.

No public DNS, installed system Caddy, privileged listener, or internet access is required.

## Admin fake coverage

`go test ./internal/adapters/outbound/caddy` uses a controllable `httptest` Admin API for route generation, idempotent add/remove, namespace filtering, typed retry/error mapping, malformed owned route repair, and reconciliation. The failure-injection table fails every write position in a mixed update/add/delete reconciliation and proves exact restoration of all original route JSON, including unrelated configuration.

Run all verification with `mise run check`, `mise run test-race`, and `mise run caddy-integration`.
