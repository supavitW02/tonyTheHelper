// Package server wires up the HTTP surface of the API: the health check and the
// embedded web app. It knows nothing about where the web app came from.
package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// New builds the HTTP handler.
//
// assets is the built web app. It is passed in rather than embedded here so that
// tests can supply a fake filesystem and so that this package does not depend on
// anyone having run the frontend build.
func New(assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.Handle("/", webApp(assets))
	return mux
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// webApp serves the built frontend, falling back to the app shell for client-side
// routes. A request for a path that looks like a file (it has an extension) but is
// not present is a genuine 404: answering it with HTML would turn a missing script
// into a baffling parse error in the browser.
func webApp(assets fs.FS) http.Handler {
	files := http.FileServerFS(assets)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}

		// Only regular files are served. A directory would otherwise be rendered as a
		// browsable listing of the build's file names.
		if info, err := fs.Stat(assets, name); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}

		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}

		appShell(w, assets)
	})
}

func appShell(w http.ResponseWriter, assets fs.FS) {
	shell, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		http.Error(w, "web app is not built", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(shell)
}
