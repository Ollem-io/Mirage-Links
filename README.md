# Mirage

Mirage is a single-node manager for temporary local application environments.
Mirage v1 provides a CLI, private HTTP API and HTMX dashboard backed by
embedded libSQL. It supervises temporary host processes, health-gates public
Caddy routes, enforces TTLs, and reconciles state after crashes.

## Prerequisites

[mise](https://mise.jdx.dev/) installs the repository-pinned Go 1.26 toolchain.
From a clean checkout:

```sh
mise install
mise run fmt-check
mise run test
mise run coverage
mise run build
mise run smoke
```

`mise run check` runs the complete bootstrap gate. The built executable is
`bin/mirage`.

```sh
bin/mirage --help
bin/mirage version
```

A release build may inject its version without changing source:

```sh
go build -ldflags '-X github.com/primeintellect/mirage/internal/buildinfo.Version=v1.2.3' -o bin/mirage ./cmd/mirage
bin/mirage version # mirage v1.2.3
```

## Layout and boundaries

- `internal/domain`: business vocabulary and invariants (introduced in MIR-02)
- `internal/application`: use cases and ports (introduced in MIR-02)
- `internal/adapters/inbound`: CLI/HTTP/dashboard delivery adapters
- `internal/adapters/outbound`: storage, Caddy, process, and health adapters
- `internal/composition`: executable-only wiring
- `cmd/mirage`: process entry point

Domain and application packages must not import inbound or outbound adapters.
`internal/architecture` tests enforce this rule before product code arrives.
The entry point calls `os.Exit` once; parsing, output, and exit statuses are
injected and tested without spawning a process.

## Quality policy

`mise run coverage` enforces at least 85% statement coverage for packages with executable Go statements. The sole
`main` process-exit call is intentionally exercised by the black-box smoke test;
all injectable bootstrap logic participates in Go coverage. Generated code is
not checked in and no bootstrap behavior is excluded. `scripts/smoke.sh` provides the clean-temporary-directory
black-box binary check used by `mise run smoke`.

## Installation and operations

See [the installation guide](docs/setup.md) for the recommended systemd setup,
DNS, upgrades, and verification. See [the operator runbook](docs/runbook.md) for
startup, recovery, shutdown, release checksum, and incident procedures.

## Contributing

Run `mise run fmt` before committing, then `mise run check`. Tests must not
require DNS, privileged ports, Caddy, or a database service. Keep dependencies
pointing inward: adapters may depend on domain/application ports, while
composition is the only wiring layer.

## External advertised URLs

Keep Mirage's managed Caddy listener on `public_address: ":9955"` even when a TLS terminator publishes it elsewhere. Configure the advertised endpoint separately (example values are deployment-specific):

```yaml
base_host: temp.lab.ollem.io
external_scheme: https
external_port: 443
dashboard_ssl: true # only when a trusted TLS terminator fronts the private dashboard
```

`external_scheme` is `http` or `https` (case-insensitive); with it set, zero/unset `external_port` selects 80 or 443. Without it Mirage retains legacy URL inference from `public_address`. The dashboard login posts its token in the form body, never the query string. A Zero Trust gateway (such as Pangolin) can still issue its own 401 before Mirage sees the request; that is distinct from Mirage's login landing page.


## Installation admin token

Optional installation-wide administration is enabled with:

```yaml
admin:
  token_hash_file: /etc/mirage/admin-token.sha256
```

Create credentials offline (both paths are deliberately required):
`mirage admin init --token-file PATH --hash-file PATH`. The token file is 0600 and hash file is 0640; creation is exclusive and raw credentials are never printed. Use `--admin-token`, `MIRAGE_ADMIN_TOKEN`, or `./.mirage_admin_token` for administrative API/CLI operations. Without `admin.token_hash_file`, Mirage retains legacy unauthenticated global space administration for backwards compatibility; operators should configure it in production.
