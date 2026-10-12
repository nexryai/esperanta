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
	ln, err := listenConfig.Listen(context.Background(), "tcp", c.Listen)
	if err != nil {
		return err
	}
	defer ln.Close()
	if err := policy.Restrict(); err != nil {
		return err
	}
	slog.Info("Landlock sandbox enabled")
	listener := &proxy.Listener{
		Listener: ln,
		Config:   c,
		Slots:    make(chan struct{}, c.MaxConnections),
	}
	p := proxy.New(c)
	defer p.Close()
	srv := &http.Server{
		Handler:           p,
		ReadHeaderTimeout: c.HeaderTimeout,
		ReadTimeout:       c.RequestTimeout,
		WriteTimeout:      c.RequestTimeout,
		IdleTimeout:       c.IdleTimeout,
		MaxHeaderBytes:    32 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(listener)
	}()
	slog.Info("proxy listening", "address", c.Listen, "uid", os.Geteuid())
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
		srv.Close()
		return err
	}
	return nil
}
