package securefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRestrictCurrentUserAppliesProtectedSingleUserDACL(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(root, 0o777); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	file := filepath.Join(root, "fixture")
	if err := os.WriteFile(file, []byte("fixture"), 0o666); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := ValidateDirectory(root); err == nil {
		t.Fatal("ValidateDirectory(inherited DACL) error = nil, want refusal")
	}
	if err := ValidateFile(file); err == nil {
		t.Fatal("ValidateFile(inherited DACL) error = nil, want refusal")
	}

	if err := RestrictDirectory(root); err != nil {
		t.Fatalf("RestrictDirectory() error = %v", err)
	}
	if err := RestrictFile(file); err != nil {
		t.Fatalf("RestrictFile() error = %v", err)
	}
	if err := ValidateDirectory(root); err != nil {
		t.Fatalf("ValidateDirectory(restricted) error = %v", err)
	}
	if err := ValidateFile(file); err != nil {
		t.Fatalf("ValidateFile(restricted) error = %v", err)
	}
}
