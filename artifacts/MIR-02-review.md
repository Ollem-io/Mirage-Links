# MIR-02 domain contract artifact

Run `mise run test-domain`. This black-box-facing contract suite invokes only exported domain constructors/parsers and serializes a `domain.Space` golden-shaped JSON value. Review its output with `go test -v ./internal/domain`; it verifies the snapshot has no plaintext token or token-hash field, alongside hostname, duration, health URL, token, and lifecycle contracts.

Run `mise run check` for format, architecture, test, coverage, build, and smoke gates.
