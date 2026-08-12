# Mirage operator runbook

This runbook covers Mirage v0.1.0. For installation, wildcard DNS, systemd configuration, and direct or Zero Trust deployment, use the canonical [setup guide](setup.md).

## Start and verify

Mirage is normally managed by systemd:

```sh
sudo systemctl start mirage.service
sudo systemctl status mirage.service
curl -fsS http://127.0.0.1:9956/healthz
```

Readiness is unavailable until storage migration and startup reconciliation finish. In managed-Caddy mode, Caddy's public listener serves temporary links, while the private listener serves `/healthz`, `/dashboard`, and `/api/v1/`. Never expose the private listener directly to the Internet.

Follow service logs with:

```sh
sudo journalctl -u mirage.service -f
```

## Recovery and shutdown

After an unclean exit, restart the service. Mirage removes orphaned routes, expires records, terminates stale recorded process groups, and restores only active, alive, loopback-healthy links. It does not alter unrelated Caddy routes.

```sh
sudo systemctl restart mirage.service
curl -fsS http://127.0.0.1:9956/healthz
```

For graceful shutdown:

```sh
sudo systemctl stop mirage.service
```

Mirage stops accepting mutations, removes owned routes, terminates owned link groups, stops only the Caddy child it manages, and closes HTTP and storage. In external-Caddy mode, it never stops Caddy.

## Release verification

For a source release candidate:

```sh
VERSION=v0.1.0 mise run release
(cd dist && sha256sum -c checksums.txt)
./dist/mirage-v0.1.0-linux-amd64 version
```

The full repository validation is:

```sh
mise run ci
```

Keep the prior installed binary until the replacement passes `GET /healthz`. Follow the upgrade and rollback procedure in the [setup guide](setup.md#7-upgrade-or-roll-back).

## Connector checks

When a Zero Trust connector is used:

1. Confirm `GET http://127.0.0.1:9956/healthz` succeeds locally.
2. Confirm the connector can reach the configured public origin.
3. Verify wildcard DNS resolves through the provider.
4. Distinguish a provider-generated 401/403 from a Mirage response.
5. Confirm the management application has a separate identity policy if it is remotely accessible.

Do not weaken Mirage token authorization because a Zero Trust gateway is present.

## Credential incidents

Never place bearer tokens in shell history, service logs, tickets, or source control. Use `MIRAGE_TOKEN` or a mode-0600 `./.mirage_token` for space operations. For admin-token rotation and incident response, follow [admin-token.md](admin-token.md).
