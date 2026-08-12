# Admin-token runbook

Mirage uses one installation-wide admin bearer token for global space and link
operations. The service stores only a SHA-256 hash; the raw token remains with
the operator. Configure this credential for every production installation.

## Initial setup

Create both files offline on the Mirage host. Paths are explicit, must not
already exist, and are created without following symlinks:

```sh
sudo install -d -m 0700 /secure/mirage
sudo mirage admin init \
  --token-file /secure/mirage/admin.token \
  --hash-file /etc/mirage/admin-token.sha256
```

The raw token file is mode `0600`; the hash file is mode `0640`. `admin init`
never prints the token, never overwrites either path, and removes a newly
created token file if hash-file creation fails. Give the Mirage service account
read access to the **hash file only**. Add this to the server config:

```yaml
admin:
  token_hash_file: /etc/mirage/admin-token.sha256
```

Restart Mirage and confirm `/healthz` is ready. A missing, malformed,
symlinked, non-regular, or group/world-writable hash file prevents startup.
Administrative service methods fail closed when no hash is configured; legacy
unscoped compatibility endpoints remain separate and should not be exposed in
production.

## Use and retrieve the token

Read the raw token only from its protected operator file. It cannot be recovered
from the hash:

```sh
sudo cat /secure/mirage/admin.token
export MIRAGE_ADMIN_TOKEN="$(sudo cat /secure/mirage/admin.token)"
mirage space list
```

For a single invocation, prefer `--admin-token`; for local operator workflows,
`MIRAGE_ADMIN_TOKEN` or a mode-`0600` `./.mirage_admin_token` is supported.
Never place the raw token in configuration, command output, JSON, logs, tickets,
or source control. Dashboard admin sessions expire after one hour.

## Rotate

Create a new pair under new names, update `admin.token_hash_file`, and restart:

```sh
sudo mirage admin init \
  --token-file /secure/mirage/admin-2026-08.token \
  --hash-file /etc/mirage/admin-token-2026-08.sha256
# update config to the new .sha256 path, then restart Mirage
```

Verify the new token before deleting the old files. Rotation becomes effective
when the restarted process loads the new hash; existing dashboard cookies made
from the old token then fail authorization. Securely remove the old raw token
and hash according to the host's credential-retention policy. To roll back,
restore the old hash-file path and restart while the old files still exist.

## Incident response

If disclosure is suspected, rotate immediately, restart Mirage, verify the old
token is rejected, inspect audit events for force-delete/restart/delete actions,
and invalidate any operator copies of the old raw token.
