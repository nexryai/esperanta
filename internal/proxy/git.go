package proxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"

	"github.com/nexryai/esperanta/internal/config"
)

func (p *Proxy) gitTarget(target config.RequestTarget) http.Handler {
	host, prefixes, err := target.GitPrefixes()
	if err != nil {
		return http.NotFoundHandler()
	}
	repos := make(map[string]struct{}, len(prefixes))
	for _, repo := range prefixes {
		repos[strings.TrimSuffix(repo, ".git")] = struct{}{}
	}
	// Keep pools separate: two listeners can use the same TLS hostname but
	// connect to different servers. The shared dial policy still checks the IP.
	transport := p.transport.Clone()
	dial := transport.DialContext
	address := net.JoinHostPort(config.CanonicalDomain(target.UpstreamHost), "443")
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dial(ctx, network, address)
	}
	p.mu.Lock()
	p.gitTransports = append(p.gitTransports, transport)
	p.mu.Unlock()

	headers := headerSet(target.UpstreamHeaders)
	forward := p.newForward(func(r *httputil.ProxyRequest) {
		r.Out.URL.Scheme = "https"
		r.Out.URL.Host = host
		r.Out.Host = host
		// Do not let client-supplied routing or method override headers change
		// how the upstream interprets the checked Git request.
		gitHeaders := make(http.Header)
		for _, name := range []string{
			"Accept", "Accept-Encoding", "Authorization", "Content-Encoding", "Content-Type",
			"Expect", "Git-Protocol", "Pragma", "User-Agent",
		} {
			if values := r.Out.Header.Values(name); len(values) != 0 {
				gitHeaders[http.CanonicalHeaderKey(name)] = values
			}
		}
		applyHeaders(gitHeaders, headers)
		r.Out.Header = gitHeaders
	})
	forward.Transport = transport
	forward.ModifyResponse = func(r *http.Response) error {
		if r.StatusCode >= 300 && r.StatusCode < 400 && r.Header.Get("Location") != "" {
			return errors.New("Git upstream redirects are not supported")
		}
		return nil
	}
	return p.serveTarget(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		service, status := gitService(r, repos)
		if status != http.StatusOK {
			http.Error(w, http.StatusText(status), status)
			return
		}
		if target.ReadOnly && service == "git-receive-pack" {
			http.Error(w, "Git mirror is read-only", http.StatusForbidden)
			return
		}
		forward.ServeHTTP(w, r)
	}))
}

func gitService(r *http.Request, repos map[string]struct{}) (string, int) {
	if r.URL.RawPath != "" || !config.ValidGitPath(r.URL.Path) {
		return "", http.StatusBadRequest
	}
	var repo, service, method string
	switch {
	case strings.HasSuffix(r.URL.Path, "/info/refs"):
		repo, _ = strings.CutSuffix(r.URL.Path, "/info/refs")
		switch r.URL.RawQuery {
		case "service=git-upload-pack":
			service = "git-upload-pack"
		case "service=git-receive-pack":
			service = "git-receive-pack"
		default:
			return "", http.StatusBadRequest
		}
		method = http.MethodGet
	default:
		i := strings.LastIndexByte(r.URL.Path, '/')
		repo = r.URL.Path[:i]
		endpoint := r.URL.Path[i+1:]
		if endpoint != "git-upload-pack" && endpoint != "git-receive-pack" {
			return "", http.StatusNotFound
		}
		if r.URL.RawQuery != "" || r.URL.ForceQuery {
			return "", http.StatusBadRequest
		}
		service, method = endpoint, http.MethodPost
	}
	if _, allowed := repos[strings.TrimSuffix(repo, ".git")]; !allowed {
		return "", http.StatusForbidden
	}
	if r.Method != method {
		return "", http.StatusMethodNotAllowed
	}
	if method == http.MethodGet && r.ContentLength != 0 {
		return "", http.StatusBadRequest
	}
	if method == http.MethodPost && r.Header.Get("Content-Type") != "application/x-"+service+"-request" {
		return "", http.StatusUnsupportedMediaType
	}
	return service, http.StatusOK
}
