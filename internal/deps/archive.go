package deps

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Extraction bounds shared by every helper archive. The largest pinned
// archive (Cua for macOS) holds a few dozen entries and expands to well under
// the total limit.
const (
	maxArchiveEntries    = 4096
	maxArchiveEntryBytes = 256 << 20
	maxArchiveTotalBytes = 512 << 20
)

// archiveEntry is one validated archive member. name is a clean relative
// slash path without a trailing slash; mode is fs.ModeDir or a regular file
// with permission bits. open is valid only during the visit callback.
type archiveEntry struct {
	name string
	size int64
	mode fs.FileMode
	open func() (io.ReadCloser, error)
}

// walkArchive visits every member of a .zip (zipped) or .tar.gz archive in
// order. Before each visit it enforces the shared bounds: entry count,
// per-entry and total declared size, relative names without traversal, drive
// or backslash syntax, no duplicates, and only regular files or directories.
// visit decides which entries to extract.
func walkArchive(ctx context.Context, archive string, zipped bool, visit func(archiveEntry) error) error {
	seen := make(map[string]bool)
	var total int64
	check := func(entry archiveEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(seen) >= maxArchiveEntries || entry.size < 0 || entry.size > maxArchiveEntryBytes ||
			total+entry.size > maxArchiveTotalBytes {
			return errors.New("archive exceeds extraction limits")
		}
		total += entry.size
		raw := entry.name
		if strings.ContainsAny(raw, `\:`) || path.IsAbs(raw) || strings.Contains("/"+raw+"/", "/../") {
			return fmt.Errorf("unsafe archive path %q", raw)
		}
		name := strings.TrimSuffix(raw, "/")
		if name == "" || name == "." || name != path.Clean(name) {
			return fmt.Errorf("unexpected archive path %q", raw)
		}
		if seen[name] {
			return fmt.Errorf("duplicate archive entry %q", name)
		}
		seen[name] = true
		if !entry.mode.IsRegular() && !entry.mode.IsDir() {
			return fmt.Errorf("archive entry %q is a link or special file", name)
		}
		entry.name = name
		return visit(entry)
	}

	if zipped {
		reader, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer reader.Close()
		for _, file := range reader.File {
			if file.UncompressedSize64 > maxArchiveEntryBytes {
				return errors.New("archive exceeds extraction limits")
			}
			entry := archiveEntry{name: file.Name, size: int64(file.UncompressedSize64), mode: file.Mode(), open: file.Open}
			if err := check(entry); err != nil {
				return err
			}
		}
		return nil
	}

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
	reader := tar.NewReader(&contextReader{ctx: ctx, reader: gz})
	open := func() (io.ReadCloser, error) { return io.NopCloser(reader), nil }
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		// FileInfo.Mode does not distinguish hard links from ordinary files.
		mode := header.FileInfo().Mode()
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			mode = fs.ModeIrregular
		}
		if err := check(archiveEntry{name: header.Name, size: header.Size, mode: mode, open: open}); err != nil {
			return err
		}
	}
}

// extractArchiveFile copies a regular entry to a new file at target, creating
// parent directories. It refuses an existing target and an entry whose
// content length differs from its declared size.
func extractArchiveFile(ctx context.Context, entry archiveEntry, target string, perm fs.FileMode) error {
	if !entry.mode.IsRegular() {
		return fmt.Errorf("archive entry %q is not a regular file", entry.name)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	in, err := entry.open()
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(out, io.LimitReader(&contextReader{ctx: ctx, reader: in}, entry.size+1))
	if err := errors.Join(copyErr, out.Close()); err != nil {
		return err
	}
	if n != entry.size {
		return fmt.Errorf("archive entry %q size mismatch", entry.name)
	}
	return nil
}

// contextReader stops a copy once ctx is done.
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
