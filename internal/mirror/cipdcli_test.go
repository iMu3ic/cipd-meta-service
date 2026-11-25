package mirror

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestParseResolve(t *testing.T) {
	id := idFor("x")
	got, err := parseResolve([]byte("some log line\ntest/pkg:" + id + "\n"))
	if err != nil || got != id {
		t.Fatalf("got %q, %v; want %q", got, err, id)
	}
	for _, bad := range []string{"", "no colon here", "test/pkg:short", "test/pkg:" + strings.Repeat("A", 44)} {
		if _, err := parseResolve([]byte(bad)); err == nil {
			t.Errorf("parseResolve(%q) succeeded", bad)
		}
	}
}

func TestCLIArgs(t *testing.T) {
	id := idFor("x")
	var got []string
	c := &CLI{
		Binary:     "/opt/cipd",
		ServiceURL: "https://up.example",
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			got = append([]string{name}, args...)
			return []byte("test/pkg:" + id + "\n"), nil
		},
	}

	if _, err := c.Resolve(context.Background(), "test/pkg", "version:1.0"); err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/cipd", "resolve", "test/pkg", "-version", "version:1.0", "-service-url", "https://up.example"}
	if !slices.Equal(got, want) {
		t.Fatalf("resolve args = %v, want %v", got, want)
	}

	if err := c.Fetch(context.Background(), "test/pkg", id, "/tmp/out.cipd"); err != nil {
		t.Fatal(err)
	}
	want = []string{"/opt/cipd", "pkg-fetch", "test/pkg", "-version", id, "-out", "/tmp/out.cipd", "-service-url", "https://up.example"}
	if !slices.Equal(got, want) {
		t.Fatalf("fetch args = %v, want %v", got, want)
	}
}
