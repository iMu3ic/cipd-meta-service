package nexus

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/iMu3ic/cipd-meta-service/internal/storage"
)

type fakeNexus struct {
	mu    sync.Mutex
	files map[string][]byte
	users []string
	deny  bool
}

func (f *fakeNexus) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	user, _, _ := r.BasicAuth()
	f.users = append(f.users, user)
	name := strings.TrimPrefix(r.URL.Path, "/repo/")
	switch r.Method {
	case http.MethodPut:
		if f.deny {
			http.Error(w, "Repository does not allow updating assets", http.StatusBadRequest)
			return
		}
		data, _ := io.ReadAll(r.Body)
		f.files[name] = data
		w.WriteHeader(http.StatusCreated)
	case http.MethodGet, http.MethodHead:
		data, ok := f.files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func newClient(t *testing.T) (*Client, *fakeNexus) {
	t.Helper()
	f := &fakeNexus{files: map[string][]byte{}}
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	return New(ts.URL+"/repo/", "mirror", "secret"), f
}

func TestRoundTrip(t *testing.T) {
	c, f := newClient(t)
	ctx := context.Background()

	if ok, err := c.Exists(ctx, "pkgs/a b.cipd"); err != nil || ok {
		t.Fatalf("Exists before Put = %v, %v", ok, err)
	}
	if _, err := c.Get(ctx, "pkgs/a b.cipd"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Get before Put: %v", err)
	}
	if err := c.Put(ctx, "pkgs/a b.cipd", strings.NewReader("payload"), 7); err != nil {
		t.Fatal(err)
	}
	if string(f.files["pkgs/a b.cipd"]) != "payload" {
		t.Fatalf("server has %q", f.files["pkgs/a b.cipd"])
	}
	if ok, err := c.Exists(ctx, "pkgs/a b.cipd"); err != nil || !ok {
		t.Fatalf("Exists after Put = %v, %v", ok, err)
	}
	rc, err := c.Get(ctx, "pkgs/a b.cipd")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if string(data) != "payload" {
		t.Fatalf("got %q", data)
	}
	for _, u := range f.users {
		if u != "mirror" {
			t.Fatalf("request without credentials: %q", u)
		}
	}
}

func TestPutRejected(t *testing.T) {
	c, f := newClient(t)
	f.deny = true
	err := c.Put(context.Background(), "x", strings.NewReader("a"), 1)
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("got %v, want an error with the HTTP status", err)
	}
}

func TestURL(t *testing.T) {
	c := New("https://nexus.example/repository/pkgs/", "", "")
	got, err := c.URL("a b/c.cipd")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://nexus.example/repository/pkgs/a%20b/c.cipd"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if _, err := c.URL("../x"); err == nil {
		t.Fatal("expected error for unsafe path")
	}
}
