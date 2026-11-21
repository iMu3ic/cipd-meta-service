package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/iMu3ic/cipd-meta-service/internal/index"
)

func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' })
}

func main() {
	indexPath := flag.String("index", "index.json", "path to index.json")
	root := flag.String("root", ".", "store root directory")
	name := flag.String("name", "", "package name, e.g. test/pkg/mac-arm64")
	file := flag.String("file", "", "path to the .cipd file (must be inside -root)")
	refs := flag.String("refs", "latest", "comma-separated refs to point at this instance")
	tags := flag.String("tags", "", "comma-separated key:value tags")
	flag.Parse()

	if *name == "" || *file == "" {
		log.Fatal("-name and -file are required")
	}
	if err := run(*indexPath, *root, *name, *file, splitList(*refs), splitList(*tags)); err != nil {
		log.Fatal(err)
	}
}

func run(indexPath, root, name, file string, refs, tags []string) error {
	rel, err := filepath.Rel(root, file)
	if err != nil || !filepath.IsLocal(rel) {
		return fmt.Errorf("%s is not inside store root %s", file, root)
	}

	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	digest := hex.EncodeToString(h.Sum(nil))

	idx, err := index.Load(indexPath)
	if errors.Is(err, fs.ErrNotExist) {
		idx = &index.Index{}
	} else if err != nil {
		return err
	}

	p := idx.AddInstance(name, index.Instance{
		HashAlgo:  index.HashSHA256,
		HexDigest: digest,
		Size:      size,
		File:      filepath.ToSlash(rel),
	})
	for _, r := range refs {
		p.Refs[r] = digest
	}
	for _, t := range tags {
		p.Tags[t] = digest
	}

	if err := idx.Save(indexPath); err != nil {
		return err
	}
	log.Printf("indexed %s sha256=%s size=%d", name, digest, size)
	return nil
}
