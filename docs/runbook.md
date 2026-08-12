# Mirage operator runbook

## Start and verify

Install pinned tools with `mise install`, then run `mise run release` to make a
self-contained checked release candidate in `dist/`. Start it using a private,
loopback management address (the default):

```sh
./dist/mirage start --public 9955 --private 9956 --config ./mirage.yaml
curl -f http://127.0.0.1:9956/healthz
```

Readiness is deliberately unavailable until libSQL migration and startup
reconciliation have finished. The public address is Caddy-owned; it never
serves `/healthz`, `/dashboard`, `/`, or `/api/v1/`.

## Recovery and shutdown

After an unclean exit, restart the same command. Startup removes orphan Mirage
routes, expires records, terminates stale recorded process groups, and restores
only routes for active, alive, loopback-healthy links. It does not alter any
non-Mirage Caddy route. The worker repeats this reconciliation every minute.

Send SIGINT/SIGTERM for graceful shutdown. Mirage first drains private mutations,
stops the cleanup worker, removes owned routes, terminates owned link groups,
stops only its managed Caddy child, then closes HTTP and libSQL. For external
Caddy mode it never terminates Caddy. If shutdown exceeds its bounded timeout,
inspect the link command/process group and Caddy admin endpoint before retrying.

## Release verification

`mise run ci` runs formatting, vet, race, coverage, artifacts, real-Caddy
integration and hermetic release smoke. `dist/checksums.txt` is the SHA-256
manifest to retain alongside the binary. Never put bearer tokens in shell
history/logs; use `MIRAGE_TOKEN` or a mode-0600 exact `./.mirage_token`.

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
