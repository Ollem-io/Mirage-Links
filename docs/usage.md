# Using Mirage

Mirage lets a user or automation agent start a temporary server process and publish it only after a loopback health check succeeds. The process receives an assigned `PORT`; space and link TTLs provide automatic cleanup.

Complete the [installation and deployment guide](setup.md) before using these commands. The private management API must remain private, and wildcard DNS must reach the configured public ingress.

## Safe agent workflow

Run the CLI on the Mirage host or set `MIRAGE_SERVER` to an authorized, identity-protected management endpoint. Do not place tokens in prompts, repositories, tickets, command arguments visible to other users, or logs.

The examples use `jq` to read JSON safely:

```sh
(umask 077; mirage space create --ttl 1h --json > /tmp/mirage-space.json)
export MIRAGE_TOKEN="$(jq -r .token /tmp/mirage-space.json)"

mirage link create   --name preview   --command 'npm run dev'   --execution-folder "$PWD"   --health-check 'GET http://127.0.0.1:{port}/'   --grace 2m   --ttl 30m

mirage link list --json
mirage link logs preview --tail 100
mirage link restart preview
mirage link delete preview
mirage space delete "$(jq -r .alias /tmp/mirage-space.json)"

unset MIRAGE_TOKEN
rm -f /tmp/mirage-space.json
```

The `create` response contains the temporary URL. With `base_host: mydomain.com`, a link named `preview` is normally published as `preview-<space>.mydomain.com`.

## CLI server and token resolution

The CLI talks to `http://127.0.0.1:9956` by default. Select another protected endpoint with `--server` or `MIRAGE_SERVER`:

```sh
mirage --server http://127.0.0.1:9956 space list
export MIRAGE_SERVER=https://mirage-admin.mydomain.com
mirage space list
```

For space-scoped operations, the token is resolved in this order:

1. `--token VALUE`
2. `MIRAGE_TOKEN`
3. `./.mirage_token` in the exact current directory

For a local token file:

```sh
(umask 077; printf '%s
' "$MIRAGE_TOKEN" > .mirage_token)
printf '.mirage_token
' >> .gitignore
```

The CLI warns if the file is group- or world-readable. A space token can manage only links in its own space. `--json` provides machine-readable results.

Mirage executes link commands as the Mirage service user. Only submit trusted commands.

## Spaces

A space is a token-scoped collection of links. The default TTL is six hours; supported TTLs range from one minute to twelve hours.

```sh
mirage space create --alias demo --ttl 45m
mirage space list
mirage space list demo --json
mirage space delete demo
```

A new space token is returned once and is stored only as a hash by Mirage. Deleting a space removes its routes and stops its link process groups.

Installation administrators can force-delete a space only with a non-empty audit reason:

```sh
mirage space delete demo --force 'ticket OPS-42: owner lost token'
```

See [Administration tokens](admin-token.md) before using global operations.

## Links

Creating a link requires a command, execution folder, and loopback health check. Mirage reserves a port, exports it as `PORT`, and replaces literal `{port}` placeholders in the command and health-check URL.

```sh
mirage link create   --name api   --command 'go run ./cmd/api --port {port}'   --execution-folder "$PWD"   --health-check 'GET http://127.0.0.1:{port}/healthz'   --grace 30s   --ttl 2h   --restarts
```

Health-check methods may be `GET`, `HEAD`, or `POST`, and the URL must target loopback. A route is not published until the process is healthy. If startup exceeds the grace period, Mirage stops the process and returns recent startup context.

`--restarts` enables automatic recovery after an unexpected exit or an unhealthy state. The TTL always wins and stops further restarts.

Common operations are:

```sh
mirage link list
mirage link logs api --tail 100
mirage link logs api --follow
mirage link restart api
mirage link delete api
```

Restarting removes the route, restarts and health-checks the process, and restores the route without extending its TTL. Deletion removes the route before stopping the process.

## Dashboard and API

The private dashboard is available at:

```text
http://127.0.0.1:9956/dashboard
```

It uses a Mirage token login and a short-lived session. Do not treat the dashboard as safe for public exposure merely because it has a login. If remote access is required, publish it only through a separate identity-protected Zero Trust application as described in the [setup guide](setup.md#zero-trust-deployment-recommended).

The private API is under `/api/v1/`. Supply a space token as a bearer credential:

```sh
curl -H "Authorization: Bearer $MIRAGE_TOKEN"   http://127.0.0.1:9956/api/v1/links
```

Installation-wide administration is optional and requires `admin.token_hash_file`. With an admin credential, an operator can work across spaces:

```sh
export MIRAGE_ADMIN_TOKEN="$(sudo cat /secure/mirage/admin.token)"
mirage admin links list demo
mirage admin links logs demo api --tail 100
mirage admin links restart demo api --reason 'ticket OPS-42'
unset MIRAGE_ADMIN_TOKEN
```

Never put an admin token in a URL, repository, log, or committed configuration. See the [admin-token runbook](admin-token.md) for initialization, rotation, and incident response.

## Health, expiry, and recovery

Check private service readiness with:

```sh
curl -fsS http://127.0.0.1:9956/healthz
```

The public ingress does not serve management routes. On startup and during periodic reconciliation, Mirage expires TTLs, removes stale Mirage routes, stops stale recorded process groups, and restores only active, healthy links.

See the [operator runbook](runbook.md) for recovery and shutdown procedures.
