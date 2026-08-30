package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/supavitW02/tonyTheHelper/api/internal/line"
	"github.com/supavitW02/tonyTheHelper/api/internal/server"
	"github.com/supavitW02/tonyTheHelper/api/internal/session"
	"github.com/supavitW02/tonyTheHelper/api/internal/store"
)

var testSecret = []byte("a-test-secret-that-is-long-enough-32")

// assets stands in for the embedded web build, so these tests do not depend on
// anyone having run `vite build` first.
func assets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>ของใช้ในบ้าน</title>")},
		"assets/app-abc123.js": {Data: []byte("console.log('app')")},
	}
}

// fakeVerifier answers with a fixed identity or a fixed error, never the network.
type fakeVerifier struct {
	identity line.Identity
	err      error
	seen     string
}

func (f *fakeVerifier) Verify(_ context.Context, idToken string) (line.Identity, error) {
	f.seen = idToken
	return f.identity, f.err
}

// fakeUsers is an in-memory stand-in for the database.
type fakeUsers struct {
	byID     map[string]store.User
	upserted []line.Identity
	err      error
}

func newFakeUsers() *fakeUsers { return &fakeUsers{byID: map[string]store.User{}} }

func (f *fakeUsers) UpsertUser(_ context.Context, id line.Identity) (store.User, error) {
	if f.err != nil {
		return store.User{}, f.err
	}
	f.upserted = append(f.upserted, id)
	u := store.User{ID: "user-uuid-1", LineUserID: id.UserID, DisplayName: id.DisplayName, PictureURL: id.PictureURL}
	f.byID[u.ID] = u
	return u, nil
}

func (f *fakeUsers) UserByID(_ context.Context, id string) (store.User, error) {
	if f.err != nil {
		return store.User{}, f.err
	}
	u, ok := f.byID[id]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return u, nil
}

type harness struct {
	handler  http.Handler
	verifier *fakeVerifier
	users    *fakeUsers
	sessions *session.Manager
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	v := &fakeVerifier{identity: line.Identity{
		UserID:      "U1111111111",
		DisplayName: "สุภวิทย์",
		PictureURL:  "https://profile.line-scdn.net/abc",
	}}
	u := newFakeUsers()
	s := session.NewManager(testSecret)

	return &harness{
		handler:  server.New(server.Deps{Assets: assets(), LiffID: "1234-abcd", Verifier: v, Users: u, Sessions: s}),
		verifier: v,
		users:    u,
		sessions: s,
	}
}

func (h *harness) do(req *http.Request) *http.Response {
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec.Result()
}

func (h *harness) get(path string) *http.Response {
	return h.do(httptest.NewRequest(http.MethodGet, path, nil))
}

func (h *harness) signIn(token string) *http.Response {
	req := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(`{"idToken":"`+token+`"}`))
	req.Header.Set("Content-Type", "application/json")
	return h.do(req)
}

func body(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func decodeUser(t *testing.T, res *http.Response) store.User {
	t.Helper()
	var u store.User
	if err := json.Unmarshal([]byte(body(t, res)), &u); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	return u
}

// --- health and static serving -------------------------------------------------

func TestHealthzReportsOK(t *testing.T) {
	res := newHarness(t).get("/healthz")

	if res.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(body(t, res)), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["status"] != "ok" {
		t.Errorf(`status field = %q, want "ok"`, got["status"])
	}
}

func TestRootServesTheWebApp(t *testing.T) {
	res := newHarness(t).get("/")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if b := body(t, res); b != string(assets()["index.html"].Data) {
		t.Errorf("body = %q, want the contents of index.html", b)
	}
}

func TestBuiltAssetIsServed(t *testing.T) {
	res := newHarness(t).get("/assets/app-abc123.js")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if b := body(t, res); b != "console.log('app')" {
		t.Errorf("body = %q, want the asset contents", b)
	}
}

func TestClientSideRouteFallsBackToTheAppShell(t *testing.T) {
	res := newHarness(t).get("/household/some-id")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if b := body(t, res); b != string(assets()["index.html"].Data) {
		t.Errorf("body = %q, want the contents of index.html", b)
	}
}

func TestMissingAssetIsNotFound(t *testing.T) {
	if res := newHarness(t).get("/assets/does-not-exist.js"); res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
}

func TestDirectoryListingIsNotExposed(t *testing.T) {
	res := newHarness(t).get("/assets/")

	b := body(t, res)
	if strings.Contains(b, "app-abc123.js") {
		t.Errorf("directory listing exposed: body = %q", b)
	}
	if b != string(assets()["index.html"].Data) {
		t.Errorf("body = %q, want the contents of index.html", b)
	}
}

// An API route must never be swallowed by the app-shell fallback.
func TestAPIRoutesAreNotShadowedByTheFallback(t *testing.T) {
	for _, path := range []string{"/healthz", "/api/config"} {
		if b := body(t, newHarness(t).get(path)); b == string(assets()["index.html"].Data) {
			t.Errorf("%s returned the app shell; the route is being shadowed", path)
		}
	}
}

// --- configuration -------------------------------------------------------------

func TestConfigExposesTheLiffID(t *testing.T) {
	res := newHarness(t).get("/api/config")

	var got map[string]string
	if err := json.Unmarshal([]byte(body(t, res)), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["liffId"] != "1234-abcd" {
		t.Errorf("liffId = %q, want 1234-abcd", got["liffId"])
	}
}

// --- signing in ----------------------------------------------------------------

func TestSignInCreatesTheUserAndStartsASession(t *testing.T) {
	h := newHarness(t)

	res := h.signIn("a.valid.token")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", res.StatusCode, http.StatusOK, body(t, res))
	}
	if h.verifier.seen != "a.valid.token" {
		t.Errorf("verifier saw %q, want the token from the request body", h.verifier.seen)
	}
	if len(h.users.upserted) != 1 {
		t.Fatalf("upserted %d users, want 1", len(h.users.upserted))
	}

	user := decodeUser(t, res)
	if user.DisplayName != "สุภวิทย์" {
		t.Errorf("displayName = %q, want the LINE display name", user.DisplayName)
	}

	cookies := res.Cookies()
	if len(cookies) != 1 || cookies[0].Name != session.CookieName {
		t.Fatalf("got cookies %v, want one %s cookie", cookies, session.CookieName)
	}
	if got, err := h.sessions.UserID(cookies[0].Value); err != nil || got != "user-uuid-1" {
		t.Errorf("session subject = %q (err %v), want user-uuid-1", got, err)
	}
}

// The LINE user id identifies the person but is not the browser's business.
func TestSignInDoesNotLeakTheLineUserID(t *testing.T) {
	if b := body(t, newHarness(t).signIn("a.valid.token")); strings.Contains(b, "U1111111111") {
		t.Errorf("response leaked the LINE user id: %s", b)
	}
}

func TestSignInRejectsATokenLINEDoesNotAccept(t *testing.T) {
	h := newHarness(t)
	h.verifier.err = line.ErrInvalidToken

	res := h.signIn("bad.token")

	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusUnauthorized)
	}
	if len(res.Cookies()) != 0 {
		t.Error("a session cookie was set for a rejected token")
	}
	if len(h.users.upserted) != 0 {
		t.Error("a user was created for a rejected token")
	}
}

// LINE being unreachable is an upstream fault. Reporting it as 401 would tell the
// user to sign in again, which cannot possibly fix it.
func TestSignInReportsLINEBeingUnreachableAsAnUpstreamFault(t *testing.T) {
	h := newHarness(t)
	h.verifier.err = errors.New("dial tcp: connection refused")

	if res := h.signIn("a.valid.token"); res.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusBadGateway)
	}
}

func TestSignInRejectsAMalformedBody(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader("not json"))

	if res := h.do(req); res.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusBadRequest)
	}
}

// --- reading the signed-in user -------------------------------------------------

func TestMeReturnsTheSignedInUser(t *testing.T) {
	h := newHarness(t)
	signIn := h.signIn("a.valid.token")

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(signIn.Cookies()[0])
	res := h.do(req)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if got := decodeUser(t, res).DisplayName; got != "สุภวิทย์" {
		t.Errorf("displayName = %q, want the LINE display name", got)
	}
}

func TestMeWithoutASessionIsUnauthorised(t *testing.T) {
	if res := newHarness(t).get("/api/me"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusUnauthorized)
	}
}

func TestMeWithAForgedSessionIsUnauthorised(t *testing.T) {
	h := newHarness(t)
	forged, err := session.NewManager([]byte("a-different-secret-also-long-enough!")).Issue("user-uuid-1")
	if err != nil {
		t.Fatalf("issue forged token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: forged})

	if res := h.do(req); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusUnauthorized)
	}
}

// A validly signed session for a user who has since been removed reads as signed
// out, not as a server error.
func TestMeForAUserThatNoLongerExistsIsUnauthorised(t *testing.T) {
	h := newHarness(t)
	token, err := h.sessions.Issue("user-that-was-deleted")
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: token})

	if res := h.do(req); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusUnauthorized)
	}
}
