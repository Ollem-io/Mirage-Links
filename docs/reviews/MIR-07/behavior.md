# MIR-07 Black-Box Behavior Review

## Verdict: APPROVE

Reviewed only the rebuilt compiled artifact `/tmp/mirage-MIR-07-artifact/httpapi.test`; no source or worktree was inspected. Artifact SHA-256: `b4de078635d19aa45d25fc82f6ab484c813bed5c5b66ef7e9b359024840e0787`.

## Evidence

- Full verbose suite, 100 repetitions: `./httpapi.test -test.v -test.count=100`
  - Exit 0; 1,401 PASS reports; 0 FAIL reports.
- Concurrent full-suite stress: 8 independent processes, each `-test.v -test.count=50`
  - 8/8 exited 0; 0 failures.
- Concurrent targeted stress, 8 processes × 100 repetitions each:
  - Drain/shutdown (`TestDrainAndReadiness|TestDecodeAndDrainEdges|TestServerShutdown|TestShutdownCanceled`): 8/8 pass, 0 failures, 3,201 PASS reports total.
  - Stream (`TestSpaceAndStreamEdges|TestFollowReadError`): 8/8 pass, 0 failures, 1,601 PASS reports total.
  - Public listener isolation (`TestServersListenerIsolation`): 8/8 pass, 0 failures, 801 PASS reports total.

The prior attempt showed timing-sensitive `expected canceled drain` failures under concurrent load. Those failures were not reproduced against this rebuilt artifact, including focused concurrent drain/stream/public-isolation stress. The rebuilt artifact satisfies the requested black-box behavior checks; approve.
