//go:build linux

package landlock

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The policy is irreversible and process-wide, so each scenario runs in a
// separate process. The parent owns fixtures/listeners and remains unrestricted.
func TestKernelSandbox(t *testing.T) {
	version, err := supportedVersion()
	if err != nil {
		if os.Getenv("ESPERANTA_REQUIRE_LANDLOCK") == "1" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	t.Logf("testing real kernel enforcement with Landlock ABI %d", version)

	allowed := testListener(t)
	defer allowed.Close()
	denied := testListener(t)
	defer denied.Close()
	directory := t.TempDir()
	for _, name := range []string{"allowed", "denied"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(directory, "denied"), filepath.Join(directory, "escape")); err != nil {
		t.Fatal(err)
	}

	for _, scenario := range []string{"filesystem", "network", "threads", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSandboxHelper$", "-test.v")
			command.Env = append(os.Environ(),
				"ESPERANTA_SANDBOX_TEST="+scenario,
				"ESPERANTA_TEST_DIRECTORY="+directory,
				"ESPERANTA_ALLOWED_ADDRESS="+allowed.Addr().String(),
				"ESPERANTA_DENIED_ADDRESS="+denied.Addr().String(),
			)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("sandbox child: %v\n%s", err, output)
			}
			t.Log(string(output))
		})
	}
}

func TestSandboxHelper(t *testing.T) {
	scenario := os.Getenv("ESPERANTA_SANDBOX_TEST")
	if scenario == "" {
		return
	}
	if os.Geteuid() == 0 {
		t.Fatal("kernel sandbox tests must run as a non-root user")
	}
	if scenario == "unavailable" {
		testUnavailable(t)
		return
	}

	var policy Policy
	directory := os.Getenv("ESPERANTA_TEST_DIRECTORY")
	allowedPath := filepath.Join(directory, "allowed")
	if err := policy.AllowReadFile(allowedPath); err != nil {
		t.Fatal(err)
	}
	// Reading an executable must not implicitly grant execute permission.
	if err := policy.AllowReadFile(os.Args[0]); err != nil {
		t.Fatal(err)
	}
	allowed := os.Getenv("ESPERANTA_ALLOWED_ADDRESS")
	_, portString, err := net.SplitHostPort(allowed)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.AllowPort(port); err != nil {
		t.Fatal(err)
	}

	// Pin workers before enforcement so this catches thread-local-only policies.
	const workers = 6
	var ready sync.WaitGroup
	var finished sync.WaitGroup
	release := make(chan struct{})
	if scenario == "threads" {
		for range workers {
			ready.Add(1)
			finished.Add(1)
			go func() {
				runtime.LockOSThread()
				defer runtime.UnlockOSThread()
				defer finished.Done()
				ready.Done()
				<-release
				checkThreadRestrictions(t, directory)
			}()
		}
		ready.Wait()
	}
	if err := policy.Restrict(); err != nil {
		t.Fatal(err)
	}
	if err := policy.AllowPort(12345); err == nil {
		t.Fatal("frozen policy accepted a new port")
	}
	if err := policy.AllowReadFile(allowedPath); err == nil {
		t.Fatal("frozen policy accepted a new file")
	}
	if err := policy.AllowDNS(); err == nil {
		t.Fatal("frozen policy accepted DNS permission")
	}
	if err := policy.Restrict(); err == nil {
		t.Fatal("policy could be applied twice")
	}

	switch scenario {
	case "filesystem":
		checkFilesystem(t, directory)
	case "network":
		connection, err := classicDial(allowed)
		if err != nil {
			t.Fatalf("allowed TCP connection failed: %v", err)
		}
		connection.Close()
		checkDeniedConnection(t)
		var listenConfig net.ListenConfig
		listenConfig.SetMultipathTCP(false)
		listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
		if listener != nil {
			listener.Close()
		}
		requireDenied(t, "bind new TCP listener", err)
	case "threads":
		close(release)
		finished.Wait()
		for range workers {
			finished.Add(1)
			go func() {
				defer finished.Done()
				checkThreadRestrictions(t, directory)
			}()
		}
		finished.Wait()
	default:
		t.Fatalf("unknown sandbox test %q", scenario)
	}
}

func checkFilesystem(t *testing.T, directory string) {
	t.Helper()
	allowed := filepath.Join(directory, "allowed")
	contents, err := os.ReadFile(allowed)
	if err != nil || string(contents) != "allowed" {
		t.Fatalf("allowed read: %q %v", contents, err)
	}
	for _, name := range []string{"denied", "escape"} {
		_, err := os.ReadFile(filepath.Join(directory, name))
		requireDenied(t, "read "+name, err)
	}
	_, err = os.ReadDir(directory)
	requireDenied(t, "list parent directory", err)
	requireDenied(t, "write allowed file", os.WriteFile(allowed, []byte("overwritten"), 0600))
	requireDenied(t, "truncate allowed file", os.Truncate(allowed, 0))
	requireDenied(t, "create file", os.WriteFile(filepath.Join(directory, "new"), nil, 0600))
	requireDenied(t, "remove allowed file", os.Remove(allowed))
	requireDenied(t, "rename allowed file", os.Rename(allowed, filepath.Join(directory, "moved")))

	command := exec.Command(os.Args[0], "-test.run=^$")
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	requireDenied(t, "execute readable file", command.Run())
}

func checkThreadRestrictions(t *testing.T, directory string) {
	t.Helper()
	_, err := os.ReadFile(filepath.Join(directory, "denied"))
	requireDenied(t, "thread file read", err)
	checkDeniedConnection(t)
	value, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if err != nil || value != 1 {
		t.Errorf("no_new_privs = %d, %v", value, err)
	}
}

func checkDeniedConnection(t *testing.T) {
	t.Helper()
	connection, err := classicDial(os.Getenv("ESPERANTA_DENIED_ADDRESS"))
	if connection != nil {
		connection.Close()
	}
	requireDenied(t, "connect to forbidden TCP port", err)
}

func requireDenied(t *testing.T, operation string, err error) {
	t.Helper()
	if !errors.Is(err, os.ErrPermission) {
		t.Errorf("%s: want permission denied, got %v", operation, err)
	}
}

func classicDial(address string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: time.Second}
	dialer.SetMultipathTCP(false)
	return dialer.Dial("tcp", address)
}

func testListener(t *testing.T) net.Listener {
	t.Helper()
	var config net.ListenConfig
	config.SetMultipathTCP(false)
	listener, err := config.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			connection.Close()
		}
	}()
	return listener
}

func testUnavailable(t *testing.T) {
	t.Helper()
	// Emulate a kernel or outer sandbox that disables Landlock. This changes
	// real syscall behavior in the child rather than mocking the policy API.
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
	result, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER,
		unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&program)))
	runtime.KeepAlive(filters)
	if errno != 0 {
		t.Fatal(errno)
	}
	if result != 0 {
		t.Fatalf("seccomp thread synchronization failed for thread %d", result)
	}
	var policy Policy
	if err := policy.Restrict(); !errors.Is(err, unix.ENOSYS) {
		t.Fatal(fmt.Errorf("unsupported kernel must fail closed: %w", err))
	}
}
