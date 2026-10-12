package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexryai/esperanta/internal/config"
)

func localConfig(t testing.TB, address string) config.Config {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.AllowedSources = []string{"127.0.0.1"}
	c.AllowedPorts = []int{number}
	c.DenyPrivate = false
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestForwardHeadersAndConnectionReuse(t *testing.T) {
	var connections atomic.Int32
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, header := range []string{"Proxy-Authorization", "Proxy-Connection", "X-Hop", "X-Forwarded-For", "Forwarded"} {
			if r.Header.Get(header) != "" {
				t.Errorf("header leaked upstream: %s", header)
			}
		}
		if r.Host == "spoofed.invalid" {
			t.Error("spoofed Host forwarded")
		}
		w.Header().Set("Connection", "X-Response-Hop")
		w.Header().Set("X-Response-Hop", "secret")
		fmt.Fprint(w, r.URL.RequestURI())
	}))
	origin.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	origin.Start()
	defer origin.Close()

	p := New(localConfig(t, origin.Listener.Addr().String()))
	defer p.Close()
	server := httptest.NewServer(p)
	defer server.Close()
	client, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(3 * time.Second))
	reader := bufio.NewReader(client)
	for range 2 {
		// Write raw HTTP so the absolute target and Host are deliberately different.
		_, err := fmt.Fprintf(client,
			"GET %s/path?q=a%%2Fb HTTP/1.1\r\n"+
				"Host: spoofed.invalid\r\n"+
				"Connection: X-Hop\r\n"+
				"X-Hop: secret\r\n"+
				"Proxy-Authorization: secret\r\n"+
				"Proxy-Connection: secret\r\n"+
				"X-Forwarded-For: secret\r\n"+
				"Forwarded: secret\r\n\r\n", origin.URL)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || string(body) != "/path?q=a%2Fb" {
			t.Fatalf("unexpected response: %s %q %v", response.Status, body, err)
		}
		if response.Header.Get("X-Response-Hop") != "" {
			t.Error("response hop header leaked")
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("upstream connections = %d; want reuse", connections.Load())
	}
}

func TestPolicyAndOverload(t *testing.T) {
	c := localConfig(t, "127.0.0.1:8081")
	c.MaxConcurrent = 1
	p := New(c)
	defer p.Close()
	tests := []struct {
		name   string
		target string
		remote string
		status int
	}{
		{"denied source", "http://example.org:8081/", "192.0.2.1:1234", 403},
		{"origin form", "/", "127.0.0.1:1234", 400},
		{"denied port", "http://example.org:22/", "127.0.0.1:1234", 403},
		{"invalid scheme", "ftp://example.org:8081/", "127.0.0.1:1234", 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tt.target, nil)
			r.RemoteAddr = tt.remote
			r.Header.Set("X-Forwarded-For", "127.0.0.1")
			w := httptest.NewRecorder()
			p.ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("status = %d; want %d", w.Code, tt.status)
			}
		})
	}
	p.slots <- struct{}{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://example.org:8081/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	p.ServeHTTP(w, r)
	<-p.slots
	if w.Code != 503 {
		t.Fatalf("overload status = %d", w.Code)
	}
}

func TestPrivateDestinationPolicy(t *testing.T) {
	p := New(localConfig(t, "127.0.0.1:8081"))
	defer p.Close()
	p.c.DenyPrivate = true
	if conn, err := p.dial(context.Background(), "tcp", "127.0.0.1:8081"); err == nil {
		conn.Close()
		t.Fatal("private destination accepted")
	}
	for _, address := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "fc00::1", "fe80::1", "224.0.0.1", "198.18.0.1", "64:ff9b::7f00:1"} {
		if publicIP(netip.MustParseAddr(address)) {
			t.Errorf("non-public IP accepted: %s", address)
		}
	}
	for _, address := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !publicIP(netip.MustParseAddr(address)) {
			t.Errorf("public IP rejected: %s", address)
		}
	}
}

func TestHTTPSViaCONNECT(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(w, r.Body)
	}))
	defer origin.Close()
	p := New(localConfig(t, origin.Listener.Addr().String()))
	defer p.Close()
	server := httptest.NewServer(p)
	defer server.Close()
	proxyURL, _ := url.Parse(server.URL)
	transport := origin.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	const payload = "encrypted request body"
	response, err := client.Post(origin.URL, "text/plain", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || string(body) != payload {
		t.Fatalf("HTTPS transfer failed: %s %q %v", response.Status, body, err)
	}
}

func TestCONNECTBufferedDataAndShutdown(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	upstreamDone := make(chan struct{})
	go func() {
		defer close(upstreamDone)
		conn, err := upstream.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		io.Copy(conn, conn)
	}()

	p := New(localConfig(t, upstream.Addr().String()))
	defer p.Close()
	server := httptest.NewServer(p)
	defer server.Close()
	client, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	// Data in the same write as CONNECT may already be in the HTTP reader.
	fmt.Fprintf(client, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\nping", upstream.Addr(), upstream.Addr())
	reader := bufio.NewReader(client)
	response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("CONNECT response: %v %v", response, err)
	}
	payload := make([]byte, 4)
	if _, err := io.ReadFull(reader, payload); err != nil || string(payload) != "ping" {
		t.Fatalf("buffered data lost: %q %v", payload, err)
	}
	fmt.Fprint(client, "pong")
	if _, err := io.ReadFull(reader, payload); err != nil || string(payload) != "pong" {
		t.Fatalf("tunnel transfer failed: %q %v", payload, err)
	}
	p.Close()
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("tunnel still open after shutdown")
	}
	select {
	case <-upstreamDone:
	case <-time.After(time.Second):
		t.Fatal("upstream leaked after shutdown")
	}
}

func BenchmarkForward(b *testing.B) {
	payload := strings.Repeat("x", 32<<10)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, payload)
	}))
	defer origin.Close()
	p := New(localConfig(b, origin.Listener.Addr().String()))
	defer p.Close()
	server := httptest.NewServer(p)
	defer server.Close()
	proxyURL, _ := url.Parse(server.URL)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), MaxIdleConnsPerHost: 64}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			response, err := client.Get(origin.URL)
			if err != nil {
				b.Error(err)
				return
			}
			_, err = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 {
				b.Errorf("forward failed: %s %v", response.Status, err)
				return
			}
		}
	})
}
