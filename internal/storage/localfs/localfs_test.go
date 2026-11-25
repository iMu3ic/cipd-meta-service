package localfs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iMu3ic/cipd-meta-service/internal/storage"
)

func TestPutGetExists(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	ctx := context.Background()

	ok, err := s.Exists(ctx, "a/b.txt")
	if err != nil || ok {
		t.Fatalf("Exists before Put = %v, %v", ok, err)
	}
	if _, err := s.Get(ctx, "a/b.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Get before Put: %v, want ErrNotFound", err)
	}

	if err := s.Put(ctx, "a/b.txt", strings.NewReader("hello"), 5); err != nil {
		t.Fatal(err)
	}
	ok, err = s.Exists(ctx, "a/b.txt")
	if err != nil || !ok {
		t.Fatalf("Exists after Put = %v, %v", ok, err)
	}
	rc, err := s.Get(ctx, "a/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if string(data) != "hello" {
		t.Fatalf("got %q", data)
	}
}

func TestPutSizeMismatchLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.Put(context.Background(), "x.txt", strings.NewReader("abc"), 10); err == nil {
		t.Fatal("expected size mismatch error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestRejectsUnsafePaths(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "root"))
	ctx := context.Background()
	for _, p := range []string{"", ".", "../x", "a/../../x", "/abs", `a\b`, "a//b"} {
		if err := s.Put(ctx, p, strings.NewReader("x"), 1); err == nil {
			t.Errorf("Put(%q) succeeded", p)
		}
		if _, err := s.Get(ctx, p); err == nil || errors.Is(err, storage.ErrNotFound) {
			t.Errorf("Get(%q) = %v, want a validation error", p, err)
		}
	}
}
