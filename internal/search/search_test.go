package search

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSearchFindsMatchesInConfiguredScopes(t *testing.T) {
	tempDir := t.TempDir()
	referencesDir := filepath.Join(tempDir, "references")
	if err := os.MkdirAll(referencesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "README.md"), []byte("Slack attachment flow\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(referencesDir, "notes.md"), []byte("attachment staging contract\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	matches, err := Search(Options{
		Root:       tempDir,
		Query:      "attachment",
		IgnoreCase: true,
		Limit:      10,
		Scopes:     DefaultScopes(),
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("len(matches) = %d, want 2", len(matches))
	}
}
