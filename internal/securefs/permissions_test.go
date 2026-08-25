//go:build !windows

package securefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRestrictCurrentUserRepairsUnixPermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(root, 0o777); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	file := filepath.Join(root, "fixture")
	if err := os.WriteFile(file, []byte("fixture"), 0o666); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Chmod(root, 0o777); err != nil {
		t.Fatalf("Chmod(root) error = %v", err)
	}
	if err := os.Chmod(file, 0o666); err != nil {
		t.Fatalf("Chmod(file) error = %v", err)
	}
	if err := ValidateDirectory(root); err == nil {
		t.Fatal("ValidateDirectory(permissive) error = nil, want refusal")
	}
	if err := ValidateFile(file); err == nil {
		t.Fatal("ValidateFile(permissive) error = nil, want refusal")
	}

	if err := RestrictDirectory(root); err != nil {
		t.Fatalf("RestrictDirectory() error = %v", err)
	}
	if err := RestrictFile(file); err != nil {
		t.Fatalf("RestrictFile() error = %v", err)
	}
	assertMode(t, root, 0o700)
	assertMode(t, file, 0o600)
	if err := ValidateDirectory(root); err != nil {
		t.Fatalf("ValidateDirectory(restricted) error = %v", err)
	}
	if err := ValidateFile(file); err != nil {
		t.Fatalf("ValidateFile(restricted) error = %v", err)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode(%q) = %o, want %o", path, got, want)
	}
}
