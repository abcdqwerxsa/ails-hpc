package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	port := flag.String("port", "3000", "Port to serve on")
	dir := flag.String("dir", "/opt/ails-hpc-web", "Directory to serve")
	flag.Parse()

	fileServer := http.FileServer(http.Dir(*dir))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent browser caching for instant updates
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, max-age=0")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")

		path := filepath.Join(*dir, r.URL.Path)
		_, err := os.Stat(path)
		if os.IsNotExist(err) {
			// Fallback to index.html for Single Page Application (SPA) routing
			http.ServeFile(w, r, filepath.Join(*dir, "index.html"))
			return
		}

		fileServer.ServeHTTP(w, r)
	})

	addr := fmt.Sprintf("0.0.0.0:%s", *port)
	log.Printf("Starting AILS HPC Web Server on %s for dir %s...", addr, *dir)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
