// Package server wires up the HTTP surface: the health check, the sign-in endpoints,
// and the embedded web app. Its dependencies are interfaces so that the handlers can
// be tested without a database or a network.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strings"

	"github.com/supavitW02/tonyTheHelper/api/internal/line"
	"github.com/supavitW02/tonyTheHelper/api/internal/session"
	"github.com/supavitW02/tonyTheHelper/api/internal/store"
)

// Verifier turns a LINE ID token into the identity it asserts.
type Verifier interface {
	Verify(ctx context.Context, idToken string) (line.Identity, error)
}

// Users persists the people behind those identities.
type Users interface {
	UpsertUser(ctx context.Context, identity line.Identity) (store.User, error)
	UserByID(ctx context.Context, id string) (store.User, error)
}

// Households persists the groups those people share a stock of items with.
type Households interface {
	CreateHousehold(ctx context.Context, ownerID, name string) (store.Household, error)
	HouseholdsForUser(ctx context.Context, userID string) ([]store.Household, error)
}

// Items persists a household's stock and the log of every change to it.
type Items interface {
	CreateItem(ctx context.Context, householdID, userID, name, unit, description string) (store.Item, error)
	ItemsForHousehold(ctx context.Context, householdID, userID string) ([]store.Item, error)
	UnitsForHousehold(ctx context.Context, householdID, userID string) ([]string, error)
	AdjustQuantity(ctx context.Context, itemID, userID string, change store.QuantityChange) (store.Item, error)
}

type Deps struct {
	Assets     fs.FS // the built web app
	LiffID     string
	Verifier   Verifier
	Users      Users
	Households Households
	Items      Items
	Sessions   *session.Manager
}

func New(deps Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.HandleFunc("GET /api/config", deps.config)
	mux.HandleFunc("POST /api/session", deps.signIn)
	mux.HandleFunc("GET /api/me", deps.requireUser(deps.me))
	mux.HandleFunc("GET /api/households", deps.requireUser(deps.listHouseholds))
	mux.HandleFunc("POST /api/households", deps.requireUser(deps.createHousehold))
	mux.HandleFunc("GET /api/households/{householdId}/items", deps.requireUser(deps.listItems))
	mux.HandleFunc("POST /api/households/{householdId}/items", deps.requireUser(deps.createItem))
	mux.HandleFunc("POST /api/items/{itemId}/quantity", deps.requireUser(deps.adjustQuantity))
	mux.Handle("/", webApp(deps.Assets))
	return mux
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// config hands the frontend the values it needs to start LIFF. The LIFF id is public
// — it appears in the URL people open — so serving it at runtime is safe, and it means
// changing it does not require rebuilding the web app into a new image.
func (d Deps) config(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"liffId": d.LiffID})
}

type signInRequest struct {
	IDToken string `json:"idToken"`
}

// signIn exchanges a LINE ID token for a session. This is the only place an ID token
// is accepted; every later request carries the session cookie instead.
func (d Deps) signIn(w http.ResponseWriter, r *http.Request) {
	var body signInRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	identity, err := d.Verifier.Verify(r.Context(), body.IDToken)
	switch {
	case errors.Is(err, line.ErrInvalidToken):
		writeError(w, http.StatusUnauthorized, "the LINE token was not accepted")
		return
	case err != nil:
		// LINE is unreachable or erroring. This is not the user's fault, and telling
		// them to sign in again would not help, so it is reported as an upstream fault.
		log.Printf("sign-in: verify: %v", err)
		writeError(w, http.StatusBadGateway, "could not reach LINE")
		return
	}

	user, err := d.Users.UpsertUser(r.Context(), identity)
	if err != nil {
		log.Printf("sign-in: upsert user: %v", err)
		writeError(w, http.StatusInternalServerError, "could not record the user")
		return
	}

	token, err := d.Sessions.Issue(user.ID)
	if err != nil {
		log.Printf("sign-in: issue session: %v", err)
		writeError(w, http.StatusInternalServerError, "could not start a session")
		return
	}

	d.Sessions.SetCookie(w, token)
	writeJSON(w, http.StatusOK, user)
}

// requireUser resolves the session into a user before the handler runs, so no handler
// has to remember to check. A missing, forged or expired session and a session for a user
// who no longer exists are all "not signed in" — the answer is the same either way.
func (d Deps) requireUser(next func(http.ResponseWriter, *http.Request, store.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := d.Sessions.FromRequest(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}

		user, err := d.Users.UserByID(r.Context(), userID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		case err != nil:
			log.Printf("auth: load user: %v", err)
			writeError(w, http.StatusInternalServerError, "could not load the user")
			return
		}

		next(w, r, user)
	}
}

// me returns the signed-in user, and is how the frontend discovers on load that it
// already has a session and can skip the LINE round trip entirely.
func (d Deps) me(w http.ResponseWriter, _ *http.Request, user store.User) {
	writeJSON(w, http.StatusOK, user)
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
