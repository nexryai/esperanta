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

func TestReplaceableReadFileDirectory(t *testing.T) {
	targetDirectory := t.TempDir()
	target := filepath.Join(targetDirectory, "resolv.conf")
	if err := os.WriteFile(target, []byte("nameserver 192.0.2.1"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	got, err := replaceableReadFileDirectory(link)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(targetDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("read-file tree = %q; want resolved target parent %q", got, want)
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
