# MIR-02 Independent Code Review — REJECT

**Reviewer:** Ollem GPT-5.6 Luna (high)  
**Commit:** `7e3c120` (`feat(domain): add MIR-02 domain contracts and ports`)  
**Scope:** MIR-02 plan and changed domain/ports code; source was not modified.

## Automated checks

- `mise exec -- go test ./...`: PASS.
- `mise exec -- go vet ./...`: PASS.
- `mise exec -- go test -cover ./...`: PASS as a command, but reports `cmd/mirage` 50%, domain 97.3%; the repository-level 85% requirement is not actually enforced by this invocation (and `mise run coverage` only checks CLI/composition).
- `mise exec -- go test -race ./...`: NOT RUN: toolchain reports `-race requires cgo; enable cgo by setting CGO_ENABLED=1`.

## Blocking findings

1. **Bearer token plaintext can be JSON serialized.** `internal/domain/token.go:13-14` declares `Token` as a string and claims it has no JSON representation, but provides no `MarshalJSON` (or equivalent). `encoding/json.Marshal(domain.Token("mir_secret"))` emits `"mir_secret"`. This violates the MIR-02 contract that plaintext tokens exist only in the one-time create result and creates an accidental secret-leak path in DTOs, logs, or persistence. The test at `internal/domain/domain_test.go:86-91` only marshals `Space`, so it misses the direct leak. Add an explicit safe serialization policy (and tests for all structures containing Token), while preserving an intentional one-time response mechanism outside persisted domain records.

2. **Health-check URL validation accepts malformed ports.** `internal/domain/network.go:68-76` validates scheme/host and loopback hostname but never validates `u.Port()` / the authority. Go URL parsing accepts authorities such as `http://localhost:bad/` (and malformed/out-of-range port forms can similarly pass); this then reaches adapters despite the required malformed-URL validation. Add strict authority/port validation (including numeric range 1–65535 if ports are allowed) and table tests.

3. **MIR-02 port contract is incomplete.** `internal/application/ports/ports.go` contains only a minimal Repository (`Find/Save` for space/link), TokenGenerator, Clock, Proxy, Process, Health, and LogStream. The approved MIR-02 scope explicitly calls for alias/ID/token/hash contracts, loopback-only port allocator, process supervisor, health checker, log sink/stream, Caddy/proxy, audit, and scheduler interfaces. No allocator, audit, scheduler, log sink, ID/alias/hash abstractions, or transaction/repository operations needed by lifecycle orchestration are declared. This leaves downstream MIR-03/MIR-06 implementations to invent boundary contracts and makes the claimed adapter-independence contract incomplete.

## Additional observations

- Lifecycle transitions in `internal/domain/lifecycle.go:22-38` have no `Healthy -> Failed` or `Active -> Failed` path. Whether failure is represented by stopping/restart must be explicitly documented and tested against the approved usage; as written, direct failure state cannot model common unexpected process/health failures.
- `ParseToken` (`internal/domain/token.go:27-35`) checks prefix/base64 but not the generated 32-byte payload, accepting arbitrary credentials such as `mir_A`; establish and test a canonical token length/encoding policy to avoid multiple unintended credential formats.
- The domain suite has good boundary coverage (reported 97.3%), but fuzz tests discard returned errors and do not assert invariants; they do not compensate for the missing serialization and malformed-authority cases.

## Verdict

**REJECT.** Tests and basic architecture checks pass, but the plaintext-token JSON leak is a security blocker; malformed health URL acceptance and incomplete approved ports are additional blockers. Do not advance MIR-02 until these are corrected and targeted regression tests pass (including race testing with cgo enabled).
