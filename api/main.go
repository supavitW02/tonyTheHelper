// Command server runs the tonyTheHelper API and serves the web app embedded in it.
//
// Per ADR-0003 the frontend ships inside this binary: `vite build` writes into
// ./static, which is embedded below. One image, one deployment, no CORS.
package main

import (
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/supavitW02/tonyTheHelper/api/internal/server"
)

//go:embed all:static
var embedded embed.FS

func main() {
	assets, err := fs.Sub(embedded, "static")
	if err != nil {
		log.Fatalf("embedded web app: %v", err)
	}

	addr := ":" + port()
	srv := &http.Server{
		Addr:              addr,
		Handler:           server.New(assets),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// port follows the PORT environment variable so the same binary runs locally and
// on Fly.io without a flag.
func port() string {
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return "8080"
}
