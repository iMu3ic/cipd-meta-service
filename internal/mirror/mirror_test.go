package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iMu3ic/cipd-meta-service/internal/index"
	"github.com/iMu3ic/cipd-meta-service/internal/storage/localfs"
)

const pkg = "test/pkg/linux-amd64"

func idFor(content string) string {
	sum := sha256.Sum256([]byte(content))
	return base64.URLEncoding.EncodeToString(append(sum[:], 2))
}

func hexFor(t *testing.T, content string) string {
	t.Helper()
	h, ok := index.HexFromInstanceID(idFor(content))
	if !ok {
		t.Fatal("bad instance ID")
	}
	return h
}

// fakeUpstream maps "package@version" to file content.
type fakeUpstream struct {
	versions map[string]string
	corrupt  bool
	resolves int
	fetches  int
}

func (f *fakeUpstream) Resolve(_ context.Context, p, version string) (string, error) {
	f.resolves++
	if content, ok := f.versions[p+"@"+version]; ok {
		return idFor(content), nil
	}
	for _, content := range f.versions {
		if idFor(content) == version {
			return version, nil
		}
	}
	return "", fmt.Errorf("upstream: %s@%s not found", p, version)
}

func (f *fakeUpstream) Fetch(_ context.Context, _, id, dst string) error {
	f.fetches++
	for _, content := range f.versions {
		if idFor(content) == id {
			if f.corrupt {
				content = "tampered"
			}
			return os.WriteFile(dst, []byte(content), 0o644)
		}
	}
	return fmt.Errorf("upstream: instance %s not found", id)
}

type fixture struct {
	t   *testing.T
	dir string
	up  *fakeUpstream
	m   *Mirror
}

func newFixture(t *testing.T, versions map[string]string) *fixture {
	t.Helper()
	dir := t.TempDir()
	st := localfs.New(dir)
	up := &fakeUpstream{versions: versions}
	tick := 0
	base := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	m := &Mirror{
		Upstream: up,
		Files:    st,
		Indexes:  st,
		Now: func() time.Time {
			tick++
			return base.Add(time.Duration(tick) * time.Second)
		},
	}
	return &fixture{t: t, dir: dir, up: up, m: m}
}

func (f *fixture) run(req Request) (*Result, error) {
	f.t.Helper()
	return f.m.Run(context.Background(), req)
}

func (f *fixture) mustRun(req Request) *Result {
	f.t.Helper()
	res, err := f.run(req)
	if err != nil {
		f.t.Fatalf("Run(%+v): %v", req, err)
	}
	return res
}

func (f *fixture) index() *index.Index {
	f.t.Helper()
	idx, err := index.Load(filepath.Join(f.dir, index.FileName))
	if err != nil {
		f.t.Fatal(err)
	}
	return idx
}

func (f *fixture) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(f.dir, filepath.FromSlash(rel)))
	return err == nil
}

func TestNewPackageWithTag(t *testing.T) {
	f := newFixture(t, map[string]string{pkg + "@version:1.0": "v1"})
	res := f.mustRun(Request{Package: pkg, Version: "version:1.0"})
	if !res.NewInstance || !res.Uploaded || !res.BindingChanged {
		t.Fatalf("unexpected result %+v", res)
	}
	digest := hexFor(t, "v1")
	p := f.index().Packages[pkg]
	if p == nil || p.Tags["version:1.0"] != digest {
		t.Fatalf("index does not bind the tag: %+v", p)
	}
	rel := InstancePath(pkg, digest)
	data, err := os.ReadFile(filepath.Join(f.dir, filepath.FromSlash(rel)))
	if err != nil || string(data) != "v1" {
		t.Fatalf("stored file: %q, %v", data, err)
	}
	if inst, err := f.index().Resolve(pkg, "version:1.0"); err != nil || inst.File != rel || inst.Size != 2 {
		t.Fatalf("Resolve: %+v, %v", inst, err)
	}
}

func TestRerunChangesNothing(t *testing.T) {
	f := newFixture(t, map[string]string{pkg + "@version:1.0": "v1"})
	f.mustRun(Request{Package: pkg, Version: "version:1.0"})
	res := f.mustRun(Request{Package: pkg, Version: "version:1.0"})
	if res.Changed() || res.Uploaded {
		t.Fatalf("unexpected result %+v", res)
	}
	if f.up.fetches != 1 {
		t.Fatalf("fetched %d times, want 1", f.up.fetches)
	}
	if f.exists(snapshotDir) {
		t.Fatal("a no-op run must not write a snapshot")
	}
}

func TestSecondBindingReusesInstance(t *testing.T) {
	f := newFixture(t, map[string]string{
		pkg + "@version:1.0": "v1",
		pkg + "@latest":      "v1",
	})
	f.mustRun(Request{Package: pkg, Version: "version:1.0"})
	res := f.mustRun(Request{Package: pkg, Version: "latest"})
	if res.NewInstance || res.Uploaded || !res.BindingChanged {
		t.Fatalf("unexpected result %+v", res)
	}
	if f.up.fetches != 1 {
		t.Fatalf("fetched %d times, want 1", f.up.fetches)
	}
	p := f.index().Packages[pkg]
	digest := hexFor(t, "v1")
	if p.Tags["version:1.0"] != digest || p.Refs["latest"] != digest {
		t.Fatalf("bindings missing: %+v", p)
	}
	if !f.exists(snapshotDir) {
		t.Fatal("a changing run must keep a snapshot of the previous index")
	}
}

func TestTagConflict(t *testing.T) {
	f := newFixture(t, map[string]string{pkg + "@version:1.0": "v1"})
	f.mustRun(Request{Package: pkg, Version: "version:1.0"})
	f.up.versions[pkg+"@version:1.0"] = "v2"

	_, err := f.run(Request{Package: pkg, Version: "version:1.0"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v, want ErrConflict", err)
	}
	if f.up.fetches != 1 {
		t.Fatalf("the conflicting instance must not be downloaded (fetches = %d)", f.up.fetches)
	}
	if got := f.index().Packages[pkg].Tags["version:1.0"]; got != hexFor(t, "v1") {
		t.Fatalf("tag was moved to %s", got)
	}
}

func TestRefMove(t *testing.T) {
	f := newFixture(t, map[string]string{pkg + "@latest": "v1"})
	f.mustRun(Request{Package: pkg, Version: "latest"})
	f.up.versions[pkg+"@latest"] = "v2"

	if _, err := f.run(Request{Package: pkg, Version: "latest"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v, want ErrConflict without AllowMoveRef", err)
	}
	f.mustRun(Request{Package: pkg, Version: "latest", AllowMoveRef: true})

	p := f.index().Packages[pkg]
	if p.Refs["latest"] != hexFor(t, "v2") {
		t.Fatalf("ref did not move: %+v", p.Refs)
	}
	if len(p.Instances) != 2 {
		t.Fatalf("old instance must stay in the index, got %d instances", len(p.Instances))
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	f := newFixture(t, map[string]string{pkg + "@version:1.0": "v1"})
	res := f.mustRun(Request{Package: pkg, Version: "version:1.0", DryRun: true})
	if !res.NewInstance || res.Uploaded {
		t.Fatalf("unexpected result %+v", res)
	}
	if f.up.fetches != 0 {
		t.Fatal("dry run must not download")
	}
	entries, _ := os.ReadDir(f.dir)
	if len(entries) != 0 {
		t.Fatalf("dry run wrote files: %v", entries)
	}
}

func TestCorruptDownloadIsRejected(t *testing.T) {
	f := newFixture(t, map[string]string{pkg + "@version:1.0": "v1"})
	f.up.corrupt = true
	_, err := f.run(Request{Package: pkg, Version: "version:1.0"})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("got %v, want a digest mismatch error", err)
	}
	entries, _ := os.ReadDir(f.dir)
	if len(entries) != 0 {
		t.Fatalf("nothing may be written after a failed download: %v", entries)
	}
}

func TestVersionByInstanceID(t *testing.T) {
	f := newFixture(t, map[string]string{pkg + "@version:1.0": "v1"})
	res := f.mustRun(Request{Package: pkg, Version: idFor("v1")})
	if res.Binding != "" || res.BindingChanged || !res.NewInstance {
		t.Fatalf("unexpected result %+v", res)
	}
	p := f.index().Packages[pkg]
	if len(p.Tags) != 0 || len(p.Refs) != 0 {
		t.Fatalf("an instance ID must not create a tag or ref: %+v", p)
	}
	if _, err := f.index().Resolve(pkg, hexFor(t, "v1")); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidInputIsRejectedEarly(t *testing.T) {
	f := newFixture(t, map[string]string{pkg + "@latest": "v1"})
	for _, req := range []Request{
		{Package: "../escape", Version: "latest"},
		{Package: "A/B", Version: "latest"},
		{Package: pkg, Version: ""},
		{Package: pkg, Version: "Latest"},
		{Package: pkg, Version: ":x"},
	} {
		if _, err := f.run(req); err == nil {
			t.Errorf("Run(%+v) succeeded", req)
		}
	}
	if f.up.resolves != 0 {
		t.Fatalf("upstream was contacted %d times for invalid input", f.up.resolves)
	}
}

func TestRecoversFromHalfDoneRun(t *testing.T) {
	f := newFixture(t, map[string]string{pkg + "@version:1.0": "v1"})
	rel := InstancePath(pkg, hexFor(t, "v1"))
	if err := f.m.Files.Put(context.Background(), rel, strings.NewReader("v1"), 2); err != nil {
		t.Fatal(err)
	}
	res := f.mustRun(Request{Package: pkg, Version: "version:1.0"})
	if res.Uploaded || !res.NewInstance {
		t.Fatalf("unexpected result %+v", res)
	}
	if f.up.fetches != 0 {
		t.Fatal("the stored file was already valid, nothing to download")
	}
	if _, err := f.index().Resolve(pkg, "version:1.0"); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptStoredFileIsNotIndexed(t *testing.T) {
	f := newFixture(t, map[string]string{pkg + "@version:1.0": "v1"})
	rel := InstancePath(pkg, hexFor(t, "v1"))
	if err := f.m.Files.Put(context.Background(), rel, strings.NewReader("junk"), 4); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run(Request{Package: pkg, Version: "version:1.0"}); err == nil {
		t.Fatal("expected an error for a corrupt stored file")
	}
	if f.exists(index.FileName) {
		t.Fatal("index must not be written")
	}
}
