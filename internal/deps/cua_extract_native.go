package deps

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Extract only reviewed native executables. No SDK, addon, installer or shell
// extension is copied or executed. The selected bytes are checked separately.
func extractCuaNative(ctx context.Context, archive, destination, artifact string, files map[string]string) error {
	root := strings.TrimSuffix(artifact, archiveSuffix(artifact))
	err := walkArchive(ctx, archive, strings.HasSuffix(artifact, ".zip"), func(entry archiveEntry) error {
		if entry.name != root && !strings.HasPrefix(entry.name, root+"/") {
			return errors.New("unexpected Cua archive path")
		}
		rel := strings.TrimPrefix(entry.name, root+"/")
		if _, selected := files[rel]; !selected {
			return nil
		}
		return extractArchiveFile(ctx, entry, filepath.Join(destination, filepath.FromSlash(rel)), 0o755)
	})
	if err != nil {
		return fmt.Errorf("extract Cua archive: %w", err)
	}
	return verifyCuaNativeFiles(ctx, destination, files)
}
