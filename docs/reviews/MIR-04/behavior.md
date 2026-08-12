# MIR-04 Black-box Behavior Review

## Verdict: APPROVE (Attempt 4)

Black-box review used only `/tmp/mirage-MIR-04-artifact`; no source/worktree inspection was performed.

## Evidence after follow-up fix

- Fresh full `adapter.test -test.v` run exited 0. All available acceptance tests passed, including route namespace/ownership, idempotent add/remove, reconciliation repair and orphan deletion, third-party byte preservation, rollback matrix, transport/error handling, and managed/external child lifecycle. The optional `TestRealCaddyAdminHarness` remains explicitly skipped in the standalone binary.
- The previously flaky cancellation/deadline path was stress-tested in three independent fresh directories with `-test.run '^TestReconcileCompensatesAfterCallerCancellationOrDeadline$' -test.count=100 -test.shuffle=on -test.v`; all three exited 0 (600 cancellation/deadline subtest executions total, including cancel and deadline cases).
- A further shuffled full-suite run with `-test.count=3` exited 0.
- No failures, flakes, hangs, or unexpected stderr were observed after the follow-up rebuild.

## Assessment

The prior intermittent defect—returning `rollback_incomplete` instead of preserving the caller timeout/cancellation result and leaving route state differing from the snapshot—did not recur under 600 focused repetitions after the reported fix. Core namespaced route ownership, idempotency, reconciliation, compensation/rollback, error behavior, and process ownership now meet the supplied black-box acceptance surface.

**APPROVE** MIR-04.

## Limitation

The source-free artifact has no `mise` task definitions, so documented `mise run caddy-integration`, `mise run test-race`, and `mise run check` cannot be independently run here; the standalone real-Caddy test is explicitly skipped. This limits claims to the compiled harness behavior.
