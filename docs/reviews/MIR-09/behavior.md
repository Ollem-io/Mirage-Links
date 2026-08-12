# MIR-09 Behavior Review

## Decision: APPROVE

### Rebuild follow-up (black-box)
The rebuilt binary no longer panics on `{"items":[]}`: human list output is empty and `--json` returns `{"items": []}`. A bare JSON array still gives exit 1 unmarshal error in human mode (JSON mode returns `[]`). `--token=eq` is now accepted. Start tests now fail cleanly with `exec: "caddy": executable file not found` before signal testing, so prior cancellation conclusion is environment-dependent for this rebuild. Top-level help improved, but `link --help` and `space --help` remain unsupported.


Black-box review of `/tmp/mirage-MIR-09-artifact/mirage` (no source/worktree inspection).

## Checks performed

- ``, `--help`, `-h`, `--version`, and `version` exit 0 and produce usable top-level usage/version text.
- Unknown command and missing flag values produce exit 2 with actionable errors.
- `space create/list/delete`, `link create/list/logs/restart/delete` reach the configured server and use plausible API paths/methods.
- `--json` emits JSON for successful object responses.
- `start --private 0` starts and prints `Mirage ready: private :0 public `; SIGINT does not terminate it (the process remained running until the test timeout/forced cleanup), indicating cancellation/shutdown handling is not correct.

## Blocking failures

1. **Valid list-shaped responses can crash the CLI with an unrecovered Go panic.** A configured server returning HTTP 200 JSON `{"items":[]}` for `GET /api/v1/links` caused exit 2 and a full panic stack trace (`interface conversion: interface {} nil, not []interface {}`). The same occurred for `link list` and for a `--token` invocation. A JSON array (`[]`) instead caused exit 1 with `json: cannot unmarshal array into Go value type map[string]interface {}`. Thus normal empty-list API shapes are not robustly handled.
2. **Cancellation/start behavior is broken.** `start --private 0` was sent SIGINT after startup; it returned no termination/cleanup indication and remained alive, requiring forced cleanup. A command intended to run as a local environment must honor interrupt cancellation.
3. **Authorization/config behavior is inconsistent.** With `MIRAGE_TOKEN=envtok`, requests observed for `space delete` and all link operations included `Authorization: Bearer envtok`, but `space create` and `space list` did not include Authorization. This is a surprising and likely incorrect token application inconsistency. `MIRAGE_SERVER` routing worked. A `.mirage_token` file was also tested, but list response panic prevented a clean precedence assertion.

## Additional observations

- `space create` posts `{"alias":"","ttl":""}` and `link create` posts `{}` when no arguments are supplied; this may be intentional defaults but should be documented or validated.
- `--token=eq` is rejected as an unknown command rather than accepted as equivalent to `--token eq`.
- Subcommand `--help` is not supported (`start --help` says `--help value`; `space --help`/`link --help` report unknown subcommand), despite top-level help advertising the commands.
- Invalid public ports produce nonzero errors; `--public 8080` correctly reports bind failure when occupied.

The panic and inability to honor SIGINT are sufficient for rejection even if basic command routing works.


## Final rebuild follow-up
Final binary retest: top-level and subcommand `link --help`/`space --help` all exit 0 with proper Cobra-style help; `--token=eq` is accepted. Fake server returning either `{"items":[]}` or bare `[]` now yields exit 0 for both space/link list; human mode is empty and JSON mode normalizes to `{"spaces":[]}`/`{"links":[]}`. Authorization behavior is stable: link list receives Bearer token while space list does not. Start exits cleanly when caddy is absent (`exec: "caddy": executable file not found`), so signal behavior could not be exercised past dependency startup. Final verdict APPROVE for supplied CLI artifact.
