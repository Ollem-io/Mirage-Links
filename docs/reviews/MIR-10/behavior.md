# MIR-10 Black-box behavior review (Attempt 2)

## Decision: APPROVE

Reviewed only `/tmp/mirage-MIR-10-artifact` as a black box; no source/worktree access.

## Evidence

- **Checksum PASS:** `sha256sum -c checksums.txt` reports `mirage: OK` (manifest hash `61eaf1d9...8969e748`).
- **Release version PASS:** `./mirage --version` and `./mirage version` both report `mirage v1.0.0`.
- **Source-free clean-directory PASS:** copying only `mirage` into a new empty directory, `--version`, `--help`, and `start --help` each return 0. Binary is stripped ELF; no source/worktree paths observed in prior strings scan.
- **CLI shape PASS:** top-level and start help expose expected commands and flags; unknown-command behavior is bounded and concise.
- **Invalid/negative start validation PASS:** with a fake Caddy placed first in `PATH`, repeated probes (3 repeats) returned code 1 immediately without invoking Caddy:
  - `--private -1 --public 9955` -> `mirage: invalid private bind address ":-1"`
  - `--private 0 --public 0` -> `mirage: invalid public bind address ":0"`
  - `--private abc --public def` -> `mirage: invalid public bind address ":def"`
  No hangs/timeouts and fake-Caddy argument log remained absent.
- **Config failure hygiene PASS:** `--config /no/such` returns code 1 with clear bounded error `mirage: config "/no/such": open /no/such: no such file or directory`; Caddy is not invoked. Repeated successfully.
- **Secret hygiene PASS:** errors observed contain no bearer token or unrelated environment secret. No secret was written to report/artifact during testing.
- **Runbook usability:** RUNBOOK.md gives actionable start, readiness, recovery/shutdown, release verification, checksum retention, and token handling instructions. Versioned artifact now aligns with release expectations.

## Reproduction

```sh
cd /tmp/mirage-MIR-10-artifact
sha256sum -c checksums.txt
./mirage --version
./mirage start --help
./mirage start --private -1 --public 9955
./mirage start --private 0 --public 0
./mirage start --private abc --public def
./mirage start --private 9956 --public 9955 --config /no/such
```

For validation isolation, a temporary fake `caddy` executable was first in `PATH`; it logged invocation and stayed alive. Every malformed/config-failure probe exited promptly and the invocation log remained absent.

## Release recommendation

Approve MIR-10 attempt 2. Checksum, versioning, source-free operation, CLI behavior, input validation, config errors, and clean-directory repeatability all meet the reviewed black-box criteria.
