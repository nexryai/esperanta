package proxy

import (
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

func (p *Proxy) connect(w http.ResponseWriter, r *http.Request) {
	host, port, err := net.SplitHostPort(r.Host)
	invalidTarget := err != nil || host == "" || strings.ContainsAny(host, "/\\@")
	hasBody := r.ContentLength > 0 || len(r.TransferEncoding) > 0
	if invalidTarget || hasBody {
		http.Error(w, "CONNECT requires host:port and no body", 400)
		return
	}
	if !p.c.AllowsPort(port) {
		http.Error(w, "destination port forbidden", 403)
		return
	}
	up, err := p.dial(r.Context(), "tcp", r.Host)
	if err != nil {
		http.Error(w, "upstream unavailable", 502)
		return
	}
	defer up.Close()
	h, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "HTTP/1 CONNECT required", 505)
		return
	}
	client, rw, err := h.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.tunnels[client] = up
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.tunnels, client)
		p.mu.Unlock()
	}()
	deadline := time.Now().Add(p.c.TunnelTimeout)
	client.SetDeadline(deadline)
	up.SetDeadline(deadline)
	if _, err = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err = rw.Flush(); err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		b := p.buffers.Get()
		defer p.buffers.Put(b)
		_, err := io.CopyBuffer(up, rw.Reader, b)
		if err != nil {
			up.Close()
			client.Close()
		} else if tcp, ok := up.(*net.TCPConn); ok {
			tcp.CloseWrite()
		}
		close(done)
	}()
	b := p.buffers.Get()
	_, _ = io.CopyBuffer(client, up, b)
	p.buffers.Put(b)
	client.Close()
	up.Close()
	<-done
}
