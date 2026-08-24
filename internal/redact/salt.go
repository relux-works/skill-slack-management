package redact

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/relux-works/skill-slack-management/internal/securefs"
)

const saltBytes = 32

func LoadDefault() (*Redactor, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve redaction config directory: %w", err)
	}
	return LoadOrCreate(filepath.Join(configDir, "slack-mgmt", "redaction-salt"), rand.Reader)
}

// LoadOrCreate reads or atomically publishes one installation salt. The path
// itself is intentionally omitted from errors because output redaction depends
// on this function succeeding.
func LoadOrCreate(path string, random io.Reader) (*Redactor, error) {
	if random == nil {
		random = rand.Reader
	}
	if existing, err := readSalt(path); err == nil {
		return New(existing)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create redaction config directory: %w", err)
	}
	if err := securefs.RestrictDirectory(dir); err != nil {
		return nil, fmt.Errorf("secure redaction config directory: %w", err)
	}

	salt := make([]byte, saltBytes)
	if _, err := io.ReadFull(random, salt); err != nil {
		return nil, fmt.Errorf("generate redaction salt: %w", err)
	}

	temp, err := os.CreateTemp(dir, ".redaction-salt-*")
	if err != nil {
		return nil, fmt.Errorf("create redaction salt temporary file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if err := securefs.RestrictFile(tempPath); err != nil {
		_ = temp.Close()
		return nil, fmt.Errorf("secure redaction salt temporary file: %w", err)
	}
	if _, err := temp.Write(salt); err != nil {
		_ = temp.Close()
		return nil, fmt.Errorf("write redaction salt: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return nil, fmt.Errorf("sync redaction salt: %w", err)
	}
	if err := temp.Close(); err != nil {
		return nil, fmt.Errorf("close redaction salt: %w", err)
	}

	if err := os.Link(tempPath, path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("publish redaction salt: %w", err)
		}
		return loadExistingSalt(path)
	}
	return loadExistingSalt(path)
}

func loadExistingSalt(path string) (*Redactor, error) {
	salt, err := readSalt(path)
	if err != nil {
		return nil, err
	}
	return New(salt)
}

func readSalt(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("redaction salt is not a regular file")
	}
	if err := securefs.RestrictFile(path); err != nil {
		return nil, fmt.Errorf("secure redaction salt: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read redaction salt: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("redaction salt is empty")
	}
	return data, nil
}
