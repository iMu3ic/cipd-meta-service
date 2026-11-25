// Package storage defines the minimal file storage that the mirror writes to
// and the service reads from.
package storage

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
)

// ErrNotFound is returned when a path does not exist in the storage.
var ErrNotFound = errors.New("not found")

// Storage is a flat namespace of files addressed by slash-separated relative
// paths, for example "pkgs/test/pkg/<digest>.cipd".
type Storage interface {
	// Get opens a file. It returns ErrNotFound if the file does not exist.
	Get(ctx context.Context, rel string) (io.ReadCloser, error)
	// Put writes a file. size is the exact number of bytes in r.
	Put(ctx context.Context, rel string, r io.Reader, size int64) error
	// Exists reports whether a file exists.
	Exists(ctx context.Context, rel string) (bool, error)
}

// ValidPath reports whether rel is a safe relative path for a Storage:
// slash-separated, no "." or ".." elements, not rooted, no backslashes.
func ValidPath(rel string) bool {
	return rel != "." && fs.ValidPath(rel) && !strings.ContainsRune(rel, '\\')
}
