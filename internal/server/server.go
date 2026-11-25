// Package server implements a read-only subset of cipd.Repository.
package server

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"

	"go.chromium.org/luci/cipd/api/cipd/v1/caspb"
	"go.chromium.org/luci/cipd/api/cipd/v1/repopb"

	"github.com/iMu3ic/cipd-meta-service/internal/index"
	"github.com/iMu3ic/cipd-meta-service/internal/prpc"
)

type snapshot struct {
	idx   *index.Index
	files map[string]bool // relative paths that are allowed to be served
}

type Server struct {
	root    string
	baseURL string
	snap    atomic.Pointer[snapshot]

	// fileBase, when set, is the base URL of the storage that holds the
	// package files (a Nexus repository). Clients then download from it
	// directly and this server does not serve files itself.
	fileBase atomic.Pointer[string]
}

func New(root, publicURL string) *Server {
	return &Server{root: root, baseURL: strings.TrimRight(publicURL, "/")}
}

// SetFileBase makes GetInstanceURL return URLs under base instead of this
// server's own /files/ endpoint, and turns that endpoint off.
func (s *Server) SetFileBase(base string) {
	base = strings.TrimRight(base, "/")
	s.fileBase.Store(&base)
}

// Reload re-reads the index from a local file. On error the previous index
// stays in use.
func (s *Server) Reload(indexPath string) error {
	idx, err := index.Load(indexPath)
	if err != nil {
		return fmt.Errorf("loading index: %w", err)
	}
	s.Apply(idx)
	return nil
}

// Apply starts serving a new, already validated index.
func (s *Server) Apply(idx *index.Index) {
	files := make(map[string]bool)
	for _, p := range idx.Packages {
		for _, inst := range p.Instances {
			files[inst.File] = true
		}
	}
	s.snap.Store(&snapshot{idx: idx, files: files})
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /prpc/cipd.Repository/ResolveVersion", s.resolveVersion)
	mux.HandleFunc("POST /prpc/cipd.Repository/GetInstanceURL", s.getInstanceURL)
	mux.HandleFunc("GET /files/", s.serveFile)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("unhandled: %s %s", r.Method, r.URL.Path)
		prpc.WriteError(w, prpc.CodeUnimplemented, "not implemented")
	})
	return mux
}

func (s *Server) writeIndexError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, index.ErrNotFound):
		prpc.WriteError(w, prpc.CodeNotFound, err.Error())
	case errors.Is(err, index.ErrInvalid):
		prpc.WriteError(w, prpc.CodeInvalidArgument, err.Error())
	default:
		log.Printf("internal error: %v", err)
		prpc.WriteError(w, prpc.CodeInternal, "internal error")
	}
}

func (s *Server) resolveVersion(w http.ResponseWriter, r *http.Request) {
	var req repopb.ResolveVersionRequest
	if !prpc.Decode(w, r, &req) {
		return
	}
	version := req.GetVersion()
	if hexDigest, ok := index.HexFromInstanceID(version); ok {
		version = hexDigest
	}
	inst, err := s.snap.Load().idx.Resolve(req.GetPackage(), version)
	if err != nil {
		s.writeIndexError(w, err)
		return
	}
	log.Printf("ResolveVersion %s@%s -> %s", req.GetPackage(), req.GetVersion(), inst.HexDigest)
	prpc.WriteMessage(w, &repopb.Instance{
		Package: req.GetPackage(),
		Instance: &caspb.ObjectRef{
			HashAlgo:  caspb.HashAlgo_SHA256,
			HexDigest: inst.HexDigest,
		},
	})
}

func (s *Server) getInstanceURL(w http.ResponseWriter, r *http.Request) {
	var req repopb.GetInstanceURLRequest
	if !prpc.Decode(w, r, &req) {
		return
	}
	ref := req.GetInstance()
	if ref == nil || ref.GetHashAlgo() != caspb.HashAlgo_SHA256 || !index.IsHexDigest(ref.GetHexDigest()) {
		prpc.WriteError(w, prpc.CodeInvalidArgument, "unsupported or malformed instance reference")
		return
	}
	inst, err := s.snap.Load().idx.Resolve(req.GetPackage(), ref.GetHexDigest())
	if err != nil {
		s.writeIndexError(w, err)
		return
	}
	u := url.URL{Path: "/files/" + inst.File}
	target := s.baseURL + u.EscapedPath()
	if base := s.fileBase.Load(); base != nil && *base != "" {
		u = url.URL{Path: "/" + inst.File}
		target = *base + u.EscapedPath()
	}
	log.Printf("GetInstanceURL %s@%s", req.GetPackage(), inst.HexDigest)
	prpc.WriteMessage(w, &caspb.ObjectURL{SignedUrl: target})
}

// serveFile serves only files listed in the index.
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request) {
	if base := s.fileBase.Load(); base != nil && *base != "" {
		http.NotFound(w, r)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, "/files/")
	if !s.snap.Load().files[rel] {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.root, filepath.FromSlash(rel)))
}
