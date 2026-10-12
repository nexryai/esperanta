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
	c         config.Config
	transport *http.Transport
	forward   *httputil.ReverseProxy
	slots     chan struct{}
	buffers   bufferPool
	mu        sync.Mutex
	tunnels   map[net.Conn]net.Conn
	closed    bool
}

func New(c config.Config) *Proxy {
	p := &Proxy{
		c:       c,
		slots:   make(chan struct{}, c.MaxConcurrent),
		tunnels: make(map[net.Conn]net.Conn),
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
	p.forward = &httputil.ReverseProxy{
		Transport:  p.transport,
		BufferPool: &p.buffers,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.Host = r.In.URL.Host
			r.Out.Header.Del("Proxy-Authorization")
			r.Out.Header.Del("Proxy-Connection")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Debug("upstream failed", "error", err)
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		},
	}
	return p
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	ctx, cancel := context.WithTimeout(r.Context(), p.c.RequestTimeout)
	defer cancel()
	p.forward.ServeHTTP(w, r.WithContext(ctx))
}

// Close also terminates hijacked connections, which http.Server.Shutdown ignores.
func (p *Proxy) Close() {
	p.mu.Lock()
	p.closed = true
	for client, up := range p.tunnels {
		client.Close()
		up.Close()
	}
	p.mu.Unlock()
	p.transport.CloseIdleConnections()
}
