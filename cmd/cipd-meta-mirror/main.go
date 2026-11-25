// Command cipd-meta-mirror copies CIPD packages from an upstream backend into
// a store (a Nexus repository or a local directory) and records them in the
// index served by cipd-meta-service.
//
// It needs internet access and write access to the store, so run it only in
// the sync zone, never next to the build runners.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/iMu3ic/cipd-meta-service/internal/mirror"
	"github.com/iMu3ic/cipd-meta-service/internal/nexus"
	"github.com/iMu3ic/cipd-meta-service/internal/storage"
	"github.com/iMu3ic/cipd-meta-service/internal/storage/localfs"
)

func main() { os.Exit(run()) }

func run() int {
	pkg := flag.String("package", "", "package name, e.g. infra/3pp/tools/foo/linux-amd64")
	version := flag.String("version", "", "version to mirror: tag (key:value), ref or instance ID")
	list := flag.String("list", "", "file with \"<package> <version>\" lines, instead of -package and -version")
	cipdBin := flag.String("cipd", "cipd", "path to the cipd client")
	upstream := flag.String("upstream-url", "", "upstream CIPD backend URL (default: the client's default)")
	storeDir := flag.String("store-dir", "", "local store directory (alternative to Nexus, for testing)")
	nexusFiles := flag.String("nexus-files-url", "", "Nexus raw repository for package files (should forbid redeploy)")
	nexusIndex := flag.String("nexus-index-url", "", "Nexus raw repository for index.json (must allow redeploy)")
	moveRef := flag.Bool("allow-move-ref", false, "allow an existing ref to point to a new instance")
	dryRun := flag.Bool("dry-run", false, "show what would change without downloading or writing")
	flag.Parse()

	reqs, err := requests(*pkg, *version, *list)
	if err != nil {
		log.Print(err)
		return 2
	}

	var files, indexes storage.Storage
	switch {
	case *storeDir != "" && *nexusFiles == "" && *nexusIndex == "":
		st := localfs.New(*storeDir)
		files, indexes = st, st
	case *storeDir == "" && *nexusFiles != "" && *nexusIndex != "":
		// Write credentials come from the environment, not from flags, so
		// they do not end up in process lists or shell history.
		user, pass := os.Getenv("NEXUS_USER"), os.Getenv("NEXUS_PASSWORD")
		files = nexus.New(*nexusFiles, user, pass)
		indexes = nexus.New(*nexusIndex, user, pass)
	default:
		log.Print("set either -store-dir, or both -nexus-files-url and -nexus-index-url")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	m := &mirror.Mirror{
		Upstream: &mirror.CLI{Binary: *cipdBin, ServiceURL: *upstream},
		Files:    files,
		Indexes:  indexes,
	}
	failed := 0
	for _, req := range reqs {
		req.AllowMoveRef = *moveRef
		req.DryRun = *dryRun
		res, err := m.Run(ctx, req)
		if err != nil {
			log.Printf("FAIL %s@%s: %v", req.Package, req.Version, err)
			failed++
			continue
		}
		log.Printf("ok %s@%s id=%s new_instance=%t uploaded=%t binding=%q binding_changed=%t",
			req.Package, req.Version, res.InstanceID, res.NewInstance, res.Uploaded, res.Binding, res.BindingChanged)
	}
	if failed > 0 {
		log.Printf("%d of %d requests failed", failed, len(reqs))
		return 1
	}
	return 0
}

func requests(pkg, version, list string) ([]mirror.Request, error) {
	if list == "" {
		if pkg == "" || version == "" {
			return nil, fmt.Errorf("set -package and -version, or -list")
		}
		return []mirror.Request{{Package: pkg, Version: version}}, nil
	}
	if pkg != "" || version != "" {
		return nil, fmt.Errorf("-list cannot be combined with -package or -version")
	}
	f, err := os.Open(list)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var reqs []mirror.Request
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s:%d: want \"<package> <version>\", got %q", list, n, line)
		}
		reqs = append(reqs, mirror.Request{Package: fields[0], Version: fields[1]})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(reqs) == 0 {
		return nil, fmt.Errorf("%s: no packages listed", list)
	}
	return reqs, nil
}
