package index

import (
	"strings"
	"testing"
)

func TestValidNames(t *testing.T) {
	for _, s := range []string{"a", "infra/3pp/tools/foo/linux-amd64", "x_y.z/w-1"} {
		if !ValidPackageName(s) {
			t.Errorf("ValidPackageName(%q) = false", s)
		}
	}
	for _, s := range []string{"", ".", "..", "../x", "a/../b", "/abs", "A/b", "a//b", "a b", `a\b`} {
		if ValidPackageName(s) {
			t.Errorf("ValidPackageName(%q) = true", s)
		}
	}
	for _, s := range []string{"latest", "release-1.2", "a/b"} {
		if !ValidRef(s) {
			t.Errorf("ValidRef(%q) = false", s)
		}
	}
	for _, s := range []string{"", "Latest", "a b", strings.Repeat("a", 257)} {
		if ValidRef(s) {
			t.Errorf("ValidRef(%q) = true", s)
		}
	}
	for _, s := range []string{"version:1.0", "git_revision:abc"} {
		if !ValidTag(s) {
			t.Errorf("ValidTag(%q) = false", s)
		}
	}
	for _, s := range []string{"", "version", ":x", "x:"} {
		if ValidTag(s) {
			t.Errorf("ValidTag(%q) = true", s)
		}
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	data, err := sample().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	idx, err := Parse(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Resolve(pkgName, "latest"); err != nil {
		t.Fatal(err)
	}
}

func TestMarshalRejectsInvalid(t *testing.T) {
	idx := sample()
	idx.Packages[pkgName].Refs["latest"] = strings.Repeat("c", 64)
	if _, err := idx.Marshal(); err == nil {
		t.Fatal("expected validation error")
	}
}
