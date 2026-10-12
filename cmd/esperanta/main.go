package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/nexryai/esperanta/internal/config"
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
	ln, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
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
