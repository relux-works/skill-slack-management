package attachments

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveInputPrefersExistingPath(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "trace.log")
	if err := os.WriteFile(sourcePath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := ResolveInput(sourcePath, "", nil)
	if err != nil {
		t.Fatalf("ResolveInput() error = %v", err)
	}
	if got.ResolvedFrom != "path" {
		t.Fatalf("ResolvedFrom = %q, want path", got.ResolvedFrom)
	}
	if got.SizeBytes != 5 {
		t.Fatalf("SizeBytes = %d, want 5", got.SizeBytes)
	}
}

func TestStageCopiesAttachmentToDestinationDirectory(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "trace.log")
	if err := os.WriteFile(sourcePath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	result, err := Stage(Input{
		ID:           "att-1",
		Name:         "trace.log",
		MIMEType:     "text/plain",
		SizeBytes:    5,
		LocalPath:    sourcePath,
		ResolvedFrom: "manifest",
	}, StageOptions{
		Destination: filepath.Join(tempDir, "artifacts") + string(os.PathSeparator),
	})
	if err != nil {
		t.Fatalf("Stage() error = %v", err)
	}

	got, err := os.ReadFile(result.DestinationPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("destination content = %q, want hello", string(got))
	}
}

func TestStageFailsWhenDestinationExistsWithoutForce(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "trace.log")
	destPath := filepath.Join(tempDir, "dest.log")
	if err := os.WriteFile(sourcePath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(destPath, []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Stage(Input{
		Name:         "trace.log",
		LocalPath:    sourcePath,
		ResolvedFrom: "path",
	}, StageOptions{
		Destination: destPath,
	})
	if err == nil {
		t.Fatal("Stage() error = nil, want existing destination error")
	}
}

func TestStageRefusesSourceDestinationIdentityWithoutMutatingSource(t *testing.T) {
	tests := []struct {
		name        string
		destination func(t *testing.T, root, source string) string
	}{
		{
			name: "same path",
			destination: func(_ *testing.T, _, source string) string {
				return source
			},
		},
		{
			name: "symlink",
			destination: func(t *testing.T, root, source string) string {
				path := filepath.Join(root, "source-link.bin")
				if err := os.Symlink(source, path); err != nil {
					t.Fatalf("Symlink() error = %v", err)
				}
				return path
			},
		},
		{
			name: "hard link",
			destination: func(t *testing.T, root, source string) string {
				path := filepath.Join(root, "source-hard-link.bin")
				if err := os.Link(source, path); err != nil {
					t.Fatalf("Link() error = %v", err)
				}
				return path
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source.bin")
			want := []byte("source-must-survive")
			if err := os.WriteFile(source, want, 0o600); err != nil {
				t.Fatalf("WriteFile(source) error = %v", err)
			}
			destination := tt.destination(t, root, source)

			_, err := Stage(Input{
				Name:         "source.bin",
				LocalPath:    source,
				ResolvedFrom: "path",
			}, StageOptions{Destination: destination, Force: true})
			if err == nil || !strings.Contains(err.Error(), "same file") {
				t.Fatalf("Stage(source identity) error = %v, want same-file refusal", err)
			}
			got, readErr := os.ReadFile(source)
			if readErr != nil || string(got) != string(want) {
				t.Fatalf("source after refusal = %q error=%v, want unchanged", got, readErr)
			}
		})
	}
}
