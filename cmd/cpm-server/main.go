// Command cpm-server runs the CPM scheduling kernel as a single-container
// HTTP service with a local file job repository.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"cpm/httpapi"
	"cpm/store"
)

func main() {
	addr := envOr("CPM_ADDR", ":8080")
	dataDir := envOr("CPM_DATA_DIR", "/data")
	flag.StringVar(&addr, "addr", addr, "listen address")
	flag.StringVar(&dataDir, "data", dataDir, "directory for persisted job files")
	flag.Parse()

	jobs, err := store.NewFileStore(dataDir)
	if err != nil {
		log.Fatalf("init file store: %v", err)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewServer(jobs),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("cpm-scheduler listening on %s, jobs in %s", addr, dataDir)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
