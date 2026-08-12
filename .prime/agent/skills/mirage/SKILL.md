---
name: mirage
description: Operate the Mirage temporary environment manager using its CLI, dashboard, private API, spaces, links, tokens, logs, restarts, and cleanup lifecycle. Use when a user or coding agent needs to start Mirage, create temporary preview environments, expose a local service through Caddy, inspect logs, restart or delete links, manage spaces, troubleshoot readiness, or verify DNS and token configuration.
compatibility: Linux host with Mirage v1, Caddy 2, and access to the private management listener. systemd is recommended for server installation.
---

# Mirage

Mirage runs trusted local commands as temporary services and publishes healthy
ones through Caddy. The private listener is the management plane; the public
listener serves temporary links only.

## Before operating Mirage

1. Read `docs/usage.md` for the approved behavior and `docs/setup.md` for
   installation.
2. Confirm wildcard DNS points at the host:
   - `*.mirage.example.com` for `base_host: mirage.example.com`
   - `*-mirage.example.com` for `base_host: -mirage.example.com`
3. Confirm private readiness:

```sh
curl -f http://127.0.0.1:9956/healthz
```

4. Do not expose the private listener publicly. Never print or commit space
   tokens. Prefer `MIRAGE_TOKEN` or a mode-0600 `./.mirage_token` ignored by Git.

## Start the server

For production, follow the recommended systemd installation in
`docs/setup.md`. For a foreground development run:

```sh
mirage start --public 9955 --private 9956 --config /etc/mirage/config.yaml
```

Mirage starts managed Caddy by default, migrates libSQL, reconciles state, and
only then becomes ready. SIGINT/SIGTERM performs a graceful shutdown.

## Create and use a space

Create a six-hour space and capture its one-time token:

```sh
mirage space create --json > /tmp/mirage-space.json
export MIRAGE_TOKEN="$(jq -r .token /tmp/mirage-space.json)"
```

For a shorter space:

```sh
mirage space create --ttl 45m --json
```

List spaces without exposing credentials:

```sh
mirage space list
mirage space list calm-fox --json
```

## Publish a temporary link

The command must listen on `$PORT` or `{port}`. Health URLs must target
loopback. Mirage does not create a public route until health succeeds.

```sh
mirage link create \
  --name api \
  --command 'go run ./cmd/api --port {port}' \
  --execution-folder "$PWD" \
  --health-check 'GET http://127.0.0.1:{port}/healthz' \
  --grace 30s \
  --ttl 2h \
  --restarts
```

For an application that reads `PORT` directly:

```sh
mirage link create \
  --command 'npm run dev' \
  --execution-folder ./web \
  --health-check 'GET http://localhost:{port}/' \
  --ttl 30m
```

Use only trusted commands: Mirage executes link commands through the platform
shell in the selected folder.

## Inspect and manage links

```sh
mirage link list
mirage link logs api --tail 100
mirage link logs api --follow
mirage link restart api
mirage link delete api
```

A manual restart preserves the original expiry. Automatic restarts use bounded
backoff and never continue past TTL.

## Delete a space

Normal deletion uses its token and removes all links:

```sh
mirage space delete calm-fox
```

Only use the private administrative force path when the token is unavailable.
Always provide a useful audit reason:

```sh
mirage space delete calm-fox --force 'lost token; ticket OPS-42'
```

## Token and server resolution

Token precedence:

1. `--token`
2. `MIRAGE_TOKEN`
3. exact `./.mirage_token`

Mirage never searches parent directories for `.mirage_token`.

Server precedence:

1. `--server`
2. `MIRAGE_SERVER`
3. `http://127.0.0.1:9956`

## Dashboard and API

Open the private dashboard:

```text
http://127.0.0.1:9956/dashboard
```

Useful API calls:

```sh
curl http://127.0.0.1:9956/api/v1/spaces
curl -H "Authorization: Bearer $MIRAGE_TOKEN" \
  http://127.0.0.1:9956/api/v1/links
```

Never expect `/healthz`, `/dashboard`, `/`, or `/api/v1/` on the public
listener.

## Troubleshooting

- Not ready: inspect the systemd journal and Caddy Admin endpoint, then verify
  the configured data directory is writable.
- Link never appears publicly: check `mirage link logs <name>`, confirm the
  process listens on `$PORT`, and reproduce the loopback health request.
- Public hostname does not resolve: correct wildcard DNS or external port
  forwarding; Mirage does not provision DNS.
- Token rejected: verify token precedence and that `.mirage_token` is in the
  current directory with mode 0600.
- After a crash: restart Mirage with the same config/data path. Startup and the
  minute worker reconcile expired records, processes, and Mirage-owned routes.
- Do not edit or delete unrelated Caddy routes; Mirage owns only IDs prefixed
  for its namespace.

## Completion checks for agents

After creating a link, report:

- space alias (never its token),
- link name and public URL,
- health/status and expiry,
- whether automatic restart is enabled.

Before leaving a task, delete the link or space unless the user explicitly
asked to keep it until TTL.

## External advertised URLs

Keep Mirage's managed Caddy listener on `public_address: ":9955"` even when a TLS terminator publishes it elsewhere. Configure the advertised endpoint separately (example values are deployment-specific):

```yaml
base_host: temp.lab.ollem.io
external_scheme: https
external_port: 443
dashboard_ssl: true # only when a trusted TLS terminator fronts the private dashboard
```

`external_scheme` is `http` or `https` (case-insensitive); with it set, zero/unset `external_port` selects 80 or 443. Without it Mirage retains legacy URL inference from `public_address`. The dashboard login posts its token in the form body, never the query string. A Zero Trust gateway (such as Pangolin) can still issue its own 401 before Mirage sees the request; that is distinct from Mirage's login landing page.



## Installation-wide admin tokens

Initialize on the Mirage host using explicit, new secure paths:

```sh
mirage admin init \
  --token-file /secure/mirage/admin.token \
  --hash-file /etc/mirage/admin-token.sha256
```

Put only the hash path in configuration:

```yaml
admin:
  token_hash_file: /etc/mirage/admin-token.sha256
```

The operator retains the mode-0600 raw token file; the service account needs
read access only to the mode-0640 hash. Initialization is exclusive and rolls
back the raw token if hash creation fails. Never print, log, paste into JSON, or
commit the raw token. Retrieve it only from its protected token file; it cannot
be recovered from the hash. Use `--admin-token`, `MIRAGE_ADMIN_TOKEN`, or a
mode-0600 `./.mirage_admin_token`.

To rotate, create a new pair under new filenames, point the config at the new
hash, restart Mirage, verify the new token, then securely remove the old pair.
Keep the old pair until verification so rollback is possible. Mirage rejects
malformed, symlinked, non-regular, or writable hash files at startup, and admin
service methods fail closed when no valid hash is loaded. Dashboard admin
sessions last one hour; rotating and restarting invalidates old-token sessions.
