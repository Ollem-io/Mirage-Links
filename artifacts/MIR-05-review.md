# MIR-05 Verification Artifact

Run `mise run process-artifact` to compile and exercise the standalone fixture at
`internal/adapters/outbound/process/testdata/service`. Its modes cover healthy,
never-healthy, never-bind, crashing, forked-child, SIGTERM-ignoring, and noisy
services. The script checks real HTTP/port behavior, exit status, whole process
groups, escalation, stdout/stderr, and before/after process, port, goroutine and
file-descriptor ownership through the real adapter tests.

`StartAllocated` now treats allocation and readiness as one bounded operation:
it reserves a loopback candidate, releases immediately before shell exec, then
requires verified TCP binding. A bind collision, early exit, or never-bind grace
expiry stops the complete failed process group before allocating another port.
The adversarial allocator test binds the exact candidate synchronously in the
release window and proves a clean retry; the never-bind test proves every failed
attempt is terminated without ownership or goroutine leakage.

Coverage is enforced by `mise run coverage-mir05` and is part of `mise run check`:
health 90.0%, process 91.0%, logs 90.8%; repository aggregate 92.3%.

Verification:

- `mise run process-artifact`: PASS
- `mise exec -- go test -race ./...`: PASS
- `mise run check`: PASS
