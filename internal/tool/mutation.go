package tool

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// mutationFile is the temporary file owned by one synchronous atomic write.
type mutationFile interface {
	io.Writer
	Sync() error
	Close() error
}

// mutationOps keeps fault injection local to the atomic commit boundary.
type mutationOps struct {
	open   func(string, int, os.FileMode) (mutationFile, error)
	rename func(string, string) error
	remove func(string) error
}

func defaultMutationOps() mutationOps {
	return mutationOps{
		open: func(path string, flags int, mode os.FileMode) (mutationFile, error) {
			return os.OpenFile(path, flags, mode)
		},
		rename: os.Rename,
		remove: os.Remove,
	}
}

// The caller holds mutationMu until all I/O and cleanup have finished.
func (w *Workspace) atomicWrite(ctx context.Context, path string, content []byte, mode os.FileMode) (returnErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if directory != "." {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return fmt.Errorf("create parent directories: %w", err)
		}
	}

	temporaryPath, err := temporaryName(directory, filepath.Base(path))
	if err != nil {
		return err
	}
	file, err := w.mutationOps.open(temporaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
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
			if cleanupErr := w.mutationOps.remove(temporaryPath); cleanupErr != nil && !os.IsNotExist(cleanupErr) {
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
	// Rename is the commit point. Host I/O cannot be interrupted safely: wait
	// for it, and never replace a successful commit with a later cancellation.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := w.mutationOps.rename(temporaryPath, path); err != nil {
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
