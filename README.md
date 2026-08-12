# Mirage

Mirage is a simple, single-host tool that lets people and automation agents create temporary links for preview environments. It works with any stack or application server that can listen on an assigned port.

Mirage starts a trusted process, assigns it a `PORT`, waits for its loopback health check to pass, and then publishes a temporary URL through Caddy. Each space and link has a TTL. When a link expires or is deleted, Mirage removes its route and stops its process.

Typical uses include pull-request previews, agent-built demos, temporary APIs, and short-lived development environments for Go, Node.js, Python, Ruby, or any other server stack.

## How it works

```text
User or agent
    |
    | mirage CLI (private management API)
    v
Mirage systemd service
    |-- starts the application with an assigned PORT
    |-- waits for a loopback health check
    |-- creates a Caddy route only after the application is healthy
    `-- removes the route and process when the TTL expires

*.mydomain.com --> host or Zero Trust gateway --> public listener :9955
                                                  |
                                                  `--> healthy preview process

Private management: 127.0.0.1:9956
                      |-- GET /healthz
                      |-- /dashboard
                      `-- /api/v1/
```

The normal deployment is:

1. Create wildcard DNS such as `*.mydomain.com` for the host or gateway.
2. Install Mirage and run it as a systemd service on the application host.
3. Start a server through `mirage link create`.
4. Use the temporary URL returned after the server becomes healthy.

## Quick start

Follow the [setup guide](docs/setup.md) first. It covers the binary, Caddy, wildcard DNS, configuration, and systemd service.

Verify the private listener:

```sh
curl -fsS http://127.0.0.1:9956/healthz
```

Create a space while protecting the one-time token response:

```sh
(umask 077; mirage space create --ttl 1h --json > /tmp/mirage-space.json)
export MIRAGE_TOKEN="$(jq -r .token /tmp/mirage-space.json)"
```

Start a preview server. This example uses Python, but the command can start any server that uses `$PORT` or an interpolated `{port}`:

```sh
mirage link create   --name preview   --command 'python3 -m http.server "$PORT" --bind 127.0.0.1'   --execution-folder "$PWD"   --health-check 'GET http://127.0.0.1:{port}/'   --grace 30s   --ttl 30m
```

Mirage prints the temporary URL after the health check succeeds. See [Using Mirage](docs/usage.md) for logs, restart, deletion, dashboard, API, and agent-oriented workflows.

## Recommended: add Zero Trust

For shared or Internet-connected hosts, place public links—and especially any remote management route—behind a Zero Trust gateway. Cloudflare Zero Trust and Pangolin are common choices. This adds a short provider-specific setup step and provides identity policies, access controls, and better control than directly exposing the host.

The general process is:

1. Create a tunnel or site in the provider and generate its connector installation command.
2. Install the connector on the Mirage host using the provider's systemd or Docker instructions.
3. Add a wildcard application or resource for `*.mydomain.com` that targets Mirage's public ingress on port `9955`.
4. If remote administration is required, add a separate identity-protected management application. Keep port `9956` private and use `GET /healthz` for readiness checks.

Docker in this workflow runs the **Zero Trust connector only**. Running Mirage itself in Docker is not supported in v0.1.0. A containerized connector cannot normally reach host loopback; use the host address and firewall rules recommended by the provider. Never publish port `9956` directly to the Internet.

See [Zero Trust deployment](docs/setup.md#zero-trust-deployment-recommended) for the managed-Caddy and networking details.

## Features

- Temporary, token-scoped spaces and links with bounded TTLs.
- Host-native processes for previews in any server stack.
- Health-gated Caddy routing: unhealthy processes are not published.
- Logs, restart, deletion, and optional automatic restarts.
- Embedded libSQL state and startup reconciliation after interruptions.
- Private CLI/API and a daisyUI dashboard.
- Optional installation-wide administration token.
- Reproducible Go and frontend toolchains managed through `mise`.

Mirage executes trusted commands as its service user. Access to the management API is equivalent to permission to start commands with that account's privileges.

## Documentation

- [Setup and deployment](docs/setup.md)
- [Using Mirage](docs/usage.md)
- [Operator runbook](docs/runbook.md)
- [Administration tokens](docs/admin-token.md)
- [Security policy](SECURITY.md)
- [Changelog](CHANGELOG.md)

## Development

The repository pins Go 1.26 and its other build tools through [`mise`](https://mise.jdx.dev/):

```sh
git clone https://github.com/Ollem-io/Mirage-Links.git
cd Mirage-Links
mise trust
mise install
mise run check
VERSION=v0.1.0 mise run release
```

The project uses hexagonal architecture and enforces at least 85% aggregate Go statement coverage.
