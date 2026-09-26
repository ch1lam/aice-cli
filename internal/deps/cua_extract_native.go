package deps

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Extract only reviewed native executables. No SDK, addon, installer or shell
// extension is copied or executed. The selected bytes are checked separately.
func extractCuaNative(ctx context.Context, archive, destination, artifact string, files map[string]string) error {
	root := strings.TrimSuffix(artifact, archiveSuffix(artifact))
	seen := make(map[string]bool)
	var total int64
	entry := func(name string, size int64, mode os.FileMode, source func() (io.ReadCloser, error)) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(seen) >= 4096 || size < 0 || size > 256<<20 || total+size > 512<<20 {
			return errors.New("Cua archive exceeds extraction limits")
		}
		total += size
		if strings.ContainsAny(name, "\\:") || path.IsAbs(name) || strings.Contains("/"+name+"/", "/../") {
			return errors.New("unsafe Cua archive path")
		}
		name = strings.TrimSuffix(name, "/")
		if name != path.Clean(name) || (name != root && !strings.HasPrefix(name, root+"/")) {
			return errors.New("unexpected Cua archive path")
		}
		if seen[name] {
			return errors.New("duplicate Cua archive entry")
		}
		seen[name] = true
		if !mode.IsRegular() && !mode.IsDir() {
			return errors.New("Cua archive contains an unexpected link or special file")
		}
		rel := strings.TrimPrefix(name, root+"/")
		if _, selected := files[rel]; !selected {
			return nil
		}
		if !mode.IsRegular() {
			return errors.New("Cua executable is not a regular archive entry")
		}
		in, err := source()
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(filepath.Join(destination, rel), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(out, io.LimitReader(&cuaContextReader{ctx: ctx, reader: in}, size+1))
		if err := errors.Join(copyErr, out.Close()); err != nil {
			return err
		}
		if n != size {
			return errors.New("Cua archive entry size mismatch")
		}
		return nil
	}
	if strings.HasSuffix(artifact, ".zip") {
		z, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer z.Close()
		for _, f := range z.File {
			if f.UncompressedSize64 > 256<<20 {
				return errors.New("Cua archive entry exceeds extraction limit")
			}
			if err := entry(f.Name, int64(f.UncompressedSize64), f.Mode(), f.Open); err != nil {
				return err
			}
		}
	} else {
		f, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		r := tar.NewReader(&cuaContextReader{ctx: ctx, reader: gz})
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			h, err := r.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			// FileInfo.Mode does not distinguish hard links from ordinary files.
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
				return errors.New("Cua archive contains an unexpected link or special file")
			}
			if err := entry(h.Name, h.Size, h.FileInfo().Mode(), func() (io.ReadCloser, error) { return io.NopCloser(r), nil }); err != nil {
				return err
			}
		}
	}
	return verifyCuaNativeFiles(ctx, destination, files)
}
