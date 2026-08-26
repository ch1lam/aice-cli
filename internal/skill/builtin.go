package skill

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed builtin
var builtinFS embed.FS

// Builtin returns skills embedded in the binary. They follow the same Scan
// and parse path as disk skills; invalid embedded files are skipped with
// diagnostics like any other source.
func Builtin() ([]Skill, []Diagnostic, error) {
	fsys, err := fs.Sub(builtinFS, "builtin")
	if err != nil {
		return nil, nil, fmt.Errorf("skill: open builtin: %w", err)
	}
	return Scan(fsys, SourceBuiltin, "")
}
