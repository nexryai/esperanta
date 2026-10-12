package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadYAML(t *testing.T, contents string) (Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestLoadAndSourceACL(t *testing.T) {
	c, err := loadYAML(t, "allowed_sources: [192.168.1.0/24, '::1']\ndial_timeout: 3s\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.DialTimeout != 3*time.Second || !c.DenyPrivate {
		t.Fatalf("unexpected defaults or duration: %+v", c)
	}
	tests := []struct {
		remote  string
		allowed bool
	}{
		{"192.168.1.25:1234", true},
		{"[::ffff:192.168.1.25]:1234", true},
		{"[::1]:1234", true},
		{"192.168.2.25:1234", false},
		{"invalid", false},
	}
	for _, tt := range tests {
		if got := c.AllowsSource(tt.remote); got != tt.allowed {
			t.Errorf("AllowsSource(%q) = %v", tt.remote, got)
		}
	}
}

func TestInvalidConfigurations(t *testing.T) {
	for name, contents := range map[string]string{
		"empty ACL":          "allowed_sources: []",
		"invalid IP":         "allowed_sources: [no-such-ip]",
		"unknown field":      "allowed_sources: [127.0.0.1]\nallow_all: true",
		"duplicate field":    "allowed_sources: [127.0.0.1]\nallowed_sources: ['::1']",
		"multiple documents": "allowed_sources: [127.0.0.1]\n---\nallowed_sources: ['::1']",
		"root port":          "allowed_sources: [127.0.0.1]\nlisten: '127.0.0.1:80'",
		"invalid port":       "allowed_sources: [127.0.0.1]\nallowed_ports: [65536]",
		"zero limit":         "allowed_sources: [127.0.0.1]\nmax_concurrent: 0",
		"negative timeout":   "allowed_sources: [127.0.0.1]\ntunnel_timeout: -1s",
		"oversized":          "allowed_sources: [127.0.0.1]\n#" + strings.Repeat("x", 1<<20),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadYAML(t, contents); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}
