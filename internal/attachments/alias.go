package attachments

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/relux-works/skill-slack-management/internal/securefs"
)

const AliasRelativeRoot = ".temp/slack-mgmt/attachment-aliases"

var ErrAttachmentAlias = errors.New("attachment_alias_error")

type Alias struct {
	LocalPath    string
	AbsolutePath string
	SizeBytes    int64
}

// AliasRuntime exposes bounded filesystem seams so atomic publication failures
// can be tested without weakening the production entry point.
type AliasRuntime struct {
	WorkingDir string
	Random     io.Reader
	Copy       func(io.Writer, io.Reader) (int64, error)
	Sync       func(*os.File) error
	Chmod      func(*os.File, os.FileMode) error
	Publish    func(string, string) error
	Remove     func(string) error
}

func PublishAlias(sourcePath string, runtime AliasRuntime) (Alias, error) {
	workingDir := runtime.WorkingDir
	if workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			return Alias{}, aliasError("resolve working directory", err)
		}
	}
	absWorkingDir, err := filepath.Abs(workingDir)
	if err != nil {
		return Alias{}, aliasError("resolve working directory", err)
	}
	root, err := prepareAliasRoot(absWorkingDir)
	if err != nil {
		return Alias{}, err
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return Alias{}, aliasError("open source", err)
	}
	defer source.Close()

	temporary, err := os.CreateTemp(root, ".attachment-alias-*")
	if err != nil {
		return Alias{}, aliasError("create temporary alias", err)
	}
	temporaryPath := temporary.Name()
	remove := runtime.Remove
	if remove == nil {
		remove = os.Remove
	}
	defer remove(temporaryPath)

	chmod := runtime.Chmod
	if chmod == nil {
		chmod = func(file *os.File, _ os.FileMode) error { return securefs.RestrictFile(file.Name()) }
	}
	if err := chmod(temporary, 0o600); err != nil {
		_ = temporary.Close()
		return Alias{}, aliasError("secure temporary alias", err)
	}
	copyFile := runtime.Copy
	if copyFile == nil {
		copyFile = io.Copy
	}
	written, err := copyFile(temporary, source)
	if err != nil {
		_ = temporary.Close()
		return Alias{}, aliasError("copy alias bytes", err)
	}
	syncFile := runtime.Sync
	if syncFile == nil {
		syncFile = func(file *os.File) error { return file.Sync() }
	}
	if err := syncFile(temporary); err != nil {
		_ = temporary.Close()
		return Alias{}, aliasError("sync alias bytes", err)
	}
	if err := temporary.Close(); err != nil {
		return Alias{}, aliasError("close temporary alias", err)
	}

	random := runtime.Random
	if random == nil {
		random = rand.Reader
	}
	publish := runtime.Publish
	if publish == nil {
		publish = os.Link
	}

	for attempts := 0; attempts < 128; attempts++ {
		id := make([]byte, 16)
		if _, err := io.ReadFull(random, id); err != nil {
			return Alias{}, aliasError("generate alias name", err)
		}
		name := "attachment-" + hex.EncodeToString(id)
		finalPath := filepath.Join(root, name)
		if err := publish(temporaryPath, finalPath); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return Alias{}, aliasError("publish alias", err)
		}
		if err := remove(temporaryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(finalPath)
			return Alias{}, aliasError("remove temporary alias", err)
		}
		return Alias{
			LocalPath:    filepath.ToSlash(filepath.Join(AliasRelativeRoot, name)),
			AbsolutePath: finalPath,
			SizeBytes:    written,
		}, nil
	}

	return Alias{}, aliasError("publish alias after collisions", os.ErrExist)
}

func prepareAliasRoot(absWorkingDir string) (string, error) {
	root := absWorkingDir
	parts := strings.Split(filepath.FromSlash(AliasRelativeRoot), string(os.PathSeparator))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		root = filepath.Join(root, part)
		info, err := os.Lstat(root)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(root, 0o700); err != nil {
				return "", aliasError("create alias directory", err)
			}
			info, err = os.Lstat(root)
		}
		if err != nil {
			return "", aliasError("inspect alias directory", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", aliasError("refuse unsafe alias directory", os.ErrInvalid)
		}
	}
	if err := securefs.RestrictDirectory(root); err != nil {
		return "", aliasError("secure alias directory", err)
	}
	return root, nil
}

func aliasError(stage string, err error) error {
	_ = err
	return fmt.Errorf("%w: %s", ErrAttachmentAlias, stage)
}
