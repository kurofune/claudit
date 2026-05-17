package serve

import (
	"os"
	"time"
)

// chtimes sets both atime and mtime to t. Tests use it to force a
// future mtime so cache invalidation logic runs deterministically on
// filesystems with coarse mtime resolution.
func chtimes(path string, t time.Time) error {
	return os.Chtimes(path, t, t)
}
