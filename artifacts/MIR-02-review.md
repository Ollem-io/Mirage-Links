# MIR-02 domain contract artifact

Run `mise run test-domain` (or `go test -v ./internal/domain` after `mise exec --`) to execute the exported domain contract suite. It validates:

- canonical 32-byte base64url bearer tokens, one-time `Reveal`, hashes, and direct **and nested** JSON redaction;
- DNS aliases/labels, both approved base-host grammars and public URL interpolation;
- TTL/grace and expiry boundaries, strict loopback health authority/port grammar, lifecycle/restart health gates.

Run `mise run coverage` to write `coverage.out` from a single complete-repository test invocation and enforce the aggregate 85% statement gate. The current aggregate result is 95.2%. `mise run check` additionally validates format, architecture, lint, build and bootstrap smoke behavior. No network, DNS, Caddy, libSQL, or privileged port is required.
