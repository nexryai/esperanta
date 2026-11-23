# Esperanta

A simple HTTP forward proxy written in Go.

## Installation

### Snap

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

The packaged systemd service runs as a `DynamicUser` with an empty capability
set, a read-only system, a system call allow-list and Esperanta's own Landlock
policy.

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

## Run locally

See `go.mod` for the required Go version. Run as a regular user.

```sh
go build -trimpath -o bin/esperanta ./cmd/esperanta
./bin/esperanta --config config.yaml --check
./bin/esperanta --config config.yaml
```

You can test it from another terminal:

```sh
curl --noproxy '' --proxy http://127.0.0.1:8080 http://example.com/
curl --noproxy '' --proxy http://127.0.0.1:8080 https://example.com/
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

The proxy checks the IP addresses returned by DNS for each destination and connects directly to a checked IP address.
It also rejects DNS responses that contain a mix of public and nonpublic IP addresses. If you need to connect to internal services,
set `deny_private_destinations: false`.

## Releasing

Push a `v<version>` tag, or run the `release` workflow manually with a tag:

```sh
git tag -s v0.1.3 -m "esperanta 0.1.3"
git push origin v0.1.3
```

The release workflow builds Linux archives, `.deb` packages and Arch Linux
packages, then publishes the APT and pacman repositories to GitHub Pages. The
same tag publishes the Snap to the `stable` channel, while `main` builds go to
`edge`. Arch package metadata and the RPM spec are committed back to `main` as
a signed commit. COPR builds RPMs directly from the pushed tag using
`.copr/Makefile` and `esperanta.spec`.

GitHub Pages must use the "GitHub Actions" source. The repository also needs:

- A Fedora COPR project configured for this repository (SCM source).
- `APT_GPG_PRIVATE_KEY`: an ASCII-armored private signing key.
- `APT_GPG_PASSPHRASE`: its passphrase, if it has one.

The APT and pacman repositories are not deployed until the signing key is
configured. Never commit the private key; only the generated public keyring is
published to Pages.
