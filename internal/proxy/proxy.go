package proxy

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"

	"github.com/nexryai/esperanta/internal/config"
)

type bufferPool struct {
	sync.Pool
}

func (p *bufferPool) Get() []byte {
	if b := p.Pool.Get(); b != nil {
		return b.([]byte)
	}
	return make([]byte, 32<<10)
}
func (p *bufferPool) Put(b []byte) {
	p.Pool.Put(b)
}

type Proxy struct {
	c             config.Config
	transport     *http.Transport
	forward       *httputil.ReverseProxy
	slots         chan struct{}
	buffers       bufferPool
	domainHeaders map[string]http.Header
	mu            sync.Mutex
	tunnels       map[net.Conn]net.Conn
	gitTransports []*http.Transport
	closed        bool
}

func New(c config.Config) *Proxy {
	p := &Proxy{
		c:             c,
		slots:         make(chan struct{}, c.MaxConcurrent),
		tunnels:       make(map[net.Conn]net.Conn),
		domainHeaders: make(map[string]http.Header, len(c.UpstreamHeaders)),
	}
	for domain, headers := range c.UpstreamHeaders {
		p.domainHeaders[config.CanonicalDomain(domain)] = headerSet(headers)
	}
	p.transport = &http.Transport{
		Proxy:                  nil,
		DialContext:            p.dial,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           c.MaxIdleConns,
		MaxIdleConnsPerHost:    c.MaxIdlePerHost,
		MaxConnsPerHost:        c.MaxConcurrent,
		IdleConnTimeout:        c.IdleTimeout,
		TLSHandshakeTimeout:    c.DialTimeout,
		ResponseHeaderTimeout:  c.HeaderTimeout,
		ExpectContinueTimeout:  time.Second,
		DisableCompression:     true,
		MaxResponseHeaderBytes: 64 << 10,
	}
	p.forward = p.newForward(func(r *httputil.ProxyRequest) {
		r.Out.Host = r.In.URL.Host
		applyHeaders(r.Out.Header, p.domainHeaders[config.CanonicalDomain(r.In.URL.Hostname())])
	})
	return p
}

func (p *Proxy) newForward(rewrite func(*httputil.ProxyRequest)) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Transport:  p.transport,
		BufferPool: &p.buffers,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.Header.Del("Proxy-Authorization")
			r.Out.Header.Del("Proxy-Connection")
			rewrite(r)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Debug("upstream failed", "error", err)
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		},
	}
}

func applyHeaders(dst, src http.Header) {
	for name, value := range src {
		dst[name] = value
	}
}

func headerSet(values map[string]string) http.Header {
	headers := make(http.Header, len(values))
	for name, value := range values {
		headers.Set(name, value)
	}
	return headers
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.serve(w, r, p.serveProxy)
}

func (p *Proxy) serve(w http.ResponseWriter, r *http.Request, handler http.HandlerFunc) {
	if !p.c.AllowsSource(r.RemoteAddr) {
		http.Error(w, "forbidden", 403)
		return
	}
	select {
	case p.slots <- struct{}{}:
		defer func() {
			<-p.slots
		}()
	default:
		http.Error(w, "proxy busy", 503)
		return
	}
	handler(w, r)
}

func (p *Proxy) serveProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.connect(w, r)
		return
	}
	validScheme := r.URL.Scheme == "http" || r.URL.Scheme == "https"
	if !r.URL.IsAbs() || !validScheme || r.URL.Hostname() == "" ||
		r.URL.User != nil || r.URL.Fragment != "" {
		http.Error(w, "absolute HTTP(S) URL required", 400)
		return
	}
	port := r.URL.Port()
	if port == "" {
		port = "80"
		if r.URL.Scheme == "https" {
			port = "443"
		}
	}
	if !p.c.AllowsPort(port) {
		http.Error(w, "destination port forbidden", 403)
		return
	}
	if r.Header.Get("Upgrade") != "" {
		http.Error(w, "use CONNECT for protocol upgrades", 400)
		return
	}
	p.forwardRequest(w, r, p.forward)
}

func (p *Proxy) forwardRequest(w http.ResponseWriter, r *http.Request, handler http.Handler) {
	ctx, cancel := context.WithTimeout(r.Context(), p.c.RequestTimeout)
	defer cancel()
	handler.ServeHTTP(w, r.WithContext(ctx))
}

// Close also terminates hijacked connections, which http.Server.Shutdown ignores.
func (p *Proxy) Close() {
	p.mu.Lock()
	p.closed = true
	for client, up := range p.tunnels {
		client.Close()
		up.Close()
	}
	for _, transport := range p.gitTransports {
		transport.CloseIdleConnections()
	}
	p.mu.Unlock()
	p.transport.CloseIdleConnections()
}
