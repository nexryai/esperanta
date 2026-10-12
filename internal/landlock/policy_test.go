package landlock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPolicyRejectsInvalidGrants(t *testing.T) {
	var policy Policy
	for _, port := range []int{-1, 0, 65536} {
		if err := policy.AllowPort(port); err == nil {
			t.Errorf("accepted invalid port %d", port)
		}
	}
	for _, path := range []string{"", "relative.txt", t.TempDir(), "/does-not-exist/esperanta"} {
		if err := policy.AllowReadFile(path); err == nil {
			t.Errorf("accepted invalid read grant %q", path)
		}
	}
	if err := policy.AllowPort(443); err != nil {
		t.Fatal(err)
	}
}

func TestRestrictionRejectsDisappearingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "configuration")
	if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	var policy Policy
	if err := policy.AllowReadFile(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := policy.Restrict(); err == nil {
		t.Fatal("missing read grant silently ignored")
	}
	if err := policy.AllowPort(80); err == nil {
		t.Fatal("policy could be changed after an enforcement attempt")
	}
}
