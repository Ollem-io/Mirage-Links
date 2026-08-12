# Mirage Usage (approved v1 contract)

This document is the user-approved v1 behavior. Commands use the `mirage` binary.

> Approved by the product owner on 2026-08-12.

## 1. Install and configure

The project pins Go 1.26, Caddy, Node/Tailwind tooling, and development commands through `mise.toml`.

Mirage needs a wildcard DNS record pointing to the machine running its public port. For base host `mirage.example.com`, configure `*.mirage.example.com`. If `--public` is not 80/443, an upstream load balancer/NAT must forward public traffic to it.

Example A — subdomain pattern:

```text
DNS: *.mirage.example.com -> 203.0.113.10
base_host: mirage.example.com
result: api-calm-fox.mirage.example.com
```

Example B — suffix pattern (base starts with `-`):

```text
DNS: *-mirage.example.com -> 203.0.113.10
base_host: -mirage.example.com
result: api-calm-fox-mirage.example.com
```

Proposed config at `~/.config/mirage/config.yaml`:

```yaml
base_host: mirage.example.com
public_address: ":9955"
private_address: "127.0.0.1:9956"
data_path: ~/.local/share/mirage/mirage.db
caddy:
  admin_url: http://127.0.0.1:2019
  binary: caddy
  managed: true
```

A CLI can address another private server:

```sh
mirage --server http://127.0.0.1:9956 space list
MIRAGE_SERVER=http://10.0.0.8:9956 mirage space list
```

## 2. Start the server

Defaults are public port 9955, private port 9956. Management binds to loopback unless config explicitly changes the address.

Example A:

```sh
mirage start --public 9955 --private 9956
```

Example B:

```sh
mirage start --config ./mirage.yaml --public 8080 --private 8081
```

CLI flags override config. Startup validates DNS-independent configuration, opens libSQL, starts or connects to Caddy, reconciles state, starts cleanup, then reports readiness. `GET http://127.0.0.1:9956/healthz` is unauthenticated and returns process readiness only. `/`, `/dashboard`, and `/api/v1/` are never registered on the public listener.

## 3. Spaces

### Create

Default TTL is 6h; accepted range is 1m–12h. The token is printed once.

Example A:

```sh
$ mirage space create
Alias: calm-fox
Token: mir_6z...Pq
Expires: 2026-08-12T10:00:00Z
```

Example B:

```sh
mirage space create --ttl 45m --json > space.json
jq -r .token space.json > .mirage_token
printf '
.mirage_token
' >> .gitignore
chmod 600 .mirage_token
```

### List or inspect

Example A — all active spaces:

```sh
mirage space list
```

Example B — one alias:

```sh
mirage space list calm-fox --json
```

The private server is trusted-local in v1: listing spaces does not reveal tokens or token hashes.

### Delete

Normal deletion requires the space bearer token. `--force` is an administrative bypass and requires a non-empty audit reason; it is accepted only via the private management interface.

Example A:

```sh
mirage space delete calm-fox --token "$TOKEN"
```

Example B:

```sh
mirage space delete calm-fox --force "owner lost token; ticket OPS-42"
```

Deleting a space removes its routes, terminates its link process groups, then removes/archives its records.

## 4. Token resolution

For link and authenticated space operations, precedence is:

1. `--token VALUE`
2. `MIRAGE_TOKEN`
3. exact file `./.mirage_token`

Whitespace around a token file is removed. Mirage does not search parent directories in v1. It warns when the file is group/world-readable and never prints the token in logs or process arguments it creates.

Example A:

```sh
MIRAGE_TOKEN="mir_..." mirage link list
```

Example B:

```sh
printf '%s
' 'mir_...' > .mirage_token
chmod 600 .mirage_token
mirage link list
```

## 5. Create links

Required: token, command, execution folder, and health check. Defaults: grace 15s, TTL 6h, random name, no automatic restarts. TTL range is 1m–12h and grace range is 1s–15m.

Mirage reserves a loopback port, exports it as `PORT`, and replaces literal `{port}` in the command and health-check URL. The command is trusted local input and is executed through the platform shell from the execution folder. A health check has the form `METHOD URL`; supported methods are GET, HEAD, and POST, and URL host must be loopback.

Example A — named Go service:

```sh
mirage link create   --token "$TOKEN"   --name api   --command 'go run ./cmd/api --port {port}'   --execution-folder "$PWD"   --health-check 'GET http://127.0.0.1:{port}/healthz'   --grace 30s --ttl 2h --restarts
# URL: http://api-calm-fox.mirage.example.com:9955 (unless forwarded from 80)
```

Example B — random name, environment-based port:

```sh
MIRAGE_TOKEN="$TOKEN" mirage link create   --command 'npm run dev'   --execution-folder ./web   --health-check 'GET http://localhost:{port}/'   --grace 2m --ttl 30m
```

Creation waits until the service becomes healthy or grace expires. The Caddy route is not added before health succeeds. On failure, Mirage terminates the process and returns a non-zero exit with recent startup logs.

Names are lowercase DNS labels (`[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?`) and unique within a space.

## 6. List links

Example A:

```sh
mirage link list --token "$TOKEN"
```

Example B:

```sh
mirage link list --json | jq '.links[] | {name,url,status,expires_at}'
```

Only links belonging to the token's space are returned.

## 7. Logs

Logs combine stdout and stderr, carry timestamps/stream labels, and are held in a bounded local ring (proposed: latest 10 MiB per link). Tokens are redacted on best effort.

Example A — recent output:

```sh
mirage link logs api --token "$TOKEN" --tail 100
```

Example B — follow:

```sh
mirage link logs web --follow
```

`--follow` ends when the process terminates or the client disconnects.

## 8. Restart links

A manual restart is allowed regardless of whether automatic `--restarts` was set. It removes the route, stops the process group, starts it again, waits for health, and restores the route without extending TTL.

Example A:

```sh
mirage link restart api --token "$TOKEN"
```

Example B:

```sh
MIRAGE_TOKEN="$TOKEN" mirage link restart web --json
```

With automatic restarts, unexpected exit or sustained failed health checks trigger exponential backoff (1s to 1m); TTL always wins and stops retries.

## 9. Delete links

Example A:

```sh
mirage link delete api --token "$TOKEN"
```

Example B:

```sh
MIRAGE_TOKEN="$TOKEN" mirage link delete web --json
```

Deletion is idempotent for an already deleted link, but an unknown name returns not-found. Route removal happens before process termination.

## 10. Dashboard and API

Open `http://127.0.0.1:9956/dashboard`. The HTMX/Tailwind dashboard lists spaces and links, statuses, expiry, URLs, recent logs, and provides restart/delete actions. Destructive administrative actions require typing a reason. In v1 it relies on loopback/private-network isolation rather than browser login, and never displays bearer tokens.

Example API calls:

```sh
curl http://127.0.0.1:9956/api/v1/spaces
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:9956/api/v1/links
```

Example mutations:

```sh
curl -X POST http://127.0.0.1:9956/api/v1/spaces   -H 'Content-Type: application/json' -d '{"ttl":"45m"}'

curl -X DELETE http://127.0.0.1:9956/api/v1/links/api   -H "Authorization: Bearer $TOKEN"
```

API errors use JSON with `code`, `message`, and optional `details`; validation is 400, missing/invalid token 401, cross-space access 404, conflict 409, and internal failure 500.

## 11. Cleanup and recovery

Cleanup runs once at startup and every minute. It expires TTLs, removes Mirage-owned Caddy routes without live records, stops recorded processes that should no longer run, and repairs missing routes only for healthy active links. It never modifies unrelated Caddy configuration.

Example A: after a server crash, restarting `mirage start` reconciles database and Caddy before accepting creates.

Example B: if Caddy was manually edited and a Mirage route disappeared, the next reconciliation restores it only if its link process is alive and healthy.

## Approved decisions

The product owner approved these choices:

1. Host grammar: `base_host: mirage.example.com` means `<link>-<space>.mirage.example.com`; a leading dash means `<link>-<space>-mirage.example.com`.
2. Public URLs include the configured public port unless external forwarding supplies 80/443; Mirage itself does not manage TLS/DNS in v1.
3. Mirage starts a child Caddy by default, but can use an external Caddy admin API; it owns only a namespaced route subtree.
4. Link commands use the platform shell, receive `PORT`, and support `{port}` interpolation.
5. Token lookup checks only `./.mirage_token`, not parents.
6. Private management is unauthenticated except bearer-token link/space actions; dashboard administrative force actions require an audit reason.
7. `--restarts` retries unexpected exits and later health failures with bounded backoff; manual restart never extends TTL.
8. Logs are bounded to 10 MiB per link and are not preserved indefinitely after deletion.
9. Space/link TTL ranges are both 1m–12h; grace range is 1s–15m.
10. The CLI uses `mirage link ...` (not a nested `space link ...`) and the token identifies the space.

## External advertised URLs

Keep Mirage's managed Caddy listener on `public_address: ":9955"` even when a TLS terminator publishes it elsewhere. Configure the advertised endpoint separately (example values are deployment-specific):

```yaml
base_host: temp.lab.ollem.io
external_scheme: https
external_port: 443
dashboard_ssl: true # only when a trusted TLS terminator fronts the private dashboard
```

`external_scheme` is `http` or `https` (case-insensitive); with it set, zero/unset `external_port` selects 80 or 443. Without it Mirage retains legacy URL inference from `public_address`. The dashboard login posts its token in the form body, never the query string. A Zero Trust gateway (such as Pangolin) can still issue its own 401 before Mirage sees the request; that is distinct from Mirage's login landing page.



## Installation administration

Configure `admin.token_hash_file` and follow [admin-token.md](admin-token.md). Admin credentials operate across spaces; ordinary space tokens remain one-space scoped.
