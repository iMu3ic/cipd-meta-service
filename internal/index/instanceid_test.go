package index

import "testing"

func TestHexFromInstanceID(t *testing.T) {
	const (
		id  = "8NKSPFuh_JFg9QueS8wP8tCQsQ7byycxivMZTa_vyAsC"
		hex = "f0d2923c5ba1fc9160f50b9e4bcc0ff2d090b10edbcb27318af3194dafefc80b"
	)
	got, ok := HexFromInstanceID(id)
	if !ok || got != hex {
		t.Fatalf("got (%q, %v), want (%q, true)", got, ok, hex)
	}
	for _, bad := range []string{"", "latest", "version:1.0", id[:43], id[:43] + "A"} {
		if _, ok := HexFromInstanceID(bad); ok {
			t.Errorf("HexFromInstanceID(%q) unexpectedly succeeded", bad)
		}
	}
}
