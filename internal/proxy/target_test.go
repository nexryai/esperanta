package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexryai/esperanta/internal/config"
)

func TestRequestTarget(t *testing.T) {
	const host = "example.com"
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host || r.TLS.ServerName != host {
			t.Errorf("Host = %q, TLS server name = %q", r.Host, r.TLS.ServerName)
		}
		if r.Method != http.MethodPost || r.URL.RequestURI() != "/a%2Fb?q=a%2Bb" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
		}
		if got := r.Header.Values("CF-Access-Client-Secret"); len(got) != 1 || got[0] != "target-secret" {
			t.Error("configured header did not replace client header")
		}
		if r.Header.Get("X-Forward-Only") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("forward proxy header leaked to target")
		}
		io.Copy(w, r.Body)
	}))
	defer origin.Close()
	c := config.Defaults()
	c.AllowedSources = []string{"127.0.0.1"}
	c.UpstreamHeaders = map[string]map[string]string{host: {"X-Forward-Only": "forward"}}
	c.RequestTargets = []config.RequestTarget{{
		Port: 8081, UpstreamHost: "EXAMPLE.COM.",
		UpstreamHeaders: map[string]string{"CF-Access-Client-Secret": "target-secret"},
	}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	p := New(c)
	defer p.Close()
	p.transport.TLSClientConfig = origin.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	p.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "example.com:443" {
			return nil, fmt.Errorf("unexpected upstream address %q", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, origin.Listener.Addr().String())
	}
	handler := p.RequestTarget(c.RequestTargets[0])
	for _, tt := range []struct {
		method, target, remote string
		status                 int
	}{
		{"GET", "http://example.com/a", "127.0.0.1:1234", 400},
		{"GET", "https://other.example.com/a", "127.0.0.1:1234", 400},
		{"CONNECT", "example.com:443", "127.0.0.1:1234", 405},
		{"TRACE", "/", "127.0.0.1:1234", 405},
		{"GET", "/", "192.0.2.1:1234", 403},
		{"POST", "/a%2Fb?q=a%2Bb", "127.0.0.1:1234", 200},
	} {
		r := httptest.NewRequest(tt.method, tt.target, strings.NewReader("payload"))
		r.RemoteAddr = tt.remote
		r.Host = "other.example.com"
		r.Header.Set("CF-Access-Client-Secret", "client-value")
		r.Header.Set("Proxy-Authorization", "client-value")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tt.status || tt.status == 200 && w.Body.String() != "payload" {
			t.Errorf("%s %s: got %d %q, want status %d", tt.method, tt.target, w.Code, w.Body, tt.status)
		}
	}
}
