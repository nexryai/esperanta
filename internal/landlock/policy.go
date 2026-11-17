// Package landlock provides a deny-by-default sandbox for Esperanta.
//
// Callers describe application needs without handling kernel ABI versions,
// access masks or file descriptors. Restrict applies the policy to all threads
// and their future children. Failure is fatal to startup; there is no unsandboxed
// fallback. Rules cannot grant access denied by an existing sandbox.
//
// Landlock does not revoke already-open descriptors, restrict IP addresses, or
// mediate every filesystem operation (for example, stat). Callers must close
// unnecessary descriptors before restriction. TCP rules cover classic TCP;
// applications must disable Multipath TCP on their listeners and dialers.
package landlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// Policy is a one-shot sandbox configuration. Its zero value denies all
// supported filesystem and network operations. Do not copy a Policy.
type Policy struct {
	mu        sync.Mutex
	files     []string
	fileTrees []string
	ports     []uint16
	dns       bool
	frozen    bool
}

// AllowReadFile permits reading one existing regular file. It never permits
// execution, writes, directory listing or access to sibling files. Symlinks are
// resolved when Restrict builds the kernel policy; a replacement inode will not
// inherit the permission. Paths must be absolute and administrator-controlled.
func (p *Policy) AllowReadFile(path string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen {
		return errors.New("landlock policy is frozen")
	}
	if err := validateReadFile(path); err != nil {
		return err
	}
	p.files = append(p.files, path)
	return nil
}

// AllowPort permits outbound TCP connections to a destination port on any IP.
// It does not grant permission to bind a listener or use UDP.
func (p *Policy) AllowPort(port int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen {
		return errors.New("landlock policy is frozen")
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid destination port %d", port)
	}
	p.ports = append(p.ports, uint16(port))
	return nil
}

// AllowDNS permits reading the Go resolver's three configuration files and
// contacting DNS on port 53 (TCP and, where Landlock supports it, UDP).
// Resolver files may be atomically replaced while the process is running, so
// read-file access is granted to their resolved parent directories instead of
// only to their initial inodes. This also permits reading other files beneath
// those directories, subject to OS access controls, but not listing, writing or
// executing them. Missing resolver files remain inaccessible.
// Use a Go resolver with PreferGo enabled.
func (p *Policy) AllowDNS() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen {
		return errors.New("landlock policy is frozen")
	}

	var directories []string
	for _, path := range []string{"/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf"} {
		directory, err := replaceableReadFileDirectory(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if !slices.Contains(directories, directory) {
			directories = append(directories, directory)
		}
	}
	p.fileTrees = append(p.fileTrees, directories...)
	p.dns = true
	return nil
}

// replaceableReadFileDirectory returns the directory whose read-file rule will
// also cover replacement inodes at path.
func replaceableReadFileDirectory(path string) (string, error) {
	if err := validateReadFile(path); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve read permission for %q: %w", path, err)
	}
	return filepath.Dir(resolved), nil
}

// Restrict irreversibly enables the policy process-wide, including no_new_privs.
// At least Landlock ABI 4 (filesystem and TCP restrictions) is required. New
// TCP listeners are denied: open the service listener before calling Restrict,
// but do not accept requests until it succeeds. Treat any error as fatal.
func (p *Policy) Restrict() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen {
		return errors.New("landlock policy is frozen")
	}
	p.frozen = true

	for _, path := range p.files {
		if err := validateReadFile(path); err != nil {
			return err
		}
	}
	return p.restrict()
}

func validateReadFile(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("read permission requires an absolute file path: %q", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("read permission for %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("read permission requires a regular file: %q", path)
	}
	return nil
}
