// Package index describes the package index read by cipd-meta-service.
package index

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	HashSHA256   = "sha256"
	maxIndexSize = 16 << 20 // 16 MiB
)

var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid argument")

	hexDigestRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	pkgNameRe   = regexp.MustCompile(`^[a-z0-9_.-]+(/[a-z0-9_.-]+)*$`)
	refRe       = regexp.MustCompile(`^[a-z0-9_./-]{1,256}$`)
)

// Instance is a single package file in the store.
type Instance struct {
	HashAlgo  string `json:"hash_algo"`
	HexDigest string `json:"hex_digest"`
	Size      int64  `json:"size"`
	File      string `json:"file"` // path relative to the store root
}

// Package holds the instances of one package, plus its refs and tags.
// Refs and tags point to the hex digest of an instance.
// In the MVP a tag points to at most one instance.
type Package struct {
	Instances []Instance        `json:"instances"`
	Refs      map[string]string `json:"refs"`
	Tags      map[string]string `json:"tags"`
}

type Index struct {
	Packages map[string]*Package `json:"packages"`
}

// Parse reads and validates an index. The input size is limited.
func Parse(r io.Reader) (*Index, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxIndexSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxIndexSize {
		return nil, fmt.Errorf("index is larger than %d bytes", maxIndexSize)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var idx Index
	if err := dec.Decode(&idx); err != nil {
		return nil, fmt.Errorf("decoding index: %w", err)
	}
	if dec.More() {
		return nil, errors.New("decoding index: unexpected data after JSON value")
	}
	if err := idx.Validate(); err != nil {
		return nil, err
	}
	return &idx, nil
}

func Load(file string) (*Index, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Save validates the index and writes it to a file atomically.
func (idx *Index) Save(file string) error {
	if err := idx.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".index-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, file); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// AddInstance adds an instance (or replaces an existing one with the same
// digest) and returns the package so the caller can set refs and tags.
func (idx *Index) AddInstance(name string, inst Instance) *Package {
	if idx.Packages == nil {
		idx.Packages = make(map[string]*Package)
	}
	p := idx.Packages[name]
	if p == nil {
		p = &Package{}
		idx.Packages[name] = p
	}
	if p.Refs == nil {
		p.Refs = make(map[string]string)
	}
	if p.Tags == nil {
		p.Tags = make(map[string]string)
	}
	for i := range p.Instances {
		if p.Instances[i].HexDigest == inst.HexDigest {
			p.Instances[i] = inst
			return p
		}
	}
	p.Instances = append(p.Instances, inst)
	return p
}

// Validate checks the whole index. This is a trust boundary: the index decides
// which files the service will serve, so paths and digests are checked strictly.
func (idx *Index) Validate() error {
	for name, p := range idx.Packages {
		if !pkgNameRe.MatchString(name) {
			return fmt.Errorf("package %q: invalid name", name)
		}
		if p == nil {
			return fmt.Errorf("package %q: null entry", name)
		}
		known := make(map[string]bool)
		for _, inst := range p.Instances {
			switch {
			case inst.HashAlgo != HashSHA256:
				return fmt.Errorf("package %q: unsupported hash_algo %q", name, inst.HashAlgo)
			case !hexDigestRe.MatchString(inst.HexDigest):
				return fmt.Errorf("package %q: invalid hex_digest %q", name, inst.HexDigest)
			case known[inst.HexDigest]:
				return fmt.Errorf("package %q: duplicate instance %s", name, inst.HexDigest)
			case inst.Size < 0:
				return fmt.Errorf("package %q: negative size", name)
			case !filepath.IsLocal(inst.File) || strings.ContainsRune(inst.File, '\\'):
				return fmt.Errorf("package %q: unsafe file path %q", name, inst.File)
			}
			known[inst.HexDigest] = true
		}
		for ref, digest := range p.Refs {
			if !refRe.MatchString(ref) {
				return fmt.Errorf("package %q: invalid ref %q", name, ref)
			}
			if !known[digest] {
				return fmt.Errorf("package %q: ref %q points to unknown instance", name, ref)
			}
		}
		for tag, digest := range p.Tags {
			key, val, ok := strings.Cut(tag, ":")
			if !ok || key == "" || val == "" {
				return fmt.Errorf("package %q: invalid tag %q", name, tag)
			}
			if !known[digest] {
				return fmt.Errorf("package %q: tag %q points to unknown instance", name, tag)
			}
		}
	}
	return nil
}

// Resolve turns a version (hex digest, "key:value" tag or ref) into an instance.
func (idx *Index) Resolve(pkg, version string) (Instance, error) {
	if version == "" {
		return Instance{}, fmt.Errorf("%w: empty version", ErrInvalid)
	}
	p, ok := idx.Packages[pkg]
	if !ok || p == nil {
		return Instance{}, fmt.Errorf("%w: package %q", ErrNotFound, pkg)
	}
	digest := version
	switch {
	case hexDigestRe.MatchString(version):
		// the version is already a digest
	case strings.Contains(version, ":"):
		digest, ok = p.Tags[version]
	default:
		digest, ok = p.Refs[version]
	}
	if !ok {
		return Instance{}, fmt.Errorf("%w: version %q of package %q", ErrNotFound, version, pkg)
	}
	for _, inst := range p.Instances {
		if inst.HexDigest == digest {
			return inst, nil
		}
	}
	return Instance{}, fmt.Errorf("%w: instance %s of package %q", ErrNotFound, digest, pkg)
}
