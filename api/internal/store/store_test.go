package store_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/supavitW02/tonyTheHelper/api/internal/line"
	"github.com/supavitW02/tonyTheHelper/api/internal/store"
)

// open connects to a throwaway Postgres and applies the migrations. These tests hit a
// real database on purpose: an upsert, a unique constraint and a uuid cast are exactly
// the things a fake would get wrong.
func open(t *testing.T) *store.DB {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping database tests")
	}

	ctx := context.Background()
	db, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	reset(t, url)
	return db
}

// reset empties the tables so each test starts from a known state.
//
// Without this the suite only passes against a fresh database: the second run doubles
// every row and any test that counts fails. TEST_DATABASE_URL must therefore point at a
// throwaway database — never a shared one.
func reset(t *testing.T, url string) {
	t.Helper()

	conn, err := pgx.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("reset: connect: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	if _, err := conn.Exec(context.Background(),
		"truncate household_members, households, users restart identity cascade"); err != nil {
		t.Fatalf("reset: truncate: %v", err)
	}
}

func identity(userID, name, picture string) line.Identity {
	return line.Identity{UserID: userID, DisplayName: name, PictureURL: picture}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	db := open(t)

	// A second run must be a no-op, because every restart of the app calls this.
	if err := db.Migrate(context.Background()); err != nil {
		t.Errorf("second Migrate() error = %v", err)
	}
}

func TestUpsertCreatesThenReusesTheSameUser(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	lineID := "U-create-then-reuse"

	first, err := db.UpsertUser(ctx, identity(lineID, "สุภวิทย์", "https://example.test/a.jpg"))
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if first.ID == "" {
		t.Fatal("first upsert returned no id")
	}

	second, err := db.UpsertUser(ctx, identity(lineID, "สุภวิทย์ (ใหม่)", "https://example.test/b.jpg"))
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	// Same person: the id must not change, or every sign-in would orphan their data.
	if second.ID != first.ID {
		t.Errorf("id changed on second sign-in: %q then %q", first.ID, second.ID)
	}
	// ...but their LINE profile is refreshed, because LINE owns it, not us.
	if second.DisplayName != "สุภวิทย์ (ใหม่)" {
		t.Errorf("displayName = %q, want the refreshed name", second.DisplayName)
	}
	if second.PictureURL != "https://example.test/b.jpg" {
		t.Errorf("pictureUrl = %q, want the refreshed picture", second.PictureURL)
	}
}

func TestUpsertKeepsDifferentPeopleApart(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	a, err := db.UpsertUser(ctx, identity("U-distinct-a", "แม่", ""))
	if err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	b, err := db.UpsertUser(ctx, identity("U-distinct-b", "พ่อ", ""))
	if err != nil {
		t.Fatalf("upsert b: %v", err)
	}

	if a.ID == b.ID {
		t.Error("two different LINE users share one id")
	}
}

// The picture is optional on LINE's side, so an empty one must be stored as absent
// rather than as the empty string.
func TestUpsertHandlesAMissingPicture(t *testing.T) {
	db := open(t)

	u, err := db.UpsertUser(context.Background(), identity("U-no-picture", "ไม่มีรูป", ""))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if u.PictureURL != "" {
		t.Errorf("pictureUrl = %q, want empty", u.PictureURL)
	}
}

func TestUserByIDRoundTrips(t *testing.T) {
	db := open(t)
	ctx := context.Background()

	created, err := db.UpsertUser(ctx, identity("U-round-trip", "สุภวิทย์", "https://example.test/a.jpg"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := db.UserByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if got.ID != created.ID || got.DisplayName != created.DisplayName {
		t.Errorf("UserByID = %+v, want %+v", got, created)
	}
}

func TestUserByIDReportsAMissingUser(t *testing.T) {
	db := open(t)

	_, err := db.UserByID(context.Background(), "6f1e7b1a-0000-4000-8000-000000000000")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// A session carrying a subject that is not a uuid at all is a malformed session, not
// a server fault, so it must read as "not found" rather than erroring out.
func TestUserByIDTreatsAMalformedIDAsNotFound(t *testing.T) {
	db := open(t)

	_, err := db.UserByID(context.Background(), "definitely-not-a-uuid")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// --- households ----------------------------------------------------------------

func newUser(t *testing.T, db *store.DB, lineID, name string) store.User {
	t.Helper()
	u, err := db.UpsertUser(context.Background(), identity(lineID, name, ""))
	if err != nil {
		t.Fatalf("create user %s: %v", lineID, err)
	}
	return u
}

func TestCreateHouseholdMakesTheCreatorOwnerAndMember(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	dad := newUser(t, db, "U-hh-owner", "พ่อ")

	h, err := db.CreateHousehold(ctx, dad.ID, "บ้านเรา")
	if err != nil {
		t.Fatalf("CreateHousehold: %v", err)
	}
	if h.ID == "" {
		t.Fatal("no household id returned")
	}
	if h.Name != "บ้านเรา" {
		t.Errorf("Name = %q, want บ้านเรา", h.Name)
	}
	if !h.IsOwner {
		t.Error("creator is not marked as owner")
	}

	// Creator must also be a member, or the household is invisible to them.
	member, err := db.IsMember(ctx, h.ID, dad.ID)
	if err != nil {
		t.Fatalf("IsMember: %v", err)
	}
	if !member {
		t.Error("creator is not a member of the household they created")
	}

	// ...and it becomes the household they land in next time.
	reloaded, err := db.UserByID(ctx, dad.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if reloaded.LastHouseholdID != h.ID {
		t.Errorf("LastHouseholdID = %q, want %q", reloaded.LastHouseholdID, h.ID)
	}
}

func TestHouseholdsForUserListsOnlyTheirOwn(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	mine := newUser(t, db, "U-hh-mine", "ฉัน")
	stranger := newUser(t, db, "U-hh-stranger", "คนอื่น")

	h, err := db.CreateHousehold(ctx, mine.ID, "บ้านฉัน")
	if err != nil {
		t.Fatalf("CreateHousehold: %v", err)
	}
	if _, err := db.CreateHousehold(ctx, stranger.ID, "บ้านคนอื่น"); err != nil {
		t.Fatalf("CreateHousehold (stranger): %v", err)
	}

	got, err := db.HouseholdsForUser(ctx, mine.ID)
	if err != nil {
		t.Fatalf("HouseholdsForUser: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d households, want 1", len(got))
	}
	if got[0].ID != h.ID {
		t.Errorf("returned household %q, want %q", got[0].ID, h.ID)
	}
}

// A user who belongs to nothing gets an empty list, not an error — that is the state
// that puts them on the create-household screen.
func TestHouseholdsForUserIsEmptyForANewUser(t *testing.T) {
	db := open(t)
	fresh := newUser(t, db, "U-hh-fresh", "ใหม่")

	got, err := db.HouseholdsForUser(context.Background(), fresh.ID)
	if err != nil {
		t.Fatalf("HouseholdsForUser: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d households, want 0", len(got))
	}
}

func TestHouseholdsForUserOrdersOldestFirst(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u := newUser(t, db, "U-hh-two", "สองบ้าน")

	first, err := db.CreateHousehold(ctx, u.ID, "บ้านแรก")
	if err != nil {
		t.Fatalf("CreateHousehold: %v", err)
	}
	second, err := db.CreateHousehold(ctx, u.ID, "บ้านที่สอง")
	if err != nil {
		t.Fatalf("CreateHousehold: %v", err)
	}

	got, err := db.HouseholdsForUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("HouseholdsForUser: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d households, want 2", len(got))
	}
	if got[0].ID != first.ID || got[1].ID != second.ID {
		t.Errorf("order = [%s %s], want [%s %s]", got[0].Name, got[1].Name, first.Name, second.Name)
	}
}

// A household id arriving from a client is a claim, not proof of access.
func TestIsMemberRejectsAnOutsider(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	owner := newUser(t, db, "U-hh-inside", "เจ้าของ")
	outsider := newUser(t, db, "U-hh-outside", "คนนอก")

	h, err := db.CreateHousehold(ctx, owner.ID, "บ้านส่วนตัว")
	if err != nil {
		t.Fatalf("CreateHousehold: %v", err)
	}

	member, err := db.IsMember(ctx, h.ID, outsider.ID)
	if err != nil {
		t.Fatalf("IsMember: %v", err)
	}
	if member {
		t.Error("an outsider was reported as a member")
	}
}

func TestSetLastHouseholdRefusesAHouseholdTheUserIsNotIn(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	owner := newUser(t, db, "U-hh-setowner", "เจ้าของ")
	outsider := newUser(t, db, "U-hh-setoutsider", "คนนอก")

	h, err := db.CreateHousehold(ctx, owner.ID, "บ้านของเจ้าของ")
	if err != nil {
		t.Fatalf("CreateHousehold: %v", err)
	}

	if err := db.SetLastHousehold(ctx, outsider.ID, h.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}

	reloaded, err := db.UserByID(ctx, outsider.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if reloaded.LastHouseholdID != "" {
		t.Errorf("LastHouseholdID = %q, want empty — an outsider changed their own landing household", reloaded.LastHouseholdID)
	}
}
