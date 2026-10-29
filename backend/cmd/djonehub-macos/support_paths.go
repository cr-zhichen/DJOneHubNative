package main

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const supportDirectoryEnvironment = "DJONEHUB_SUPPORT_DIR"

func defaultAppSupportDirectory() string {
	if value := strings.TrimSpace(os.Getenv(supportDirectoryEnvironment)); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "FlameDJHub")
}

func legacyAppSupportDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "DJOneHubNative")
}

func prepareAppSupportDirectory() (string, error) {
	destination := defaultAppSupportDirectory()
	if destination == "" {
		return "", os.ErrNotExist
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(destination, 0o700); err != nil {
		return "", err
	}
	legacy := legacyAppSupportDirectory()
	if legacy == "" || legacy == destination {
		return destination, nil
	}
	if _, err := os.Stat(legacy); os.IsNotExist(err) {
		return destination, nil
	} else if err != nil {
		return "", err
	}
	if err := copyLegacySupportData(legacy, destination); err != nil {
		return "", err
	}
	return destination, nil
}

func copyLegacySupportData(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if isTransientSupportFile(entry.Name()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if _, err := os.Lstat(target); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return err
		}
		return copySupportFile(path, target, info.Mode().Perm())
	})
}

func isTransientSupportFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".sock") ||
		strings.HasSuffix(lower, ".log") ||
		strings.HasSuffix(lower, ".pid") ||
		strings.HasSuffix(lower, ".tmp")
}

func copySupportFile(source, destination string, permissions fs.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()

	output, err := os.CreateTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".tmp-")
	if err != nil {
		return err
	}
	temporary := output.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporary)
		}
	}()

	if err := output.Chmod(permissions); err != nil {
		_ = output.Close()
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := unix.RenameatxNp(unix.AT_FDCWD, temporary, unix.AT_FDCWD, destination, unix.RENAME_EXCL); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return nil
		}
		return err
	}
	removeTemporary = false
	return nil
}
