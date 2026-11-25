// Package localfs implements storage.Storage on a local directory.
package localfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/iMu3ic/cipd-meta-service/internal/storage"
)

// Store keeps files under a root directory.
type Store struct {
	root string
}

// New returns a Store rooted at dir.
func New(dir string) *Store { return &Store{root: dir} }

func (s *Store) path(rel string) (string, error) {
	if !storage.ValidPath(rel) {
		return "", fmt.Errorf("invalid storage path %q", rel)
	}
	return filepath.Join(s.root, filepath.FromSlash(rel)), nil
}

func (s *Store) Get(_ context.Context, rel string) (io.ReadCloser, error) {
	p, err := s.path(rel)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (s *Store) Exists(_ context.Context, rel string) (bool, error) {
	p, err := s.path(rel)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Put writes the file atomically: the data goes to a temporary file in the
// same directory, which is then renamed into place.
func (s *Store) Put(_ context.Context, rel string, r io.Reader, size int64) (err error) {
	p, err := s.path(rel)
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".put-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			os.Remove(tmpName)
		}
	}()
	n, err := io.Copy(tmp, r)
	if err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if n != size {
		return fmt.Errorf("writing %s: got %d bytes, expected %d", rel, n, size)
	}
	return os.Rename(tmpName, p)
}
