//go:build windows

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/relux-works/skill-slack-management/internal/securefs"
)

func TestWriteFileConfigAppliesCurrentUserOnlyWindowsProtection(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "slack-mgmt")
	path := filepath.Join(dir, "auth.json")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"access_token":"xoxb-permissive-fixture"}`), 0o666); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := securefs.ValidateDirectory(dir); err == nil {
		t.Fatal("ValidateDirectory(permissive fixture) error = nil, want refusal")
	}
	if err := securefs.ValidateFile(path); err == nil {
		t.Fatal("ValidateFile(permissive fixture) error = nil, want refusal")
	}

	if err := WriteFileConfig(path, FileConfig{Profiles: map[string]FileProfile{
		"acme": {AccessToken: "xoxb-windows-protection"},
	}}); err != nil {
		t.Fatalf("WriteFileConfig() error = %v", err)
	}
	if err := securefs.ValidateDirectory(dir); err != nil {
		t.Fatalf("WriteFileConfig() directory protection = %v", err)
	}
	if err := securefs.ValidateFile(path); err != nil {
		t.Fatalf("WriteFileConfig() file protection = %v", err)
	}
}
