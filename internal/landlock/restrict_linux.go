//go:build linux

package landlock

import (
	"fmt"

	lsm "github.com/landlock-lsm/go-landlock/landlock"
	llsys "github.com/landlock-lsm/go-landlock/landlock/syscall"
)

func (p *Policy) restrict() error {
	version, err := supportedVersion()
	if err != nil {
		return err
	}

	// Select an explicit supported policy. BestEffort can silently become a
	// no-op on unsupported kernels, which is unsuitable for this service.
	configs := map[int]lsm.Config{
		4:  lsm.V4,
		5:  lsm.V5,
		6:  lsm.V6,
		7:  lsm.V7,
		8:  lsm.V8,
		9:  lsm.V9,
		10: lsm.V10,
	}
	configuration := configs[min(version, 10)]
	rules := make([]lsm.Rule, 0, len(p.files)+len(p.ports)+3)
	for _, path := range p.files {
		// ROFiles also grants execute permission. Grant only read instead.
		rules = append(rules, lsm.PathAccess(llsys.AccessFSReadFile, path))
	}
	for _, port := range p.ports {
		rules = append(rules, lsm.ConnectTCP(port))
	}
	if p.dns {
		rules = append(rules, lsm.ConnectTCP(53))
		if version >= 10 {
			rules = append(rules, lsm.BindUDP(0), lsm.ConnectSendUDP(53))
		}
	}

	// go-landlock uses kernel TSYNC where available, and all-thread syscalls
	// otherwise (including cgo threads through libpsx). With cgo on ABI < 8,
	// it also needs permission to list /proc/self/task to synchronize threads.
	if err := configuration.Restrict(rules...); err != nil {
		return fmt.Errorf("enforce Landlock policy: %w", err)
	}
	return nil
}

func supportedVersion() (int, error) {
	version, err := llsys.LandlockGetABIVersion()
	if err != nil {
		return 0, fmt.Errorf("Landlock is required but unavailable: %w", err)
	}
	if version < 4 {
		return 0, fmt.Errorf("Landlock ABI %d lacks required TCP restrictions (need ABI 4+)", version)
	}
	if version >= 6 {
		// Match go-landlock's compatibility check: kernels missing the signal
		// scoping fix must use ABI 5 to avoid interfering with Go's runtime.
		errata, err := llsys.LandlockGetErrata()
		if err != nil || errata&2 == 0 {
			version = 5
		}
	}
	return version, nil
}
