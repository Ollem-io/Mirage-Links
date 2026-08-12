# Mirage

Mirage is a single-node manager for temporary local application environments.
This repository currently contains the **MIR-01 bootstrap only**: a pinned Go
module, build tooling, and a deliberately small executable skeleton. Space,
link, HTTP, storage, Caddy, and process behavior are implemented in later
milestones.

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

## Operations
See [the operator runbook](docs/runbook.md) for startup, recovery, shutdown, release checksum and incident procedures.

## Contributing

Run `mise run fmt` before committing, then `mise run check`. Tests must not
require DNS, privileged ports, Caddy, or a database service. Keep dependencies
pointing inward: adapters may depend on domain/application ports, while
composition is the only wiring layer.
