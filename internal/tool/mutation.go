package tool

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func (w *Workspace) atomicWrite(path string, content []byte, mode os.FileMode) (returnErr error) {
	directory := filepath.Dir(path)
	if directory != "." {
		if err := w.root.MkdirAll(directory, 0o750); err != nil {
			return fmt.Errorf("create parent directories: %w", err)
		}
	}

	temporaryPath, err := temporaryName(directory, filepath.Base(path))
	if err != nil {
		return err
	}
	file, err := w.root.OpenFile(temporaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	defer func() {
		if file != nil {
			if closeErr := file.Close(); closeErr != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close temporary file: %w", closeErr))
			}
		}
		if returnErr != nil {
			if cleanupErr := w.root.Remove(temporaryPath); cleanupErr != nil && !os.IsNotExist(cleanupErr) {
				returnErr = errors.Join(returnErr, fmt.Errorf("remove temporary file: %w", cleanupErr))
			}
		}
	}()

	if _, err := io.Copy(file, bytes.NewReader(content)); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := file.Close(); err != nil {
		file = nil
		return fmt.Errorf("close temporary file: %w", err)
	}
	file = nil
	if err := w.root.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace target file: %w", err)
	}
	return nil
}

func temporaryName(directory, base string) (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate temporary file name: %w", err)
	}
	name := "." + base + ".aice-" + hex.EncodeToString(random[:])
	if directory == "." {
		return name, nil
	}
	return filepath.Join(directory, name), nil
}
