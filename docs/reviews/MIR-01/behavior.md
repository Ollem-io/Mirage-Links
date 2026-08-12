# MIR-01 Black-box Behavior Review

**Verdict: APPROVE**

## Scope and method

Reviewed only the supplied compiled executable (`mirage`) and the accompanying TASK/USAGE documentation in `/tmp/mirage-MIR-01-artifact`; no source tree or worktree was inspected. Each invocation ran from a newly-created temporary directory with stdout and stderr captured separately. The executable was run directly (no shell pipelines), and exit status and elapsed time were recorded.

## Reproducible evidence

Artifact: `/tmp/mirage-MIR-01-artifact/mirage` (executable, 2,430,634 bytes).

| Invocation | Exit | stdout | stderr |
|---|---:|---|---|
| `mirage --help` | 0 | Help text containing a `Usage:` section (includes `mirage [--help] [--version]`) | empty |
| `mirage version` | 0 | `mirage dev\n` | empty |
| `mirage wat` | 2 | empty | `mirage: unknown command "wat"\nRun 'mirage --help' for usage.\n` |

Observed elapsed times were approximately 5–17 ms per invocation, with no hangs or filesystem requirements. Additional smoke checks: no-argument invocation and `-h` both returned 0 with the same help output; `--version` returned 0 and `mirage dev` with empty stderr.

## Acceptance mapping

* `--help` exits 0, emits Usage on stdout, and emits nothing on stderr: **PASS**.
* `version` exits 0 and emits `mirage dev`: **PASS**.
* Unknown command exits 2, reports unknown command on stderr, and points to `mirage --help`: **PASS**.
* No later behavior is required by TASK.md: **satisfied**.

The documented MIR-01 bootstrap acceptance behavior is met.
