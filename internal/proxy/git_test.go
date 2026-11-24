package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nexryai/esperanta/internal/config"
)

func gitTargetTestProxy(t *testing.T, upstream http.Handler) (*Proxy, config.RequestTarget) {
	t.Helper()
	origin := httptest.NewTLSServer(upstream)
	t.Cleanup(origin.Close)
	c := config.Defaults()
	c.AllowedSources = []string{"127.0.0.1"}
	target := config.RequestTarget{
		Port: 8081, Type: config.RequestTargetGit, UpstreamHost: "192.168.1.20",
		AllowedPrefixes: []string{"https://example.com/nexryai/hoge", "https://example.com/nexryai/fuge"},
		ReadOnly:        true,
		UpstreamHeaders: map[string]string{"CF-Access-Client-Secret": "target-secret"},
	}
	c.RequestTargets = []config.RequestTarget{target}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	p := New(c)
	t.Cleanup(p.Close)
	p.transport.TLSClientConfig = origin.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	p.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "192.168.1.20:443" {
			return nil, fmt.Errorf("unexpected dial address %q", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, origin.Listener.Addr().String())
	}
	return p, target
}

func TestGitTarget(t *testing.T) {
	p, target := gitTargetTestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "example.com" || r.TLS.ServerName != "example.com" {
			t.Errorf("Host = %q, TLS name = %q", r.Host, r.TLS.ServerName)
		}
		if r.Header.Get("CF-Access-Client-Secret") != "target-secret" || r.Header.Get("Git-Protocol") != "version=2" ||
			r.Header.Get("X-Original-URL") != "" {
			t.Error("unexpected headers received")
		}
		if strings.HasPrefix(r.URL.Path, "/nexryai/fuge/") {
			http.Redirect(w, r, "https://elsewhere.example/repo.git", http.StatusFound)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	readOnly := p.RequestTarget(target)
	target.ReadOnly = false
	writable := p.RequestTarget(target)
	for _, tt := range []struct {
		method, path, service string
		writable              bool
		status                int
	}{
		{"GET", "/nexryai/hoge/info/refs?service=git-upload-pack", "", false, 204},
		{"POST", "/nexryai/hoge.git/git-upload-pack", "git-upload-pack", false, 204},
		{"GET", "/nexryai/hoge/info/refs?service=git-receive-pack", "", false, 403},
		{"POST", "/nexryai/hoge.git/git-receive-pack", "git-receive-pack", false, 403},
		{"GET", "/nexryai/hoge/info/refs?service=git-receive-pack", "", true, 204},
		{"POST", "/nexryai/hoge.git/git-receive-pack", "git-receive-pack", true, 204},
		{"GET", "/nexryai/hoge-other/info/refs?service=git-upload-pack", "", false, 403},
		{"GET", "/nexryai/hoge/other/info/refs?service=git-upload-pack", "", false, 403},
		{"GET", "/nexryai/%68oge/info/refs?service=git-upload-pack", "", false, 400},
		{"GET", "/nexryai/hoge/../hoge/info/refs?service=git-upload-pack", "", false, 400},
		{"GET", "/nexryai/hoge//info/refs?service=git-upload-pack", "", false, 400},
		{"GET", "/nexryai/hoge/info/refs?service=git-upload-pack&service=git-receive-pack", "", false, 400},
		{"GET", "/nexryai/hoge/info/refs", "", false, 400},
		{"GET", "/nexryai/hoge/objects/info/packs", "", false, 404},
		{"GET", "/nexryai/hoge/git-upload-pack", "", false, 405},
		{"POST", "/nexryai/hoge/git-upload-pack", "", false, 415},
		{"GET", "/nexryai/fuge/info/refs?service=git-upload-pack", "", false, 502},
	} {
		r := httptest.NewRequest(tt.method, tt.path, nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Git-Protocol", "version=2")
		r.Header.Set("CF-Access-Client-Secret", "client-secret")
		r.Header.Set("X-Original-URL", "/other")
		if tt.service != "" {
			r.Header.Set("Content-Type", "application/x-"+tt.service+"-request")
		}
		w := httptest.NewRecorder()
		h := readOnly
		if tt.writable {
			h = writable
		}
		h.ServeHTTP(w, r)
		if w.Code != tt.status || w.Header().Get("Location") != "" {
			t.Errorf("%s %s: got %d (Location %q), want %d", tt.method, tt.path, w.Code, w.Header().Get("Location"), tt.status)
		}
	}
}

func TestGitCloneThroughTarget(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), git, args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	root := t.TempDir()
	repo := filepath.Join(root, "nexryai", "hoge.git")
	work := t.TempDir()
	run("init", "--bare", repo)
	run("-C", work, "init")
	run("-C", work, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "initial")
	run("-C", work, "push", repo, "HEAD")
	p, target := gitTargetTestProxy(t, &cgi.Handler{
		Path: git, Args: []string{"http-backend"},
		Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"},
	})
	server := httptest.NewServer(p.RequestTarget(target))
	t.Cleanup(server.Close)
	clone := filepath.Join(t.TempDir(), "clone")
	run("-c", "http.proxy=", "clone", server.URL+"/nexryai/hoge.git", clone)
	run("-C", clone, "rev-parse", "--verify", "HEAD")
}
