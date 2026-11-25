// Package mirror copies CIPD packages from an upstream CIPD backend into a
// Storage and records them in the index that cipd-meta-service serves.
//
// The order of writes matters: package files go first, the index goes last, so
// the service can never point at a file that does not exist yet.
package mirror

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/iMu3ic/cipd-meta-service/internal/index"
	"github.com/iMu3ic/cipd-meta-service/internal/storage"
)

const snapshotDir = "snapshots"

// ErrConflict means a tag or ref in the index disagrees with upstream.
var ErrConflict = errors.New("conflict")

// Upstream is the source of packages, normally the public CIPD backend.
type Upstream interface {
	// Resolve returns the CIPD instance ID that version of pkg points to.
	Resolve(ctx context.Context, pkg, version string) (string, error)
	// Fetch downloads the instance file of pkg with the given instance ID
	// into the file dst.
	Fetch(ctx context.Context, pkg, instanceID, dst string) error
}

// Mirror mirrors packages.
type Mirror struct {
	Upstream Upstream
	// Files holds package files. It should refuse to overwrite them.
	Files storage.Storage
	// Indexes holds index.json and its snapshots. It must allow overwrites.
	Indexes storage.Storage
	// Now returns the current time; time.Now if nil.
	Now func() time.Time
}

// Request asks to mirror one version of one package.
type Request struct {
	Package string
	// Version is a tag ("key:value"), a ref, or an instance ID.
	Version string
	// AllowMoveRef lets an existing ref point to a different instance.
	AllowMoveRef bool
	// DryRun reports what would change without downloading or writing.
	DryRun bool
}

// Result describes what a request did (or would do, for a dry run).
type Result struct {
	InstanceID string
	HexDigest  string
	// NewInstance is true if the instance was not in the index before.
	NewInstance bool
	// Uploaded is true if a package file was written to Files.
	Uploaded bool
	// Binding is the tag or ref recorded for the instance, if any.
	Binding string
	// BindingChanged is true if Binding was added or moved.
	BindingChanged bool
}

// Changed reports whether the index changes.
func (r *Result) Changed() bool { return r.NewInstance || r.BindingChanged }

// InstancePath is where the file of an instance is stored.
func InstancePath(pkg, hexDigest string) string {
	return path.Join("pkgs", pkg, hexDigest+".cipd")
}

type bindingKind int

const (
	bindNone bindingKind = iota // the version is an instance ID
	bindTag
	bindRef
)

func kindOf(version string) bindingKind {
	if index.IsHexDigest(version) {
		return bindNone
	}
	if _, ok := index.HexFromInstanceID(version); ok {
		return bindNone
	}
	if strings.Contains(version, ":") {
		return bindTag
	}
	return bindRef
}

func (m *Mirror) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Mirror) loadIndex(ctx context.Context) (*index.Index, []byte, error) {
	rc, err := m.Indexes.Get(ctx, index.FileName)
	if errors.Is(err, storage.ErrNotFound) {
		return &index.Index{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer rc.Close()
	var raw bytes.Buffer
	idx, err := index.Parse(io.TeeReader(rc, &raw))
	if err != nil {
		return nil, nil, fmt.Errorf("existing index is invalid, refusing to touch it: %w", err)
	}
	return idx, raw.Bytes(), nil
}

// Run mirrors one version of one package.
func (m *Mirror) Run(ctx context.Context, req Request) (*Result, error) {
	if !index.ValidPackageName(req.Package) {
		return nil, fmt.Errorf("invalid package name %q", req.Package)
	}
	kind := kindOf(req.Version)
	switch {
	case req.Version == "":
		return nil, errors.New("empty version")
	case kind == bindTag && !index.ValidTag(req.Version):
		return nil, fmt.Errorf("invalid tag %q", req.Version)
	case kind == bindRef && !index.ValidRef(req.Version):
		return nil, fmt.Errorf("invalid ref %q", req.Version)
	}

	idx, oldRaw, err := m.loadIndex(ctx)
	if err != nil {
		return nil, err
	}

	id, err := m.Upstream.Resolve(ctx, req.Package, req.Version)
	if err != nil {
		return nil, fmt.Errorf("resolving %s@%s upstream: %w", req.Package, req.Version, err)
	}
	digest, ok := index.HexFromInstanceID(id)
	if !ok {
		return nil, fmt.Errorf("%s@%s: upstream instance %q is not a SHA-256 instance ID", req.Package, req.Version, id)
	}
	res := &Result{InstanceID: id, HexDigest: digest}

	var existing *index.Instance
	var current string // what the tag or ref points to now, if anything
	var hasCurrent bool
	if p := idx.Packages[req.Package]; p != nil {
		for i := range p.Instances {
			if p.Instances[i].HexDigest == digest {
				existing = &p.Instances[i]
			}
		}
		switch kind {
		case bindTag:
			current, hasCurrent = p.Tags[req.Version]
		case bindRef:
			current, hasCurrent = p.Refs[req.Version]
		}
	}
	res.NewInstance = existing == nil
	if kind != bindNone {
		res.Binding = req.Version
		res.BindingChanged = !hasCurrent || current != digest
	}
	if hasCurrent && current != digest {
		if kind == bindTag || !req.AllowMoveRef {
			return nil, fmt.Errorf("%w: %s of %s points to %s in the index, but upstream says %s",
				ErrConflict, req.Version, req.Package, current, digest)
		}
	}

	if req.DryRun || !res.Changed() {
		return res, nil
	}

	var inst index.Instance
	if existing != nil {
		inst = *existing
	} else {
		rel := InstancePath(req.Package, digest)
		size, uploaded, err := m.ensureFile(ctx, req.Package, id, digest, rel)
		if err != nil {
			return nil, err
		}
		res.Uploaded = uploaded
		inst = index.Instance{
			HashAlgo:  index.HashSHA256,
			HexDigest: digest,
			Size:      size,
			File:      rel,
		}
	}

	p := idx.AddInstance(req.Package, inst)
	switch kind {
	case bindTag:
		p.Tags[req.Version] = digest
	case bindRef:
		p.Refs[req.Version] = digest
	}
	data, err := idx.Marshal()
	if err != nil {
		return nil, fmt.Errorf("new index is invalid: %w", err)
	}

	if oldRaw != nil {
		name := fmt.Sprintf("%s/index-%s.json", snapshotDir, m.now().UTC().Format("20060102T150405.000000000Z"))
		if err := m.Indexes.Put(ctx, name, bytes.NewReader(oldRaw), int64(len(oldRaw))); err != nil {
			return nil, fmt.Errorf("saving the previous index: %w", err)
		}
	}
	if err := m.Indexes.Put(ctx, index.FileName, bytes.NewReader(data), int64(len(data))); err != nil {
		return nil, fmt.Errorf("writing the index: %w", err)
	}
	return res, nil
}

// ensureFile makes sure the instance file is in Files and matches digest. It
// returns the file size and whether a new file was written.
func (m *Mirror) ensureFile(ctx context.Context, pkg, id, digest, rel string) (int64, bool, error) {
	exists, err := m.Files.Exists(ctx, rel)
	if err != nil {
		return 0, false, err
	}
	if exists {
		// A previous run may have stopped before it updated the index. Do not
		// trust the stored file blindly.
		rc, err := m.Files.Get(ctx, rel)
		if err != nil {
			return 0, false, err
		}
		defer rc.Close()
		h := sha256.New()
		n, err := io.Copy(h, rc)
		if err != nil {
			return 0, false, err
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != digest {
			return 0, false, fmt.Errorf("stored file %s has digest %s, expected %s: remove it manually", rel, got, digest)
		}
		return n, false, nil
	}

	dir, err := os.MkdirTemp("", "cipd-mirror-*")
	if err != nil {
		return 0, false, err
	}
	defer os.RemoveAll(dir)
	tmp := filepath.Join(dir, "pkg.cipd")
	if err := m.Upstream.Fetch(ctx, pkg, id, tmp); err != nil {
		return 0, false, fmt.Errorf("downloading %s@%s: %w", pkg, id, err)
	}
	f, err := os.Open(tmp)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return 0, false, err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != digest {
		return 0, false, fmt.Errorf("downloaded file digest %s does not match instance %s", got, digest)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, false, err
	}
	if err := m.Files.Put(ctx, rel, f, size); err != nil {
		return 0, false, fmt.Errorf("uploading %s: %w", rel, err)
	}
	return size, true, nil
}
