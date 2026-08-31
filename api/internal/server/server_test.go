package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

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
		"index.html":           {Data: []byte("<!doctype html><title>Tony The Helper</title>")},
		"assets/app-abc123.js": {Data: []byte("console.log('app')")},
		"manifest.webmanifest": {Data: []byte(`{"name":"Tony The Helper"}`)},
		"sw.js":                {Data: []byte("/* service worker */")},
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

// fakeHouseholds is an in-memory stand-in for the household, membership and invite
// tables. It models ownership and invite consumption, because "only the owner manages
// people" and "an invite grants one person membership" are the behaviours under test.
type fakeHouseholds struct {
	byUser  map[string][]store.Household
	names   map[string]string
	owners  map[string]string
	invites map[string]*fakeInvite
	last    map[string]string
	err     error
	nextID  int
}

type fakeInvite struct {
	householdID string
	expiresAt   time.Time
	consumedBy  string
}

func newFakeHouseholds() *fakeHouseholds {
	return &fakeHouseholds{
		byUser:  map[string][]store.Household{},
		names:   map[string]string{},
		owners:  map[string]string{},
		invites: map[string]*fakeInvite{},
		last:    map[string]string{},
	}
}

func (f *fakeHouseholds) CreateHousehold(_ context.Context, ownerID, name string) (store.Household, error) {
	if f.err != nil {
		return store.Household{}, f.err
	}
	f.nextID++
	h := store.Household{ID: fmt.Sprintf("household-%d", f.nextID), Name: name, OwnerID: ownerID, IsOwner: true}
	f.byUser[ownerID] = append(f.byUser[ownerID], h)
	f.names[h.ID] = name
	f.owners[h.ID] = ownerID
	f.last[ownerID] = h.ID
	return h, nil
}

func (f *fakeHouseholds) RenameHousehold(_ context.Context, householdID, ownerID, name string) (store.Household, error) {
	if f.err != nil {
		return store.Household{}, f.err
	}
	if err := f.requireOwner(householdID, ownerID); err != nil {
		return store.Household{}, err
	}
	f.names[householdID] = name
	// One row changes in the real store, but this fake keeps a copy per member, so every
	// copy has to move together or a member would still read the old name.
	for user := range f.byUser {
		for i := range f.byUser[user] {
			if f.byUser[user][i].ID == householdID {
				f.byUser[user][i].Name = name
			}
		}
	}
	return store.Household{ID: householdID, Name: name, OwnerID: ownerID, IsOwner: true}, nil
}

func (f *fakeHouseholds) HouseholdsForUser(_ context.Context, userID string) ([]store.Household, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byUser[userID], nil
}

func (f *fakeHouseholds) isMember(householdID, userID string) bool {
	for _, h := range f.byUser[userID] {
		if h.ID == householdID {
			return true
		}
	}
	return false
}

func (f *fakeHouseholds) requireOwner(householdID, userID string) error {
	if !f.isMember(householdID, userID) {
		return store.ErrNotFound
	}
	if f.owners[householdID] != userID {
		return store.ErrNotOwner
	}
	return nil
}

func (f *fakeHouseholds) CreateInvite(_ context.Context, householdID, ownerID string, now time.Time) (store.Invite, error) {
	if f.err != nil {
		return store.Invite{}, f.err
	}
	if err := f.requireOwner(householdID, ownerID); err != nil {
		return store.Invite{}, err
	}
	token := fmt.Sprintf("token-%d", len(f.invites)+1)
	f.invites[token] = &fakeInvite{householdID: householdID, expiresAt: now.Add(store.InviteTTL)}
	return store.Invite{Token: token, ExpiresAt: now.Add(store.InviteTTL)}, nil
}

func (f *fakeHouseholds) AcceptInvite(_ context.Context, token, userID string, now time.Time) (store.Household, error) {
	if f.err != nil {
		return store.Household{}, f.err
	}
	invite, ok := f.invites[token]
	if !ok {
		return store.Household{}, store.ErrNotFound
	}
	if !invite.expiresAt.After(now) {
		return store.Household{}, store.ErrInviteExpired
	}

	already := f.isMember(invite.householdID, userID)
	if !already && invite.consumedBy != "" && invite.consumedBy != userID {
		return store.Household{}, store.ErrInviteUsed
	}

	h := store.Household{
		ID: invite.householdID, Name: f.names[invite.householdID],
		OwnerID: f.owners[invite.householdID],
	}
	h.IsOwner = h.OwnerID == userID
	if !already {
		f.byUser[userID] = append(f.byUser[userID], h)
	}
	if invite.consumedBy == "" {
		invite.consumedBy = userID
	}
	f.last[userID] = invite.householdID
	return h, nil
}

func (f *fakeHouseholds) MembersOf(_ context.Context, householdID, userID string) ([]store.Member, error) {
	if f.err != nil {
		return nil, f.err
	}
	if !f.isMember(householdID, userID) {
		return nil, store.ErrNotFound
	}
	members := []store.Member{}
	for candidate, households := range f.byUser {
		for _, h := range households {
			if h.ID == householdID {
				members = append(members, store.Member{
					UserID: candidate, DisplayName: "ผู้ใช้ " + candidate,
					IsOwner: f.owners[householdID] == candidate,
				})
			}
		}
	}
	sort.Slice(members, func(a, b int) bool {
		if members[a].IsOwner != members[b].IsOwner {
			return members[a].IsOwner
		}
		return members[a].UserID < members[b].UserID
	})
	return members, nil
}

func (f *fakeHouseholds) RemoveMember(_ context.Context, householdID, ownerID, targetUserID string) error {
	if f.err != nil {
		return f.err
	}
	if err := f.requireOwner(householdID, ownerID); err != nil {
		return err
	}
	if targetUserID == ownerID {
		return store.ErrCannotRemoveOwner
	}
	if !f.isMember(householdID, targetUserID) {
		return store.ErrNotFound
	}
	kept := []store.Household{}
	for _, h := range f.byUser[targetUserID] {
		if h.ID != householdID {
			kept = append(kept, h)
		}
	}
	f.byUser[targetUserID] = kept
	return nil
}

func (f *fakeHouseholds) SetLastHousehold(_ context.Context, userID, householdID string) error {
	if f.err != nil {
		return f.err
	}
	if !f.isMember(householdID, userID) {
		return store.ErrNotFound
	}
	f.last[userID] = householdID
	return nil
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
	// The real store orders the household-wide history by time, so the fake has to give
	// its movements an order too. A counter, not the wall clock: tests must not depend on
	// how fast they run.
	clock int
}

// tick returns a timestamp strictly later than the previous one.
func (f *fakeItems) tick() time.Time {
	f.clock++
	return time.Date(2026, 8, 31, 9, 0, f.clock, 0, time.UTC)
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
			CreatedAt: f.tick(),
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
	if len(out) > store.ItemHistoryLimit {
		out = out[:store.ItemHistoryLimit]
	}
	return out, nil
}

func (f *fakeItems) MovementsForHousehold(_ context.Context, householdID, userID string, filter store.MovementFilter) ([]store.HouseholdMovement, error) {
	if f.err != nil {
		return nil, f.err
	}
	if !f.isMember(householdID, userID) {
		return nil, store.ErrNotFound
	}

	// Flatten every item's log, deleted items included — their history is why they were
	// soft-deleted rather than removed.
	out := []store.HouseholdMovement{}
	for _, item := range f.byHousehold[householdID] {
		for _, m := range f.movements[item.ID] {
			if filter.ItemID != "" && filter.ItemID != item.ID {
				continue
			}
			if q := strings.ToLower(filter.Query); q != "" &&
				!strings.Contains(strings.ToLower(item.Name), q) &&
				!strings.Contains(strings.ToLower(m.Note), q) {
				continue
			}
			out = append(out, store.HouseholdMovement{
				ID: m.ID, ItemID: item.ID, ItemName: item.Name, Unit: item.Unit,
				ActorName: m.ActorName, QuantityBefore: m.QuantityBefore,
				QuantityAfter: m.QuantityAfter, Note: m.Note, CreatedAt: m.CreatedAt,
			})
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })

	limit := filter.Limit
	if limit <= 0 {
		limit = store.HouseholdHistoryLimit
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeItems) ItemsWithHistory(_ context.Context, householdID, userID string) ([]store.ItemRef, error) {
	if f.err != nil {
		return nil, f.err
	}
	if !f.isMember(householdID, userID) {
		return nil, store.ErrNotFound
	}
	refs := []store.ItemRef{}
	for _, item := range f.byHousehold[householdID] {
		if len(f.movements[item.ID]) > 0 {
			refs = append(refs, store.ItemRef{ID: item.ID, Name: item.Name})
		}
	}
	sort.Slice(refs, func(a, b int) bool { return refs[a].Name < refs[b].Name })
	return refs, nil
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

// asUser returns a session cookie for another person, registering them in the fake user
// store. The invite flow only means anything with two people in it.
func (h *harness) asUser(t *testing.T, userID, name string) *http.Cookie {
	t.Helper()
	h.users.byID[userID] = store.User{ID: userID, DisplayName: name}
	token, err := h.sessions.Issue(userID)
	if err != nil {
		t.Fatalf("issue session for %s: %v", userID, err)
	}
	return &http.Cookie{Name: session.CookieName, Value: token}
}

// doAs makes a request as whoever the cookie belongs to.
func (h *harness) doAs(t *testing.T, cookie *http.Cookie, method, path, payload string) *http.Response {
	t.Helper()
	var req *http.Request
	if payload == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(cookie)
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
		{http.MethodPatch, "/api/households/household-1", `{"name":"บ้านเรา"}`},
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

// --- invites, members and switching ---------------------------------------------

// inviteToken creates an invite as the owner and pulls the token out of the LINE link.
func (h *harness) inviteToken(t *testing.T, householdID string) (token, url string) {
	t.Helper()
	res := h.signedIn(t, http.MethodPost, "/api/households/"+householdID+"/invites", "")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create invite: status = %d; body = %s", res.StatusCode, body(t, res))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(body(t, res)), &got); err != nil {
		t.Fatalf("decode invite: %v", err)
	}
	url, _ = got["url"].(string)
	parts := strings.SplitN(url, "invite=", 2)
	if len(parts) != 2 {
		t.Fatalf("url %q carries no invite token", url)
	}
	return parts[1], url
}

// The owner sends a link, never a code. It must open inside LINE, not a browser.
func TestTheInviteLinkOpensTheLiffApp(t *testing.T) {
	h := newHarness(t)
	_, url := h.inviteToken(t, h.withHousehold(t))

	if !strings.HasPrefix(url, "https://liff.line.me/1234-abcd?invite=") {
		t.Errorf("url = %q, want a liff.line.me link for this LIFF app", url)
	}
}

func TestOnlyTheOwnerCanInvite(t *testing.T) {
	h := newHarness(t)
	hh := h.withHousehold(t)
	token, _ := h.inviteToken(t, hh)

	mum := h.asUser(t, "user-mum", "แม่")
	if res := h.doAs(t, mum, http.MethodPost, "/api/invites/"+token+"/accept", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("accept: status = %d", res.StatusCode)
	}

	// She is a member now, but ownership is about people and she is not the owner.
	if res := h.doAs(t, mum, http.MethodPost, "/api/households/"+hh+"/invites", ""); res.StatusCode != http.StatusForbidden {
		t.Errorf("member inviting: status = %d, want %d", res.StatusCode, http.StatusForbidden)
	}
}

func TestAnOutsiderCannotInviteAndLearnsNothing(t *testing.T) {
	h := newHarness(t)
	hh := h.withHousehold(t)
	stranger := h.asUser(t, "user-stranger", "คนแปลกหน้า")

	// 404 rather than 403: whether this household exists is not their business.
	if res := h.doAs(t, stranger, http.MethodPost, "/api/households/"+hh+"/invites", ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
}

func TestAcceptingAnInviteJoinsTheHousehold(t *testing.T) {
	h := newHarness(t)
	hh := h.withHousehold(t)
	token, _ := h.inviteToken(t, hh)
	mum := h.asUser(t, "user-mum", "แม่")

	res := h.doAs(t, mum, http.MethodPost, "/api/invites/"+token+"/accept", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", res.StatusCode, http.StatusOK, body(t, res))
	}
	var joined store.Household
	if err := json.Unmarshal([]byte(body(t, res)), &joined); err != nil {
		t.Fatalf("decode household: %v", err)
	}
	if joined.ID != hh {
		t.Errorf("joined %q, want %q", joined.ID, hh)
	}
	if joined.IsOwner {
		t.Error("the invitee is reported as the owner")
	}

	// ...and it is the household she lands in.
	listed := decodeHouseholds(t, h.doAs(t, mum, http.MethodGet, "/api/households", ""))
	if listed.CurrentID != hh {
		t.Errorf("currentId = %q, want %q", listed.CurrentID, hh)
	}
}

// Tapping the same LINE message twice must not produce a failure message.
func TestAcceptingYourOwnInviteTwiceJustOpensTheHousehold(t *testing.T) {
	h := newHarness(t)
	token, _ := h.inviteToken(t, h.withHousehold(t))
	mum := h.asUser(t, "user-mum", "แม่")

	h.doAs(t, mum, http.MethodPost, "/api/invites/"+token+"/accept", "")
	if res := h.doAs(t, mum, http.MethodPost, "/api/invites/"+token+"/accept", ""); res.StatusCode != http.StatusOK {
		t.Errorf("second accept: status = %d, want %d", res.StatusCode, http.StatusOK)
	}
}

// An invite grants membership to one person, not to everyone the link is forwarded to.
func TestAnInviteCannotBeReusedBySomeoneElse(t *testing.T) {
	h := newHarness(t)
	token, _ := h.inviteToken(t, h.withHousehold(t))

	mum := h.asUser(t, "user-mum", "แม่")
	h.doAs(t, mum, http.MethodPost, "/api/invites/"+token+"/accept", "")

	forwarded := h.asUser(t, "user-neighbour", "เพื่อนบ้าน")
	if res := h.doAs(t, forwarded, http.MethodPost, "/api/invites/"+token+"/accept", ""); res.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusConflict)
	}
}

func TestAnUnknownInviteIsNotFound(t *testing.T) {
	h := newHarness(t)
	h.withHousehold(t)

	if res := h.signedIn(t, http.MethodPost, "/api/invites/no-such-token/accept", ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
}

// A link that has run out is reported distinctly, because "try again" helps for an
// expired invite in a way it does not for a used one.
func TestAnExpiredInviteIsGone(t *testing.T) {
	h := newHarness(t)
	hh := h.withHousehold(t)
	token, _ := h.inviteToken(t, hh)
	h.households.invites[token].expiresAt = time.Now().Add(-time.Minute)

	mum := h.asUser(t, "user-mum", "แม่")
	if res := h.doAs(t, mum, http.MethodPost, "/api/invites/"+token+"/accept", ""); res.StatusCode != http.StatusGone {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusGone)
	}
}

func TestTheMemberListPutsTheOwnerFirst(t *testing.T) {
	h := newHarness(t)
	hh := h.withHousehold(t)
	token, _ := h.inviteToken(t, hh)
	h.doAs(t, h.asUser(t, "user-mum", "แม่"), http.MethodPost, "/api/invites/"+token+"/accept", "")

	var got struct {
		Members []store.Member `json:"members"`
	}
	if err := json.Unmarshal([]byte(body(t, h.signedIn(t, http.MethodGet, "/api/households/"+hh+"/members", ""))), &got); err != nil {
		t.Fatalf("decode members: %v", err)
	}
	if len(got.Members) != 2 {
		t.Fatalf("got %d members, want 2", len(got.Members))
	}
	if !got.Members[0].IsOwner {
		t.Error("the owner is not first in the member list")
	}
}

func TestTheOwnerCanRemoveAMemberWhoThenLosesAccess(t *testing.T) {
	h := newHarness(t)
	hh := h.withHousehold(t)
	token, _ := h.inviteToken(t, hh)
	mum := h.asUser(t, "user-mum", "แม่")
	h.doAs(t, mum, http.MethodPost, "/api/invites/"+token+"/accept", "")

	if res := h.signedIn(t, http.MethodDelete, "/api/households/"+hh+"/members/user-mum", ""); res.StatusCode != http.StatusNoContent {
		t.Fatalf("remove: status = %d, want %d", res.StatusCode, http.StatusNoContent)
	}

	if listed := decodeHouseholds(t, h.doAs(t, mum, http.MethodGet, "/api/households", "")); len(listed.Households) != 0 {
		t.Errorf("removed member still belongs to %d households", len(listed.Households))
	}
	if res := h.doAs(t, mum, http.MethodGet, "/api/households/"+hh+"/members", ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("removed member reading members: status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
}

// Removing the owner would leave a household nobody can manage.
func TestTheOwnerCannotRemoveThemselves(t *testing.T) {
	h := newHarness(t)
	hh := h.withHousehold(t)

	if res := h.signedIn(t, http.MethodDelete, "/api/households/"+hh+"/members/user-uuid-1", ""); res.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusBadRequest)
	}
}

func TestAMemberCannotRemoveAnyone(t *testing.T) {
	h := newHarness(t)
	hh := h.withHousehold(t)
	token, _ := h.inviteToken(t, hh)
	mum := h.asUser(t, "user-mum", "แม่")
	h.doAs(t, mum, http.MethodPost, "/api/invites/"+token+"/accept", "")

	if res := h.doAs(t, mum, http.MethodDelete, "/api/households/"+hh+"/members/user-uuid-1", ""); res.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusForbidden)
	}
}

// Ownership is about people, not about stock: every member manages the items.
func TestAnyMemberCanAddItemsAndChangeQuantities(t *testing.T) {
	h := newHarness(t)
	hh := h.withHousehold(t)
	token, _ := h.inviteToken(t, hh)
	mum := h.asUser(t, "user-mum", "แม่")
	h.doAs(t, mum, http.MethodPost, "/api/invites/"+token+"/accept", "")

	res := h.doAs(t, mum, http.MethodPost, "/api/households/"+hh+"/items", `{"name":"ผงซักฟอก","unit":"ถุง"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("member creating an item: status = %d, want %d", res.StatusCode, http.StatusCreated)
	}
	var item store.Item
	json.Unmarshal([]byte(body(t, res)), &item)

	if res := h.doAs(t, mum, http.MethodPost, "/api/items/"+item.ID+"/quantity", `{"delta":1}`); res.StatusCode != http.StatusOK {
		t.Errorf("member adjusting quantity: status = %d, want %d", res.StatusCode, http.StatusOK)
	}
}

// --- switching (#11) ------------------------------------------------------------

func TestSelectingAHouseholdChangesWhereYouLand(t *testing.T) {
	h := newHarness(t)
	first := h.withHousehold(t)
	second := h.withHousehold(t)

	// Creating the second made it current; go back to the first.
	if res := h.signedIn(t, http.MethodPost, "/api/households/"+first+"/select", ""); res.StatusCode != http.StatusNoContent {
		t.Fatalf("select: status = %d, want %d", res.StatusCode, http.StatusNoContent)
	}
	if got := decodeHouseholds(t, h.signedIn(t, http.MethodGet, "/api/households", "")).CurrentID; got != first {
		t.Errorf("currentId = %q, want %q", got, first)
	}
	if second == first {
		t.Fatal("the two households are the same; the test proves nothing")
	}
}

func TestSelectingAHouseholdYouAreNotInIsNotFound(t *testing.T) {
	h := newHarness(t)
	hh := h.withHousehold(t)
	stranger := h.asUser(t, "user-stranger", "คนแปลกหน้า")

	if res := h.doAs(t, stranger, http.MethodPost, "/api/households/"+hh+"/select", ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
}

func TestMemberAndInviteEndpointsRequireASession(t *testing.T) {
	h := newHarness(t)

	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/households/x/invites"},
		{http.MethodGet, "/api/households/x/members"},
		{http.MethodDelete, "/api/households/x/members/y"},
		{http.MethodPost, "/api/households/x/select"},
		{http.MethodPost, "/api/invites/t/accept"},
	} {
		if res := h.do(httptest.NewRequest(c.method, c.path, nil)); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want %d", c.method, c.path, res.StatusCode, http.StatusUnauthorized)
		}
	}
}

// --- renaming a household -------------------------------------------------------

func TestTheOwnerCanRenameTheHousehold(t *testing.T) {
	h := newHarness(t)
	id := h.withHousehold(t)

	res := h.signedIn(t, http.MethodPatch, "/api/households/"+id, `{"name":"บ้านสวน"}`)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", res.StatusCode, http.StatusOK, body(t, res))
	}
	var got store.Household
	if err := json.Unmarshal([]byte(body(t, res)), &got); err != nil {
		t.Fatalf("decode household: %v", err)
	}
	if got.Name != "บ้านสวน" {
		t.Errorf("name = %q, want บ้านสวน", got.Name)
	}

	// The list is what the app reads on every open, so the new name has to be there too.
	listed := decodeHouseholds(t, h.signedIn(t, http.MethodGet, "/api/households", ""))
	if len(listed.Households) != 1 || listed.Households[0].Name != "บ้านสวน" {
		t.Errorf("list = %+v, want one household named บ้านสวน", listed.Households)
	}
}

func TestRenamingTrimsSurroundingWhitespace(t *testing.T) {
	h := newHarness(t)
	id := h.withHousehold(t)

	res := h.signedIn(t, http.MethodPatch, "/api/households/"+id, `{"name":"  บ้านสวน  "}`)

	var got store.Household
	if err := json.Unmarshal([]byte(body(t, res)), &got); err != nil {
		t.Fatalf("decode household: %v", err)
	}
	if got.Name != "บ้านสวน" {
		t.Errorf("name = %q, want it trimmed to บ้านสวน", got.Name)
	}
}

func TestRenamingRejectsAnEmptyName(t *testing.T) {
	for _, name := range []string{"", "   "} {
		h := newHarness(t)
		id := h.withHousehold(t)

		res := h.signedIn(t, http.MethodPatch, "/api/households/"+id, `{"name":"`+name+`"}`)

		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("name %q: status = %d, want %d", name, res.StatusCode, http.StatusBadRequest)
		}
	}
}

func TestRenamingRejectsAnOverlongName(t *testing.T) {
	h := newHarness(t)
	id := h.withHousehold(t)

	res := h.signedIn(t, http.MethodPatch, "/api/households/"+id, `{"name":"`+strings.Repeat("ก", 61)+`"}`)

	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusBadRequest)
	}
}

// A 60-character Thai name is 180 bytes, so a byte-based limit would reject a name that
// is well within range. Same guard as creating a household.
func TestRenamingCountsNameLengthInCharactersNotBytes(t *testing.T) {
	h := newHarness(t)
	id := h.withHousehold(t)

	res := h.signedIn(t, http.MethodPatch, "/api/households/"+id, `{"name":"`+strings.Repeat("ก", 60)+`"}`)

	if res.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d; body = %s", res.StatusCode, http.StatusOK, body(t, res))
	}
}

func TestAMemberCannotRenameTheHousehold(t *testing.T) {
	h := newHarness(t)
	id := h.withHousehold(t)
	token, _ := h.inviteToken(t, id)
	mum := h.asUser(t, "user-mum", "แม่")
	if res := h.doAs(t, mum, http.MethodPost, "/api/invites/"+token+"/accept", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("accept: status = %d", res.StatusCode)
	}

	res := h.doAs(t, mum, http.MethodPatch, "/api/households/"+id, `{"name":"บ้านของแม่"}`)

	if res.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d; body = %s", res.StatusCode, http.StatusForbidden, body(t, res))
	}
}

func TestAnOutsiderRenamingAHouseholdLearnsNothing(t *testing.T) {
	h := newHarness(t)
	id := h.withHousehold(t)
	stranger := h.asUser(t, "user-stranger", "คนแปลกหน้า")

	res := h.doAs(t, stranger, http.MethodPatch, "/api/households/"+id, `{"name":"บ้านฉัน"}`)

	// 404, not 403: a stranger must not be told the household is there to be renamed.
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
}

func TestRenamingRejectsAMalformedBody(t *testing.T) {
	h := newHarness(t)
	id := h.withHousehold(t)

	res := h.signedIn(t, http.MethodPatch, "/api/households/"+id, `{"name":`)

	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusBadRequest)
	}
}

// Go's own MIME table has no entry for .webmanifest, so without a registration the file
// is content-sniffed and served as text/plain, which some browsers refuse to parse as a
// manifest. Serving it correctly is what makes the app installable.
func TestManifestIsServedAsAManifest(t *testing.T) {
	res := newHarness(t).get("/manifest.webmanifest")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if got := res.Header.Get("Content-Type"); !strings.Contains(got, "application/manifest+json") {
		t.Errorf("Content-Type = %q, want it to contain application/manifest+json", got)
	}
}

// --- caching ---------------------------------------------------------------------

// Build output carries a content hash in its filename, so a given URL can never change
// meaning. Serving it immutable is what lets a repeat open skip the whole bundle.
func TestBuiltAssetsAreCachedForever(t *testing.T) {
	res := newHarness(t).get("/assets/app-abc123.js")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	got := res.Header.Get("Cache-Control")
	for _, want := range []string{"max-age=31536000", "immutable"} {
		if !strings.Contains(got, want) {
			t.Errorf("Cache-Control = %q, want it to contain %q", got, want)
		}
	}
}

// The shell is the one file that must always be revalidated. A deploy replaces the hashed
// asset filenames and the old ones stop existing, so a browser left holding a stale shell
// would ask for a bundle that is gone and render nothing at all.
func TestTheAppShellIsAlwaysRevalidated(t *testing.T) {
	for _, path := range []string{"/", "/some/client/route"} {
		res := newHarness(t).get(path)

		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d, want %d", path, res.StatusCode, http.StatusOK)
		}
		if got := res.Header.Get("Cache-Control"); !strings.Contains(got, "no-cache") {
			t.Errorf("%s: Cache-Control = %q, want it to contain no-cache", path, got)
		}
	}
}

// index.html is reachable by its own name as well as by the fallback, and it must not be
// cacheable by that route either.
func TestTheShellIsNotCacheableUnderItsOwnName(t *testing.T) {
	res := newHarness(t).get("/index.html")

	if got := res.Header.Get("Cache-Control"); !strings.Contains(got, "no-cache") {
		t.Errorf("Cache-Control = %q, want it to contain no-cache", got)
	}
}

// The service worker decides what the browser caches, so a stale copy of it is the one
// that could pin someone to old behaviour with no way out.
func TestTheServiceWorkerIsAlwaysRevalidated(t *testing.T) {
	res := newHarness(t).get("/sw.js")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if got := res.Header.Get("Cache-Control"); !strings.Contains(got, "no-cache") {
		t.Errorf("Cache-Control = %q, want it to contain no-cache", got)
	}
}

// --- the household-wide history ---------------------------------------------------

func decodeItem(t *testing.T, res *http.Response) store.Item {
	t.Helper()
	if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK {
		t.Fatalf("decodeItem: status = %d; body = %s", res.StatusCode, body(t, res))
	}
	var item store.Item
	if err := json.Unmarshal([]byte(body(t, res)), &item); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	return item
}

func decodeHistory(t *testing.T, res *http.Response) struct {
	Movements []store.HouseholdMovement `json:"movements"`
	Items     []store.ItemRef           `json:"items"`
} {
	t.Helper()
	var out struct {
		Movements []store.HouseholdMovement `json:"movements"`
		Items     []store.ItemRef           `json:"items"`
	}
	if err := json.Unmarshal([]byte(body(t, res)), &out); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	return out
}

// twoChangedItems sets up a household with two items that both have history. The rice
// change happens last, so it is the newest.
func (h *harness) twoChangedItems(t *testing.T) (householdID, fishID, riceID string) {
	t.Helper()
	householdID = h.withHousehold(t)
	fish := decodeItem(t, h.signedIn(t, http.MethodPost, "/api/households/"+householdID+"/items",
		`{"name":"น้ำปลา","unit":"ขวด"}`))
	rice := decodeItem(t, h.signedIn(t, http.MethodPost, "/api/households/"+householdID+"/items",
		`{"name":"ข้าวสาร","unit":"ถุง"}`))

	h.signedIn(t, http.MethodPost, "/api/items/"+fish.ID+"/quantity", `{"delta":1}`)
	h.signedIn(t, http.MethodPost, "/api/items/"+rice.ID+"/quantity", `{"quantity":4,"note":"ซื้อมาจากตลาด"}`)
	return householdID, fish.ID, rice.ID
}

func TestHouseholdHistoryListsChangesAcrossItemsNewestFirst(t *testing.T) {
	h := newHarness(t)
	householdID, _, riceID := h.twoChangedItems(t)

	res := h.signedIn(t, http.MethodGet, "/api/households/"+householdID+"/movements", "")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", res.StatusCode, http.StatusOK, body(t, res))
	}
	got := decodeHistory(t, res)
	if len(got.Movements) != 2 {
		t.Fatalf("got %d movements, want 2: %+v", len(got.Movements), got.Movements)
	}
	if got.Movements[0].ItemID != riceID {
		t.Errorf("newest movement is for item %q, want %q", got.Movements[0].ItemID, riceID)
	}
	if got.Movements[0].ItemName != "ข้าวสาร" || got.Movements[0].Unit != "ถุง" {
		t.Errorf("newest movement = %+v, want it to name ข้าวสาร/ถุง", got.Movements[0])
	}
	if got.Movements[0].Note != "ซื้อมาจากตลาด" {
		t.Errorf("note = %q, want ซื้อมาจากตลาด", got.Movements[0].Note)
	}
}

// The list is read down rather than looked at, so it carries no picture at all — there is
// no field on the payload for one.
func TestHouseholdHistoryCarriesNoPictures(t *testing.T) {
	h := newHarness(t)
	householdID, _, _ := h.twoChangedItems(t)

	raw := body(t, h.signedIn(t, http.MethodGet, "/api/households/"+householdID+"/movements", ""))

	if strings.Contains(raw, "pictureUrl") {
		t.Errorf("history payload mentions a picture: %s", raw)
	}
}

func TestHouseholdHistoryOffersTheItemsItCanBeFilteredTo(t *testing.T) {
	h := newHarness(t)
	householdID, _, _ := h.twoChangedItems(t)
	// A third item that has never been touched must not be offered.
	h.signedIn(t, http.MethodPost, "/api/households/"+householdID+"/items", `{"name":"สบู่","unit":"ก้อน"}`)

	got := decodeHistory(t, h.signedIn(t, http.MethodGet, "/api/households/"+householdID+"/movements", ""))

	names := []string{}
	for _, i := range got.Items {
		names = append(names, i.Name)
	}
	if len(names) != 2 || names[0] != "ข้าวสาร" || names[1] != "น้ำปลา" {
		t.Errorf("filter offers %v, want [ข้าวสาร น้ำปลา]", names)
	}
}

func TestHouseholdHistoryCanBeNarrowedToOneItemOverHTTP(t *testing.T) {
	h := newHarness(t)
	householdID, fishID, _ := h.twoChangedItems(t)

	got := decodeHistory(t, h.signedIn(t, http.MethodGet,
		"/api/households/"+householdID+"/movements?itemId="+fishID, ""))

	if len(got.Movements) != 1 || got.Movements[0].ItemID != fishID {
		t.Errorf("filtered history = %+v, want only the น้ำปลา change", got.Movements)
	}
	// The choices stay whole: the filter is how the user changes their mind.
	if len(got.Items) != 2 {
		t.Errorf("filter offers %d items, want 2 even while filtered", len(got.Items))
	}
}

func TestHouseholdHistorySearchMatchesNamesAndNotes(t *testing.T) {
	h := newHarness(t)
	householdID, fishID, riceID := h.twoChangedItems(t)

	byName := decodeHistory(t, h.signedIn(t, http.MethodGet,
		"/api/households/"+householdID+"/movements?q="+url.QueryEscape("น้ำปลา"), ""))
	if len(byName.Movements) != 1 || byName.Movements[0].ItemID != fishID {
		t.Errorf("search by name = %+v, want the น้ำปลา change", byName.Movements)
	}

	byNote := decodeHistory(t, h.signedIn(t, http.MethodGet,
		"/api/households/"+householdID+"/movements?q="+url.QueryEscape("ตลาด"), ""))
	if len(byNote.Movements) != 1 || byNote.Movements[0].ItemID != riceID {
		t.Errorf("search by note = %+v, want the ข้าวสาร change", byNote.Movements)
	}
}

func TestHouseholdHistorySearchIsTrimmed(t *testing.T) {
	h := newHarness(t)
	householdID, fishID, _ := h.twoChangedItems(t)

	got := decodeHistory(t, h.signedIn(t, http.MethodGet,
		"/api/households/"+householdID+"/movements?q="+url.QueryEscape("  น้ำปลา  "), ""))

	if len(got.Movements) != 1 || got.Movements[0].ItemID != fishID {
		t.Errorf("padded search = %+v, want the น้ำปลา change", got.Movements)
	}
}

func TestHouseholdHistoryRejectsAnOverlongSearch(t *testing.T) {
	h := newHarness(t)
	householdID, _, _ := h.twoChangedItems(t)

	res := h.signedIn(t, http.MethodGet,
		"/api/households/"+householdID+"/movements?q="+url.QueryEscape(strings.Repeat("ก", 101)), "")

	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusBadRequest)
	}
}

// A 100-character Thai term is 300 bytes, so a byte limit would reject something well
// inside the allowance.
func TestHouseholdHistorySearchLengthIsCountedInCharacters(t *testing.T) {
	h := newHarness(t)
	householdID, _, _ := h.twoChangedItems(t)

	res := h.signedIn(t, http.MethodGet,
		"/api/households/"+householdID+"/movements?q="+url.QueryEscape(strings.Repeat("ก", 100)), "")

	if res.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d; body = %s", res.StatusCode, http.StatusOK, body(t, res))
	}
}

func TestHouseholdHistoryOfAnotherHouseholdIsNotFound(t *testing.T) {
	h := newHarness(t)
	householdID, _, _ := h.twoChangedItems(t)
	stranger := h.asUser(t, "user-stranger-history", "คนแปลกหน้า")

	res := h.doAs(t, stranger, http.MethodGet, "/api/households/"+householdID+"/movements", "")

	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
}

func TestHouseholdHistoryRequiresASession(t *testing.T) {
	res := newHarness(t).get("/api/households/household-1/movements")

	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusUnauthorized)
	}
}

// Requirement one: a single item's history is capped, so a much-used item does not return
// a year of rows to a phone.
func TestAnItemsHistoryOverHTTPIsCapped(t *testing.T) {
	h := newHarness(t)
	householdID := h.withHousehold(t)
	item := decodeItem(t, h.signedIn(t, http.MethodPost, "/api/households/"+householdID+"/items",
		`{"name":"น้ำปลา","unit":"ขวด"}`))
	for i := 0; i < store.ItemHistoryLimit+4; i++ {
		h.signedIn(t, http.MethodPost, "/api/items/"+item.ID+"/quantity", `{"delta":1}`)
	}

	var got struct {
		Movements []store.Movement `json:"movements"`
	}
	res := h.signedIn(t, http.MethodGet, "/api/items/"+item.ID+"/movements", "")
	if err := json.Unmarshal([]byte(body(t, res)), &got); err != nil {
		t.Fatalf("decode movements: %v", err)
	}
	if len(got.Movements) != store.ItemHistoryLimit {
		t.Errorf("got %d movements, want %d", len(got.Movements), store.ItemHistoryLimit)
	}
	if got.Movements[0].QuantityAfter != store.ItemHistoryLimit+4 {
		t.Errorf("newest quantityAfter = %d, want %d", got.Movements[0].QuantityAfter, store.ItemHistoryLimit+4)
	}
}
