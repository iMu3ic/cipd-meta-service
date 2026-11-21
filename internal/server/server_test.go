package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"go.chromium.org/luci/cipd/api/cipd/v1/caspb"
	"go.chromium.org/luci/cipd/api/cipd/v1/repopb"

	"github.com/iMu3ic/cipd-meta-service/internal/index"
)

const (
	testPkg    = "test/pkg"
	publicBase = "http://example.test"
	prpcBinary = "application/prpc; encoding=binary"
)

type env struct {
	srv       *Server
	ts        *httptest.Server
	indexPath string
	digest    string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("payload")
	write := func(path string, data []byte) {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "a.cipd"), payload)
	write(filepath.Join(root, "unlisted.txt"), []byte("unlisted-content-do-not-serve"))
	write(filepath.Join(dir, "secret.txt"), []byte("secret-content-do-not-serve"))

	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])

	idx := &index.Index{}
	p := idx.AddInstance(testPkg, index.Instance{
		HashAlgo: index.HashSHA256, HexDigest: digest, Size: int64(len(payload)), File: "a.cipd",
	})
	p.Refs["latest"] = digest
	p.Tags["version:1.0"] = digest
	indexPath := filepath.Join(root, "index.json")
	if err := idx.Save(indexPath); err != nil {
		t.Fatal(err)
	}

	srv := New(root, publicBase)
	if err := srv.Reload(indexPath); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &env{srv: srv, ts: ts, indexPath: indexPath, digest: digest}
}

func (e *env) call(t *testing.T, method, contentType string, req proto.Message) (*http.Response, []byte) {
	t.Helper()
	body, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(e.ts.URL+"/prpc/cipd.Repository/"+method, contentType, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

// get does not follow redirects, so the server's actual response is visible.
func (e *env) get(t *testing.T, path string) (int, string) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(e.ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func instanceID(t *testing.T, hexDigest string) string {
	t.Helper()
	raw, err := hex.DecodeString(hexDigest)
	if err != nil {
		t.Fatal(err)
	}
	return base64.URLEncoding.EncodeToString(append(raw, 2))
}

func grpcCode(resp *http.Response) string { return resp.Header.Get("X-Prpc-Grpc-Code") }

func TestResolveVersionOK(t *testing.T) {
	e := newEnv(t)
	for _, version := range []string{"latest", "version:1.0", e.digest, instanceID(t, e.digest)} {
		t.Run(version, func(t *testing.T) {
			resp, body := e.call(t, "ResolveVersion", prpcBinary,
				&repopb.ResolveVersionRequest{Package: testPkg, Version: version})
			if resp.StatusCode != 200 || grpcCode(resp) != "0" {
				t.Fatalf("status %d, grpc code %q", resp.StatusCode, grpcCode(resp))
			}
			var inst repopb.Instance
			if err := proto.Unmarshal(body, &inst); err != nil {
				t.Fatal(err)
			}
			if inst.GetInstance().GetHexDigest() != e.digest {
				t.Fatalf("got digest %q, want %q", inst.GetInstance().GetHexDigest(), e.digest)
			}
		})
	}
}

func TestResolveVersionNotFound(t *testing.T) {
	e := newEnv(t)
	for _, req := range []*repopb.ResolveVersionRequest{
		{Package: testPkg, Version: "stable"},
		{Package: testPkg, Version: "version:9.9"},
		{Package: "no/such/pkg", Version: "latest"},
	} {
		resp, _ := e.call(t, "ResolveVersion", prpcBinary, req)
		if resp.StatusCode != 404 || grpcCode(resp) != "5" {
			t.Errorf("%v: status %d, grpc code %q", req, resp.StatusCode, grpcCode(resp))
		}
	}
}

func TestResolveVersionBadRequests(t *testing.T) {
	e := newEnv(t)
	req := &repopb.ResolveVersionRequest{Package: testPkg, Version: "latest"}

	resp, _ := e.call(t, "ResolveVersion", "application/json", req)
	if grpcCode(resp) != "3" {
		t.Errorf("wrong content type: grpc code %q, want 3", grpcCode(resp))
	}
	resp, _ = e.call(t, "ResolveVersion", prpcBinary, &repopb.ResolveVersionRequest{Package: testPkg})
	if grpcCode(resp) != "3" {
		t.Errorf("empty version: grpc code %q, want 3", grpcCode(resp))
	}
}

func TestGetInstanceURL(t *testing.T) {
	e := newEnv(t)
	resp, body := e.call(t, "GetInstanceURL", prpcBinary, &repopb.GetInstanceURLRequest{
		Package:  testPkg,
		Instance: &caspb.ObjectRef{HashAlgo: caspb.HashAlgo_SHA256, HexDigest: e.digest},
	})
	if resp.StatusCode != 200 || grpcCode(resp) != "0" {
		t.Fatalf("status %d, grpc code %q", resp.StatusCode, grpcCode(resp))
	}
	var u caspb.ObjectURL
	if err := proto.Unmarshal(body, &u); err != nil {
		t.Fatal(err)
	}
	if want := publicBase + "/files/a.cipd"; u.GetSignedUrl() != want {
		t.Fatalf("got URL %q, want %q", u.GetSignedUrl(), want)
	}
}

func TestGetInstanceURLRejects(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.call(t, "GetInstanceURL", prpcBinary, &repopb.GetInstanceURLRequest{
		Package:  testPkg,
		Instance: &caspb.ObjectRef{HashAlgo: caspb.HashAlgo_SHA1, HexDigest: strings.Repeat("a", 40)},
	})
	if grpcCode(resp) != "3" {
		t.Errorf("sha1 ref: grpc code %q, want 3", grpcCode(resp))
	}
	resp, _ = e.call(t, "GetInstanceURL", prpcBinary, &repopb.GetInstanceURLRequest{
		Package:  testPkg,
		Instance: &caspb.ObjectRef{HashAlgo: caspb.HashAlgo_SHA256, HexDigest: strings.Repeat("c", 64)},
	})
	if grpcCode(resp) != "5" {
		t.Errorf("unknown instance: grpc code %q, want 5", grpcCode(resp))
	}
}

func TestUnimplementedMethod(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.call(t, "RegisterInstance", prpcBinary, &repopb.ResolveVersionRequest{})
	if resp.StatusCode != 501 || grpcCode(resp) != "12" {
		t.Fatalf("status %d, grpc code %q", resp.StatusCode, grpcCode(resp))
	}
}

func TestServeFile(t *testing.T) {
	e := newEnv(t)
	if code, body := e.get(t, "/files/a.cipd"); code != 200 || body != "payload" {
		t.Fatalf("listed file: status %d, body %q", code, body)
	}
	for _, path := range []string{
		"/files/index.json",
		"/files/unlisted.txt",
		"/files/../secret.txt",
		"/files/%2e%2e/secret.txt",
		"/files/..%2fsecret.txt",
		"/files/",
	} {
		// Without following redirects: what the server answers to the request itself.
		code, body := e.get(t, path)
		if code == 200 || strings.Contains(body, "-content-do-not-serve") {
			t.Errorf("%s: status %d, body %q (must not be served)", path, code, body)
		}

		// With redirects: what an ordinary client ends up receiving.
		resp, err := http.Get(e.ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(data), "-content-do-not-serve") {
			t.Errorf("%s: content leaked after following redirects", path)
		}
	}
}

func TestReloadKeepsPreviousIndexOnError(t *testing.T) {
	e := newEnv(t)
	if err := os.WriteFile(e.indexPath, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.Reload(e.indexPath); err == nil {
		t.Fatal("expected reload error")
	}
	resp, _ := e.call(t, "ResolveVersion", prpcBinary,
		&repopb.ResolveVersionRequest{Package: testPkg, Version: "latest"})
	if grpcCode(resp) != "0" {
		t.Fatalf("old index lost after failed reload: grpc code %q", grpcCode(resp))
	}
}
