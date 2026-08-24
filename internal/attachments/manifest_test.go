package attachments

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManifestSupportsArrayAndNormalizesPath(t *testing.T) {
	tempDir := t.TempDir()
	manifestPath := filepath.Join(tempDir, "agents-attachments-manifest.json")
	localDir := filepath.Join(tempDir, "files")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	content := `[{"id":"att-1","name":"trace.log","mime_type":"text/plain","size_bytes":12,"local_path":"files/trace.log"}]`
	if err := os.WriteFile(manifestPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, path, err := LoadManifest(manifestPath, nil)
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	if path != manifestPath {
		t.Fatalf("manifest path = %q, want %q", path, manifestPath)
	}
	if len(got) != 1 {
		t.Fatalf("len(attachments) = %d, want 1", len(got))
	}
	if got[0].LocalPath != filepath.Join(tempDir, "files", "trace.log") {
		t.Fatalf("LocalPath = %q", got[0].LocalPath)
	}
}

func TestLoadManifestSupportsEnvelope(t *testing.T) {
	tempDir := t.TempDir()
	manifestPath := filepath.Join(tempDir, "agents-attachments-manifest.json")
	content := `{"attachments":[{"id":"att-1","name":"image.png","local_path":"/tmp/image.png"}]}`
	if err := os.WriteFile(manifestPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, _, err := LoadManifest(manifestPath, nil)
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "att-1" {
		t.Fatalf("attachments = %#v", got)
	}
}

func TestLoadManifestReturnsSentinelWhenDefaultIsMissing(t *testing.T) {
	_, _, err := LoadManifest("", func(string) string { return "" })
	if err != ErrManifestNotFound {
		t.Fatalf("LoadManifest() error = %v, want %v", err, ErrManifestNotFound)
	}
}

func TestFindAttachmentMatchesIDNameAndBasename(t *testing.T) {
	attachments := []Attachment{
		{ID: "att-1", Name: "trace.log", LocalPath: "/tmp/a/trace.log"},
	}

	for _, ref := range []string{"att-1", "trace.log"} {
		got, err := FindAttachment(attachments, ref)
		if err != nil {
			t.Fatalf("FindAttachment(%q) error = %v", ref, err)
		}
		if got.ID != "att-1" {
			t.Fatalf("FindAttachment(%q) = %#v", ref, got)
		}
	}
}
