package attachments

import (
	"path/filepath"
	"testing"
)

func TestBuildMaterializeArgs(t *testing.T) {
	args, manifestPath := BuildMaterializeArgs(MaterializeOptions{
		ThreadID:     "thread-123",
		SessionPath:  "/tmp/rollout.jsonl",
		OutDir:       ".temp/custom",
		ManifestPath: ".temp/custom-manifest.json",
	}, nil)

	wantArgs := []string{
		"materialize",
		"--thread-id", "thread-123",
		"--session", "/tmp/rollout.jsonl",
		"--out-dir", ".temp/custom",
		"--manifest", ".temp/custom-manifest.json",
	}
	if len(args) != len(wantArgs) {
		t.Fatalf("args len = %d, want %d (%v)", len(args), len(wantArgs), args)
	}
	for idx := range args {
		if args[idx] != wantArgs[idx] {
			t.Fatalf("args[%d] = %q, want %q", idx, args[idx], wantArgs[idx])
		}
	}
	if filepath.Base(manifestPath) != "custom-manifest.json" {
		t.Fatalf("manifestPath = %q", manifestPath)
	}
}
