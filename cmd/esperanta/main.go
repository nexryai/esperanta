package main

import (
	"context"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/nexryai/esperanta/internal/config"
	"github.com/nexryai/esperanta/internal/landlock"
	"github.com/nexryai/esperanta/internal/proxy"
)

func main() {
	if err := run(); err != nil {
		slog.Error("esperanta stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	path := flag.String("config", "config.yaml", "YAML configuration path")
	check := flag.Bool("check", false, "validate configuration and exit")
	flag.Parse()
	if os.Getuid() == 0 || os.Geteuid() == 0 {
		return errors.New("refusing to run as root; launch as an unprivileged user")
	}
	c, err := config.Load(*path)
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	if *check {
		fmt.Println("configuration valid")
		return nil
	}
	// Load trust roots and timezone data before filesystem access is restricted.
	// The proxy needs no runtime access to certificates or its YAML file.
	if _, err := x509.SystemCertPool(); err != nil {
		return fmt.Errorf("load system trust roots: %w", err)
	}
	time.Now().In(time.Local).Zone()

	var policy landlock.Policy
	for _, port := range c.AllowedPorts {
		if err := policy.AllowPort(port); err != nil {
			return err
		}
	}
	if err := policy.AllowDNS(); err != nil {
		return err
	}

	// Landlock does not mediate Multipath TCP. Use classic TCP explicitly,
	// even when GODEBUG or a future Go release enables MPTCP by default.
	var listenConfig net.ListenConfig
	listenConfig.SetMultipathTCP(false)
	addresses := []string{c.Listen}
	host, _, _ := net.SplitHostPort(c.Listen) // validated by config.Load
	for _, target := range c.RequestTargets {
		addresses = append(addresses, net.JoinHostPort(host, strconv.Itoa(target.Port)))
	}
	slots := make(chan struct{}, c.MaxConnections)
	listeners := make([]*proxy.Listener, 0, len(addresses))
	for _, address := range addresses {
		ln, err := listenConfig.Listen(context.Background(), "tcp", address)
		if err != nil {
			return fmt.Errorf("listen on %s: %w", address, err)
		}
		defer ln.Close()
		listeners = append(listeners, &proxy.Listener{Listener: ln, Config: c, Slots: slots})
	}
	if err := policy.Restrict(); err != nil {
		return err
	}
	slog.Info("Landlock sandbox enabled")
	p := proxy.New(c)
	defer p.Close()
	var handler http.Handler = p
	if len(c.RequestTargets) > 0 {
		targets := make(map[int]http.Handler, len(c.RequestTargets))
		for _, target := range c.RequestTargets {
			targets[target.Port] = p.RequestTarget(target)
		}
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Select by the accepted socket, never by client-provided Host.
			local := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
			if target := targets[local.Port]; target != nil {
				target.ServeHTTP(w, r)
				return
			}
			p.ServeHTTP(w, r)
		})
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: c.HeaderTimeout,
		ReadTimeout:       c.RequestTimeout,
		WriteTimeout:      c.RequestTimeout,
		IdleTimeout:       c.IdleTimeout,
		MaxHeaderBytes:    32 << 10,
	}
	defer srv.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan error, len(listeners))
	for _, listener := range listeners {
		go func() {
			done <- srv.Serve(listener)
		}()
		slog.Info("proxy listening", "address", listener.Addr().String(), "uid", os.Geteuid())
	}
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	p.Close()
	shutdown, cancel := context.WithTimeout(context.Background(), c.ShutdownTimeout)
	defer cancel()
	if err = srv.Shutdown(shutdown); err != nil {
		return err
	}
	return nil
}
