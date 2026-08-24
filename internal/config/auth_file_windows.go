//go:build windows

package config

import "github.com/relux-works/skill-slack-management/internal/securefs"

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

func syncAuthDirectory(string) error {
	return nil
}
