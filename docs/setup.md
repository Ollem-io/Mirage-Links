# Installing Mirage

Mirage v1 is a Linux, single-host service. The recommended installation uses
**systemd** because link processes run directly on the host and can reach any
host-internal loopback port. Docker and Kubernetes packaging are planned but
are not supported in v1.

## Installation support

| Method | Status | Recommendation |
|---|---|---|
| systemd | Supported | **Recommended for production and shared hosts** |
| Foreground process | Supported | Development, evaluation, and debugging |
| Docker | TODO | Planned for a future release; do not use as a supported deployment yet |
| Kubernetes | TODO | Planned for a future release; do not use as a supported deployment yet |

## 1. Host prerequisites

Use a Linux host with:

- systemd;
- a user able to install files under `/usr/local/bin` and `/etc/systemd/system`;
- wildcard DNS pointing at the host or its public load balancer/NAT;
- ports selected for the public and private listeners;
- outbound access needed to install the pinned tools when building from source.

Mirage pins Go 1.26, Caddy 2.10.2, Node, and Tailwind through
[`mise`](https://mise.jdx.dev/).

Install `mise` using its official instructions, clone the project, and build a
verified release:

```sh
git clone <your-mirage-repository-url> /opt/mirage-src
cd /opt/mirage-src
mise trust
mise install
mise run ci
VERSION=v1.0.0 mise run release
sha256sum -c dist/checksums.txt
./dist/mirage version
```

Expected version output:

```text
mirage v1.0.0
```

Install the binary:

```sh
sudo install -m 0755 dist/mirage /usr/local/bin/mirage
/usr/local/bin/mirage version
```

## 2. DNS and network

Mirage does not provision DNS. Point a wildcard record at the host.

For subdomain mode:

```text
*.mirage.example.com -> HOST_IP
base_host: mirage.example.com
```

A link is published as:

```text
<link>-<space>.mirage.example.com
```

For suffix mode:

```text
*-mirage.example.com -> HOST_IP
base_host: -mirage.example.com
```

A link is published as:

```text
<link>-<space>-mirage.example.com
```

The example below uses public port `9955`. Forward ports 80/443 to that port or
put an external load balancer in front if URLs should omit the port. Mirage v1
does not configure public TLS certificates.

Keep the private management listener on loopback. Never expose port 9956 to the
public Internet.

## 3. Create the service account and directories

Mirage starts trusted host commands on behalf of its users/agents. A dedicated
service account is recommended. Give it access only to the project directories
from which links may be launched.

```sh
sudo useradd --system --create-home --home-dir /var/lib/mirage   --shell /usr/sbin/nologin mirage
sudo install -d -o mirage -g mirage -m 0750   /etc/mirage /var/lib/mirage /var/log/mirage
```

If link commands must access repositories elsewhere on the host, grant the
`mirage` user read/execute permissions for those paths. Do not run Mirage as
root merely to bypass permissions.

## 4. Configure Mirage

Create `/etc/mirage/config.yaml`:

```yaml
base_host: mirage.example.com
public_address: ":9955"
private_address: "127.0.0.1:9956"
data_path: /var/lib/mirage/mirage.db
caddy:
  admin_url: http://127.0.0.1:2019
  binary: /usr/local/bin/caddy
  managed: true
```

Install Caddy at the configured path. When built through the repository, the
pinned binary can be located with:

```sh
cd /opt/mirage-src
mise which caddy
```

Either copy that binary to `/usr/local/bin/caddy`, or set `caddy.binary` to its
absolute path:

```sh
sudo install -m 0755 "$(mise which caddy)" /usr/local/bin/caddy
```

Protect the configuration and state directory:

```sh
sudo chown root:mirage /etc/mirage/config.yaml
sudo chmod 0640 /etc/mirage/config.yaml
sudo chown -R mirage:mirage /var/lib/mirage
```

### External Caddy mode

To connect to an already-running Caddy instead of starting a managed child:

```yaml
caddy:
  admin_url: http://127.0.0.1:2019
  managed: false
```

Mirage modifies only its namespaced routes and never stops an external Caddy.
The systemd unit below does not need to depend on a Caddy unit in managed mode.
For external mode, add an appropriate `After=`/`Requires=` dependency for your
Caddy service.

## 5. Install the recommended systemd service

Create `/etc/systemd/system/mirage.service`:

```ini
[Unit]
Description=Mirage temporary environment manager
Documentation=file:/opt/mirage-src/docs/setup.md
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=mirage
Group=mirage
WorkingDirectory=/var/lib/mirage
ExecStart=/usr/local/bin/mirage start --config /etc/mirage/config.yaml
Restart=on-failure
RestartSec=3s
TimeoutStartSec=60s
TimeoutStopSec=30s
KillMode=mixed
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=/var/lib/mirage /var/log/mirage

# Link commands inherit the service environment. Add only non-secret,
# installation-wide variables here. Space tokens are client credentials and
# must not be configured in this unit.
Environment=HOME=/var/lib/mirage

[Install]
WantedBy=multi-user.target
```

`ProtectSystem` and `ProtectHome` can prevent link commands from reaching
application repositories. If Mirage must launch code outside `/var/lib/mirage`,
add narrowly scoped paths, for example:

```ini
ReadOnlyPaths=/srv/projects
```

or adjust the sandbox directives deliberately. systemd is recommended precisely
because the Mirage service and its child processes run on the host network and
can reach internal loopback ports without container-network translation.

Reload and start:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now mirage.service
sudo systemctl status mirage.service
```

Follow logs:

```sh
sudo journalctl -u mirage.service -f
```

Verify private readiness:

```sh
curl -fsS http://127.0.0.1:9956/healthz
```

The command must succeed before creating spaces or links.

## 6. Verify the installation

Create a short-lived space from the host:

```sh
mirage space create --ttl 15m --json > /tmp/mirage-space.json
export MIRAGE_TOKEN="$(jq -r .token /tmp/mirage-space.json)"
```

For a test application that reads `$PORT`, create a link from an accessible
folder:

```sh
mirage link create   --name smoke   --command 'python3 -m http.server "$PORT" --bind 127.0.0.1'   --execution-folder /tmp   --health-check 'GET http://127.0.0.1:{port}/'   --grace 30s   --ttl 10m
```

Then verify:

```sh
mirage link list
mirage link logs smoke --tail 20
```

Test the generated public hostname using DNS or a temporary `Host` header. The
public listener must return 404 for management paths:

```sh
curl -i -H 'Host: smoke-SPACE_ALIAS.mirage.example.com'   http://127.0.0.1:9955/
curl -i http://127.0.0.1:9955/healthz
curl -i http://127.0.0.1:9955/api/v1/spaces
```

Clean up:

```sh
mirage link delete smoke
# Replace SPACE_ALIAS with the alias returned by space create.
mirage space delete SPACE_ALIAS
unset MIRAGE_TOKEN
rm -f /tmp/mirage-space.json
```

## 7. Updating and rollback

Build and verify the new release before replacing the binary:

```sh
cd /opt/mirage-src
git pull --ff-only
mise install
mise run ci
VERSION=v1.0.1 mise run release
sha256sum -c dist/checksums.txt
```

Then install it with a backup and restart:

```sh
sudo systemctl stop mirage
sudo cp /usr/local/bin/mirage /usr/local/bin/mirage.previous
sudo install -m 0755 dist/mirage /usr/local/bin/mirage
sudo systemctl start mirage
curl -fsS http://127.0.0.1:9956/healthz
```

Rollback if readiness fails:

```sh
sudo systemctl stop mirage
sudo mv /usr/local/bin/mirage.previous /usr/local/bin/mirage
sudo systemctl start mirage
```

Retain `/var/lib/mirage/mirage.db*` and the prior binary before an upgrade.

## 8. Foreground development installation

For local development without systemd:

```sh
cd /opt/mirage-src
mise install
mise run build
./bin/mirage start --public 9955 --private 9956   --config ./mirage.yaml
```

Stop it with Ctrl-C. Do not use this mode as a long-running production service.

## Docker — TODO

Docker installation is intentionally unsupported in Mirage v1. Containerizing
Mirage requires explicit designs for:

- reaching arbitrary host-internal application ports;
- safely launching and terminating host workloads;
- Caddy ownership and Admin API networking;
- persistent libSQL storage and process recovery;
- a security model for access to host processes.

Do not publish or rely on an unofficial container image as the recommended
installation method. Track Docker support as a future release task.

## Kubernetes — TODO

Kubernetes installation is intentionally unsupported in Mirage v1. A future
implementation must define whether links are host processes, Pods, or Jobs, and
must address wildcard ingress, per-link process supervision, storage,
reconciliation, RBAC, and multi-node scheduling.

No Helm chart, operator, manifest, or supported Kubernetes deployment exists
for v1. Track Kubernetes support as a future release task.

## Troubleshooting

### Service does not start

```sh
sudo systemctl status mirage.service
sudo journalctl -u mirage.service -n 200 --no-pager
/usr/local/bin/mirage start --config /etc/mirage/config.yaml
```

Check that:

- the config file exists and is readable by the service;
- public/private/Caddy Admin ports are available;
- `data_path` is writable by `mirage`;
- the configured Caddy binary exists and is executable;
- wildcard DNS is correct.

### Readiness fails after a crash

Restart with the same `data_path`. Mirage performs startup reconciliation before
accepting mutations and repeats cleanup every minute. It removes only
Mirage-owned Caddy routes.

### Link commands cannot access source files

Check ownership and systemd sandbox paths. Add the smallest necessary
`ReadOnlyPaths`/`ReadWritePaths` entries, then reload and restart the unit.

### Security reminders

- Keep the private listener on `127.0.0.1` or a protected management network.
- Use mode 0600 for `.mirage_token` and add it to `.gitignore`.
- Never put bearer tokens in unit files, logs, tickets, or committed config.
- Run link commands only from trusted repositories; they execute through the
  host shell as the `mirage` service user.

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
