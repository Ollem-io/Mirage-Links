# MIR-02 Black-box Behavior Review (attempt 2 artifact)

## Verdict: APPROVE

Only the compiled `/tmp/mirage-MIR-02-artifact/domain.test` and the supplied
`TASK.md`/`USAGE.md` were inspected; no source or worktree was accessed.

## Evidence

`domain.test -test.list .` exposes 11 regular contract tests plus two fuzz
entrypoints, including the newly relevant strict health-authority and lifecycle
gating checks:

- `TestDurationBoundaries`
- `TestLabels`
- `TestHostAndURL`
- `TestHealth`
- `TestTransitionAndExpiry`
- `TestTokenDoesNotLeak`
- `TestErrorsAndIDs`
- `TestTokenParsingAndMarshaling`
- `TestBaseHostAndEntityExpiry`
- `TestHealthStrictAuthority`
- `TestFailureCanRestartButHealthyMustGateActive`
- `FuzzParseHealthCheck`, `FuzzParseLinkName`

A complete verbose run (`-test.v -test.count=1`) exited 0: every regular test
and both fuzz seeds passed. Every regular test was independently rerun three
times with an exact `-test.run` selector; all exited 0. Each fuzz seed suite
was also repeated ten times; all exited 0.

Thus the artifact's executable evidence covers approved duration and expiry
boundaries, identity/labels, DNS aliases and both base-host grammars with URL
interpolation, health URL parsing, strict loopback authority/port grammar,
lifecycle/restart and healthy-gating rules, canonical token parsing/marshaling,
one-time reveal/hash behavior and direct/nested token redaction. The explicit
`TestTokenDoesNotLeak` and token parsing/marshaling checks pass.

`mise run test-domain`, `mise run coverage`, and `mise run check` cannot be
invoked in this exported directory because it has no mise task definitions.
This is an artifact packaging limitation; direct execution of the exported
contract binary succeeds. No observable behavioral failure was found.

**APPROVE.**
