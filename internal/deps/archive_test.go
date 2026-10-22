package deps

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestWalkArchiveRejectsUnsafeEntries(t *testing.T) {
	t.Parallel()

	regular := func(name string) tar.Header {
		return tar.Header{Name: name, Mode: 0o644, Typeflag: tar.TypeReg}
	}
	tests := []struct {
		name    string
		headers []tar.Header
	}{
		{name: "parent traversal", headers: []tar.Header{regular("root/../escape")}},
		{name: "trailing parent", headers: []tar.Header{regular("root/..")}},
		{name: "absolute", headers: []tar.Header{regular("/etc/passwd")}},
		{name: "backslash", headers: []tar.Header{regular(`root\escape`)}},
		{name: "drive or stream", headers: []tar.Header{regular("root/extra:stream")}},
		{name: "unclean", headers: []tar.Header{regular("root//file")}},
		{name: "dot root", headers: []tar.Header{{Name: "./", Mode: 0o755, Typeflag: tar.TypeDir}}},
		{name: "duplicate", headers: []tar.Header{regular("root/file"), regular("root/file")}},
		{name: "symlink", headers: []tar.Header{{Name: "root/link", Linkname: "/outside", Typeflag: tar.TypeSymlink}}},
		{name: "hard link", headers: []tar.Header{regular("root/file"), {Name: "root/hard", Linkname: "root/file", Typeflag: tar.TypeLink}}},
		{name: "oversized entry", headers: []tar.Header{{Name: "root/big", Mode: 0o644, Size: maxArchiveEntryBytes + 1, Typeflag: tar.TypeReg}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			archive := writeTarGz(t, tt.headers)
			visited := 0
			err := walkArchive(t.Context(), archive, false, func(archiveEntry) error {
				visited++
				return nil
			})
			if err == nil {
				t.Fatalf("walkArchive accepted unsafe archive after %d entries", visited)
			}
		})
	}
}

func TestWalkArchiveEntryCountLimit(t *testing.T) {
	t.Parallel()

	headers := make([]tar.Header, maxArchiveEntries+1)
	for i := range headers {
		headers[i] = tar.Header{Name: "root/" + strconv.Itoa(i), Mode: 0o644, Typeflag: tar.TypeReg}
	}
	archive := writeTarGz(t, headers)
	if err := walkArchive(t.Context(), archive, false, func(archiveEntry) error { return nil }); err == nil {
		t.Fatal("walkArchive accepted more than the entry limit")
	}
}

func TestWalkArchiveExtractsSelectedZipFile(t *testing.T) {
	t.Parallel()

	var data bytes.Buffer
	w := zip.NewWriter(&data)
	for name, body := range map[string]string{"root/bin/tool": "binary", "root/README": "docs"} {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "archive.zip")
	if err := os.WriteFile(archive, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "nested", "tool")
	err := walkArchive(t.Context(), archive, true, func(entry archiveEntry) error {
		if entry.name != "root/bin/tool" {
			return nil
		}
		return extractArchiveFile(t.Context(), entry, target, 0o755)
	})
	if err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != "binary" {
		t.Fatalf("extracted = %q, %v", body, err)
	}
	if err := walkArchive(t.Context(), archive, true, func(entry archiveEntry) error {
		return extractArchiveFile(t.Context(), entry, target, 0o755)
	}); err == nil {
		t.Fatal("extractArchiveFile replaced an existing file")
	}
}

func writeTarGz(t *testing.T, headers []tar.Header) string {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	w := tar.NewWriter(gz)
	oversized := false
	for _, header := range headers {
		if err := w.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		// An oversized declared body is never written: the walker must reject
		// the header before reading it.
		oversized = oversized || header.Size > maxArchiveEntryBytes
	}
	if err := w.Close(); err != nil && !oversized {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(archive, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return archive
}
