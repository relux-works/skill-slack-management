package attachments

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Input struct {
	ID           string
	Name         string
	MIMEType     string
	SizeBytes    int64
	LocalPath    string
	ResolvedFrom string
}

type StageOptions struct {
	Destination string
	Name        string
	Force       bool
}

type StageResult struct {
	ID              string `json:"id,omitempty"`
	Name            string `json:"name"`
	MIMEType        string `json:"mime_type,omitempty"`
	SizeBytes       int64  `json:"size_bytes"`
	ResolvedFrom    string `json:"resolved_from"`
	SourcePath      string `json:"source_path"`
	DestinationPath string `json:"destination_path"`
}

func ResolveInput(ref, manifestPath string, getenv func(string) string) (Input, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Input{}, fmt.Errorf("attachment reference is empty")
	}

	if info, err := os.Stat(ref); err == nil && !info.IsDir() {
		abs, err := filepath.Abs(ref)
		if err != nil {
			return Input{}, fmt.Errorf("resolve source path")
		}
		return Input{
			Name:         filepath.Base(abs),
			MIMEType:     guessMIMEType(abs),
			SizeBytes:    info.Size(),
			LocalPath:    abs,
			ResolvedFrom: "path",
		}, nil
	}

	items, _, err := LoadManifest(manifestPath, getenv)
	if err != nil {
		if err == ErrManifestNotFound {
			return Input{}, fmt.Errorf("attachment manifest not found; run `slack-mgmt attachment materialize` or set %s", ManifestEnv)
		}
		return Input{}, err
	}

	item, err := FindAttachment(items, ref)
	if err != nil {
		return Input{}, err
	}

	info, err := os.Stat(item.LocalPath)
	if err != nil {
		return Input{}, fmt.Errorf("stat attachment local_path")
	}

	return Input{
		ID:           item.ID,
		Name:         item.Name,
		MIMEType:     item.MIMEType,
		SizeBytes:    info.Size(),
		LocalPath:    item.LocalPath,
		ResolvedFrom: "manifest",
	}, nil
}

func Stage(input Input, opts StageOptions) (StageResult, error) {
	sourcePath := strings.TrimSpace(input.LocalPath)
	if sourcePath == "" {
		return StageResult{}, fmt.Errorf("source path is empty")
	}

	targetPath, err := resolveDestination(opts.Destination, coalesceName(opts.Name, input.Name))
	if err != nil {
		return StageResult{}, err
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return StageResult{}, fmt.Errorf("create destination directory")
	}

	src, err := os.Open(sourcePath)
	if err != nil {
		return StageResult{}, fmt.Errorf("open source file")
	}
	defer src.Close()
	sourceInfo, err := src.Stat()
	if err != nil {
		return StageResult{}, fmt.Errorf("stat source file")
	}

	flags := os.O_WRONLY | os.O_CREATE
	if !opts.Force {
		flags |= os.O_EXCL
	}
	dst, err := os.OpenFile(targetPath, flags, 0o666)
	if err != nil {
		if !opts.Force && os.IsExist(err) {
			return StageResult{}, fmt.Errorf("destination already exists")
		}
		return StageResult{}, fmt.Errorf("create destination file")
	}
	destinationInfo, err := dst.Stat()
	if err != nil {
		_ = dst.Close()
		return StageResult{}, fmt.Errorf("stat destination file")
	}
	if os.SameFile(sourceInfo, destinationInfo) {
		_ = dst.Close()
		return StageResult{}, fmt.Errorf("source and destination refer to the same file")
	}
	if opts.Force {
		if err := dst.Truncate(0); err != nil {
			_ = dst.Close()
			return StageResult{}, fmt.Errorf("truncate destination file")
		}
		if _, err := dst.Seek(0, io.SeekStart); err != nil {
			_ = dst.Close()
			return StageResult{}, fmt.Errorf("seek destination file")
		}
	}

	written, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return StageResult{}, fmt.Errorf("copy file: %w", copyErr)
	}
	if closeErr != nil {
		return StageResult{}, fmt.Errorf("finalize destination file: %w", closeErr)
	}

	return StageResult{
		ID:              input.ID,
		Name:            filepath.Base(targetPath),
		MIMEType:        input.MIMEType,
		SizeBytes:       written,
		ResolvedFrom:    input.ResolvedFrom,
		SourcePath:      sourcePath,
		DestinationPath: targetPath,
	}, nil
}

func resolveDestination(destination, fileName string) (string, error) {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return "", fmt.Errorf("destination is empty")
	}
	if strings.TrimSpace(fileName) == "" {
		return "", fmt.Errorf("destination file name is empty")
	}

	if info, err := os.Stat(destination); err == nil && info.IsDir() {
		return filepath.Join(destination, fileName), nil
	}

	if strings.HasSuffix(destination, string(os.PathSeparator)) {
		return filepath.Join(destination, fileName), nil
	}

	return filepath.Clean(destination), nil
}

func coalesceName(override, fallback string) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	return strings.TrimSpace(fallback)
}
