# MIR-08 Black-box Behavior Review

**Reviewer:** Ollem Luna (independent, high scrutiny)  
**Decision:** **APPROVE**

## Scope and method

I did not inspect source code or the worktree. I treated `/tmp/mirage-MIR-08-artifact/dashboard.test` as the only interface and exercised the supplied black-box Go test binary. The test inventory included dashboard DOM/HTMX behavior, escaping, private/public isolation, mutation fragments/cookies, API conformance, error mapping, authorization/delete variants, readiness/drain/shutdown, listener isolation, and edge/error paths.

## Evidence

* Baseline verbose suite: all 17 discovered tests passed:
  `TestDashboardPrivateFragmentsAndEscaping`, `TestDashboardNeedsReasons`, `TestDashboardMutationFragmentsAndCookies`, `TestAPIConformance`, `TestErrorMappingAndIsolation`, `TestDrainAndReadiness`, `TestServersListenerIsolation`, `TestMoreHTTPBranches`, `TestServerShutdown`, `TestRemainingRoutes`, `TestHelperAndErrorBranches`, `TestAllErrorEndpointPaths`, `TestAuthorizationAndDeleteVariants`, `TestSpaceAndStreamEdges`, `TestDecodeAndDrainEdges`, `TestFollowReadError`, and `TestShutdownCanceled`.
* Repeated deterministic run: `-test.v -test.count=3 -test.parallel=8 -test.timeout=120s` completed with exit code 0; every repetition passed.
* Concurrent process stress: 12 independent processes (six simultaneously), each with `-test.shuffle=on -test.count=5 -test.parallel=16`; all 12 exited 0 with no failures.
* Higher repetition: `-test.v -test.count=50 -test.parallel=32` exited 0 (851 passing test cases, zero failures).
* Shuffled repetition: `-test.v -test.shuffle=on -test.count=30 -test.parallel=32` exited 0 (511 passing test cases, zero failures).

## Security/accessibility/isolation observations

The supplied dashboard-focused cases repeatedly passed checks covering HTML escaping and private fragments, reason-required behavior, mutation response fragments and cookies, and isolation/error behavior. No token leak, cross-public/private data exposure, malformed fragment behavior, or concurrency-dependent failure was observed through the provided interface. Listener isolation and lifecycle tests also remained stable under repeated/shuffled/concurrent execution.

## Limitations

This is a black-box review of the supplied executable and its included tests; it is not a source audit or a browser/manual accessibility audit. No additional browser runtime or external service contract was available in the artifact.

## Final disposition

**APPROVE** — no reproducible behavioral, security, DOM/HTMX, or public-isolation defect found in extensive repeated and concurrent execution.


## Attempt 2 rebuilt-artifact rerun

The rebuilt `dashboard.test` exposed four additional dashboard cases, including explicit CSRF, fragment/error/mutation, helper coverage, and mutation-error-fragment tests. The complete discovered inventory (21 tests) passed with:

* `-test.v -test.count=5 -test.parallel=16`: 105/105 passing, zero failures.
* `-test.v -test.shuffle=on -test.count=30 -test.parallel=32`: 630/630 passing, zero failures.
* `-test.v -test.count=50 -test.parallel=32`: 1,050/1,050 passing, zero failures.
* 16 concurrent independent processes, each shuffled/count 10/parallel 32: all exited 0, with no stderr or failures.

The added CSRF and fragment/error mutation coverage also passed consistently. Final verdict remains **APPROVE**.


## Sol rebuilt-artifact rerun

The Sol rebuild added real-listener and cross-space authorization coverage. The discovered inventory is now 23 tests, including `TestDashboardRunningListenerArtifact` and `TestDashboardRejectsCrossSpaceMutationsWithoutSideEffects`.

* Full `-test.v -test.count=10 -test.parallel=24`: 230/230 passed.
* Shuffled `-test.v -test.shuffle=on -test.count=30 -test.parallel=32`: 690/690 passed.
* Count 50, parallel 32: 1,150/1,150 passed.
* Concurrent stress: eight simultaneous verbose processes, each shuffled count 10 / parallel 32: each 230/230 passed.
* An earlier 16-process non-verbose run had one process exit anomalously without test output; immediate individual and 8-process verbose reruns reproduced no failure, and all targeted runs passed. This was not behaviorally reproducible.

Cross-space mutation rejection had no side effects, and real-listener dashboard behavior remained stable. Final verdict remains **APPROVE**.
