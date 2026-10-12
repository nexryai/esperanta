//go:build linux && integration

package main

import (
	"bufio"
	"context"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestNonSnapProxy(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("run non-Snap integration tests as a regular user")
	}
	binary := filepath.Join(t.TempDir(), "esperanta")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build proxy: %v\n%s", err, output)
	}

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "HTTP works")
	}))
	defer origin.Close()
	tlsOrigin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "HTTPS works")
	}))
	defer tlsOrigin.Close()
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request reached a forbidden destination port")
	}))
	defer forbidden.Close()
	ports := []int{addressPort(t, origin.Listener.Addr().String()), addressPort(t, tlsOrigin.Listener.Addr().String())}

	t.Run("HTTP_DNS_TLS_CONNECT_and_denials", func(t *testing.T) {
		directory := t.TempDir()
		certificate := filepath.Join(directory, "root.pem")
		encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsOrigin.Certificate().Raw})
		if err := os.WriteFile(certificate, encoded, 0600); err != nil {
			t.Fatal(err)
		}
		address := unusedAddress(t)
		configuration := writeConfig(t, address, ports, "127.0.0.1")
		service := startService(t, binary, configuration,
			"SSL_CERT_FILE="+certificate, "SSL_CERT_DIR="+directory)
		service.waitReady(t, address)

		// Trust roots must already be cached. Removing the source also catches
		// attempts to defer certificate loading until the first HTTPS request.
		if err := os.Remove(certificate); err != nil {
			t.Fatal(err)
		}
		for _, request := range []struct {
			target string
			body   string
		}{
			{origin.URL, "HTTP works"},
			{strings.Replace(origin.URL, "127.0.0.1", "localhost", 1), "HTTP works"},
			{tlsOrigin.URL, "HTTPS works"},
		} {
			status, body, err := throughProxy(address, "GET", request.target)
			if err != nil || status != 200 || body != request.body {
				t.Fatalf("%s through sandbox: status=%d body=%q error=%v", request.target, status, body, err)
			}
		}

		proxyURL, err := url.Parse("http://" + address)
		if err != nil {
			t.Fatal(err)
		}
		transport := tlsOrigin.Client().Transport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyURL(proxyURL)
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
		response, err := client.Get(tlsOrigin.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(body) != "HTTPS works" {
			t.Fatalf("CONNECT TLS transfer: %q %v", body, err)
		}
		for _, request := range []struct {
			method string
			target string
		}{
			{"GET", forbidden.URL},
			{"CONNECT", forbidden.Listener.Addr().String()},
		} {
			status, _, err := throughProxy(address, request.method, request.target)
			if err != nil || status != 403 {
				t.Fatalf("forbidden %s: status=%d error=%v", request.method, status, err)
			}
		}
		service.stop(t)
		if !strings.Contains(service.logs(t), "Landlock sandbox enabled") {
			t.Fatal("proxy did not report sandbox enforcement")
		}
	})

	t.Run("source_rejected", func(t *testing.T) {
		address := unusedAddress(t)
		configuration := writeConfig(t, address, ports, "192.0.2.1")
		service := startService(t, binary, configuration)
		// The listener exists, but never accepts an HTTP request from loopback.
		service.waitReady(t, address)
		if _, _, err := throughProxy(address, "GET", origin.URL); err == nil {
			t.Fatal("unauthorized source received an HTTP response")
		}
	})

	t.Run("unavailable_sandbox_fails_startup", func(t *testing.T) {
		address := unusedAddress(t)
		configuration := writeConfig(t, address, ports, "127.0.0.1")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNonSnapUnavailableHelper$")
		command.Env = append(os.Environ(), "ESPERANTA_UNAVAILABLE_BINARY="+binary,
			"ESPERANTA_UNAVAILABLE_CONFIG="+configuration)
		output, err := command.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "Landlock is required but unavailable") {
			t.Fatalf("unavailable Landlock did not fail closed: %v\n%s", err, output)
		}
		connection, err := net.DialTimeout("tcp", address, time.Second)
		if connection != nil {
			connection.Close()
		}
		if err == nil {
			t.Fatal("proxy was listening after failed sandbox setup")
		}
	})
}

func TestNonSnapUnavailableHelper(t *testing.T) {
	binary := os.Getenv("ESPERANTA_UNAVAILABLE_BINARY")
	if binary == "" {
		return
	}
	// Install before exec, so the real binary and all of its runtime threads
	// inherit a filter that makes Landlock unavailable.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	filters := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: unix.SYS_LANDLOCK_CREATE_RULESET, Jf: 1},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.ENOSYS)},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
	}
	program := unix.SockFprog{Len: uint16(len(filters)), Filter: &filters[0]}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	_, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, 0,
		uintptr(unsafe.Pointer(&program)))
	runtime.KeepAlive(filters)
	if errno != 0 {
		t.Fatal(errno)
	}
	configuration := os.Getenv("ESPERANTA_UNAVAILABLE_CONFIG")
	if err := unix.Exec(binary, []string{binary, "--config", configuration}, os.Environ()); err != nil {
		t.Fatal(err)
	}
}

type serviceProcess struct {
	command *exec.Cmd
	logFile *os.File
	done    chan error
	stopped bool
}

func startService(t *testing.T, binary, configuration string, environment ...string) *serviceProcess {
	t.Helper()
	logFile, err := os.Create(filepath.Join(t.TempDir(), "proxy.log"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "--config", configuration)
	command.Env = append(os.Environ(), environment...)
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		logFile.Close()
		t.Fatal(err)
	}
	service := &serviceProcess{command: command, logFile: logFile, done: make(chan error, 1)}
	go func() {
		service.done <- command.Wait()
	}()
	t.Cleanup(func() {
		service.stop(t)
		logFile.Close()
	})
	return service
}

func (s *serviceProcess) waitReady(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-s.done:
			s.stopped = true
			t.Fatalf("proxy exited before ready: %v\n%s", err, s.logs(t))
		default:
		}
		// A TCP listener can exist briefly before restriction, so require the
		// successful-enforcement log as well before exercising the service.
		if strings.Contains(s.logs(t), "proxy listening") {
			connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
			if err == nil {
				connection.Close()
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("proxy startup timed out\n%s", s.logs(t))
}

func (s *serviceProcess) stop(t *testing.T) {
	t.Helper()
	if s.stopped {
		return
	}
	s.stopped = true
	s.command.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-s.done:
		if err != nil {
			t.Errorf("proxy shutdown: %v\n%s", err, s.logs(t))
		}
	case <-time.After(5 * time.Second):
		s.command.Process.Kill()
		<-s.done
		t.Errorf("proxy did not exit on SIGTERM\n%s", s.logs(t))
	}
}

func (s *serviceProcess) logs(t *testing.T) string {
	t.Helper()
	output, err := os.ReadFile(s.logFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

func unusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	return address
}

func addressPort(t *testing.T, address string) int {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	value, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func writeConfig(t *testing.T, address string, ports []int, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	configuration := fmt.Sprintf("listen: %q\nallowed_sources: [%q]\nallowed_ports: [%d, %d]\ndeny_private_destinations: false\n", address, source, ports[0], ports[1])
	if err := os.WriteFile(path, []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func throughProxy(address, method, target string) (int, string, error) {
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		return 0, "", err
	}
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(3 * time.Second))
	host := target
	if method != http.MethodConnect {
		parsed, err := url.Parse(target)
		if err != nil {
			return 0, "", err
		}
		host = parsed.Host
	}
	if _, err := fmt.Fprintf(connection, "%s %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", method, target, host); err != nil {
		return 0, "", err
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: method})
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return response.StatusCode, string(body), err
}
