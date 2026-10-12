# Esperanta

A simple HTTP forward proxy written in Go.

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
