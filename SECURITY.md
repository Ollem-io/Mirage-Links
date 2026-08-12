# Security policy

## Supported versions

Security fixes are provided for the current `0.1.x` release line. Other versions are unsupported.

## Reporting a vulnerability

Report suspected vulnerabilities privately through [GitHub Security Advisories](https://github.com/Ollem-io/Mirage-Links/security/advisories/new). Do not open a public issue for an unpatched vulnerability.

Include the affected version, a clear description, reproduction steps or proof of concept, impact, and any suggested mitigation. The maintainers will investigate and coordinate remediation and disclosure.

## Deployment boundaries

- The public ingress on port `9955` is for temporary application links.
- The private listener defaults to `127.0.0.1:9956` and serves `/healthz`, the dashboard, and `/api/v1/`. Never expose it directly to the Internet.
- Use a separate identity-protected Zero Trust route when remote management is necessary.
- Zero Trust authentication is an outer layer and does not replace Mirage space or admin authorization.
- Mirage starts trusted commands as its service user. Management access is equivalent to permission to execute commands with that account's privileges.
- Keep space and admin bearer tokens out of URLs, logs, prompts, tickets, and source control. Use protected files or environment variables and follow [administration-token guidance](docs/admin-token.md).

See the [setup guide](docs/setup.md) for network and service hardening.
