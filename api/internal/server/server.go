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
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

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
	RenameHousehold(ctx context.Context, householdID, ownerID, name string) (store.Household, error)
	DeleteHousehold(ctx context.Context, householdID, ownerID string) error
	HouseholdsForUser(ctx context.Context, userID string) ([]store.Household, error)
	CreateInvite(ctx context.Context, householdID, ownerID string, now time.Time) (store.Invite, error)
	AcceptInvite(ctx context.Context, token, userID string, now time.Time) (store.Household, error)
	MembersOf(ctx context.Context, householdID, userID string) ([]store.Member, error)
	RemoveMember(ctx context.Context, householdID, ownerID, targetUserID string) error
	SetLastHousehold(ctx context.Context, userID, householdID string) error
}

// Items persists a household's stock and the log of every change to it.
type Items interface {
	CreateItem(ctx context.Context, householdID, userID string, in store.ItemInput) (store.Item, error)
	ItemsForHousehold(ctx context.Context, householdID, userID string) ([]store.Item, error)
	UnitsForHousehold(ctx context.Context, householdID, userID string) ([]string, error)
	AdjustQuantity(ctx context.Context, itemID, userID string, change store.QuantityChange) (store.Item, error)
	UpdateItem(ctx context.Context, itemID, userID string, in store.ItemInput) (store.Item, error)
	DeleteItem(ctx context.Context, itemID, userID string) error
	MovementsForItem(ctx context.Context, itemID, userID string) ([]store.Movement, error)
	MovementsForHousehold(ctx context.Context, householdID, userID string, filter store.MovementFilter) ([]store.HouseholdMovement, error)
	ItemsWithHistory(ctx context.Context, householdID, userID string) ([]store.ItemRef, error)
	ItemAccessible(ctx context.Context, itemID, userID string) error
	SetItemImage(ctx context.Context, itemID, userID, path string) (store.Item, error)
	ItemImagePath(ctx context.Context, itemID, userID string) (string, error)
}

// Storage mints short-lived URLs so photo bytes go straight between the phone and object
// storage, never through this API.
type Storage interface {
	SignUpload(ctx context.Context, path string) (string, error)
	SignDownload(ctx context.Context, path string) (string, error)
}

type Deps struct {
	Assets     fs.FS // the built web app
	LiffID     string
	Verifier   Verifier
	Users      Users
	Households Households
	Items      Items
	Storage    Storage
	Sessions   *session.Manager
}

// Go's built-in MIME table has no entry for .webmanifest, so the file would be
// content-sniffed and served as text/plain — which some browsers refuse to parse as a
// manifest, quietly making the app non-installable. Registering the type keeps the
// conventional filename instead of renaming the file to .json to dodge the problem.
func init() {
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

func New(deps Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.HandleFunc("GET /api/config", deps.config)
	mux.HandleFunc("POST /api/session", deps.signIn)
	mux.HandleFunc("GET /api/me", deps.requireUser(deps.me))
	mux.HandleFunc("GET /api/households", deps.requireUser(deps.listHouseholds))
	mux.HandleFunc("POST /api/households", deps.requireUser(deps.createHousehold))
	mux.HandleFunc("PATCH /api/households/{householdId}", deps.requireUser(deps.renameHousehold))
	mux.HandleFunc("DELETE /api/households/{householdId}", deps.requireUser(deps.deleteHousehold))
	mux.HandleFunc("GET /api/households/{householdId}/items", deps.requireUser(deps.listItems))
	mux.HandleFunc("POST /api/households/{householdId}/items", deps.requireUser(deps.createItem))
	mux.HandleFunc("POST /api/items/{itemId}/quantity", deps.requireUser(deps.adjustQuantity))
	mux.HandleFunc("PUT /api/items/{itemId}", deps.requireUser(deps.updateItem))
	mux.HandleFunc("DELETE /api/items/{itemId}", deps.requireUser(deps.deleteItem))
	mux.HandleFunc("GET /api/items/{itemId}/movements", deps.requireUser(deps.listMovements))
	mux.HandleFunc("GET /api/households/{householdId}/movements", deps.requireUser(deps.listHouseholdMovements))
	mux.HandleFunc("POST /api/items/{itemId}/image", deps.requireUser(deps.requestImageUpload))
	mux.HandleFunc("PUT /api/items/{itemId}/image", deps.requireUser(deps.confirmImageUpload))
	mux.HandleFunc("GET /api/items/{itemId}/image", deps.requireUser(deps.serveImage))
	mux.HandleFunc("POST /api/households/{householdId}/select", deps.requireUser(deps.selectHousehold))
	mux.HandleFunc("POST /api/households/{householdId}/invites", deps.requireUser(deps.createInvite))
	mux.HandleFunc("GET /api/households/{householdId}/members", deps.requireUser(deps.listMembers))
	mux.HandleFunc("DELETE /api/households/{householdId}/members/{userId}", deps.requireUser(deps.removeMember))
	mux.HandleFunc("POST /api/invites/{token}/accept", deps.requireUser(deps.acceptInvite))
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
			w.Header().Set("Cache-Control", cacheControl(name))
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

// cacheControl decides how long a browser may keep each kind of file.
//
// The split matters more than it looks. Nothing here carried a cache directive before,
// which cost a full bundle download on every open — and left the shell subject to the
// browser's own heuristic caching, with no way to revalidate it.
func cacheControl(name string) string {
	switch {
	// Vite gives build output a content-hashed filename, so a URL under /assets/ can
	// never change meaning: if the bytes change, so does the name. Safe to keep forever,
	// and this is what lets a repeat open skip re-downloading the whole app.
	case strings.HasPrefix(name, "assets/"):
		return "public, max-age=31536000, immutable"

	// These two keep the same name across every deploy, so a stale copy is not
	// self-correcting. It has to always be revalidated: a deploy replaces the hashed
	// assets and the old ones stop existing, so a browser holding an old shell asks for
	// a bundle that is gone and renders nothing — and the person using this app cannot
	// be talked through a hard refresh. Same for the worker, which decides what else
	// gets cached.
	// version.json joins them: it is how a running app finds out a newer build exists,
	// so a cached copy would answer with the build the user is already on.
	case name == "index.html", name == "sw.js", name == "version.json":
		return "no-cache"

	// Icons and the manifest also keep stable names, but a stale one is cosmetic rather
	// than fatal, so they get a short life instead of no caching at all.
	default:
		return "public, max-age=3600"
	}
}

func appShell(w http.ResponseWriter, assets fs.FS) {
	w.Header().Set("Cache-Control", cacheControl("index.html"))

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
