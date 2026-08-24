package attachments

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	ManifestEnv         = "AGENTS_ATTACHMENTS_MANIFEST"
	DefaultManifestPath = ".temp/agents-attachments-manifest.json"
)

var ErrManifestNotFound = errors.New("attachment manifest not found")

type Attachment struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	MIMEType  string         `json:"mime_type,omitempty"`
	SizeBytes int64          `json:"size_bytes,omitempty"`
	SHA256    string         `json:"sha256,omitempty"`
	LocalPath string         `json:"local_path"`
	Source    string         `json:"source,omitempty"`
	CreatedAt string         `json:"created_at,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type manifestEnvelope struct {
	Attachments []Attachment `json:"attachments"`
}

func ResolveManifestPath(explicit string, getenv func(string) string) (path string, explicitSelection bool) {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit), true
	}
	if getenv != nil {
		if fromEnv := strings.TrimSpace(getenv(ManifestEnv)); fromEnv != "" {
			return fromEnv, true
		}
	}
	return DefaultManifestPath, false
}

func LoadManifest(explicit string, getenv func(string) string) ([]Attachment, string, error) {
	path, explicitSelection := ResolveManifestPath(explicit, getenv)
	expanded := filepath.Clean(path)

	data, err := os.ReadFile(expanded)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if explicitSelection {
				return nil, expanded, fmt.Errorf("load attachment manifest")
			}
			return nil, expanded, ErrManifestNotFound
		}
		return nil, expanded, fmt.Errorf("load attachment manifest")
	}

	attachments, err := parseManifest(data)
	if err != nil {
		return nil, expanded, fmt.Errorf("parse attachment manifest: %w", err)
	}

	baseDir := filepath.Dir(expanded)
	for idx := range attachments {
		attachments[idx].LocalPath = normalizeLocalPath(baseDir, attachments[idx].LocalPath)
		if strings.TrimSpace(attachments[idx].Name) == "" {
			attachments[idx].Name = filepath.Base(attachments[idx].LocalPath)
		}
		if attachments[idx].MIMEType == "" {
			attachments[idx].MIMEType = guessMIMEType(attachments[idx].LocalPath)
		}
	}

	sort.Slice(attachments, func(i, j int) bool {
		left := attachments[i].Name + attachments[i].ID
		right := attachments[j].Name + attachments[j].ID
		return left < right
	})

	return attachments, expanded, nil
}

func FindAttachment(attachments []Attachment, ref string) (Attachment, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Attachment{}, fmt.Errorf("attachment reference is empty")
	}

	matches := make([]Attachment, 0, 1)
	for _, item := range attachments {
		switch {
		case item.ID == ref:
			matches = append(matches, item)
		case item.Name == ref:
			matches = append(matches, item)
		case filepath.Base(item.LocalPath) == ref:
			matches = append(matches, item)
		}
	}

	if len(matches) == 0 {
		return Attachment{}, fmt.Errorf("attachment not found")
	}
	if len(matches) > 1 {
		return Attachment{}, fmt.Errorf("attachment reference is ambiguous")
	}
	return matches[0], nil
}

func parseManifest(data []byte) ([]Attachment, error) {
	var list []Attachment
	if err := json.Unmarshal(data, &list); err == nil {
		return list, nil
	}

	var envelope manifestEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	return envelope.Attachments, nil
}

func normalizeLocalPath(baseDir, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

func guessMIMEType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return "application/octet-stream"
	}
	if detected := mime.TypeByExtension(ext); detected != "" {
		return detected
	}
	return "application/octet-stream"
}
