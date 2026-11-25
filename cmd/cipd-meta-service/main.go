package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/iMu3ic/cipd-meta-service/internal/index"
	"github.com/iMu3ic/cipd-meta-service/internal/nexus"
	"github.com/iMu3ic/cipd-meta-service/internal/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8099", "address to listen on")
	root := flag.String("root", ".", "store root directory with .cipd files (local mode)")
	indexPath := flag.String("index", "", "path to index.json (local mode; default: <root>/index.json)")
	publicURL := flag.String("public-url", "", "base URL clients use to download files in local mode (default: http://<addr>)")
	nexusIndexURL := flag.String("nexus-index-url", "", "Nexus raw repository with index.json (enables Nexus mode)")
	nexusFilesURL := flag.String("nexus-files-url", "", "Nexus raw repository with package files (Nexus mode)")
	every := flag.Duration("reload-interval", 30*time.Second, "how often to re-read the index (0 disables)")
	flag.Parse()

	if *indexPath == "" {
		*indexPath = filepath.Join(*root, index.FileName)
	}
	if *publicURL == "" {
		*publicURL = "http://" + *addr
	}

	srv := server.New(*root, *publicURL)
	loadIndex := func(context.Context) (*index.Index, error) {
		return index.Load(*indexPath)
	}
	mode := "local, index " + *indexPath
	if *nexusIndexURL != "" || *nexusFilesURL != "" {
		if *nexusIndexURL == "" || *nexusFilesURL == "" {
			log.Fatal("-nexus-index-url and -nexus-files-url must be set together")
		}
		// Read-only credentials; the service never writes to Nexus.
		indexes := nexus.New(*nexusIndexURL, os.Getenv("NEXUS_USER"), os.Getenv("NEXUS_PASSWORD"))
		srv.SetFileBase(*nexusFilesURL)
		loadIndex = func(ctx context.Context) (*index.Index, error) {
			rc, err := indexes.Get(ctx, index.FileName)
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return index.Parse(rc)
		}
		mode = "nexus, index " + *nexusIndexURL + ", files " + *nexusFilesURL
	}

	reload := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		idx, err := loadIndex(ctx)
		if err != nil {
			return fmt.Errorf("loading index: %w", err)
		}
		srv.Apply(idx)
		return nil
	}
	if err := reload(); err != nil {
		log.Fatal(err)
	}

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	var tick <-chan time.Time
	if *every > 0 {
		t := time.NewTicker(*every)
		defer t.Stop()
		tick = t.C
	}
	go func() {
		for {
			select {
			case <-hup:
			case <-tick:
			}
			if err := reload(); err != nil {
				log.Printf("index reload failed, keeping the previous one: %v", err)
			}
		}
	}()

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	log.Printf("listening on %s, mode: %s", *addr, mode)
	log.Fatal(httpSrv.ListenAndServe())
}
