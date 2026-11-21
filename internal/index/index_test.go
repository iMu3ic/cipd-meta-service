package index

import (
	"errors"
	"strings"
	"testing"
)

var (
	d1 = strings.Repeat("a", 64)
	d2 = strings.Repeat("b", 64)
)

const pkgName = "test/pkg/mac-arm64"

func sample() *Index {
	return &Index{Packages: map[string]*Package{
		pkgName: {
			Instances: []Instance{
				{HashAlgo: "sha256", HexDigest: d1, Size: 10, File: "a.cipd"},
				{HashAlgo: "sha256", HexDigest: d2, Size: 20, File: "sub/b.cipd"},
			},
			Refs: map[string]string{"latest": d2},
			Tags: map[string]string{"version:1.0": d1},
		},
	}}
}

func TestValidateOK(t *testing.T) {
	if err := sample().Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(p *Package)
	}{
		{"path traversal", func(p *Package) { p.Instances[0].File = "../x.cipd" }},
		{"absolute path", func(p *Package) { p.Instances[0].File = "/etc/passwd" }},
		{"empty path", func(p *Package) { p.Instances[0].File = "" }},
		{"backslash path", func(p *Package) { p.Instances[0].File = `a\b.cipd` }},
		{"bad digest", func(p *Package) { p.Instances[0].HexDigest = "xyz" }},
		{"bad algo", func(p *Package) { p.Instances[0].HashAlgo = "md5" }},
		{"negative size", func(p *Package) { p.Instances[0].Size = -1 }},
		{"duplicate digest", func(p *Package) { p.Instances[1].HexDigest = d1 }},
		{"dangling ref", func(p *Package) { p.Refs["latest"] = strings.Repeat("c", 64) }},
		{"bad ref name", func(p *Package) { p.Refs["Bad Ref"] = d1 }},
		{"tag without colon", func(p *Package) { p.Tags["nocolon"] = d1 }},
		{"dangling tag", func(p *Package) { p.Tags["k:v"] = strings.Repeat("c", 64) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx := sample()
			tc.mutate(idx.Packages[pkgName])
			if err := idx.Validate(); err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}

func TestResolve(t *testing.T) {
	idx := sample()
	cases := []struct {
		name, pkg, version string
		wantDigest         string
		wantErr            error
	}{
		{"ref", pkgName, "latest", d2, nil},
		{"tag", pkgName, "version:1.0", d1, nil},
		{"digest", pkgName, d1, d1, nil},
		{"unknown ref", pkgName, "stable", "", ErrNotFound},
		{"unknown tag", pkgName, "version:9.9", "", ErrNotFound},
		{"unknown digest", pkgName, strings.Repeat("c", 64), "", ErrNotFound},
		{"unknown package", "nope", "latest", "", ErrNotFound},
		{"empty version", pkgName, "", "", ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inst, err := idx.Resolve(tc.pkg, tc.version)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("got error %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if inst.HexDigest != tc.wantDigest {
				t.Fatalf("got digest %s, want %s", inst.HexDigest, tc.wantDigest)
			}
		})
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	_, err := Parse(strings.NewReader(`{"packages":{},"extra":1}`))
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestParseRejectsTrailingData(t *testing.T) {
	_, err := Parse(strings.NewReader(`{"packages":{}} {}`))
	if err == nil {
		t.Fatal("expected error for trailing data")
	}
}
