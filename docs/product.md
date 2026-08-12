# Mirage Product Requirements

## Summary

Mirage is a single-node temporary-environment manager. Its CLI talks to a private management server, starts local application processes, monitors their health and TTL, persists desired state in embedded libSQL, and exposes healthy environments through Caddy on a public listener.

## Goals

- Give a user or automation agent a short-lived **space** with a bearer token.
- Run one or more named **links** (local processes) within that space.
- Publish only healthy links at predictable hostnames through Caddy.
- Automatically stop expired processes and reconcile stale Caddy routes every minute.
- Offer equivalent CLI, versioned HTTP API, and a small HTMX/Tailwind dashboard.
- Keep management surfaces private and public traffic isolated.

## Non-goals (v1)

- Distributed/multi-node scheduling, containers, remote execution, user accounts/RBAC, billing, DNS provisioning, or production-grade secret management.
- Serving management routes from the public listener.
- Persisting application logs forever.

## Domain model

### Space

A space has a random human-readable alias, a cryptographically random token (shown only when created), creation/expiry timestamps, and status. Default TTL is 6h; accepted range is 1m–12h. Token hashes, never plaintext tokens, are stored. Deleting a space cascades to its links.

### Link

A link belongs to a space and has a unique name within that space, command, execution folder, health-check request, grace period, TTL, restart policy, allocated loopback port, process identity, bounded logs, and lifecycle status. Its public hostname is `<link-name>-<space-alias>.<base-host>` (or `<link-name>-<space-alias><base-host>` when the configured base host begins with `-`).

## Interfaces

### Server

`mirage start --public 9955 --private 9956 [--config PATH]` starts/reconciles Caddy, the public proxy listener, private HTTP management listener, libSQL state, and minute cleanup worker. Public listener serves temporary links only. Private listener serves `/healthz`, `/`, `/dashboard`, and `/api/v1/`.

### CLI

- `space create|list|delete`
- `link create|logs|list|restart|delete`
- Token resolution: explicit `--token`, then `MIRAGE_TOKEN`, then `.mirage_token` walking only the current directory (v1).
- CLI server URL resolves from `--server`, `MIRAGE_SERVER`, then `http://127.0.0.1:9956`.
- Machine-readable JSON is supported with `--json`.

### Adapter boundaries (hexagonal architecture)

Domain/application code depends on ports for state repository, reverse proxy, process supervisor, clock/ID/token generation, health checker, log store, and scheduler. Caddy API and go-libsql are outbound adapters; Cobra CLI and private HTTP/HTMX are inbound adapters. Tests use fakes at every port.

## Caddy behavior

Mirage manages Caddy through an adapter over its admin API. It owns a clearly identified subtree/config namespace, creates routes only after a link becomes healthy, removes routes before process termination, and idempotently reconciles desired database state against proxy state every minute. `start` may launch a configured Caddy binary and must terminate the child it launched during graceful shutdown; externally managed Caddy is configurable.

## Process lifecycle

1. Validate input and authorization.
2. Reserve link name and a free loopback port; interpolate `{port}` in command and health-check URL and set `PORT`.
3. Start command as a process group in the requested folder.
4. Probe until healthy or grace expires.
5. Add Caddy route only when healthy.
6. Restart after unexpected exit/unhealthy state only when `--restarts` is enabled, with bounded backoff.
7. At TTL/delete: remove route, terminate process group gracefully, force kill after timeout, then mark terminal state.

## Security and safety

- Bind management to loopback by default; explicit config is required to expose it.
- Treat space tokens as bearer credentials; redact tokens and environment secrets from logs.
- Execute commands without adding an extra shell unless explicitly configured as a shell command; v1 accepts a string and runs through the platform shell, documented as trusted local input.
- Validate host labels, paths, durations, verbs, and URLs. Health checks may target loopback only.
- Prevent Mirage-owned Caddy configuration from overwriting unrelated routes.

## Reliability

Startup performs reconciliation before accepting mutations. Cleanup runs immediately and every minute. Orphan routes/process records are repaired or removed. Writes and lifecycle transitions are idempotent where practical. Graceful shutdown stops accepting mutations, removes active routes, terminates owned processes/Caddy, and closes storage.

## Quality gates

- Go 1.26 managed by mise.
- Overall statement coverage at least 85% (`go test -cover ./...`), with race and vet checks.
- Hexagonal package boundaries and no Caddy/libSQL/process primitives in domain packages.
- Every implementation task requires isolated worktree, artifact/black-box review, and code-standards review before merge.

## Open decisions requiring usage review

The usage document records proposed v1 decisions for base-host grammar, Caddy ownership, token file lookup, port interpolation, delete force semantics, restart behavior, API authentication, and log retention.
