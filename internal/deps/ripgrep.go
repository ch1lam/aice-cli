package deps

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// installRipgrep downloads, verifies, and extracts the ripgrep binary for the
// current platform into opts.BinDir.
func installRipgrep(ctx context.Context, opts Options) error {
	platform := opts.Goos + "/" + opts.Goarch
	asset, ok := ripgrepAsset[platform]
	if !ok {
		return fmt.Errorf("unsupported platform %s", platform)
	}
	want, ok := ripgrepSHA256[platform]
	if !ok {
		return fmt.Errorf("missing pinned checksum for %s", asset)
	}
	if err := os.MkdirAll(opts.BinDir, 0o755); err != nil {
		return fmt.Errorf("create bin dir: %w", err)
	}

	archivePath, err := download(ctx, opts, ripgrepDownloadURL(opts.BaseURL, asset), want, archiveSuffix(asset))
	if err != nil {
		return err
	}
	defer os.Remove(archivePath)

	extractTo, err := os.MkdirTemp("", "aice-rg-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(extractTo)

	if strings.HasSuffix(asset, ".zip") {
		if err := extractZip(archivePath, extractTo); err != nil {
			return fmt.Errorf("extract %s: %w", asset, err)
		}
	} else {
		if err := extractTarGz(archivePath, extractTo); err != nil {
			return fmt.Errorf("extract %s: %w", asset, err)
		}
	}

	binName := "rg"
	if opts.Goos == "windows" {
		binName = "rg.exe"
	}
	from, err := findFile(extractTo, binName)
	if err != nil {
		return err
	}
	dest := filepath.Join(opts.BinDir, binName)
	if err := moveFile(from, dest); err != nil {
		return fmt.Errorf("install %s: %w", binName, err)
	}
	if opts.Goos != "windows" {
		if err := os.Chmod(dest, 0o755); err != nil {
			return fmt.Errorf("chmod %s: %w", binName, err)
		}
	}
	return nil
}

// downloadTimeout bounds a whole dependency download so a stalled connection
// cannot hang startup indefinitely. It is a variable so tests can shrink it.
var downloadTimeout = 10 * time.Minute

// maxDownloadBytes caps a downloaded asset so an abnormally large response
// cannot exhaust the temp disk before the SHA-256 check can reject it. The
// largest legitimate asset is the ~60 MB PortableGit self-extractor.
var maxDownloadBytes int64 = 256 << 20 // 256 MiB

// download fetches url into a temporary file and verifies its SHA-256 matches
// want before returning the file path. suffix is appended to the temporary
// file name: the Git Bash self-extractor is executed on Windows and must keep
// the .exe extension for CreateProcess to find it.
func download(ctx context.Context, opts Options, url, want, suffix string) (string, error) {
	fmt.Fprintf(opts.Log, "aice: downloading %s ...\n", url)
	requestCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	response, err := opts.Client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: unexpected status %d", url, response.StatusCode)
	}

	file, err := os.CreateTemp("", "aice-download-*"+suffix)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	written, err := io.Copy(
		io.MultiWriter(file, hash),
		io.LimitReader(response.Body, maxDownloadBytes+1),
	)
	if err != nil {
		os.Remove(file.Name())
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	if written > maxDownloadBytes {
		os.Remove(file.Name())
		return "", fmt.Errorf("download %s: response exceeds %d bytes", url, maxDownloadBytes)
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != want {
		os.Remove(file.Name())
		return "", fmt.Errorf("download %s: checksum mismatch", url)
	}
	return file.Name(), nil
}

func extractZip(archive, dest string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, entry := range reader.File {
		path, err := safeJoin(dest, entry.Name)
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		err = writeFile(path, source)
		source.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(archive, dest string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()

	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		path, err := safeJoin(dest, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(path, reader); err != nil {
				return err
			}
		}
	}
}

// archiveSuffix returns the recognizable extension of a ripgrep asset so the
// downloaded temp file is tidy; extraction does not rely on it.
func archiveSuffix(asset string) string {
	if strings.HasSuffix(asset, ".tar.gz") {
		return ".tar.gz"
	}
	return filepath.Ext(asset)
}

// safeJoin joins an archive entry name onto base, rejecting entries that would
// escape base. Archive names use forward slashes, so path.Clean gives the same
// result on every platform before the cleaned path is converted back.
func safeJoin(base, name string) (string, error) {
	clean := path.Clean(name)
	if clean == "." || clean == ".." ||
		strings.HasPrefix(clean, "../") ||
		strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("archive entry escapes destination: %q", name)
	}
	return filepath.Join(base, filepath.FromSlash(clean)), nil
}

func writeFile(path string, source io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	target, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		return err
	}
	return target.Close()
}

// findFile returns the path of the file named name anywhere under root.
func findFile(root, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && filepath.Base(path) == name {
			found = path
			return io.EOF
		}
		return nil
	})
	if err != nil && err != io.EOF {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("archive does not contain %s", name)
	}
	return found, nil
}

// moveFile renames from to to, falling back to a copy on cross-device errors.
func moveFile(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if err := os.Rename(from, to); err == nil {
		return nil
	}
	return copyAndRemove(from, to)
}

func copyAndRemove(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	if err := writeFile(to, source); err != nil {
		return err
	}
	return os.Remove(from)
}
