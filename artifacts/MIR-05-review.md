# MIR-05 adapter artifact

Run with `mise run process-artifact`.

The deterministic fixture suite exercises: a shell child receiving `PORT`, a `{port}` command and loopback execution folder; GET/HEAD/POST health success and grace expiry; direct and redirect public-host rejection (including injected HTTP client); held loopback reservation/release and collision-safe allocation handoff; natural exit removal, TERM and forced KILL of signal-ignoring process groups; stdout/stderr timestamps and labels; secret redaction; 10 MiB complete-record retention; follow close/cancellation. Run `mise run test-race` for concurrent ring/follower checks.
