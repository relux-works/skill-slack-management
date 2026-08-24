package attachments

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/relux-works/skill-slack-management/internal/securefs"
)

func TestPublishAliasCreatesPrivateSourceIdenticalFile(t *testing.T) {
	workingDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source.bin")
	want := []byte{0x00, 0x01, 0xff, 0x42}
	if err := os.WriteFile(source, want, 0o644); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}

	alias, err := PublishAlias(source, AliasRuntime{
		WorkingDir: workingDir,
		Random:     bytes.NewReader(bytes.Repeat([]byte{0x11}, 16)),
	})
	if err != nil {
		t.Fatalf("PublishAlias() error = %v", err)
	}
	if alias.LocalPath != AliasRelativeRoot+"/attachment-11111111111111111111111111111111" {
		t.Fatalf("LocalPath = %q", alias.LocalPath)
	}
	got, err := os.ReadFile(alias.AbsolutePath)
	if err != nil {
		t.Fatalf("ReadFile(alias) error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("alias bytes = %v, want %v", got, want)
	}
	if err := securefs.ValidateDirectory(filepath.Join(workingDir, filepath.FromSlash(AliasRelativeRoot))); err != nil {
		t.Fatalf("alias root is not current-user-only: %v", err)
	}
	if err := securefs.ValidateFile(alias.AbsolutePath); err != nil {
		t.Fatalf("alias is not current-user-only: %v", err)
	}
}

func TestPublishAliasRetriesCollisionWithoutReplacement(t *testing.T) {
	workingDir := t.TempDir()
	root := filepath.Join(workingDir, filepath.FromSlash(AliasRelativeRoot))
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	firstName := "attachment-" + strings.Repeat("11", 16)
	firstPath := filepath.Join(root, firstName)
	if err := os.WriteFile(firstPath, []byte("sentinel"), 0o600); err != nil {
		t.Fatalf("WriteFile(sentinel) error = %v", err)
	}
	source := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(source, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	random := append(bytes.Repeat([]byte{0x11}, 16), bytes.Repeat([]byte{0x22}, 16)...)

	alias, err := PublishAlias(source, AliasRuntime{WorkingDir: workingDir, Random: bytes.NewReader(random)})
	if err != nil {
		t.Fatalf("PublishAlias() error = %v", err)
	}
	if !strings.HasSuffix(alias.LocalPath, strings.Repeat("22", 16)) {
		t.Fatalf("LocalPath = %q, want second generated name", alias.LocalPath)
	}
	if got, _ := os.ReadFile(firstPath); string(got) != "sentinel" {
		t.Fatalf("collision sentinel = %q, want unchanged", got)
	}
}

func TestPublishAliasFailuresLeaveNoArtifacts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AliasRuntime)
	}{
		{
			name: "copy",
			mutate: func(rt *AliasRuntime) {
				rt.Copy = func(io.Writer, io.Reader) (int64, error) { return 0, errors.New("copy failed") }
			},
		},
		{
			name: "sync",
			mutate: func(rt *AliasRuntime) {
				rt.Sync = func(*os.File) error { return errors.New("sync failed") }
			},
		},
		{
			name: "chmod",
			mutate: func(rt *AliasRuntime) {
				rt.Chmod = func(*os.File, os.FileMode) error { return errors.New("chmod failed") }
			},
		},
		{
			name: "publish",
			mutate: func(rt *AliasRuntime) {
				rt.Publish = func(string, string) error { return errors.New("publish failed") }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workingDir := t.TempDir()
			source := filepath.Join(t.TempDir(), "source")
			if err := os.WriteFile(source, []byte("payload"), 0o600); err != nil {
				t.Fatalf("WriteFile(source) error = %v", err)
			}
			runtime := AliasRuntime{
				WorkingDir: workingDir,
				Random:     bytes.NewReader(bytes.Repeat([]byte{0x33}, 16)),
			}
			tt.mutate(&runtime)

			_, err := PublishAlias(source, runtime)
			if !errors.Is(err, ErrAttachmentAlias) {
				t.Fatalf("PublishAlias() error = %v, want ErrAttachmentAlias", err)
			}
			entries, readErr := os.ReadDir(filepath.Join(workingDir, filepath.FromSlash(AliasRelativeRoot)))
			if readErr != nil {
				t.Fatalf("ReadDir(alias root) error = %v", readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("alias root entries = %v, want empty", entries)
			}
		})
	}
}

func TestPublishAliasRejectsSymlinkedRootComponents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require privileges on Windows")
	}

	tests := []struct {
		name      string
		linkParts []string
	}{
		{name: "alias root", linkParts: []string{".temp", "slack-mgmt", "attachment-aliases"}},
		{name: "alias parent", linkParts: []string{".temp", "slack-mgmt"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workingDir := t.TempDir()
			external := t.TempDir()
			link := filepath.Join(append([]string{workingDir}, tt.linkParts...)...)
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatalf("MkdirAll(link parent) error = %v", err)
			}
			if err := os.Symlink(external, link); err != nil {
				t.Fatalf("Symlink() error = %v", err)
			}
			source := filepath.Join(t.TempDir(), "source")
			if err := os.WriteFile(source, []byte("payload"), 0o600); err != nil {
				t.Fatalf("WriteFile(source) error = %v", err)
			}

			_, err := PublishAlias(source, AliasRuntime{WorkingDir: workingDir})
			if !errors.Is(err, ErrAttachmentAlias) {
				t.Fatalf("PublishAlias() error = %v, want ErrAttachmentAlias", err)
			}
			entries, readErr := os.ReadDir(external)
			if readErr != nil {
				t.Fatalf("ReadDir(external) error = %v", readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("symlink escape wrote external entries: %v", entries)
			}
		})
	}
}
