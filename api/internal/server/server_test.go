package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/supavitW02/tonyTheHelper/api/internal/server"
)

// assets stands in for the embedded web build, so these tests do not depend on
// anyone having run `vite build` first.
func assets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":         {Data: []byte("<!doctype html><title>ของใช้ในบ้าน</title>")},
		"assets/app-abc123.js": {Data: []byte("console.log('app')")},
	}
}

func get(t *testing.T, path string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	server.New(assets()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Result()
}

func body(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestHealthzReportsOK(t *testing.T) {
	res := get(t, "/healthz")

	if res.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
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
	res := get(t, "/")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if b := body(t, res); b != string(assets()["index.html"].Data) {
		t.Errorf("body = %q, want the contents of index.html", b)
	}
}

func TestBuiltAssetIsServed(t *testing.T) {
	res := get(t, "/assets/app-abc123.js")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if b := body(t, res); b != "console.log('app')" {
		t.Errorf("body = %q, want the asset contents", b)
	}
}

// A client-side route has no file behind it, but the browser must still receive the
// app shell so the router can take over. Later tickets add real routes like /household.
func TestClientSideRouteFallsBackToTheAppShell(t *testing.T) {
	res := get(t, "/household/some-id")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if b := body(t, res); b != string(assets()["index.html"].Data) {
		t.Errorf("body = %q, want the contents of index.html", b)
	}
}

// A missing asset must 404 rather than silently returning HTML, otherwise a broken
// script tag shows up as a confusing parse error instead of a missing file.
func TestMissingAssetIsNotFound(t *testing.T) {
	res := get(t, "/assets/does-not-exist.js")

	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
}

// /healthz must not be swallowed by the app-shell fallback.
func TestHealthzIsNotShadowedByTheFallback(t *testing.T) {
	if b := body(t, get(t, "/healthz")); b == string(assets()["index.html"].Data) {
		t.Error("/healthz returned the app shell; the route is being shadowed")
	}
}

// A directory must never be rendered as a browsable listing: it exposes the build's
// file names for no benefit. Directories are not files, so they follow the same rule
// as any other unknown path.
func TestDirectoryListingIsNotExposed(t *testing.T) {
	res := get(t, "/assets/")

	b := body(t, res)
	if strings.Contains(b, "app-abc123.js") {
		t.Errorf("directory listing exposed: body = %q", b)
	}
	if b != string(assets()["index.html"].Data) {
		t.Errorf("body = %q, want the contents of index.html", b)
	}
}
