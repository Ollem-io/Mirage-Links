# MIR-05 Black-box Behavior Review

**Reviewer:** Ollem (GPT-5.6 Luna)  
**Artifact:** `/tmp/mirage-MIR-05-artifact`  
**Decision: APPROVE**

## Scope
Only the self-contained source-free artifact was inspected. The included `service` fixture binary was executed indirectly by the compiled process suite via `MIRAGE_TEST_SERVICE=/tmp/mirage-MIR-05-artifact/service`; no source/worktree was used.

## Evidence

With the fixture environment set, all three binaries were run verbose and exited 0 with final `PASS`:

- `process.test`: all 16 tests passed, including natural-exit identity/allocated handoff, allocated collision retry, never-bind cleanup of every attempt, competitor-listener ownership protection, listener ownership helpers, process-group shutdown, and cancellation/grace paths.
- `health.test`: all method/status, loopback, redirect containment, injected-client, timeout, malformed, and cancellation tests passed.
- `logs.test`: redaction/labels/close, follow cancellation/process close, bounded newest-complete ring retention, and canceled-input tests passed.

Full suites were additionally repeated three times each with `-test.v -test.count=2`; all 9 invocations exited 0. The adversarial collision test `TestStartAllocatedCollisionRetry` was run as 100 independent targeted invocations: **100/100 passed**. The natural-exit, never-bind cleanup, and competitor-listener ownership tests were each targeted with `-test.count=20`; each exited 0 without failures. No fixture processes remained afterward (`pgrep` empty), and no listener owned by the fixture remained in `ss -ltnp`.

## Final disposition
**APPROVE.** The self-contained artifact now provides and passes the requested adversarial collision, never-bind, subprocess cleanup, port ownership, health, and bounded log-follow behavior under repeated black-box execution.
