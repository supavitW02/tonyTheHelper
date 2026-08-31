// Command server runs the tonyTheHelper API and serves the web app embedded in it.
//
// Per ADR-0003 the frontend ships inside this binary: `vite build` writes into
// ./static, which is embedded below. One image, one deployment, no CORS.
package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/supavitW02/tonyTheHelper/api/internal/config"
	"github.com/supavitW02/tonyTheHelper/api/internal/line"
	"github.com/supavitW02/tonyTheHelper/api/internal/server"
	"github.com/supavitW02/tonyTheHelper/api/internal/session"
	"github.com/supavitW02/tonyTheHelper/api/internal/storage"
	"github.com/supavitW02/tonyTheHelper/api/internal/store"
)

//go:embed all:static
var embedded embed.FS

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	assets, err := fs.Sub(embedded, "static")
	if err != nil {
		return err
	}

	// A generous startup budget: Fly machines wake from suspend, and a cold Postgres
	// connection on the far side of a region is not instant.
	startupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := store.Open(startupCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	// Migrations run at startup. With a single machine there is no window in which two
	// versions of the schema are live at once; revisit this if the app is ever scaled out.
	if err := db.Migrate(startupCtx); err != nil {
		return err
	}
	log.Print("database ready")

	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: server.New(server.Deps{
			Assets:     assets,
			LiffID:     cfg.LiffID,
			Verifier:   line.NewVerifier(cfg.LineChannelID),
			Users:      db,
			Households: db,
			Items:      db,
			Storage:    storage.New(cfg.SupabaseURL, cfg.SupabaseBucket, cfg.SupabaseServiceKey),
			Sessions:   session.NewManager(cfg.SessionSecret),
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	return serve(srv)
}

// serve runs the server until interrupted, then lets in-flight requests finish.
func serve(srv *http.Server) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errs := make(chan error, 1)
	go func() {
		log.Printf("listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		log.Print("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
