package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/netip"
	"strings"

	"github.com/nexryai/esperanta/internal/config"
)

// RequestTarget serves a dedicated listener backed by a single HTTPS origin.
func (p *Proxy) RequestTarget(target config.RequestTarget) http.Handler {
	if target.Type == config.RequestTargetGit {
		return p.gitTarget(target)
	}
	host := config.CanonicalDomain(target.UpstreamHost)
	if ip, err := netip.ParseAddr(host); err == nil && ip.Is6() {
		host = "[" + host + "]"
	}
	headers := headerSet(target.UpstreamHeaders)
	forward := p.newForward(func(r *httputil.ProxyRequest) {
		r.Out.URL.Scheme = "https"
		r.Out.URL.Host = host
		r.Out.Host = host
		applyHeaders(r.Out.Header, headers)
	})
	return p.serveTarget(forward)
}

func (p *Proxy) serveTarget(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.serve(w, r, func(w http.ResponseWriter, r *http.Request) {
			// TRACE could reflect the injected credentials back to the client.
			if r.Method == http.MethodConnect || r.Method == http.MethodTrace || r.Header.Get("Upgrade") != "" {
				http.Error(w, "CONNECT, TRACE and protocol upgrades are not supported on this listener", http.StatusMethodNotAllowed)
				return
			}
			if !strings.HasPrefix(r.RequestURI, "/") || r.URL.IsAbs() || r.URL.Host != "" {
				http.Error(w, "origin-form request target required", http.StatusBadRequest)
				return
			}
			p.forwardRequest(w, r, handler)
		})
	})
}
