//go:build !windows

package securefs

import (
	"fmt"
	"os"
)

func restrictCurrentUser(path string, directory bool) error {
	mode := os.FileMode(0o600)
	if directory {
		mode = 0o700
	}
	if err := os.Chmod(path, mode); err != nil {
		return err
	}
	return validateCurrentUser(path, directory)
}

func validateCurrentUser(path string, directory bool) error {
	want := os.FileMode(0o600)
	if directory {
		want = 0o700
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if got := info.Mode().Perm(); got != want {
		return fmt.Errorf("permissions are %o, want %o", got, want)
	}
	return nil
}
