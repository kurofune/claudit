package watch

import (
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

// walkJSONL invokes fn for every .jsonl file under root with its mtime.
// Tolerates per-entry errors during the walk.
func walkJSONL(root string, fn func(path string, mod time.Time)) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		fn(path, info.ModTime())
		return nil
	})
}

func baseName(p string) string { return filepath.Base(p) }

func hasPrefixIgnoreExt(base, prefix string) bool {
	noExt := strings.TrimSuffix(base, ".jsonl")
	return strings.HasPrefix(noExt, prefix)
}
