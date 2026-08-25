//go:build !windows

package config

import (
	"os"

	"github.com/relux-works/skill-slack-management/internal/securefs"
)

func protectAuthDirectory(path string) error {
	return securefs.RestrictDirectory(path)
}

func protectAuthFile(path string) error {
	return securefs.RestrictFile(path)
}

func validateAuthDirectory(path string) error {
	return securefs.ValidateDirectory(path)
}

func validateAuthFile(path string) error {
	return securefs.ValidateFile(path)
}

func syncAuthDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
