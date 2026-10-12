package config

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen          string         `yaml:"listen"`
	AllowedSources  []string       `yaml:"allowed_sources"`
	AllowedPorts    []int          `yaml:"allowed_ports"`
	DenyPrivate     bool           `yaml:"deny_private_destinations"`
	MaxConcurrent   int            `yaml:"max_concurrent"`
	MaxConnections  int            `yaml:"max_connections"`
	MaxIdleConns    int            `yaml:"max_idle_connections"`
	MaxIdlePerHost  int            `yaml:"max_idle_connections_per_host"`
	DialTimeout     time.Duration  `yaml:"dial_timeout"`
	HeaderTimeout   time.Duration  `yaml:"header_timeout"`
	RequestTimeout  time.Duration  `yaml:"request_timeout"`
	IdleTimeout     time.Duration  `yaml:"idle_timeout"`
	TunnelTimeout   time.Duration  `yaml:"tunnel_timeout"`
	ShutdownTimeout time.Duration  `yaml:"shutdown_timeout"`
	Sources         []netip.Prefix `yaml:"-"`
}

func Defaults() Config {
	return Config{
		Listen:          "127.0.0.1:8080",
		AllowedPorts:    []int{80, 443},
		DenyPrivate:     true,
		MaxConcurrent:   1024,
		MaxConnections:  2048,
		MaxIdleConns:    1024,
		MaxIdlePerHost:  64,
		DialTimeout:     10 * time.Second,
		HeaderTimeout:   10 * time.Second,
		RequestTimeout:  2 * time.Minute,
		IdleTimeout:     90 * time.Second,
		TunnelTimeout:   time.Hour,
		ShutdownTimeout: 15 * time.Second,
	}
}

func Load(path string) (Config, error) {
	c := Defaults()
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	const maxConfigBytes = 1 << 20
	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return c, err
	}
	if len(data) > maxConfigBytes {
		return c, fmt.Errorf("configuration exceeds 1 MiB")
	}

	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return c, fmt.Errorf("configuration must contain exactly one YAML document")
	}
	return c, c.Validate()
}

func (c *Config) Validate() error {
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if _, err := netip.ParseAddr(host); err != nil {
		return fmt.Errorf("listen host must be a literal IP")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1024 || p > 65535 {
		return fmt.Errorf("listen port must be between 1024 and 65535")
	}
	if len(c.AllowedSources) == 0 {
		return fmt.Errorf("allowed_sources must not be empty")
	}
	c.Sources = nil
	for _, s := range c.AllowedSources {
		prefix, err := netip.ParsePrefix(s)
		if err != nil {
			a, e := netip.ParseAddr(s)
			if e != nil {
				return fmt.Errorf("invalid source %q", s)
			}
			a = a.Unmap()
			prefix = netip.PrefixFrom(a, a.BitLen())
		}
		if prefix.Addr().Is4In6() || prefix.Addr().Zone() != "" {
			return fmt.Errorf("use canonical IP/CIDR for source %q", s)
		}
		c.Sources = append(c.Sources, prefix.Masked())
	}
	if len(c.AllowedPorts) == 0 {
		return fmt.Errorf("allowed_ports must not be empty")
	}
	for _, p := range c.AllowedPorts {
		if p < 1 || p > 65535 {
			return fmt.Errorf("invalid destination port %d", p)
		}
	}
	if c.MaxConcurrent < 1 || c.MaxConnections < 1 || c.MaxIdleConns < 1 || c.MaxIdlePerHost < 1 {
		return fmt.Errorf("connection limits must be positive")
	}
	timeouts := []time.Duration{
		c.DialTimeout,
		c.HeaderTimeout,
		c.RequestTimeout,
		c.IdleTimeout,
		c.TunnelTimeout,
		c.ShutdownTimeout,
	}
	for _, d := range timeouts {
		if d <= 0 {
			return fmt.Errorf("timeouts must be positive")
		}
	}
	return nil
}

func (c Config) AllowsSource(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range c.Sources {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func (c Config) AllowsPort(port string) bool {
	p, err := strconv.Atoi(port)
	if err != nil {
		return false
	}
	for _, allowed := range c.AllowedPorts {
		if p == allowed {
			return true
		}
	}
	return false
}
