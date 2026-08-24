package attachments

import (
	"os"
	"path/filepath"
	"strings"
)

type MaterializeOptions struct {
	ThreadID     string
	SessionPath  string
	OutDir       string
	ManifestPath string
}

func BuildMaterializeArgs(opts MaterializeOptions, getenv func(string) string) ([]string, string) {
	manifestPath, _ := ResolveManifestPath(opts.ManifestPath, getenv)
	args := []string{"materialize"}

	if strings.TrimSpace(opts.ThreadID) != "" {
		args = append(args, "--thread-id", strings.TrimSpace(opts.ThreadID))
	}
	if strings.TrimSpace(opts.SessionPath) != "" {
		args = append(args, "--session", strings.TrimSpace(opts.SessionPath))
	}
	if strings.TrimSpace(opts.OutDir) != "" {
		args = append(args, "--out-dir", strings.TrimSpace(opts.OutDir))
	}
	if strings.TrimSpace(opts.ManifestPath) != "" {
		args = append(args, "--manifest", strings.TrimSpace(opts.ManifestPath))
	}

	return args, resolveOutputManifestPath(manifestPath)
}

func resolveOutputManifestPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return DefaultManifestPath
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Join(cwd, filepath.Clean(path))
}
