# Installing Mirage

Mirage v0.1.0 supports Linux on a single host. The recommended deployment uses systemd because Mirage starts application processes directly on the host and must reach their loopback ports.

Mirage deployment in Docker and Kubernetes is not supported in v0.1.0. A third-party Zero Trust connector may run in a container.

## 1. Prerequisites

The release artifact supports Linux amd64 systems with glibc. You need:

- a Linux host with systemd;
- `curl`, `sha256sum`, and `jq` for the installation and examples;
- permission to install under `/usr/local/bin` and `/etc/systemd/system`;
- wildcard DNS pointing to the host or a gateway;
- the runtime required by the applications you plan to preview.

Mirage uses Caddy for public routing. The source build pins Caddy 2.10.2 and Go 1.26 through [`mise`](https://mise.jdx.dev/).

## 2. Install the v0.1.0 release

Download the Linux amd64 binary and its checksum manifest from the release:

```sh
cd /tmp
curl -fLO https://github.com/Ollem-io/Mirage-Links/releases/download/v0.1.0/mirage-v0.1.0-linux-amd64
curl -fLO https://github.com/Ollem-io/Mirage-Links/releases/download/v0.1.0/checksums.txt
sha256sum -c checksums.txt
sudo install -m 0755 mirage-v0.1.0-linux-amd64 /usr/local/bin/mirage
mirage version
```

Expected output:

```text
mirage v0.1.0
```

### Build from source instead

```sh
git clone https://github.com/Ollem-io/Mirage-Links.git /opt/mirage-src
cd /opt/mirage-src
mise trust
mise install
VERSION=v0.1.0 mise run release
(cd dist && sha256sum -c checksums.txt)
sudo install -m 0755 dist/mirage-v0.1.0-linux-amd64 /usr/local/bin/mirage
sudo install -m 0755 "$(mise which caddy)" /usr/local/bin/caddy
```

## 3. Configure wildcard DNS

Mirage does not create DNS records or TLS certificates. Create a wildcard A, AAAA, or supported CNAME record:

```text
*.mydomain.com  ->  MIRAGE_HOST_OR_GATEWAY
```

Use the matching base host:

```yaml
base_host: mydomain.com
```

A link named `preview` in the space `calm-fox` becomes:

```text
preview-calm-fox.mydomain.com
```

A wildcard record does not normally cover the apex `mydomain.com`; Mirage does not require the apex for link routing.

The default public ingress is port `9955`. A direct deployment must forward the required Internet port to it. A gateway may terminate TLS and forward traffic to it instead.

## 4. Create the service account and configuration

```sh
sudo useradd --system --create-home   --home-dir /var/lib/mirage   --shell /usr/sbin/nologin mirage
sudo install -d -o mirage -g mirage -m 0750   /etc/mirage /var/lib/mirage /var/log/mirage
```

Install the pinned Caddy binary if you did not do so during the source build:

```sh
cd /opt/mirage-src
sudo install -m 0755 "$(mise which caddy)" /usr/local/bin/caddy
```

If you installed only the release binary, install Caddy 2.10.2 separately from its official release and place it at `/usr/local/bin/caddy`.

Create `/etc/mirage/config.yaml`:

```yaml
base_host: mydomain.com
public_address: ":9955"
private_address: "127.0.0.1:9956"
data_path: /var/lib/mirage/mirage.db
caddy:
  admin_url: http://127.0.0.1:2019
  binary: /usr/local/bin/caddy
  managed: true
```

Protect the configuration and state:

```sh
sudo chown root:mirage /etc/mirage/config.yaml
sudo chmod 0640 /etc/mirage/config.yaml
sudo chown -R mirage:mirage /var/lib/mirage /var/log/mirage
```

### Advertised HTTPS URLs

When a trusted gateway terminates TLS and publishes standard HTTPS, keep Mirage's internal public listener on `:9955` and add:

```yaml
external_scheme: https
external_port: 443
dashboard_ssl: true
```


`dashboard_ssl` marks the dashboard deployment as HTTPS so Mirage issues secure session cookies. Enable it only when a trusted TLS terminator fronts the dashboard.

## 5. Install the systemd service

Create `/etc/systemd/system/mirage.service`:

```ini
[Unit]
Description=Mirage temporary environment manager
Documentation=https://github.com/Ollem-io/Mirage-Links/blob/main/docs/setup.md
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
Environment=HOME=/var/lib/mirage

[Install]
WantedBy=multi-user.target
```

Mirage can start only commands accessible to the `mirage` user. If previews live outside `/var/lib/mirage`, grant narrow access in the unit, for example:

```ini
ReadOnlyPaths=/srv/projects
```

Do not run Mirage as root merely to bypass file permissions.

Start the service and verify readiness:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now mirage.service
sudo systemctl status mirage.service
curl -fsS http://127.0.0.1:9956/healthz
```

The public ingress never serves the dashboard or API. Keep the private listener on loopback unless you deliberately design a protected connector-only path.

## Direct deployment

For direct deployment, route wildcard DNS to the host. Forward port 80 or 443 through a TLS terminator or load balancer to managed Caddy on port `9955`. Do not forward port `9956`.

Mirage does not issue public TLS certificates in this model. Set `external_scheme` and `external_port` so generated URLs match the gateway's public HTTPS address.

## Zero Trust deployment (recommended)

Cloudflare Zero Trust, Pangolin, or an equivalent gateway can prevent direct exposure of the server and add identity and access policies.

1. Create a tunnel or site in the provider console and generate the connector installation command.
2. Install the connector on the Mirage host with the provider's systemd or Docker procedure.
3. Add a wildcard application or resource for `*.mydomain.com` that forwards to managed Caddy on port `9955`.
4. Optionally add a separate identity-protected management application. Use `GET /healthz` for readiness checks.

A native connector can generally target `http://127.0.0.1:9955`. A connector in Docker cannot normally reach host loopback; use the provider's documented host-gateway address and firewall the origin so only the connector can reach it.

Keep `private_address: "127.0.0.1:9956"` for local-only management. If a connector must provide remote management, make the smallest deliberate bind and firewall change required for that connector, and enforce the provider's identity policy. Never expose port `9956` directly to the Internet. Zero Trust is an outer authentication layer; Mirage tokens are still required.

### Advanced: external Caddy

With `caddy.managed: false`, Mirage does not start or stop Caddy. The configured `admin_url` must point to an existing Caddy admin API, and systemd must order Mirage after that service. For example, add `Requires=caddy.service` and `After=caddy.service` to Mirage's `[Unit]` section.

```yaml
caddy:
  admin_url: http://127.0.0.1:2019
  managed: false
```

Point the Zero Trust connector at the external Caddy service's actual ingress. Mirage modifies only its namespaced routes and never stops external Caddy.

## 6. Verify a temporary link

This example needs Python 3. Use your application's server command for another stack.

```sh
(umask 077; mirage space create --ttl 15m --json > /tmp/mirage-space.json)
export MIRAGE_TOKEN="$(jq -r .token /tmp/mirage-space.json)"

mirage link create   --name smoke   --command 'python3 -m http.server "$PORT" --bind 127.0.0.1'   --execution-folder /tmp   --health-check 'GET http://127.0.0.1:{port}/'   --grace 30s   --ttl 10m

mirage link list
mirage link logs smoke --tail 20
```

Test the returned URL through the direct or Zero Trust route, then clean up:

```sh
mirage link delete smoke
mirage space delete "$(jq -r .alias /tmp/mirage-space.json)"
unset MIRAGE_TOKEN
rm -f /tmp/mirage-space.json
```

## 7. Upgrade or roll back

Download and verify the new artifact as described above. Preserve the current binary before replacing it:

```sh
sudo cp -p /usr/local/bin/mirage /usr/local/bin/mirage.previous
sudo systemctl stop mirage.service
sudo install -m 0755 /tmp/mirage-v0.1.0-linux-amd64 /usr/local/bin/mirage
sudo systemctl start mirage.service
curl -fsS http://127.0.0.1:9956/healthz
```

If readiness fails:

```sh
sudo systemctl stop mirage.service
sudo install -m 0755 /usr/local/bin/mirage.previous /usr/local/bin/mirage
sudo systemctl start mirage.service
sudo journalctl -u mirage.service -n 200 --no-pager
```

See the [operator runbook](runbook.md) for recovery details.
