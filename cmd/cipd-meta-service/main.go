package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/iMu3ic/cipd-meta-service/internal/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8099", "address to listen on")
	root := flag.String("root", ".", "store root directory with .cipd files")
	indexPath := flag.String("index", "", "path to index.json (default: <root>/index.json)")
	publicURL := flag.String("public-url", "", "base URL clients use to download files (default: http://<addr>)")
	reload := flag.Duration("reload-interval", 30*time.Second, "how often to re-read the index (0 disables)")
	flag.Parse()

	if *indexPath == "" {
		*indexPath = filepath.Join(*root, "index.json")
	}
	if *publicURL == "" {
		*publicURL = "http://" + *addr
	}

	srv := server.New(*root, *publicURL)
	if err := srv.Reload(*indexPath); err != nil {
		log.Fatal(err)
	}

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	var tick <-chan time.Time
	if *reload > 0 {
		t := time.NewTicker(*reload)
		defer t.Stop()
		tick = t.C
	}
	go func() {
		for {
			select {
			case <-hup:
			case <-tick:
			}
			if err := srv.Reload(*indexPath); err != nil {
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
	log.Printf("listening on %s, index %s, root %s", *addr, *indexPath, *root)
	log.Fatal(httpSrv.ListenAndServe())
}
