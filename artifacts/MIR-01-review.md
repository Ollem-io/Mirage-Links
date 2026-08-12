# MIR-01 bootstrap review artifact

## Commands

From a clean checkout with `mise` installed:

```sh
mise install
mise run fmt-check
mise run test
mise run coverage
mise run build
mise run smoke
# or all of the above gate:
mise run check
```

## Expected behavior

- Tool installation selects the repository-pinned Go 1.26 toolchain.
- Formatting, tests, coverage (at least 85%), and build exit zero.
- `bin/mirage --help` exits `0`, writes a `Usage:` section to stdout, and
  writes nothing to stderr.
- `bin/mirage version` exits `0` and writes `mirage dev` for a normal build.
  A linker-injected version is displayed instead when built with
  `-X github.com/primeintellect/mirage/internal/buildinfo.Version=...`.
- An unknown command exits `2`, reports the unknown command to stderr, and
  points to `mirage --help`.
- `mise run smoke` makes a fresh temporary directory, copies only the built
  executable into it, and verifies those three black-box cases while recording
  their stdout/stderr and exit codes for the duration of the check.

The bootstrap intentionally does not start a server or implement later
space/link behavior.
