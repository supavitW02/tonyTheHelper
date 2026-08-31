package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
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

// fakeHouseholds is an in-memory stand-in for the household tables.
type fakeHouseholds struct {
	byUser map[string][]store.Household
	err    error
	nextID int
}

func newFakeHouseholds() *fakeHouseholds {
	return &fakeHouseholds{byUser: map[string][]store.Household{}}
}

func (f *fakeHouseholds) CreateHousehold(_ context.Context, ownerID, name string) (store.Household, error) {
	if f.err != nil {
		return store.Household{}, f.err
	}
	f.nextID++
	h := store.Household{ID: fmt.Sprintf("household-%d", f.nextID), Name: name, OwnerID: ownerID, IsOwner: true}
	f.byUser[ownerID] = append(f.byUser[ownerID], h)
	return h, nil
}

func (f *fakeHouseholds) HouseholdsForUser(_ context.Context, userID string) ([]store.Household, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byUser[userID], nil
}

// fakeItems is an in-memory stand-in for the items and movement tables. It enforces
// household membership, tracks soft deletion and records movements, because "an outsider
// cannot touch our stock" and "history survives deletion" are the behaviours most worth
// testing at this layer.
type fakeItems struct {
	households    *fakeHouseholds
	byHousehold   map[string][]store.Item
	itemHousehold map[string]string
	deleted       map[string]bool
	movements     map[string][]store.Movement
	imagePaths    map[string]string
	err           error
	nextID        int
}

func newFakeItems(h *fakeHouseholds) *fakeItems {
	return &fakeItems{
		households:    h,
		byHousehold:   map[string][]store.Item{},
		itemHousehold: map[string]string{},
		deleted:       map[string]bool{},
		movements:     map[string][]store.Movement{},
		imagePaths:    map[string]string{},
	}
}

func (f *fakeItems) isMember(householdID, userID string) bool {
	for _, h := range f.households.byUser[userID] {
		if h.ID == householdID {
			return true
		}
	}
	return false
}

// find locates a live item the user may touch.
func (f *fakeItems) find(itemID, userID string) (*store.Item, bool) {
	householdID, ok := f.itemHousehold[itemID]
	if !ok || f.deleted[itemID] || !f.isMember(householdID, userID) {
		return nil, false
	}
	items := f.byHousehold[householdID]
	for idx := range items {
		if items[idx].ID == itemID {
			return &items[idx], true
		}
	}
	return nil, false
}

func lowFor(threshold *int, quantity int) bool {
	return threshold != nil && quantity <= *threshold
}

func (f *fakeItems) CreateItem(_ context.Context, householdID, userID string, in store.ItemInput) (store.Item, error) {
	if f.err != nil {
		return store.Item{}, f.err
	}
	if !f.isMember(householdID, userID) {
		return store.Item{}, store.ErrNotFound
	}
	f.nextID++
	item := store.Item{
		ID: fmt.Sprintf("item-%d", f.nextID), Name: in.Name, Unit: in.Unit,
		Description: in.Description, LowStockThreshold: in.LowStockThreshold,
		IsLow: lowFor(in.LowStockThreshold, 0),
	}
	f.byHousehold[householdID] = append(f.byHousehold[householdID], item)
	f.itemHousehold[item.ID] = householdID
	return item, nil
}

func (f *fakeItems) ItemsForHousehold(_ context.Context, householdID, userID string) ([]store.Item, error) {
	if f.err != nil {
		return nil, f.err
	}
	if !f.isMember(householdID, userID) {
		return []store.Item{}, nil
	}
	live := []store.Item{}
	for _, i := range f.byHousehold[householdID] {
		if !f.deleted[i.ID] {
			live = append(live, i)
		}
	}
	// Low first, mirroring the SQL ordering.
	sort.SliceStable(live, func(a, b int) bool { return live[a].IsLow && !live[b].IsLow })
	return live, nil
}

func (f *fakeItems) UnitsForHousehold(_ context.Context, householdID, userID string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	if !f.isMember(householdID, userID) {
		return []string{}, nil
	}
	seen := map[string]bool{}
	units := []string{}
	for _, i := range f.byHousehold[householdID] {
		if !f.deleted[i.ID] && !seen[i.Unit] {
			seen[i.Unit] = true
			units = append(units, i.Unit)
		}
	}
	return units, nil
}

func (f *fakeItems) AdjustQuantity(_ context.Context, itemID, userID string, change store.QuantityChange) (store.Item, error) {
	if f.err != nil {
		return store.Item{}, f.err
	}
	item, ok := f.find(itemID, userID)
	if !ok {
		return store.Item{}, store.ErrNotFound
	}

	before := item.Quantity
	after := before
	switch {
	case change.Absolute != nil:
		after = *change.Absolute
	case change.Delta != nil:
		after = before + *change.Delta
	}
	after = max(after, 0)

	if after != before {
		f.movements[itemID] = append(f.movements[itemID], store.Movement{
			ID: fmt.Sprintf("mv-%d", len(f.movements[itemID])+1), ActorName: "สุภวิทย์",
			QuantityBefore: before, QuantityAfter: after, Note: change.Note,
		})
	}
	item.Quantity = after
	item.IsLow = lowFor(item.LowStockThreshold, after)
	return *item, nil
}

func (f *fakeItems) UpdateItem(_ context.Context, itemID, userID string, in store.ItemInput) (store.Item, error) {
	if f.err != nil {
		return store.Item{}, f.err
	}
	item, ok := f.find(itemID, userID)
	if !ok {
		return store.Item{}, store.ErrNotFound
	}
	item.Name = in.Name
	item.Unit = in.Unit
	item.Description = in.Description
	item.LowStockThreshold = in.LowStockThreshold
	item.IsLow = lowFor(in.LowStockThreshold, item.Quantity)
	return *item, nil
}

func (f *fakeItems) DeleteItem(_ context.Context, itemID, userID string) error {
	if f.err != nil {
		return f.err
	}
	if _, ok := f.find(itemID, userID); !ok {
		return store.ErrNotFound
	}
	f.deleted[itemID] = true
	return nil
}

// Movements outlive their item, which is the whole point of soft deletion.
func (f *fakeItems) MovementsForItem(_ context.Context, itemID, userID string) ([]store.Movement, error) {
	if f.err != nil {
		return nil, f.err
	}
	householdID, ok := f.itemHousehold[itemID]
	if !ok || !f.isMember(householdID, userID) {
		return nil, store.ErrNotFound
	}
	out := append([]store.Movement{}, f.movements[itemID]...)
	// Newest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (f *fakeItems) ItemAccessible(_ context.Context, itemID, userID string) error {
	if f.err != nil {
		return f.err
	}
	if _, ok := f.find(itemID, userID); !ok {
		return store.ErrNotFound
	}
	return nil
}

func (f *fakeItems) SetItemImage(_ context.Context, itemID, userID, path string) (store.Item, error) {
	if f.err != nil {
		return store.Item{}, f.err
	}
	item, ok := f.find(itemID, userID)
	if !ok {
		return store.Item{}, store.ErrNotFound
	}
	f.imagePaths[itemID] = path
	item.HasImage = true
	return *item, nil
}

func (f *fakeItems) ItemImagePath(_ context.Context, itemID, userID string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if _, ok := f.find(itemID, userID); !ok {
		return "", store.ErrNotFound
	}
	path, ok := f.imagePaths[itemID]
	if !ok {
		return "", store.ErrNotFound
	}
	return path, nil
}

// fakeStorage records what was signed instead of talking to Supabase.
type fakeStorage struct {
	signedUploads   []string
	signedDownloads []string
	err             error
}

func (f *fakeStorage) SignUpload(_ context.Context, path string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.signedUploads = append(f.signedUploads, path)
	return "https://storage.test/upload/" + path + "?token=fake", nil
}

func (f *fakeStorage) SignDownload(_ context.Context, path string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.signedDownloads = append(f.signedDownloads, path)
	return "https://storage.test/read/" + path + "?token=fake", nil
}

type harness struct {
	handler    http.Handler
	verifier   *fakeVerifier
	users      *fakeUsers
	households *fakeHouseholds
	items      *fakeItems
	storage    *fakeStorage
	sessions   *session.Manager
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	v := &fakeVerifier{identity: line.Identity{
		UserID:      "U1111111111",
		DisplayName: "สุภวิทย์",
		PictureURL:  "https://profile.line-scdn.net/abc",
	}}
	u := newFakeUsers()
	hh := newFakeHouseholds()
	it := newFakeItems(hh)
	st := &fakeStorage{}
	s := session.NewManager(testSecret)

	return &harness{
		handler: server.New(server.Deps{
			Assets: assets(), LiffID: "1234-abcd",
			Verifier: v, Users: u, Households: hh, Items: it, Storage: st, Sessions: s,
		}),
		verifier:   v,
		users:      u,
		households: hh,
		items:      it,
		storage:    st,
		sessions:   s,
	}
}

// signedIn returns a request carrying a valid session for the signed-in user.
func (h *harness) signedIn(t *testing.T, method, path, body string) *http.Response {
	t.Helper()
	cookies := h.signIn("a.valid.token").Cookies()
	if len(cookies) == 0 {
		t.Fatal("sign-in did not set a session cookie")
	}
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(cookies[0])
	return h.do(req)
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

// --- households -----------------------------------------------------------------

func decodeHouseholds(t *testing.T, res *http.Response) struct {
	Households []store.Household `json:"households"`
	CurrentID  string            `json:"currentId"`
} {
	t.Helper()
	var out struct {
		Households []store.Household `json:"households"`
		CurrentID  string            `json:"currentId"`
	}
	if err := json.Unmarshal([]byte(body(t, res)), &out); err != nil {
		t.Fatalf("decode households: %v", err)
	}
	return out
}

// A user who belongs to nothing gets an empty list and no current household — that is
// what puts them on the create-household screen rather than an error page.
func TestListHouseholdsIsEmptyForANewUser(t *testing.T) {
	h := newHarness(t)

	res := h.signedIn(t, http.MethodGet, "/api/households", "")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	got := decodeHouseholds(t, res)
	if len(got.Households) != 0 {
		t.Errorf("got %d households, want 0", len(got.Households))
	}
	if got.CurrentID != "" {
		t.Errorf("currentId = %q, want empty", got.CurrentID)
	}
}

func TestCreateHouseholdMakesTheCallerOwner(t *testing.T) {
	h := newHarness(t)

	res := h.signedIn(t, http.MethodPost, "/api/households", `{"name":"บ้านเรา"}`)

	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", res.StatusCode, http.StatusCreated, body(t, res))
	}
	var got store.Household
	if err := json.Unmarshal([]byte(body(t, res)), &got); err != nil {
		t.Fatalf("decode household: %v", err)
	}
	if got.Name != "บ้านเรา" {
		t.Errorf("name = %q, want บ้านเรา", got.Name)
	}
	if !got.IsOwner {
		t.Error("creator is not marked as owner")
	}
}

func TestCreatedHouseholdBecomesTheCurrentOne(t *testing.T) {
	h := newHarness(t)
	h.signedIn(t, http.MethodPost, "/api/households", `{"name":"บ้านเรา"}`)

	got := decodeHouseholds(t, h.signedIn(t, http.MethodGet, "/api/households", ""))

	if len(got.Households) != 1 {
		t.Fatalf("got %d households, want 1", len(got.Households))
	}
	if got.CurrentID != got.Households[0].ID {
		t.Errorf("currentId = %q, want %q", got.CurrentID, got.Households[0].ID)
	}
}

func TestCreateHouseholdRejectsAnEmptyName(t *testing.T) {
	for _, name := range []string{"", "   "} {
		res := newHarness(t).signedIn(t, http.MethodPost, "/api/households", `{"name":"`+name+`"}`)
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("name %q: status = %d, want %d", name, res.StatusCode, http.StatusBadRequest)
		}
	}
}

// The limit is counted in runes. Thai is three bytes per character, so a byte-based
// limit would reject a name well under 60 characters.
func TestCreateHouseholdCountsNameLengthInCharactersNotBytes(t *testing.T) {
	h := newHarness(t)
	sixtyThaiChars := strings.Repeat("ก", 60) // 180 bytes

	res := h.signedIn(t, http.MethodPost, "/api/households", `{"name":"`+sixtyThaiChars+`"}`)

	if res.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want %d — a 60-character Thai name was rejected", res.StatusCode, http.StatusCreated)
	}
}

func TestCreateHouseholdRejectsAnOverlongName(t *testing.T) {
	h := newHarness(t)

	res := h.signedIn(t, http.MethodPost, "/api/households", `{"name":"`+strings.Repeat("ก", 61)+`"}`)

	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusBadRequest)
	}
}

func TestCreateHouseholdTrimsSurroundingWhitespace(t *testing.T) {
	h := newHarness(t)

	res := h.signedIn(t, http.MethodPost, "/api/households", `{"name":"  บ้านเรา  "}`)

	var got store.Household
	if err := json.Unmarshal([]byte(body(t, res)), &got); err != nil {
		t.Fatalf("decode household: %v", err)
	}
	if got.Name != "บ้านเรา" {
		t.Errorf("name = %q, want it trimmed", got.Name)
	}
}

// Household endpoints must be unreachable without a session.
func TestHouseholdEndpointsRequireASession(t *testing.T) {
	h := newHarness(t)

	cases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/households", ""},
		{http.MethodPost, "/api/households", `{"name":"บ้านเรา"}`},
	}
	for _, c := range cases {
		var req *http.Request
		if c.body == "" {
			req = httptest.NewRequest(c.method, c.path, nil)
		} else {
			req = httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		}
		if res := h.do(req); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want %d", c.method, c.path, res.StatusCode, http.StatusUnauthorized)
		}
	}
}

// --- items and quantity ---------------------------------------------------------

// withHousehold signs in and creates a household, returning its id — the starting point
// for every item test.
func (h *harness) withHousehold(t *testing.T) string {
	t.Helper()
	var created store.Household
	res := h.signedIn(t, http.MethodPost, "/api/households", `{"name":"บ้านเรา"}`)
	if err := json.Unmarshal([]byte(body(t, res)), &created); err != nil {
		t.Fatalf("decode household: %v", err)
	}
	return created.ID
}

func decodeItems(t *testing.T, res *http.Response) struct {
	Items []store.Item `json:"items"`
	Units []string     `json:"units"`
} {
	t.Helper()
	var out struct {
		Items []store.Item `json:"items"`
		Units []string     `json:"units"`
	}
	if err := json.Unmarshal([]byte(body(t, res)), &out); err != nil {
		t.Fatalf("decode items: %v", err)
	}
	return out
}

func TestCreateItemAppearsInTheList(t *testing.T) {
	h := newHarness(t)
	hh := h.withHousehold(t)

	res := h.signedIn(t, http.MethodPost, "/api/households/"+hh+"/items",
		`{"name":"น้ำปลา","unit":"ขวด"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", res.StatusCode, http.StatusCreated, body(t, res))
	}

	got := decodeItems(t, h.signedIn(t, http.MethodGet, "/api/households/"+hh+"/items", ""))
	if len(got.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(got.Items))
	}
	if got.Items[0].Name != "น้ำปลา" || got.Items[0].Unit != "ขวด" {
		t.Errorf("item = %+v, want น้ำปลา/ขวด", got.Items[0])
	}
	if got.Items[0].Quantity != 0 {
		t.Errorf("quantity = %d, want 0", got.Items[0].Quantity)
	}
}

// The unit chips are what stop one unit acquiring three spellings.
func TestUnitsAreOfferedFromExistingItems(t *testing.T) {
	h := newHarness(t)
	hh := h.withHousehold(t)
	h.signedIn(t, http.MethodPost, "/api/households/"+hh+"/items", `{"name":"น้ำปลา","unit":"ขวด"}`)
	h.signedIn(t, http.MethodPost, "/api/households/"+hh+"/items", `{"name":"ซีอิ๊ว","unit":"ขวด"}`)
	h.signedIn(t, http.MethodPost, "/api/households/"+hh+"/items", `{"name":"ข้าวสาร","unit":"ถุง"}`)

	got := decodeItems(t, h.signedIn(t, http.MethodGet, "/api/households/"+hh+"/items", ""))

	if len(got.Units) != 2 {
		t.Errorf("units = %v, want 2 distinct", got.Units)
	}
}

func TestCreateItemRequiresNameAndUnit(t *testing.T) {
	cases := map[string]string{
		"no name":    `{"name":"","unit":"ขวด"}`,
		"blank name": `{"name":"   ","unit":"ขวด"}`,
		"no unit":    `{"name":"น้ำปลา","unit":""}`,
		"blank unit": `{"name":"น้ำปลา","unit":"  "}`,
	}
	for label, payload := range cases {
		h := newHarness(t)
		hh := h.withHousehold(t)
		if res := h.signedIn(t, http.MethodPost, "/api/households/"+hh+"/items", payload); res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want %d", label, res.StatusCode, http.StatusBadRequest)
		}
	}
}

// Item names are limited in runes. A 100-character Thai name is 300 bytes and must be
// accepted, or the limit is really a 33-character one for Thai users.
func TestItemNameLengthIsCountedInCharacters(t *testing.T) {
	h := newHarness(t)
	hh := h.withHousehold(t)

	res := h.signedIn(t, http.MethodPost, "/api/households/"+hh+"/items",
		`{"name":"`+strings.Repeat("ก", 100)+`","unit":"ชิ้น"}`)

	if res.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want %d — a 100-character Thai name was rejected", res.StatusCode, http.StatusCreated)
	}
}

// An item in someone else's household must be invisible, and indistinguishable from one
// that does not exist.
func TestItemsInAnotherHouseholdAreNotFound(t *testing.T) {
	h := newHarness(t)
	theirs := h.withHousehold(t)

	// A second person, with their own session and no membership of `theirs`.
	other := newHarness(t)
	other.households.byUser["user-uuid-1"] = nil // belongs to nothing

	res := other.signedIn(t, http.MethodPost, "/api/households/"+theirs+"/items",
		`{"name":"ของแอบใส่","unit":"ชิ้น"}`)

	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
}

func TestPlusAndMinusChangeTheQuantity(t *testing.T) {
	h := newHarness(t)
	hh := h.withHousehold(t)
	var item store.Item
	if err := json.Unmarshal([]byte(body(t, h.signedIn(t, http.MethodPost, "/api/households/"+hh+"/items",
		`{"name":"น้ำปลา","unit":"ขวด"}`))), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}

	for _, step := range []struct {
		payload string
		want    int
	}{
		{`{"delta":1}`, 1},
		{`{"delta":1}`, 2},
		{`{"delta":-1}`, 1},
		{`{"delta":-1}`, 0},
		{`{"delta":-1}`, 0}, // minus at zero does nothing
	} {
		res := h.signedIn(t, http.MethodPost, "/api/items/"+item.ID+"/quantity", step.payload)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d, want %d", step.payload, res.StatusCode, http.StatusOK)
		}
		var got store.Item
		if err := json.Unmarshal([]byte(body(t, res)), &got); err != nil {
			t.Fatalf("decode item: %v", err)
		}
		if got.Quantity != step.want {
			t.Errorf("after %s quantity = %d, want %d", step.payload, got.Quantity, step.want)
		}
	}

	// Four real changes; the fifth tap moved nothing and must not be recorded.
	if n := len(h.items.movements[item.ID]); n != 4 {
		t.Errorf("movements = %d, want 4", n)
	}
}

func TestSettingATotalWorks(t *testing.T) {
	h := newHarness(t)
	hh := h.withHousehold(t)
	var item store.Item
	json.Unmarshal([]byte(body(t, h.signedIn(t, http.MethodPost, "/api/households/"+hh+"/items",
		`{"name":"น้ำปลา","unit":"ขวด"}`))), &item)

	res := h.signedIn(t, http.MethodPost, "/api/items/"+item.ID+"/quantity",
		`{"quantity":12,"note":"ซื้อมาเพิ่ม"}`)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	var got store.Item
	json.Unmarshal([]byte(body(t, res)), &got)
	if got.Quantity != 12 {
		t.Errorf("quantity = %d, want 12", got.Quantity)
	}
}

// delta and quantity mean different things; sending both is ambiguous and sending
// neither is a no-op the caller probably did not intend.
func TestQuantityRequiresExactlyOneOfDeltaOrTotal(t *testing.T) {
	for label, payload := range map[string]string{
		"both":    `{"delta":1,"quantity":5}`,
		"neither": `{"note":"อะไรก็ไม่รู้"}`,
	} {
		h := newHarness(t)
		hh := h.withHousehold(t)
		var item store.Item
		json.Unmarshal([]byte(body(t, h.signedIn(t, http.MethodPost, "/api/households/"+hh+"/items",
			`{"name":"น้ำปลา","unit":"ขวด"}`))), &item)

		if res := h.signedIn(t, http.MethodPost, "/api/items/"+item.ID+"/quantity", payload); res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want %d", label, res.StatusCode, http.StatusBadRequest)
		}
	}
}

func TestQuantityRejectsOutOfRangeValues(t *testing.T) {
	for label, payload := range map[string]string{
		"negative total": `{"quantity":-1}`,
		"absurd total":   `{"quantity":999999999}`,
		"absurd delta":   `{"delta":100000}`,
	} {
		h := newHarness(t)
		hh := h.withHousehold(t)
		var item store.Item
		json.Unmarshal([]byte(body(t, h.signedIn(t, http.MethodPost, "/api/households/"+hh+"/items",
			`{"name":"น้ำปลา","unit":"ขวด"}`))), &item)

		if res := h.signedIn(t, http.MethodPost, "/api/items/"+item.ID+"/quantity", payload); res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want %d", label, res.StatusCode, http.StatusBadRequest)
		}
	}
}

func TestItemEndpointsRequireASession(t *testing.T) {
	h := newHarness(t)

	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/households/some-id/items", ""},
		{http.MethodPost, "/api/households/some-id/items", `{"name":"น้ำปลา","unit":"ขวด"}`},
		{http.MethodPost, "/api/items/some-id/quantity", `{"delta":1}`},
	} {
		var req *http.Request
		if c.body == "" {
			req = httptest.NewRequest(c.method, c.path, nil)
		} else {
			req = httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		}
		if res := h.do(req); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want %d", c.method, c.path, res.StatusCode, http.StatusUnauthorized)
		}
	}
}

// --- history, low stock and soft delete -----------------------------------------

// newItem creates an item and returns it, the starting point for the tests below.
func (h *harness) newItem(t *testing.T, householdID, payload string) store.Item {
	t.Helper()
	res := h.signedIn(t, http.MethodPost, "/api/households/"+householdID+"/items", payload)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create item: status = %d; body = %s", res.StatusCode, body(t, res))
	}
	var item store.Item
	if err := json.Unmarshal([]byte(body(t, res)), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	return item
}

func decodeMovements(t *testing.T, res *http.Response) []store.Movement {
	t.Helper()
	var out struct {
		Movements []store.Movement `json:"movements"`
	}
	if err := json.Unmarshal([]byte(body(t, res)), &out); err != nil {
		t.Fatalf("decode movements: %v", err)
	}
	return out.Movements
}

func TestHistoryShowsWhoChangedWhatNewestFirst(t *testing.T) {
	h := newHarness(t)
	hh := h.withHousehold(t)
	item := h.newItem(t, hh, `{"name":"น้ำปลา","unit":"ขวด"}`)

	h.signedIn(t, http.MethodPost, "/api/items/"+item.ID+"/quantity", `{"delta":1}`)
	h.signedIn(t, http.MethodPost, "/api/items/"+item.ID+"/quantity", `{"quantity":9,"note":"ซื้อมาเพิ่ม"}`)

	movements := decodeMovements(t, h.signedIn(t, http.MethodGet, "/api/items/"+item.ID+"/movements", ""))

	if len(movements) != 2 {
		t.Fatalf("got %d movements, want 2", len(movements))
	}
	if movements[0].QuantityAfter != 9 || movements[0].Note != "ซื้อมาเพิ่ม" {
		t.Errorf("newest movement = %+v, want the set-total with its note", movements[0])
	}
	if movements[0].ActorName == "" {
		t.Error("movement has no actor name; the point is knowing who did it")
	}
}

// An empty history is a normal state, not an error.
func TestHistoryIsEmptyForAnUntouchedItem(t *testing.T) {
	h := newHarness(t)
	item := h.newItem(t, h.withHousehold(t), `{"name":"น้ำปลา","unit":"ขวด"}`)

	res := h.signedIn(t, http.MethodGet, "/api/items/"+item.ID+"/movements", "")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if n := len(decodeMovements(t, res)); n != 0 {
		t.Errorf("got %d movements, want 0", n)
	}
}

func TestDeletingAnItemRemovesItFromTheListButKeepsItsHistory(t *testing.T) {
	h := newHarness(t)
	hh := h.withHousehold(t)
	item := h.newItem(t, hh, `{"name":"น้ำปลา","unit":"ขวด"}`)
	h.signedIn(t, http.MethodPost, "/api/items/"+item.ID+"/quantity", `{"delta":1}`)

	if res := h.signedIn(t, http.MethodDelete, "/api/items/"+item.ID, ""); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d", res.StatusCode, http.StatusNoContent)
	}

	if items := decodeItems(t, h.signedIn(t, http.MethodGet, "/api/households/"+hh+"/items", "")).Items; len(items) != 0 {
		t.Errorf("got %d items after deletion, want 0", len(items))
	}
	if n := len(decodeMovements(t, h.signedIn(t, http.MethodGet, "/api/items/"+item.ID+"/movements", ""))); n != 1 {
		t.Errorf("got %d movements after deletion, want 1 — history was lost", n)
	}
}

func TestDeletingTwiceIsNotFound(t *testing.T) {
	h := newHarness(t)
	item := h.newItem(t, h.withHousehold(t), `{"name":"น้ำปลา","unit":"ขวด"}`)
	h.signedIn(t, http.MethodDelete, "/api/items/"+item.ID, "")

	if res := h.signedIn(t, http.MethodDelete, "/api/items/"+item.ID, ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
}

func TestEditingAnItemChangesItsFields(t *testing.T) {
	h := newHarness(t)
	item := h.newItem(t, h.withHousehold(t), `{"name":"นำปลา","unit":"ขวด"}`)

	res := h.signedIn(t, http.MethodPut, "/api/items/"+item.ID,
		`{"name":"น้ำปลา","unit":"ขวดใหญ่","description":"ยี่ห้อทิพรส","lowStockThreshold":2}`)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", res.StatusCode, http.StatusOK, body(t, res))
	}
	var got store.Item
	if err := json.Unmarshal([]byte(body(t, res)), &got); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	if got.Name != "น้ำปลา" || got.Unit != "ขวดใหญ่" || got.Description != "ยี่ห้อทิพรส" {
		t.Errorf("item = %+v, want the corrected fields", got)
	}
	if got.LowStockThreshold == nil || *got.LowStockThreshold != 2 {
		t.Errorf("LowStockThreshold = %v, want 2", got.LowStockThreshold)
	}
}

func TestAnItemBecomesLowAtItsThreshold(t *testing.T) {
	h := newHarness(t)
	item := h.newItem(t, h.withHousehold(t), `{"name":"น้ำปลา","unit":"ขวด","lowStockThreshold":2}`)

	// Brand new item at 0 is already at or below 2.
	if !item.IsLow {
		t.Error("a new item at 0 with threshold 2 is not marked low")
	}

	res := h.signedIn(t, http.MethodPost, "/api/items/"+item.ID+"/quantity", `{"quantity":5}`)
	var raised store.Item
	json.Unmarshal([]byte(body(t, res)), &raised)
	if raised.IsLow {
		t.Error("an item at 5 with threshold 2 is still marked low")
	}
}

func TestThresholdIsRejectedWhenOutOfRange(t *testing.T) {
	for label, payload := range map[string]string{
		"negative": `{"name":"น้ำปลา","unit":"ขวด","lowStockThreshold":-1}`,
		"absurd":   `{"name":"น้ำปลา","unit":"ขวด","lowStockThreshold":999999999}`,
	} {
		h := newHarness(t)
		hh := h.withHousehold(t)
		if res := h.signedIn(t, http.MethodPost, "/api/households/"+hh+"/items", payload); res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want %d", label, res.StatusCode, http.StatusBadRequest)
		}
	}
}

// A threshold of zero is meaningful — "low once none is left" — and must survive the
// round trip rather than being treated as absent.
func TestAThresholdOfZeroIsKept(t *testing.T) {
	h := newHarness(t)
	item := h.newItem(t, h.withHousehold(t), `{"name":"น้ำปลา","unit":"ขวด","lowStockThreshold":0}`)

	if item.LowStockThreshold == nil {
		t.Fatal("a threshold of 0 was dropped")
	}
	if *item.LowStockThreshold != 0 {
		t.Errorf("LowStockThreshold = %d, want 0", *item.LowStockThreshold)
	}
}

func TestNewItemEndpointsRequireASession(t *testing.T) {
	h := newHarness(t)

	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/items/some-id/movements", ""},
		{http.MethodPut, "/api/items/some-id", `{"name":"น้ำปลา","unit":"ขวด"}`},
		{http.MethodDelete, "/api/items/some-id", ""},
	} {
		var req *http.Request
		if c.body == "" {
			req = httptest.NewRequest(c.method, c.path, nil)
		} else {
			req = httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		}
		if res := h.do(req); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want %d", c.method, c.path, res.StatusCode, http.StatusUnauthorized)
		}
	}
}

// --- item photos ----------------------------------------------------------------

func TestRequestingAnUploadUrlSignsTheItemsPath(t *testing.T) {
	h := newHarness(t)
	item := h.newItem(t, h.withHousehold(t), `{"name":"น้ำปลา","unit":"ขวด"}`)

	res := h.signedIn(t, http.MethodPost, "/api/items/"+item.ID+"/image", "")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", res.StatusCode, http.StatusOK, body(t, res))
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(body(t, res)), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["uploadUrl"] == "" {
		t.Error("no uploadUrl returned")
	}
	if len(h.storage.signedUploads) != 1 || !strings.HasPrefix(h.storage.signedUploads[0], "items/"+item.ID+"/") {
		t.Errorf("signed uploads = %v, want a path under the item's own prefix", h.storage.signedUploads)
	}
	if got["path"] == "" {
		t.Error("no path returned; the client cannot confirm the upload without it")
	}
}

// A signed URL is a capability. Handing one out for an item the caller cannot touch
// would let them overwrite another household's photo.
func TestNoUploadUrlForAnItemTheCallerCannotTouch(t *testing.T) {
	h := newHarness(t)
	item := h.newItem(t, h.withHousehold(t), `{"name":"น้ำปลา","unit":"ขวด"}`)

	outsider := newHarness(t)
	outsider.households.byUser["user-uuid-1"] = nil
	outsider.items.itemHousehold[item.ID] = "someone-elses-household"

	res := outsider.signedIn(t, http.MethodPost, "/api/items/"+item.ID+"/image", "")

	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
	if len(outsider.storage.signedUploads) != 0 {
		t.Errorf("signed %d uploads for an inaccessible item, want 0", len(outsider.storage.signedUploads))
	}
}

// The item must not claim to have a photo until the upload is confirmed, or a failed
// transfer leaves a permanently broken thumbnail.
func TestAnItemHasNoImageUntilTheUploadIsConfirmed(t *testing.T) {
	h := newHarness(t)
	item := h.newItem(t, h.withHousehold(t), `{"name":"น้ำปลา","unit":"ขวด"}`)

	if item.HasImage {
		t.Error("a new item claims to have an image")
	}

	path := h.requestUpload(t, item.ID)
	res := h.signedIn(t, http.MethodGet, "/api/households/"+h.households.byUser["user-uuid-1"][0].ID+"/items", "")
	if decodeItems(t, res).Items[0].HasImage {
		t.Error("item claims to have an image after only requesting an upload url")
	}

	if res := h.signedIn(t, http.MethodPut, "/api/items/"+item.ID+"/image", `{"path":"`+path+`"}`); res.StatusCode != http.StatusOK {
		t.Fatalf("confirm status = %d, want %d", res.StatusCode, http.StatusOK)
	}

	after := decodeItems(t, h.signedIn(t, http.MethodGet, "/api/households/"+h.households.byUser["user-uuid-1"][0].ID+"/items", ""))
	if !after.Items[0].HasImage {
		t.Error("item does not have an image after confirmation")
	}
}

func TestServingAnImageRedirectsToASignedUrl(t *testing.T) {
	h := newHarness(t)
	item := h.newItem(t, h.withHousehold(t), `{"name":"น้ำปลา","unit":"ขวด"}`)
	path := h.requestUpload(t, item.ID)
	h.signedIn(t, http.MethodPut, "/api/items/"+item.ID+"/image", `{"path":"`+path+`"}`)

	res := h.signedIn(t, http.MethodGet, "/api/items/"+item.ID+"/image", "")

	if res.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusFound)
	}
	if location := res.Header.Get("Location"); location == "" {
		t.Error("redirect has no Location header")
	}
	if len(h.storage.signedDownloads) != 1 {
		t.Errorf("signed %d downloads, want 1", len(h.storage.signedDownloads))
	}
}

// requestUpload asks for an upload url and returns the path the server chose.
func (h *harness) requestUpload(t *testing.T, itemID string) string {
	t.Helper()
	res := h.signedIn(t, http.MethodPost, "/api/items/"+itemID+"/image", "")
	var got map[string]string
	if err := json.Unmarshal([]byte(body(t, res)), &got); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	return got["path"]
}

// The path comes back from the client, so an item must not be pointable at an arbitrary
// object — including another household's photo.
func TestConfirmingRejectsAPathOutsideTheItemsPrefix(t *testing.T) {
	h := newHarness(t)
	item := h.newItem(t, h.withHousehold(t), `{"name":"น้ำปลา","unit":"ขวด"}`)

	for label, path := range map[string]string{
		"another item":   "items/some-other-item/abc.jpg",
		"bucket root":    "secret.jpg",
		"path traversal": "items/" + item.ID + "/../../secret.jpg",
	} {
		res := h.signedIn(t, http.MethodPut, "/api/items/"+item.ID+"/image", `{"path":"`+path+`"}`)
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want %d", label, res.StatusCode, http.StatusBadRequest)
		}
	}
}

func TestServingAnImageForAnItemWithoutOneIsNotFound(t *testing.T) {
	h := newHarness(t)
	item := h.newItem(t, h.withHousehold(t), `{"name":"น้ำปลา","unit":"ขวด"}`)

	if res := h.signedIn(t, http.MethodGet, "/api/items/"+item.ID+"/image", ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
}

// Storage being unreachable is an upstream fault, not a missing image.
func TestStorageFailureIsReportedAsAnUpstreamFault(t *testing.T) {
	h := newHarness(t)
	item := h.newItem(t, h.withHousehold(t), `{"name":"น้ำปลา","unit":"ขวด"}`)
	h.storage.err = errors.New("dial tcp: connection refused")

	if res := h.signedIn(t, http.MethodPost, "/api/items/"+item.ID+"/image", ""); res.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusBadGateway)
	}
}

func TestImageEndpointsRequireASession(t *testing.T) {
	h := newHarness(t)

	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/items/some-id/image"},
		{http.MethodPut, "/api/items/some-id/image"},
		{http.MethodGet, "/api/items/some-id/image"},
	} {
		if res := h.do(httptest.NewRequest(c.method, c.path, nil)); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want %d", c.method, c.path, res.StatusCode, http.StatusUnauthorized)
		}
	}
}
