# Admin-token runbook

Create the installation credential offline: `mirage admin init --token-file /secure/admin.token --hash-file /etc/mirage/admin-token.sha256`. Place only the hash-file path under `admin.token_hash_file` in Mirage config. The raw token is printed nowhere and is delivered only in the token file. Restart Mirage after configuration. Use `--admin-token`, `MIRAGE_ADMIN_TOKEN`, or a 0600 `./.mirage_admin_token` for global space operations. Admin sessions expire after one hour. Configure this in every production installation: absent configuration preserves legacy unauthenticated global space endpoints only for backwards compatibility.


### Ownership and service access

Create the raw token file in an operator-owned protected directory (mode 0600). The hash file is mode 0640: set its group to the Mirage service group when needed, but never make it group/world writable. Configure the absolute hash path, verify the service account can read it, then restart. If the hash is absent or malformed, global administration is fail-closed.
