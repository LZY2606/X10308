// Command server runs the fully local Petri net review workspace.
//
// Start with:
//
//	go run ./cmd/server --addr 127.0.0.1:5393
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"tflocal/internal/httpapi"
	"tflocal/internal/service"
	"tflocal/internal/store"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:5393", "listen address")
	dbPath := flag.String("db", "", "SQLite database path (defaults to ./.tflocal/workspace.db)")
	flag.Parse()
	if *dbPath == "" {
		*dbPath = filepath.Join(".tflocal", "workspace.db")
	}
	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open database %s: %v", *dbPath, err)
	}
	defer db.Close()
	svc := service.New(db)
	srv := httpapi.New(svc)
	log.Printf("tflocal workspace listening on http://%s (db=%s)", *addr, *dbPath)
	if err := http.ListenAndServe(*addr, srv.Mux); err != nil {
		log.Fatal(err)
	}
}
