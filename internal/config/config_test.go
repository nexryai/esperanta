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

func TestUpstreamHeadersLoad(t *testing.T) {
	c, err := loadYAML(t, "allowed_sources: [127.0.0.1]\nupstream_headers:\n  API.Example.COM.:\n    Authorization: Bearer domain-secret\n    X.Example: dotted-header\n")
	if err != nil {
		t.Fatal(err)
	}
	headers := c.UpstreamHeaders["API.Example.COM."]
	if headers["Authorization"] != "Bearer domain-secret" || headers["X.Example"] != "dotted-header" {
		t.Fatalf("unexpected domain headers: %#v", c.UpstreamHeaders)
	}
}

func TestRequestTargets(t *testing.T) {
	const base = "allowed_sources: [127.0.0.1]\n"
	const entry = "  - port: 8081\n    upstream_host: app.example.com\n"
	c, err := loadYAML(t, base+"request_target:\n"+entry+
		"    upstream_headers:\n      CF-Access-Client-Id: example\n"+
		"  - port: 8082\n    upstream_host: another.example.com\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.RequestTargets) != 2 || c.RequestTargets[0].Port != 8081 ||
		c.RequestTargets[0].UpstreamHost != "app.example.com" ||
		c.RequestTargets[0].UpstreamHeaders["CF-Access-Client-Id"] != "example" {
		t.Fatal("request targets not loaded")
	}
	for name, contents := range map[string]string{
		"primary port": "request_target:\n" + strings.Replace(entry, "8081", "8080", 1),
		"duplicate":    "request_target:\n" + entry + entry,
		"invalid port": "request_target:\n" + strings.Replace(entry, "8081", "0", 1),
		"invalid host": "request_target:\n" + strings.Replace(entry, "app.example.com", "https://app.example.com/path", 1),
		"forbidden":    "request_target:\n" + entry + "    upstream_headers: {Host: example.com}\n",
		"no HTTPS":     "allowed_ports: [80]\nrequest_target:\n" + entry,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadYAML(t, base+contents); err == nil {
				t.Fatal("invalid request target accepted")
			}
		})
	}
}

func TestGitRequestTarget(t *testing.T) {
	const base = "allowed_sources: [127.0.0.1]\nrequest_target:\n  - port: 8081\n    type: git\n"
	const prefixes = "    allowed_prefixes:\n      - https://git.example.com/nexryai/hoge\n      - https://GIT.example.com/nexryai/fuge.git/\n"
	for _, host := range []string{"192.168.1.20", "2001:db8::20", "git.internal.example"} {
		c, err := loadYAML(t, base+"    upstream_host: '"+host+"'\n    read_only: true\n"+prefixes)
		if err != nil {
			t.Fatal(err)
		}
		target := c.RequestTargets[0]
		gotHost, paths, err := target.GitPrefixes()
		if err != nil || target.Type != RequestTargetGit || target.UpstreamHost != host || !target.ReadOnly ||
			gotHost != "git.example.com" || strings.Join(paths, ",") != "/nexryai/hoge,/nexryai/fuge.git" {
			t.Fatalf("unexpected Git target: %+v, %q, %v, %v", target, gotHost, paths, err)
		}
	}
	const upstream = "    upstream_host: 192.168.1.20\n"
	for name, contents := range map[string]string{
		"missing prefixes": base + upstream,
		"other type":       strings.Replace(base, "type: git", "type: other", 1) + upstream + prefixes,
		"untyped prefixes": strings.Replace(base, "    type: git\n", "", 1) + upstream + prefixes,
		"HTTP prefix":      base + upstream + strings.Replace(prefixes, "https:", "http:", 1),
		"multiple hosts":   base + upstream + strings.Replace(prefixes, "GIT.example", "other.example", 1),
		"no path":          base + upstream + strings.Replace(prefixes, "/nexryai/hoge", "", 1),
		"query":            base + upstream + strings.Replace(prefixes, "/hoge", "/hoge?q=x", 1),
		"fragment":         base + upstream + strings.Replace(prefixes, "/hoge", "/hoge#", 1),
		"escaped path":     base + upstream + strings.Replace(prefixes, "/hoge", "/%68oge", 1),
		"unsafe path":      base + upstream + strings.Replace(prefixes, "/hoge", "/../hoge", 1),
		"upstream port":    base + strings.Replace(upstream, "1.20", "1.20:443", 1) + prefixes,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadYAML(t, contents); err == nil {
				t.Fatal("invalid Git target accepted")
			}
		})
	}
}

func TestInvalidConfigurations(t *testing.T) {
	for name, contents := range map[string]string{
		"empty ACL":           "allowed_sources: []",
		"invalid IP":          "allowed_sources: [no-such-ip]",
		"unknown field":       "allowed_sources: [127.0.0.1]\nallow_all: true",
		"duplicate field":     "allowed_sources: [127.0.0.1]\nallowed_sources: ['::1']",
		"multiple documents":  "allowed_sources: [127.0.0.1]\n---\nallowed_sources: ['::1']",
		"root port":           "allowed_sources: [127.0.0.1]\nlisten: '127.0.0.1:80'",
		"invalid port":        "allowed_sources: [127.0.0.1]\nallowed_ports: [65536]",
		"zero limit":          "allowed_sources: [127.0.0.1]\nmax_concurrent: 0",
		"negative timeout":    "allowed_sources: [127.0.0.1]\ntunnel_timeout: -1s",
		"global header":       "allowed_sources: [127.0.0.1]\nupstream_headers:\n  X-Test: value",
		"invalid header":      "allowed_sources: [127.0.0.1]\nupstream_headers:\n  api.example.com:\n    'Bad Header': value",
		"forbidden header":    "allowed_sources: [127.0.0.1]\nupstream_headers:\n  api.example.com:\n    Host: example.com",
		"duplicate header":    "allowed_sources: [127.0.0.1]\nupstream_headers:\n  api.example.com:\n    X-Test: one\n    x-test: two",
		"invalid value":       "allowed_sources: [127.0.0.1]\nupstream_headers:\n  api.example.com:\n    X-Test: \"line\\nbreak\"",
		"invalid domain":      "allowed_sources: [127.0.0.1]\nupstream_headers:\n  'bad/domain':\n    X-Test: one",
		"wildcard domain":     "allowed_sources: [127.0.0.1]\nupstream_headers:\n  '*.example.com':\n    X-Test: one",
		"duplicate domain":    "allowed_sources: [127.0.0.1]\nupstream_headers:\n  API.example.com:\n    X-Test: one\n  api.example.com:\n    X-Test: two",
		"invalid header type": "allowed_sources: [127.0.0.1]\nupstream_headers:\n  api.example.com: [one, two]",
		"oversized":           "allowed_sources: [127.0.0.1]\n#" + strings.Repeat("x", 1<<20),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadYAML(t, contents); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}
