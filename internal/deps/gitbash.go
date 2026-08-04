//go:build windows

package deps

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// installGitBash downloads the Git for Windows Portable edition (a
// self-extracting 7-Zip archive) and extracts it so bash.exe and git.exe are
// available under opts.BinDir/git.
func installGitBash(ctx context.Context, opts Options) error {
	gitDir := filepath.Join(opts.BinDir, "git")
	if err := os.MkdirAll(opts.BinDir, 0o755); err != nil {
		return fmt.Errorf("create bin dir: %w", err)
	}
	archivePath, err := download(ctx, opts, portableGitDownloadURL(opts.BaseURL), portableGitSHA256, ".exe")
	if err != nil {
		return err
	}
	defer os.Remove(archivePath)

	// The 7-Zip self-extractor accepts -o<dir> and -y to extract silently.
	command := exec.CommandContext(ctx, archivePath, "-y", "-o"+gitDir)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("extract PortableGit: %w: %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(gitDir, "bin", "bash.exe")); err != nil {
		return fmt.Errorf("PortableGit extraction is missing bin/bash.exe: %w", err)
	}
	return nil
}
