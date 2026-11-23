# Esperanta

A simple HTTP forward proxy written in Go.

## Installation

### Snap
<a href="https://snapcraft.io/esperanta" target="_blank">
   <img width="200px" height="auto" alt="Get it from the Snap Store" src=https://snapcraft.io/en/dark/install.svg />
</a>

　  


```sh
sudo snap install esperanta

# Edit config
sudo nano /var/snap/esperanta/current/config/config.yaml

# Apply config
sudo snap restart esperanta
```

Tagged releases are published to the Snap Store `stable` channel. Builds from
`main` are published to `edge` and can be installed with
`sudo snap install --edge esperanta`.

### Debian and Ubuntu (APT)

Packages are published from GitHub Pages. The same key signs the APT and
pacman repositories.

```sh
curl -fsSL https://nexryai.github.io/esperanta/esperanta-archive-keyring.asc \
  | sudo gpg --dearmor -o /usr/share/keyrings/esperanta-archive-keyring.gpg

echo "deb [signed-by=/usr/share/keyrings/esperanta-archive-keyring.gpg] https://nexryai.github.io/esperanta/ stable main" \
  | sudo tee /etc/apt/sources.list.d/esperanta.list

sudo apt update
sudo apt install esperanta

# Edit configuration, then enable and start the service
sudo nano /etc/esperanta/config.yaml
sudo systemctl enable --now esperanta
```

### Fedora, RHEL and derivatives (COPR)

```sh
sudo dnf copr enable nexryai/esperanta
sudo dnf install esperanta

sudo nano /etc/esperanta/config.yaml
sudo systemctl enable --now esperanta
```

### Arch Linux (pacman)

Packages for `x86_64` and `aarch64` are published to a signed pacman
repository on GitHub Pages.

```sh
curl -fsSL https://nexryai.github.io/esperanta/pacman/esperanta-archive-keyring.asc \
  -o /tmp/esperanta-archive-keyring.asc
sudo pacman-key --add /tmp/esperanta-archive-keyring.asc

key=$(gpg --with-colons --import-options show-only \
  --import /tmp/esperanta-archive-keyring.asc |
  awk -F: '/^fpr:/{print $10; exit}')
sudo pacman-key --lsign-key "$key"

sudo tee -a /etc/pacman.conf >/dev/null <<'EOF'
[esperanta]
SigLevel = Required DatabaseRequired
Server = https://nexryai.github.io/esperanta/pacman/$arch
EOF

sudo pacman -Syu
sudo pacman -S esperanta

sudo nano /etc/esperanta/config.yaml
sudo systemctl enable --now esperanta
```

If the repository key is rotated, check the published
[signing-key fingerprint](https://nexryai.github.io/esperanta/esperanta-archive-keyring.fingerprint).

### From source

```sh
go build -trimpath -ldflags='-s -w' -o bin/esperanta ./cmd/esperanta

sudo install -Dm0755 bin/esperanta /usr/bin/esperanta
sudo install -Dm0644 systemd/esperanta.service /usr/lib/systemd/system/esperanta.service
sudo install -Dm0644 config.yaml /etc/esperanta/config.yaml
sudo systemctl daemon-reload
sudo systemctl enable --now esperanta

# Edit configuration, then restart
sudo nano /etc/esperanta/config.yaml
sudo systemctl restart esperanta
```

## YAML configuration

See [`config.yaml`](config.yaml) for an example with every option.
The default configuration listens on `127.0.0.1:8080` and only allows connections from loopback addresses.
To make the proxy available on your LAN, change it as follows, for example:

```yaml
listen: "0.0.0.0:8080"
allowed_sources:
  - "192.168.1.0/24"
  - "192.168.2.10"
allowed_ports: [80, 443]
deny_private_destinations: true
```

`allowed_sources` accepts individual IPv4 or IPv6 addresses and CIDR ranges. An empty list
causes a startup error, and connections from sources that are not allowed are closed at the TCP level.
The proxy checks the TCP peer address and does not use `X-Forwarded-For` or similar headers.
If a load balancer sits in front of the proxy, its IP address is the one checked.

| Option | Purpose |
| --- | --- |
| `allowed_ports` | Restricts destination ports for HTTP forwarding and CONNECT |
| `deny_private_destinations` | Blocks connections to private, loopback, link-local, shared, and other nonpublic IP addresses |
| `upstream_headers` | Sets headers only for exactly matching destination hostnames on regular HTTP requests; CONNECT is unaffected |
| `request_target` | Opens dedicated HTTP listeners, each forwarding to one fixed HTTPS hostname with its own headers |
| `max_concurrent` | Limits the combined number of HTTP requests and CONNECT tunnels handled concurrently |
| `max_connections` | Limits client TCP connections, including idle connections |
| `max_idle_connections` | Limits idle connections to destinations kept in the pool |
| `max_idle_connections_per_host` | Limits idle connections per destination host |
| `dial_timeout` | Limits connection setup time, including DNS resolution |
| `header_timeout` | Limits how long the proxy waits for HTTP headers from clients and destinations |
| `request_timeout` | Limits the total duration of a regular HTTP request |
| `idle_timeout` | Limits how long idle client and destination connections are kept open |
| `tunnel_timeout` | Limits the maximum lifetime of a CONNECT tunnel |
| `shutdown_timeout` | Limits how long shutdown waits for regular HTTP requests to finish |

Specify durations such as `10s` or `2m`. Omitted options use their default values.
Restart the proxy to apply configuration changes.

`upstream_headers` maps an exact destination hostname to headers that are
applied only for that host. There is intentionally no global header option, so
credentials cannot be sent to every destination. Matching ignores case and a
trailing dot, and configured values replace headers supplied by clients.
Transport headers such as `Host`, `Content-Length`, `Transfer-Encoding`,
`Connection` and `Proxy-Authorization` are rejected. The setting does not apply
inside HTTPS CONNECT tunnels.

Use a dedicated listener to add Cloudflare Access service token headers without
configuring them on clients or on the forward proxy:

```yaml
request_target:
  - port: 8081
    upstream_host: app.example.com
    upstream_headers:
      CF-Access-Client-Id: "<client-id>"
      CF-Access-Client-Secret: "<client-secret>"
```

With the default listen IP, run `curl http://127.0.0.1:8081/api/status`. The
listener uses the IP from `listen` and forwards the method, path, query and body
to `https://app.example.com` on port 443. The configured hostname is used for
HTTP Host and TLS verification, regardless of the incoming Host header. The
listener rejects absolute URLs, CONNECT, TRACE and protocol upgrades. Use `upstream_headers: {}` or omit
it if no headers are needed. The top-level `upstream_headers` does not apply to
these listeners.

Port 443 must be in `allowed_ports`. The dedicated listeners share
`allowed_sources`, destination IP restrictions, connection and concurrency
limits with the forward proxy. Any allowed client can use the configured
service identity through the dedicated port. Keep the config file private and
expose the HTTP listener only to trusted clients on a trusted network.

The proxy checks the IP addresses returned by DNS for each destination and connects directly to a checked IP address.
It also rejects DNS responses that contain a mix of public and nonpublic IP addresses. If you need to connect to internal services,
set `deny_private_destinations: false`.
