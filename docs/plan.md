# Mirage v1 Implementation Plan

**Gate 0: PASSED on 2026-08-12.** The product contract in `docs/product.md` and the approved behavior in `docs/usage.md` are sufficiently complete to begin implementation. The approved usage document is normative where it resolves an open product decision.

## Execution rules

- All delegated GPT models use the **Ollem provider**: planner `ollem-io-lab/gpt-5.6-sol`, implementer `ollem-io-lab/gpt-5.6-terra`, reviewers `ollem-io-lab/gpt-5.6-luna`. Reasoning level is requested in each role brief (planner/reviewer high, implementer medium; after two failed attempts escalate implementation to Ollem Sol low).

- Implement tasks in dependency order; a task is complete only when its acceptance tests and black-box artifact pass.
- Keep the architecture hexagonal: domain/application code depends on ports, while libSQL, Caddy, OS processes, HTTP, HTMX, and Cobra are adapters.
- Use Go 1.26 through `mise`; all repository automation must be invocable through checked-in `mise.toml` tasks.
- Maintain at least **85% statement coverage for `go test -cover ./...`**, with higher-risk lifecycle, authorization, reconciliation, and validation branches explicitly exercised. Generated files and trivial entry-point wiring may be excluded only through an agreed, documented coverage policy.
- Tests must not require public DNS, internet access, a privileged port, or a developer's installed Caddy/libSQL service. Use fakes at ports for unit/application tests and isolated real artifacts for adapter/black-box tests.
- Never expose management routes on the public listener, never persist plaintext bearer tokens, and never mutate Caddy configuration outside Mirage's owned namespace.

## Task graph

| ID | Task | Depends on |
|---|---|---|
| MIR-01 | Bootstrap, toolchain, and executable skeleton | — |
| MIR-02 | Domain model, validation, and application ports | MIR-01 |
| MIR-03 | libSQL persistence adapter | MIR-02 |
| MIR-04 | Caddy ownership and proxy adapter | MIR-02 |
| MIR-05 | Process, health-check, and bounded-log adapters | MIR-02 |
| MIR-06 | Application lifecycle orchestration | MIR-03, MIR-04, MIR-05 |
| MIR-07 | Versioned private HTTP API and listener isolation | MIR-06 |
| MIR-08 | HTMX/Tailwind dashboard | MIR-07 |
| MIR-09 | Cobra CLI and token/config resolution | MIR-07 |
| MIR-10 | Startup reconciliation, shutdown, end-to-end, and release gate | MIR-08, MIR-09 |

---

## MIR-01 — Bootstrap, toolchain, and executable skeleton

**Purpose**

Create a reproducible Go 1.26 development/build environment and the minimal command/server composition structure needed by later tasks, without implementing product behavior.

**Dependencies**

None.

**Scope / files**

- `mise.toml`: pin Go 1.26, Caddy, and Node/Tailwind tooling; add tasks for format, lint, test, coverage, assets, build, and end-to-end tests.
- `go.mod`, `go.sum`.
- `cmd/mirage/main.go` and skeletal internal package directories for domain, application, inbound adapters, outbound adapters, and composition.
- `.gitignore`, test/coverage configuration, and concise contributor/build instructions in `README.md` or `docs/development.md`.
- Establish injectable exit/error handling so command behavior can be tested without spawning the compiled binary for every case.

**Acceptance tests**

1. On a clean checkout, `mise install` installs the pinned toolchain.
2. `mise run fmt-check`, `mise run test`, and `mise run build` succeed.
3. The built `mirage` executable responds to `--help` and exits zero.
4. A version variable can be injected at build time and displayed by `mirage version` (placeholder development version is acceptable at this task).
5. Package dependency checks confirm domain/application packages do not import adapter packages.

**Black-box artifact**

A CI/shell smoke script that creates a clean temporary build directory, runs the documented `mise` commands, executes the resulting binary's `--help` and `version`, and records stdout/stderr/exit codes.

**Coverage requirements**

- Repository-wide coverage command is wired from the first task and must not regress below 85% as code is added.
- Command root/version behavior and failure exit paths have automated tests.
- Bootstrap-only shell/configuration is validated by the black-box smoke artifact rather than counted as Go coverage.

---

## MIR-02 — Domain model, validation, and application ports

**Purpose**

Define the stable domain vocabulary and dependency boundaries before implementing infrastructure: spaces, links, statuses, lifecycle transitions, authorization, clocks/IDs/tokens, repositories, processes, health, logs, proxy, and scheduling.

**Dependencies**

MIR-01.

**Scope / files**

- `internal/domain/...`: `Space`, `Link`, value objects, lifecycle/status enums, expiry rules, restart policy, process identity, allocated port, public hostname/URL, and typed errors.
- `internal/application/ports/...`: repository/unit-of-work, clock, alias/ID/token/hash, loopback port allocator, process supervisor, health checker, log sink/stream, Caddy/proxy, audit, and scheduler interfaces.
- Validation for TTL 1m–12h, grace 1s–15m, DNS-label names, base-host normal/suffix grammar, supported health methods, loopback-only health URLs, folders, audit reasons, and legal state transitions.
- Token generation/hash/constant-time verification contract; plaintext tokens may exist only in the one-time create result.
- Pure hostname interpolation and public-port URL rules.

**Acceptance tests**

1. Table tests cover both TTL boundaries, grace boundaries, valid/invalid DNS labels, health methods, IPv4/IPv6/`localhost` loopback URLs, and malformed URLs.
2. Hostname tests prove both `api-calm-fox.mirage.example.com` and `api-calm-fox-mirage.example.com` forms.
3. Transition tests reject illegal lifecycle movement and preserve terminal/idempotent behavior.
4. Token tests prove hashes verify correctly, wrong tokens fail, and persisted domain records cannot serialize plaintext credentials.
5. A compile-time architecture test/fake implementation demonstrates every outbound dependency is replaceable.

**Black-box artifact**

A small domain contract test suite run as `mise run test-domain` that consumes public constructors and produces golden JSON snapshots with no token hash/plaintext leakage.

**Coverage requirements**

- At least 95% coverage for domain validation, hostname/URL construction, expiry logic, authorization helpers, and transition rules.
- Every accepted range boundary and every typed validation/conflict/not-found/unauthorized branch is tested.
- Fuzz tests cover DNS labels, duration parsing, and health-check parsing without panics.

---

## MIR-03 — libSQL persistence adapter

**Purpose**

Persist desired state and lifecycle metadata in embedded libSQL with safe migrations, transactional uniqueness/cascades, token hashes only, and queries needed for recovery/reconciliation.

**Dependencies**

MIR-02.

**Scope / files**

- `internal/adapters/outbound/libsql/...`: connection lifecycle, schema migrations, repositories, row/domain mapping, transactions.
- Schema for spaces, links, process identity/state, allocated ports, timestamps/expiry, restart metadata, bounded audit reason metadata, and migration versioning.
- Queries for active/expired spaces and links, records requiring cleanup/reconciliation, unique `(space_id, link_name)`, and deletion/archive behavior.
- Adapter integration fixtures using a temporary on-disk database, not mocks of SQL calls.

**Acceptance tests**

1. A new database migrates to the current schema and can reopen idempotently.
2. Space creation stores only a token hash; searching the database bytes cannot find the issued token.
3. Concurrent attempts to create the same link name in one space produce one success and one conflict; the same name in different spaces succeeds.
4. Space deletion transactionally cascades/archives its links according to the approved contract.
5. Expiry/reconciliation queries return deterministic records at exact clock boundaries.
6. Failed transactions leave no partial link, port, or lifecycle update.

**Black-box artifact**

An adapter integration test executable/script that creates a real temporary `.db`, migrates, performs CRUD and simulated restart/reopen, then inspects schema/data invariants with libSQL tooling.

**Coverage requirements**

- At least 90% coverage for repository mapping, migrations, transactional branches, constraint translation, and query selection logic.
- Tests include corrupt/unsupported migration version, unavailable path, rollback, not-found, conflict, and database-close behavior.

---

## MIR-04 — Caddy ownership and proxy adapter

**Purpose**

Implement safe, idempotent control of only Mirage-owned Caddy routes, supporting both a managed Caddy child and an externally managed admin API.

**Dependencies**

MIR-02.

**Scope / files**

- `internal/adapters/outbound/caddy/...`: admin client, owned namespace/subtree representation, route add/remove/list/reconcile, retry/error translation.
- Managed-child startup/readiness/termination and external mode connection behavior.
- Route generation from validated hostname to reserved loopback upstream/public listener without registering management endpoints.
- Preservation of unrelated Caddy configuration under every operation.

**Acceptance tests**

1. Adding an owned route twice is idempotent; removing it twice is harmless.
2. Adapter snapshots contain a clear Mirage namespace/identifier and the expected hostname/upstream.
3. Reconciliation adds missing desired routes and removes orphan owned routes while leaving unrelated routes byte-for-byte/semantically unchanged.
4. Managed mode waits for Caddy admin readiness and terminates only the child it launched; external mode never terminates Caddy.
5. Admin timeouts, malformed responses, and conflicts become typed application errors with no partial ownership mutation.

**Black-box artifact**

A Caddy integration harness using an isolated real Caddy process, temporary config, random admin/public ports, and a dummy loopback upstream; it verifies proxy traffic, route removal, unrelated-route preservation, and managed-child shutdown.

**Coverage requirements**

- At least 90% coverage for route generation, ownership filtering, diff/idempotency, and admin error mapping.
- Real-Caddy tests cover add, serve, remove, reconcile, managed shutdown, and external mode; fake-port tests cover all retry/failure branches.

---

## MIR-05 — Process, health-check, and bounded-log adapters

**Purpose**

Provide safe local execution through the platform shell, process-group ownership, loopback health probing, port injection/interpolation, bounded redacted logs, and observable termination behavior.

**Dependencies**

MIR-02.

**Scope / files**

- `internal/adapters/outbound/process/...`: loopback port reservation, platform-shell command start, execution folder, `PORT`, literal `{port}` interpolation, process-group identity, graceful stop then forced kill.
- `internal/adapters/outbound/health/...`: GET/HEAD/POST loopback probes, timeout and grace support.
- `internal/adapters/outbound/logs/...`: merged timestamped stdout/stderr entries with stream labels, 10 MiB per-link ring, tail/follow, client cancellation, best-effort bearer/environment-secret redaction.
- OS-specific files/build tags where process-group semantics differ.

**Acceptance tests**

1. A fixture child observes the selected `PORT`, interpolated command, correct working directory, and can be health-probed.
2. No health request can target a non-loopback host, including redirect-based escape; redirects are rejected or constrained to loopback.
3. Graceful termination reaches the entire process group; an ignoring child is force-killed after the configured timeout.
4. The log ring never exceeds 10 MiB, retains newest complete records, labels streams, supports `tail`, ends `follow` on process termination, and releases on client disconnect.
5. Tokens and configured secret values are absent from captured/logged diagnostic output in redaction fixtures.

**Black-box artifact**

Compiled fixture programs/scripts for healthy, never-healthy, crashing, forked-child, signal-ignoring, and noisy services, exercised against the real OS adapter with leak checks for processes, ports, goroutines, and file descriptors.

**Coverage requirements**

- At least 90% coverage for interpolation, health validation/probe classification, ring-buffer boundaries, redaction, follow cancellation, and termination escalation.
- OS artifact tests cover success, timeout, unexpected exit, forked child, forced kill, port collision/retry, stdout/stderr, and log overflow.

---

## MIR-06 — Application lifecycle orchestration

**Purpose**

Implement use cases and ordering guarantees for spaces and links using only ports: create/list/delete, health-gated publication, manual restart, optional automatic restart, TTL expiry, logs, and administrative force deletion.

**Dependencies**

MIR-03, MIR-04, MIR-05.

**Scope / files**

- `internal/application/...`: space and link services/use cases, authorization, one-time token result, lifecycle transaction/compensation logic, audit reason handling.
- Creation sequence: validate/authenticate, reserve name/port, persist intent, start process, probe during grace, publish only after healthy, persist active state.
- Deletion/expiry sequence: remove route before stopping process group, then terminal/archive update; idempotent known deletion and not-found unknown names.
- Manual restart without TTL extension and automatic restart for unexpected exit/sustained unhealthy state with exponential backoff from 1s to 1m, always bounded by TTL.
- Startup failure response includes recent sanitized logs and cleans all resources.

**Acceptance tests**

1. Fake-port trace assertions prove exact happy-path and rollback ordering, especially health-before-route and route-before-process-stop.
2. Grace expiry returns failure/non-ready state, recent redacted logs, no route, no running process, and no leaked port.
3. Authorization tests cover valid token, missing/invalid token (unauthorized), cross-space access (not found), and force deletion requiring a non-empty audit reason.
4. Manual restart preserves expiry; automatic restart disabled/enabled cases and 1s→1m backoff are deterministic under fake clock/scheduler.
5. TTL wins over in-flight creation, health checks, and scheduled restart.
6. Duplicate/concurrent create/delete/restart requests reach a consistent idempotent state.

**Black-box artifact**

A deterministic application scenario runner backed by the real temporary libSQL adapter and fake process/health/Caddy/clock ports; it emits an event timeline that is golden-tested for ordering and compensation.

**Coverage requirements**

- At least 95% coverage for application services and lifecycle state machines.
- Every port failure at each orchestration step has a compensation/consistency test.
- Race-enabled tests cover concurrent same-name creation, delete-vs-restart, expiry-vs-health, and duplicate mutations.

---

## MIR-07 — Versioned private HTTP API and listener isolation

**Purpose**

Expose the approved `/api/v1/` contract and readiness endpoint on the private listener while proving the public listener can serve temporary links only.

**Dependencies**

MIR-06.

**Scope / files**

- `internal/adapters/inbound/httpapi/...`: handlers, request/response DTOs, bearer middleware, validation/error mapping, streaming logs, graceful mutation drain.
- Routes for space create/list/get/delete and link create/list/logs/restart/delete.
- `GET /healthz` unauthenticated and readiness-only; stable JSON errors `{code,message,details?}`.
- Separate private/public server wiring; `/`, `/dashboard`, `/api/v1/`, and `/healthz` management semantics must never be mounted on the public listener.
- API request limits, timeouts, content types, method handling, and sanitized structured logging.

**Acceptance tests**

1. Contract tests assert status mapping: validation 400, missing/invalid token 401, cross-space 404, conflict 409, internal 500.
2. Space create returns a token exactly once; all later list/get representations omit token and hash.
3. Link operations enforce bearer scope and return approved fields including name, URL, status, and expiry.
4. Log tail/follow streams terminate on process end and client cancellation.
5. A listener-isolation test requests every management route on the public address and proves none is registered or proxied by Mirage.
6. During graceful drain, health reflects readiness policy and new mutations are rejected while in-flight work is bounded.

**Black-box artifact**

A standalone API conformance script using `curl`/an HTTP client against a started test server on random private/public ports, validating JSON schemas, auth behavior, streaming cancellation, and public-listener isolation.

**Coverage requirements**

- At least 90% coverage for handlers, middleware, DTO conversion, error mapping, and listener composition.
- Every route has success, validation, authorization, content-type/method, cancellation, and service-failure tests.
- Golden API fixtures prevent accidental exposure of token hashes, commands/secrets in errors, or unstable error shapes.

---

## MIR-08 — HTMX/Tailwind dashboard

**Purpose**

Deliver the small private dashboard for viewing spaces/links/status/expiry/URLs/recent logs and performing restart/delete/force administrative actions without displaying bearer tokens.

**Dependencies**

MIR-07.

**Scope / files**

- `internal/adapters/inbound/dashboard/...`: page/fragment handlers and templates.
- `web/...`: HTMX interactions, Tailwind source/build output, embedded static assets.
- Views for spaces, links, status/expiry/URL, bounded recent logs, mutation feedback, and empty/error/loading states.
- Destructive administrative actions require typed non-empty reason; dashboard relies on private-network isolation and must not invent browser auth in v1.

**Acceptance tests**

1. `/` and `/dashboard` render only on the private listener and contain no bearer token/token hash.
2. HTMX fragment requests update status/log/action regions with correct headers and accessible fallback responses.
3. Restart/delete actions invoke application use cases; administrative destructive actions without a reason are rejected and a supplied reason is audited.
4. Template escaping prevents command/log/alias content from injecting markup or script.
5. Asset generation is reproducible through `mise` and the Go binary embeds the generated assets.

**Black-box artifact**

A headless HTTP/DOM dashboard smoke test using seeded temporary data that loads the page, requests HTMX fragments, submits restart/delete forms, verifies reason validation, checks escaping, and confirms public-listener 404s.

**Coverage requirements**

- At least 85% coverage for dashboard handlers/view models and all mutation/validation branches.
- Golden HTML tests cover normal, empty, expired, unhealthy, startup-failed, and internal-error states.
- Static/template checks explicitly scan rendered output for token/hash leakage and unsafe unescaped fixture content.

---

## MIR-09 — Cobra CLI and token/config resolution

**Purpose**

Implement the approved human and machine-readable CLI over the private HTTP API, including server/config precedence, secure token lookup, command exit behavior, and log streaming.

**Dependencies**

MIR-07.

**Scope / files**

- `internal/adapters/inbound/cli/...` and `cmd/mirage/...`: `start`, `space create|list|delete`, `link create|list|logs|restart|delete`, global `--server`, `--token`, `--json`, and version/help.
- Config loading from the approved path and flags overriding config; `MIRAGE_SERVER` and default `http://127.0.0.1:9956`.
- Token precedence: `--token`, `MIRAGE_TOKEN`, exact `./.mirage_token`; trim whitespace, do not search parents, warn on group/world-readable files without printing the token.
- Human output, stable JSON output, stderr diagnostics, non-zero exits, interruptible `logs --follow`.
- `start --public 9955 --private 9956 [--config PATH]` composition/config validation.

**Acceptance tests**

1. Table tests prove server/config/flag precedence and exact token precedence, including no parent-directory search.
2. Insecure token-file permissions emit a warning that does not contain the token; JSON stdout remains parseable and diagnostics stay on stderr.
3. Every approved command issues the expected API method/path/body/header and maps API errors to documented non-zero exits.
4. `space create --json` contains the one-time token; all list/log/delete/restart JSON stays stable and secret-free.
5. `link create` validates required command/folder/health input and transmits grace, TTL, name, and restart policy correctly.
6. Ctrl-C/client cancellation stops `logs --follow` promptly without treating a normal interrupt as a server crash.

**Black-box artifact**

A compiled-binary CLI contract suite against a controllable fake HTTP server and, separately, the real MIR-07 server. It captures argv-independent stdout/stderr/exit status, validates JSON with `jq`, changes working directories/permissions for token lookup, and tests signals.

**Coverage requirements**

- At least 90% coverage for CLI commands, precedence/resolution, API client, rendering, and error/exit mapping.
- Every command has human and JSON golden tests, transport/error status tests, and token-redaction assertions.
- Start/config tests cover invalid bind addresses, explicit non-loopback private binding, malformed base host, unavailable ports, and flag overrides.

---

## MIR-10 — Startup reconciliation, shutdown, end-to-end, and release gate

**Purpose**

Complete production wiring and prove crash recovery, minute cleanup, Caddy/process consistency, graceful shutdown, listener isolation, and the full approved workflow in release-like conditions.

**Dependencies**

MIR-08, MIR-09.

**Scope / files**

- Server composition/startup sequence: validate config, open/migrate libSQL, start/connect Caddy, reconcile before accepting mutations, run cleanup immediately and every minute, then report ready.
- Reconciler for expired spaces/links, orphan Mirage routes, missing routes for live healthy active links, stale process records, and processes that should no longer run.
- Graceful shutdown: stop accepting mutations, drain bounded work, remove active routes, terminate owned process groups and only managed Caddy, close HTTP/storage/log resources.
- `test/e2e/...`, fixtures, release build/version metadata, checksums/artifact packaging, operator smoke/runbook documentation.
- CI gate for format/lint, race tests, coverage, real libSQL, real Caddy, process artifacts, API/CLI/dashboard black-box suites, and release smoke.

**Acceptance tests**

1. Readiness is false until initial reconciliation completes; create mutations cannot race ahead of it.
2. Cleanup runs at startup and every fake-clock minute, expires TTLs, removes orphan owned routes, stops obsolete recorded processes, and restores only missing routes whose link is active, alive, and healthy.
3. Unrelated Caddy routes are unchanged across startup, periodic reconcile, space deletion, and shutdown.
4. Crash/restart scenarios recover from: database active + process dead, process recorded + expired, route missing, route orphaned, and a partially completed lifecycle transition.
5. End-to-end flow succeeds: start server, create space, create healthy fixture link, proxy request through public port, list/tail logs, manually restart without TTL extension, delete link, delete space, and gracefully stop server.
6. Negative end-to-end flow proves never-healthy creation exits non-zero with recent sanitized logs and leaves no process/route/port leak.
7. Public requests to all management paths fail throughout startup, steady state, and shutdown.
8. `mise run ci` passes under the race detector where applicable, `go test -cover ./...` reports at least 85%, and a versioned release artifact runs its smoke test from a clean directory.

**Black-box artifact**

A hermetic release-candidate harness that launches the compiled Mirage binary, real isolated Caddy, temporary on-disk libSQL, and fixture child applications on random unprivileged ports. It drives the compiled CLI plus direct HTTP/proxy requests, forcibly crashes/restarts Mirage, advances cleanup conditions, captures redacted logs, checks for leaked processes/routes/ports, and emits a JUnit/report bundle and release checksums.

**Coverage requirements**

- Final repository-wide statement coverage is at least 85% for `go test -cover ./...`; no package containing substantive logic may be below 80% without an explicit documented exception.
- Application lifecycle, authorization, validation, route ownership, reconciliation, and shutdown packages remain at least 90%, with the core lifecycle state machine at least 95%.
- Run unit/integration suites with `-race`; execute black-box tests for real Caddy, real libSQL, OS process groups, API, dashboard, CLI, crash recovery, and release binary.
- Release is blocked by any secret/token leak, management route on the public listener, unrelated Caddy mutation, process/route/port leak, flaky retry-dependent test, or mismatch with `docs/usage.md`.

## Definition of done for v1

Mirage v1 is releasable only after MIR-01 through MIR-10 are complete in dependency order, all black-box artifacts run in CI, Gate 0 remains traceable to the approved 2026-08-12 contract, the final quality/coverage gates pass, and operator-facing usage is verified against the compiled release binary rather than only package-level tests.
