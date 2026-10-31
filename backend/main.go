package main

import (
	"bytes"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"moulahmoulah/processors"
)

// dist holds the built frontend (copied there by `make build`).
// In dev, the Vite server on :5173 serves the frontend instead.
//
//go:embed all:dist
var distFS embed.FS

func main() {
	// Register the available processors. Stripe only, for now.
	processors.NewStripeProcessor()

	// Load the product catalog (Open Food Facts, cached on disk).
	setCatalog(loadCatalog())

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/catalog", handleCatalog)
	mux.HandleFunc("POST /api/checkout", handleCheckout)
	mux.HandleFunc("GET /api/orders/{id}", handleOrder)
	mux.HandleFunc("POST /api/orders/{id}/capture", handleCapture)
	mux.HandleFunc("/", spaHandler())

	addr := ":3000"
	if v := os.Getenv("PORT"); v != "" {
		addr = ":" + v
	}
	log.Printf("moulahmoulah listening on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// spaHandler serves the embedded frontend build with an index.html fallback
// for client-side routes. /api/* is never served as a static file.
func spaHandler() http.HandlerFunc {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		log.Fatal(err)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		name := path
		data, err := fs.ReadFile(sub, path)
		if path == "" || err != nil {
			// SPA fallback: client-side route -> index.html
			name = "index.html"
			data, err = fs.ReadFile(sub, name)
			if err != nil {
				http.NotFound(w, r)
				return
			}
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	}
}
