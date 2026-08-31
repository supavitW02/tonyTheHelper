package store_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

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
		"truncate invites, stock_movements, items, household_members, households, users restart identity cascade"); err != nil {
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

// --- items and quantity ---------------------------------------------------------

// household creates a user with their own household, the common setup for item tests.
func household(t *testing.T, db *store.DB, lineID, name string) (store.User, store.Household) {
	t.Helper()
	u := newUser(t, db, lineID, name)
	h, err := db.CreateHousehold(context.Background(), u.ID, "บ้าน "+name)
	if err != nil {
		t.Fatalf("CreateHousehold: %v", err)
	}
	return u, h
}

// movementCount reads the append-only log directly, so the assertions do not depend on
// the read API that issue #6 adds.
func movementCount(t *testing.T, itemID string) int {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("movementCount: connect: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	var n int
	if err := conn.QueryRow(context.Background(),
		"select count(*) from stock_movements where item_id = $1", itemID).Scan(&n); err != nil {
		t.Fatalf("movementCount: %v", err)
	}
	return n
}

func lastMovement(t *testing.T, itemID string) (before, after int, note string) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("lastMovement: connect: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	var n *string
	err = conn.QueryRow(context.Background(),
		`select quantity_before, quantity_after, note from stock_movements
		 where item_id = $1 order by created_at desc limit 1`, itemID).Scan(&before, &after, &n)
	if err != nil {
		t.Fatalf("lastMovement: %v", err)
	}
	if n != nil {
		note = *n
	}
	return before, after, note
}

func TestCreateItemStartsAtZero(t *testing.T) {
	db := open(t)
	u, h := household(t, db, "U-item-create", "พ่อ")

	item, err := db.CreateItem(context.Background(), h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if item.Name != "น้ำปลา" || item.Unit != "ขวด" {
		t.Errorf("item = %+v, want น้ำปลา/ขวด", item)
	}
	if item.Quantity != 0 {
		t.Errorf("Quantity = %d, want 0", item.Quantity)
	}
	if item.Description != "" {
		t.Errorf("Description = %q, want empty", item.Description)
	}
}

// A household id from a client is a claim. An outsider must not be able to write into
// someone else's household, and must not learn whether it exists.
func TestCreateItemRefusesANonMember(t *testing.T) {
	db := open(t)
	_, h := household(t, db, "U-item-owner", "เจ้าของ")
	outsider := newUser(t, db, "U-item-outsider", "คนนอก")

	_, err := db.CreateItem(context.Background(), h.ID, outsider.ID, store.ItemInput{Name: "ของแอบใส่", Unit: "ชิ้น"})
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestItemsAreScopedToTheirHousehold(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	mine, myHouse := household(t, db, "U-item-mine", "ฉัน")
	theirs, theirHouse := household(t, db, "U-item-theirs", "เขา")

	if _, err := db.CreateItem(ctx, myHouse.ID, mine.ID, store.ItemInput{Name: "ของฉัน", Unit: "ชิ้น"}); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if _, err := db.CreateItem(ctx, theirHouse.ID, theirs.ID, store.ItemInput{Name: "ของเขา", Unit: "ชิ้น"}); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	items, err := db.ItemsForHousehold(ctx, myHouse.ID, mine.ID)
	if err != nil {
		t.Fatalf("ItemsForHousehold: %v", err)
	}
	if len(items) != 1 || items[0].Name != "ของฉัน" {
		t.Errorf("items = %+v, want only ของฉัน", items)
	}

	// ...and asking for someone else's household returns nothing, not their items.
	leaked, err := db.ItemsForHousehold(ctx, theirHouse.ID, mine.ID)
	if err != nil {
		t.Fatalf("ItemsForHousehold (other): %v", err)
	}
	if len(leaked) != 0 {
		t.Errorf("leaked %d items from another household", len(leaked))
	}
}

func TestUnitsAreDistinctAndScoped(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-item-units", "หน่วย")

	for _, spec := range [][2]string{{"น้ำปลา", "ขวด"}, {"ซีอิ๊ว", "ขวด"}, {"ข้าวสาร", "ถุง"}} {
		if _, err := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: spec[0], Unit: spec[1]}); err != nil {
			t.Fatalf("CreateItem: %v", err)
		}
	}

	units, err := db.UnitsForHousehold(ctx, h.ID, u.ID)
	if err != nil {
		t.Fatalf("UnitsForHousehold: %v", err)
	}
	if len(units) != 2 {
		t.Fatalf("units = %v, want 2 distinct", units)
	}
}

func TestPlusOneWritesExactlyOneMovement(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-qty-plus", "พ่อ")
	item, err := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	one := 1
	got, err := db.AdjustQuantity(ctx, item.ID, u.ID, store.QuantityChange{Delta: &one})
	if err != nil {
		t.Fatalf("AdjustQuantity: %v", err)
	}
	if got.Quantity != 1 {
		t.Errorf("Quantity = %d, want 1", got.Quantity)
	}
	if n := movementCount(t, item.ID); n != 1 {
		t.Errorf("movements = %d, want exactly 1", n)
	}

	before, after, _ := lastMovement(t, item.ID)
	if before != 0 || after != 1 {
		t.Errorf("movement = %d→%d, want 0→1", before, after)
	}
}

// Tapping minus at zero must do nothing at all — not error, and not record a movement,
// because nothing moved.
func TestMinusAtZeroIsANoOp(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-qty-floor", "พ่อ")
	item, err := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	minusOne := -1
	got, err := db.AdjustQuantity(ctx, item.ID, u.ID, store.QuantityChange{Delta: &minusOne})
	if err != nil {
		t.Fatalf("AdjustQuantity: %v", err)
	}
	if got.Quantity != 0 {
		t.Errorf("Quantity = %d, want 0", got.Quantity)
	}
	if n := movementCount(t, item.ID); n != 0 {
		t.Errorf("movements = %d, want 0 — nothing moved", n)
	}
}

// Reducing by more than is left floors at zero rather than going negative.
func TestALargeReductionFloorsAtZero(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-qty-underflow", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})

	two := 2
	if _, err := db.AdjustQuantity(ctx, item.ID, u.ID, store.QuantityChange{Absolute: &two}); err != nil {
		t.Fatalf("set to 2: %v", err)
	}

	minusTen := -10
	got, err := db.AdjustQuantity(ctx, item.ID, u.ID, store.QuantityChange{Delta: &minusTen})
	if err != nil {
		t.Fatalf("AdjustQuantity: %v", err)
	}
	if got.Quantity != 0 {
		t.Errorf("Quantity = %d, want 0", got.Quantity)
	}

	before, after, _ := lastMovement(t, item.ID)
	if before != 2 || after != 0 {
		t.Errorf("movement = %d→%d, want 2→0", before, after)
	}
}

func TestSettingATotalRecordsTheNote(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-qty-total", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})

	twelve := 12
	got, err := db.AdjustQuantity(ctx, item.ID, u.ID,
		store.QuantityChange{Absolute: &twelve, Note: "ซื้อมาเพิ่ม"})
	if err != nil {
		t.Fatalf("AdjustQuantity: %v", err)
	}
	if got.Quantity != 12 {
		t.Errorf("Quantity = %d, want 12", got.Quantity)
	}

	before, after, note := lastMovement(t, item.ID)
	if before != 0 || after != 12 {
		t.Errorf("movement = %d→%d, want 0→12", before, after)
	}
	if note != "ซื้อมาเพิ่ม" {
		t.Errorf("note = %q, want ซื้อมาเพิ่ม", note)
	}
}

// Setting the total to what it already is changes nothing, so it records nothing.
func TestSettingTheSameTotalRecordsNothing(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-qty-same", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})

	zero := 0
	if _, err := db.AdjustQuantity(ctx, item.ID, u.ID, store.QuantityChange{Absolute: &zero}); err != nil {
		t.Fatalf("AdjustQuantity: %v", err)
	}
	if n := movementCount(t, item.ID); n != 0 {
		t.Errorf("movements = %d, want 0", n)
	}
}

func TestAdjustQuantityRefusesANonMember(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	owner, h := household(t, db, "U-qty-owner", "เจ้าของ")
	outsider := newUser(t, db, "U-qty-outsider", "คนนอก")
	item, _ := db.CreateItem(ctx, h.ID, owner.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})

	one := 1
	_, err := db.AdjustQuantity(ctx, item.ID, outsider.ID, store.QuantityChange{Delta: &one})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	if n := movementCount(t, item.ID); n != 0 {
		t.Errorf("movements = %d, want 0 — an outsider changed someone else's stock", n)
	}
}

// --- history, low stock and soft delete -----------------------------------------

func threshold(n int) *int { return &n }

// The history is the reason items are soft-deleted rather than removed, so it has to
// survive deletion — otherwise the answer to "who used the last one" disappears with it.
func TestMovementsSurviveASoftDeletedItem(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-hist-delete", "พ่อ")
	item, err := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	three := 3
	if _, err := db.AdjustQuantity(ctx, item.ID, u.ID,
		store.QuantityChange{Absolute: &three, Note: "ซื้อมา"}); err != nil {
		t.Fatalf("AdjustQuantity: %v", err)
	}
	if err := db.DeleteItem(ctx, item.ID, u.ID); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	movements, err := db.MovementsForItem(ctx, item.ID, u.ID)
	if err != nil {
		t.Fatalf("MovementsForItem: %v", err)
	}
	if len(movements) != 1 {
		t.Fatalf("got %d movements after deletion, want 1", len(movements))
	}
	if movements[0].ActorName != "พ่อ" {
		t.Errorf("ActorName = %q, want พ่อ", movements[0].ActorName)
	}
	if movements[0].Note != "ซื้อมา" {
		t.Errorf("Note = %q, want ซื้อมา", movements[0].Note)
	}
}

func TestMovementsAreNewestFirst(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-hist-order", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})

	for _, n := range []int{5, 3, 9} {
		if _, err := db.AdjustQuantity(ctx, item.ID, u.ID, store.QuantityChange{Absolute: &n}); err != nil {
			t.Fatalf("AdjustQuantity: %v", err)
		}
	}

	movements, err := db.MovementsForItem(ctx, item.ID, u.ID)
	if err != nil {
		t.Fatalf("MovementsForItem: %v", err)
	}
	if len(movements) != 3 {
		t.Fatalf("got %d movements, want 3", len(movements))
	}
	if movements[0].QuantityAfter != 9 {
		t.Errorf("newest movement ended at %d, want 9", movements[0].QuantityAfter)
	}
}

func TestSoftDeletedItemsLeaveTheListAndTheUnitChips(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-del-list", "พ่อ")
	keep, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "ข้าวสาร", Unit: "ถุง"})
	drop, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})

	if err := db.DeleteItem(ctx, drop.ID, u.ID); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	items, err := db.ItemsForHousehold(ctx, h.ID, u.ID)
	if err != nil {
		t.Fatalf("ItemsForHousehold: %v", err)
	}
	if len(items) != 1 || items[0].ID != keep.ID {
		t.Errorf("items = %+v, want only ข้าวสาร", items)
	}

	units, err := db.UnitsForHousehold(ctx, h.ID, u.ID)
	if err != nil {
		t.Fatalf("UnitsForHousehold: %v", err)
	}
	if len(units) != 1 || units[0] != "ถุง" {
		t.Errorf("units = %v, want only ถุง — a deleted item's unit is still offered", units)
	}
}

func TestADeletedItemCannotBeAdjustedOrDeletedAgain(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-del-twice", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	if err := db.DeleteItem(ctx, item.ID, u.ID); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	one := 1
	if _, err := db.AdjustQuantity(ctx, item.ID, u.ID, store.QuantityChange{Delta: &one}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("AdjustQuantity error = %v, want ErrNotFound", err)
	}
	if err := db.DeleteItem(ctx, item.ID, u.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second DeleteItem error = %v, want ErrNotFound", err)
	}
}

func TestUpdateItemChangesTheEditableFields(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-edit", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "นำปลา", Unit: "ขวด"})

	updated, err := db.UpdateItem(ctx, item.ID, u.ID, store.ItemInput{
		Name: "น้ำปลา", Unit: "ขวดใหญ่", Description: "ยี่ห้อทิพรส", LowStockThreshold: threshold(2),
	})
	if err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	if updated.Name != "น้ำปลา" || updated.Unit != "ขวดใหญ่" || updated.Description != "ยี่ห้อทิพรส" {
		t.Errorf("updated = %+v, want the corrected fields", updated)
	}
	if updated.LowStockThreshold == nil || *updated.LowStockThreshold != 2 {
		t.Errorf("LowStockThreshold = %v, want 2", updated.LowStockThreshold)
	}
}

// Editing must not be a back door into the quantity column: every change to a quantity
// has to be recorded as a movement (ADR-0001).
func TestUpdateItemLeavesQuantityAlone(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-edit-qty", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})

	seven := 7
	if _, err := db.AdjustQuantity(ctx, item.ID, u.ID, store.QuantityChange{Absolute: &seven}); err != nil {
		t.Fatalf("AdjustQuantity: %v", err)
	}

	updated, err := db.UpdateItem(ctx, item.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	if err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	if updated.Quantity != 7 {
		t.Errorf("Quantity = %d, want it untouched at 7", updated.Quantity)
	}
	if n := movementCount(t, item.ID); n != 1 {
		t.Errorf("movements = %d, want 1 — editing wrote a movement", n)
	}
}

func TestAnItemIsLowAtOrBelowItsThreshold(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-low", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID,
		store.ItemInput{Name: "น้ำปลา", Unit: "ขวด", LowStockThreshold: threshold(2)})

	for _, tc := range []struct {
		quantity int
		wantLow  bool
	}{{5, false}, {3, false}, {2, true}, {1, true}, {0, true}} {
		q := tc.quantity
		got, err := db.AdjustQuantity(ctx, item.ID, u.ID, store.QuantityChange{Absolute: &q})
		if err != nil {
			t.Fatalf("AdjustQuantity(%d): %v", tc.quantity, err)
		}
		if got.IsLow != tc.wantLow {
			t.Errorf("quantity %d: IsLow = %v, want %v", tc.quantity, got.IsLow, tc.wantLow)
		}
	}
}

// An item with no threshold is never low, however little of it is left.
func TestAnItemWithoutAThresholdIsNeverLow(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-nolow", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})

	if item.IsLow {
		t.Error("a brand new item with no threshold is reported low at quantity 0")
	}
}

func TestLowItemsSortToTheTop(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-low-order", "พ่อ")

	// "ก" sorts before "ฮ", so without the low-first rule ก would come first.
	_, _ = db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "ก ของปกติ", Unit: "ชิ้น"})
	low, _ := db.CreateItem(ctx, h.ID, u.ID,
		store.ItemInput{Name: "ฮ ของใกล้หมด", Unit: "ชิ้น", LowStockThreshold: threshold(1)})

	items, err := db.ItemsForHousehold(ctx, h.ID, u.ID)
	if err != nil {
		t.Fatalf("ItemsForHousehold: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].ID != low.ID {
		t.Errorf("first item is %q, want the low one", items[0].Name)
	}
}

// --- item photos ----------------------------------------------------------------

func TestSetItemImageMarksTheItemAsHavingOne(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-img", "พ่อ")
	item, err := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if item.HasImage {
		t.Error("a new item claims to have an image")
	}

	updated, err := db.SetItemImage(ctx, item.ID, u.ID, "items/"+item.ID+".jpg")
	if err != nil {
		t.Fatalf("SetItemImage: %v", err)
	}
	if !updated.HasImage {
		t.Error("HasImage is false after setting the image")
	}

	path, err := db.ItemImagePath(ctx, item.ID, u.ID)
	if err != nil {
		t.Fatalf("ItemImagePath: %v", err)
	}
	if path != "items/"+item.ID+".jpg" {
		t.Errorf("path = %q, want the item's own path", path)
	}
}

// The path is never exposed to the browser, but it must not leak to another household
// through the API either.
func TestItemImagePathRefusesANonMember(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	owner, h := household(t, db, "U-img-owner", "เจ้าของ")
	outsider := newUser(t, db, "U-img-outsider", "คนนอก")
	item, _ := db.CreateItem(ctx, h.ID, owner.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	if _, err := db.SetItemImage(ctx, item.ID, owner.ID, "items/"+item.ID+".jpg"); err != nil {
		t.Fatalf("SetItemImage: %v", err)
	}

	if _, err := db.ItemImagePath(ctx, item.ID, outsider.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
	if err := db.ItemAccessible(ctx, item.ID, outsider.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("ItemAccessible error = %v, want ErrNotFound", err)
	}
}

// An item with no photo has no path — that is how the image route knows to 404 rather
// than redirecting to an object that was never uploaded.
func TestItemImagePathIsNotFoundWithoutAPhoto(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-img-none", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})

	if _, err := db.ItemImagePath(ctx, item.ID, u.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestADeletedItemIsNoLongerAccessible(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-img-deleted", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	if err := db.DeleteItem(ctx, item.ID, u.ID); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	if err := db.ItemAccessible(ctx, item.ID, u.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// --- invites and members --------------------------------------------------------

func TestOnlyTheOwnerCanCreateAnInvite(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	now := time.Now()
	dad, h := household(t, db, "U-inv-owner", "พ่อ")
	mum := newUser(t, db, "U-inv-mum", "แม่")
	stranger := newUser(t, db, "U-inv-stranger", "คนแปลกหน้า")

	invite, err := db.CreateInvite(ctx, h.ID, dad.ID, now)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if _, err := db.AcceptInvite(ctx, invite.Token, mum.ID, now); err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}

	// A member, but not the owner.
	if _, err := db.CreateInvite(ctx, h.ID, mum.ID, now); !errors.Is(err, store.ErrNotOwner) {
		t.Errorf("member: error = %v, want ErrNotOwner", err)
	}
	// Not even a member: must not learn the household exists.
	if _, err := db.CreateInvite(ctx, h.ID, stranger.ID, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("outsider: error = %v, want ErrNotFound", err)
	}
}

func TestEachInviteGetsItsOwnToken(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	now := time.Now()
	dad, h := household(t, db, "U-inv-unique", "พ่อ")

	first, err := db.CreateInvite(ctx, h.ID, dad.ID, now)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	second, err := db.CreateInvite(ctx, h.ID, dad.ID, now)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if first.Token == second.Token {
		t.Error("two invites share a token")
	}
	// The token travels through a chat app, so guessing must not be a strategy.
	if len(first.Token) < 40 {
		t.Errorf("token is %d characters; too short to be unguessable", len(first.Token))
	}
}

func TestAcceptingAnInviteJoinsAndBecomesTheCurrentHousehold(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	now := time.Now()
	dad, h := household(t, db, "U-acc-owner", "พ่อ")
	mum := newUser(t, db, "U-acc-mum", "แม่")

	invite, err := db.CreateInvite(ctx, h.ID, dad.ID, now)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}

	joined, err := db.AcceptInvite(ctx, invite.Token, mum.ID, now)
	if err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}
	if joined.ID != h.ID {
		t.Errorf("joined %q, want %q", joined.ID, h.ID)
	}
	if joined.IsOwner {
		t.Error("the invitee is reported as owner")
	}

	member, err := db.IsMember(ctx, h.ID, mum.ID)
	if err != nil {
		t.Fatalf("IsMember: %v", err)
	}
	if !member {
		t.Error("the invitee is not a member")
	}

	reloaded, err := db.UserByID(ctx, mum.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if reloaded.LastHouseholdID != h.ID {
		t.Errorf("LastHouseholdID = %q, want %q", reloaded.LastHouseholdID, h.ID)
	}
}

func TestAcceptingTwiceIsFineButSomeoneElseIsRefused(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	now := time.Now()
	dad, h := household(t, db, "U-reuse-owner", "พ่อ")
	mum := newUser(t, db, "U-reuse-mum", "แม่")
	neighbour := newUser(t, db, "U-reuse-neighbour", "เพื่อนบ้าน")

	invite, _ := db.CreateInvite(ctx, h.ID, dad.ID, now)
	if _, err := db.AcceptInvite(ctx, invite.Token, mum.ID, now); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	if _, err := db.AcceptInvite(ctx, invite.Token, mum.ID, now); err != nil {
		t.Errorf("second accept by the same person: %v, want no error", err)
	}
	if _, err := db.AcceptInvite(ctx, invite.Token, neighbour.ID, now); !errors.Is(err, store.ErrInviteUsed) {
		t.Errorf("forwarded link: error = %v, want ErrInviteUsed", err)
	}
}

func TestAnExpiredInviteIsRefused(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	now := time.Now()
	dad, h := household(t, db, "U-exp-owner", "พ่อ")
	mum := newUser(t, db, "U-exp-mum", "แม่")

	invite, _ := db.CreateInvite(ctx, h.ID, dad.ID, now)

	// Anything past the TTL, judged against the clock the caller passes in.
	later := now.Add(store.InviteTTL + time.Minute)
	if _, err := db.AcceptInvite(ctx, invite.Token, mum.ID, later); !errors.Is(err, store.ErrInviteExpired) {
		t.Errorf("error = %v, want ErrInviteExpired", err)
	}
}

func TestAnUnknownTokenIsNotFound(t *testing.T) {
	db := open(t)
	u := newUser(t, db, "U-unknown-token", "ใครก็ไม่รู้")

	if _, err := db.AcceptInvite(context.Background(), "no-such-token", u.ID, time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// Removing someone revokes access, but the person and their history are untouched —
// "แม่ took the last one" has to still render next year.
func TestRemovingAMemberKeepsTheirHistory(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	now := time.Now()
	dad, h := household(t, db, "U-rm-owner", "พ่อ")
	mum := newUser(t, db, "U-rm-mum", "แม่")
	invite, _ := db.CreateInvite(ctx, h.ID, dad.ID, now)
	if _, err := db.AcceptInvite(ctx, invite.Token, mum.ID, now); err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}

	item, _ := db.CreateItem(ctx, h.ID, dad.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	three := 3
	if _, err := db.AdjustQuantity(ctx, item.ID, mum.ID, store.QuantityChange{Absolute: &three}); err != nil {
		t.Fatalf("AdjustQuantity as mum: %v", err)
	}

	if err := db.RemoveMember(ctx, h.ID, dad.ID, mum.ID); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}

	// She has lost access...
	if member, _ := db.IsMember(ctx, h.ID, mum.ID); member {
		t.Error("removed member is still a member")
	}
	if _, err := db.ItemsForHousehold(ctx, h.ID, mum.ID); err == nil {
		items, _ := db.ItemsForHousehold(ctx, h.ID, mum.ID)
		if len(items) != 0 {
			t.Error("removed member can still read the household's items")
		}
	}
	// ...but she still exists, and so does what she did.
	if _, err := db.UserByID(ctx, mum.ID); err != nil {
		t.Errorf("the removed user was deleted: %v", err)
	}
	movements, err := db.MovementsForItem(ctx, item.ID, dad.ID)
	if err != nil {
		t.Fatalf("MovementsForItem: %v", err)
	}
	if len(movements) != 1 || movements[0].ActorName != "แม่" {
		t.Errorf("movements = %+v, want one still attributed to แม่", movements)
	}
}

func TestTheOwnerCannotBeRemoved(t *testing.T) {
	db := open(t)
	dad, h := household(t, db, "U-rm-self", "พ่อ")

	if err := db.RemoveMember(context.Background(), h.ID, dad.ID, dad.ID); !errors.Is(err, store.ErrCannotRemoveOwner) {
		t.Errorf("error = %v, want ErrCannotRemoveOwner", err)
	}
}

func TestMembersOfListsTheOwnerFirstAndRefusesOutsiders(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	now := time.Now()
	dad, h := household(t, db, "U-mem-owner", "พ่อ")
	mum := newUser(t, db, "U-mem-mum", "แม่")
	stranger := newUser(t, db, "U-mem-stranger", "คนแปลกหน้า")
	invite, _ := db.CreateInvite(ctx, h.ID, dad.ID, now)
	if _, err := db.AcceptInvite(ctx, invite.Token, mum.ID, now); err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}

	members, err := db.MembersOf(ctx, h.ID, mum.ID)
	if err != nil {
		t.Fatalf("MembersOf: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("got %d members, want 2", len(members))
	}
	if !members[0].IsOwner || members[0].DisplayName != "พ่อ" {
		t.Errorf("first member = %+v, want the owner", members[0])
	}

	if _, err := db.MembersOf(ctx, h.ID, stranger.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("outsider: error = %v, want ErrNotFound", err)
	}
}

func TestRenameHouseholdChangesTheNameForEveryMember(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	now := time.Now()
	dad, h := household(t, db, "U-ren-owner", "พ่อ")
	mum := newUser(t, db, "U-ren-mum", "แม่")
	invite, _ := db.CreateInvite(ctx, h.ID, dad.ID, now)
	if _, err := db.AcceptInvite(ctx, invite.Token, mum.ID, now); err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}

	renamed, err := db.RenameHousehold(ctx, h.ID, dad.ID, "บ้านสวน")
	if err != nil {
		t.Fatalf("RenameHousehold: %v", err)
	}
	if renamed.Name != "บ้านสวน" {
		t.Errorf("Name = %q, want บ้านสวน", renamed.Name)
	}
	if !renamed.IsOwner {
		t.Error("the owner who renamed it is no longer marked as owner")
	}

	// The point of renaming: everyone sees it, not just the person who typed it.
	seen, err := db.HouseholdsForUser(ctx, mum.ID)
	if err != nil {
		t.Fatalf("HouseholdsForUser: %v", err)
	}
	if len(seen) != 1 || seen[0].Name != "บ้านสวน" {
		t.Errorf("member sees %+v, want one household named บ้านสวน", seen)
	}
	if seen[0].IsOwner {
		t.Error("a member is reported as owner")
	}
}

func TestRenameHouseholdRefusesAMemberWhoIsNotTheOwner(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	now := time.Now()
	dad, h := household(t, db, "U-ren2-owner", "พ่อ")
	mum := newUser(t, db, "U-ren2-mum", "แม่")
	invite, _ := db.CreateInvite(ctx, h.ID, dad.ID, now)
	if _, err := db.AcceptInvite(ctx, invite.Token, mum.ID, now); err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}

	if _, err := db.RenameHousehold(ctx, h.ID, mum.ID, "บ้านของแม่"); !errors.Is(err, store.ErrNotOwner) {
		t.Errorf("error = %v, want ErrNotOwner", err)
	}

	// And the refusal must not have written anything.
	seen, err := db.HouseholdsForUser(ctx, dad.ID)
	if err != nil {
		t.Fatalf("HouseholdsForUser: %v", err)
	}
	if seen[0].Name != h.Name {
		t.Errorf("name = %q, want it left as %q", seen[0].Name, h.Name)
	}
}

func TestRenameHouseholdHidesTheHouseholdFromAnOutsider(t *testing.T) {
	db := open(t)
	_, h := household(t, db, "U-ren3-owner", "พ่อ")
	stranger := newUser(t, db, "U-ren3-stranger", "คนแปลกหน้า")

	// ErrNotFound, not ErrNotOwner: an outsider must not learn the household exists.
	if _, err := db.RenameHousehold(context.Background(), h.ID, stranger.ID, "บ้านฉัน"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestRenameHouseholdTreatsAMalformedIDAsNotFound(t *testing.T) {
	db := open(t)
	u := newUser(t, db, "U-ren4", "พ่อ")

	if _, err := db.RenameHousehold(context.Background(), "not-a-uuid", u.ID, "บ้าน"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// --- history: an item's own, and the household's ---------------------------------

// bump applies n separate +1 changes, each its own transaction, so they land with
// distinct timestamps and the ordering under a limit is meaningful.
func bump(t *testing.T, db *store.DB, itemID, userID string, n int) {
	t.Helper()
	one := 1
	for i := 0; i < n; i++ {
		if _, err := db.AdjustQuantity(context.Background(), itemID, userID,
			store.QuantityChange{Delta: &one}); err != nil {
			t.Fatalf("AdjustQuantity %d: %v", i, err)
		}
	}
}

func TestAnItemsHistoryIsCappedAtTheNewestChanges(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-hist-cap", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})

	bump(t, db, item.ID, u.ID, store.ItemHistoryLimit+5)

	got, err := db.MovementsForItem(ctx, item.ID, u.ID)
	if err != nil {
		t.Fatalf("MovementsForItem: %v", err)
	}
	if len(got) != store.ItemHistoryLimit {
		t.Fatalf("got %d movements, want %d", len(got), store.ItemHistoryLimit)
	}
	// Newest first, and the newest is the last bump: quantity went to 15.
	if got[0].QuantityAfter != store.ItemHistoryLimit+5 {
		t.Errorf("newest quantityAfter = %d, want %d", got[0].QuantityAfter, store.ItemHistoryLimit+5)
	}
	// ...and the oldest kept is the 6th change, not the 1st.
	if got[len(got)-1].QuantityAfter != 6 {
		t.Errorf("oldest kept quantityAfter = %d, want 6", got[len(got)-1].QuantityAfter)
	}
}

func TestHouseholdHistorySpansEveryItemNewestFirst(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-hh-hist", "พ่อ")
	fish, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	rice, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "ข้าวสาร", Unit: "ถุง"})

	bump(t, db, fish.ID, u.ID, 2)
	bump(t, db, rice.ID, u.ID, 1)

	got, err := db.MovementsForHousehold(ctx, h.ID, u.ID, store.MovementFilter{Limit: store.HouseholdHistoryLimit})
	if err != nil {
		t.Fatalf("MovementsForHousehold: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d movements, want 3", len(got))
	}
	// The rice change was last, so it comes first, and it names its own item.
	if got[0].ItemName != "ข้าวสาร" || got[0].Unit != "ถุง" {
		t.Errorf("newest = %+v, want the ข้าวสาร/ถุง change", got[0])
	}
	if got[0].ActorName != "พ่อ" {
		t.Errorf("actorName = %q, want พ่อ", got[0].ActorName)
	}
	if got[1].ItemName != "น้ำปลา" {
		t.Errorf("second = %+v, want a น้ำปลา change", got[1])
	}
}

func TestHouseholdHistoryIsCappedByTheLimit(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-hh-hist-cap", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})

	bump(t, db, item.ID, u.ID, 5)

	got, err := db.MovementsForHousehold(ctx, h.ID, u.ID, store.MovementFilter{Limit: 3})
	if err != nil {
		t.Fatalf("MovementsForHousehold: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d movements, want 3", len(got))
	}
	if got[0].QuantityAfter != 5 {
		t.Errorf("newest quantityAfter = %d, want 5", got[0].QuantityAfter)
	}
}

func TestHouseholdHistoryCanBeNarrowedToOneItem(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-hh-filter", "พ่อ")
	fish, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	rice, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "ข้าวสาร", Unit: "ถุง"})
	bump(t, db, fish.ID, u.ID, 2)
	bump(t, db, rice.ID, u.ID, 3)

	got, err := db.MovementsForHousehold(ctx, h.ID, u.ID,
		store.MovementFilter{ItemID: fish.ID, Limit: store.HouseholdHistoryLimit})
	if err != nil {
		t.Fatalf("MovementsForHousehold: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d movements, want the 2 น้ำปลา ones", len(got))
	}
	for _, m := range got {
		if m.ItemName != "น้ำปลา" {
			t.Errorf("leaked a change for %q", m.ItemName)
		}
	}
}

func TestHouseholdHistorySearchesItemNamesAndNotes(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-hh-search", "พ่อ")
	fish, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	rice, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "ข้าวสาร", Unit: "ถุง"})

	three := 3
	if _, err := db.AdjustQuantity(ctx, rice.ID, u.ID,
		store.QuantityChange{Absolute: &three, Note: "ซื้อมาใหม่จากตลาด"}); err != nil {
		t.Fatalf("AdjustQuantity: %v", err)
	}
	bump(t, db, fish.ID, u.ID, 1)

	// By item name.
	byName, err := db.MovementsForHousehold(ctx, h.ID, u.ID,
		store.MovementFilter{Query: "น้ำปลา", Limit: store.HouseholdHistoryLimit})
	if err != nil {
		t.Fatalf("search by name: %v", err)
	}
	if len(byName) != 1 || byName[0].ItemName != "น้ำปลา" {
		t.Errorf("search by name got %d rows: %+v", len(byName), byName)
	}

	// By something only the note says — the item name does not contain it.
	byNote, err := db.MovementsForHousehold(ctx, h.ID, u.ID,
		store.MovementFilter{Query: "ตลาด", Limit: store.HouseholdHistoryLimit})
	if err != nil {
		t.Fatalf("search by note: %v", err)
	}
	if len(byNote) != 1 || byNote[0].ItemName != "ข้าวสาร" {
		t.Errorf("search by note got %d rows: %+v", len(byNote), byNote)
	}
}

func TestHouseholdHistorySearchIgnoresLatinCase(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-hh-case", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "Milo", Unit: "กล่อง"})
	bump(t, db, item.ID, u.ID, 1)

	got, err := db.MovementsForHousehold(ctx, h.ID, u.ID,
		store.MovementFilter{Query: "milo", Limit: store.HouseholdHistoryLimit})
	if err != nil {
		t.Fatalf("MovementsForHousehold: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d rows for a lowercase query, want 1", len(got))
	}
}

// The search is a substring match, not a pattern. If it were built into a LIKE pattern,
// a '%' typed by the user would match every row instead of none.
func TestHouseholdHistorySearchTreatsWildcardsAsPlainText(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-hh-wild", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	bump(t, db, item.ID, u.ID, 2)

	for _, q := range []string{"%", "_", "%ปลา%"} {
		got, err := db.MovementsForHousehold(ctx, h.ID, u.ID,
			store.MovementFilter{Query: q, Limit: store.HouseholdHistoryLimit})
		if err != nil {
			t.Fatalf("query %q: %v", q, err)
		}
		if len(got) != 0 {
			t.Errorf("query %q matched %d rows, want 0 — it is being treated as a pattern", q, len(got))
		}
	}
}

func TestHouseholdHistoryKeepsADeletedItemsChanges(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-hh-deleted", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	bump(t, db, item.ID, u.ID, 2)

	if err := db.DeleteItem(ctx, item.ID, u.ID); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	// ADR-0001: the history is the reason an item is soft-deleted rather than removed.
	got, err := db.MovementsForHousehold(ctx, h.ID, u.ID, store.MovementFilter{Limit: store.HouseholdHistoryLimit})
	if err != nil {
		t.Fatalf("MovementsForHousehold: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d movements for a deleted item, want 2", len(got))
	}
}

func TestHouseholdHistoryRefusesAnOutsider(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-hh-out-owner", "พ่อ")
	stranger := newUser(t, db, "U-hh-out-stranger", "คนแปลกหน้า")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	bump(t, db, item.ID, u.ID, 1)

	if _, err := db.MovementsForHousehold(ctx, h.ID, stranger.ID,
		store.MovementFilter{Limit: store.HouseholdHistoryLimit}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
	if _, err := db.ItemsWithHistory(ctx, h.ID, stranger.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("ItemsWithHistory: error = %v, want ErrNotFound", err)
	}
}

// The filter dropdown is built from this, so it must offer exactly the items that have
// something to show — including one that has since been deleted.
func TestItemsWithHistoryListsOnlyItemsThatHaveChanged(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-hh-refs", "พ่อ")
	changed, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "ข้าวสาร", Unit: "ถุง"})
	untouched, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	gone, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "สบู่", Unit: "ก้อน"})
	bump(t, db, changed.ID, u.ID, 1)
	bump(t, db, gone.ID, u.ID, 1)
	if err := db.DeleteItem(ctx, gone.ID, u.ID); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	got, err := db.ItemsWithHistory(ctx, h.ID, u.ID)
	if err != nil {
		t.Fatalf("ItemsWithHistory: %v", err)
	}
	names := []string{}
	for _, r := range got {
		names = append(names, r.Name)
	}
	// Sorted by name: ข้าวสาร then สบู่. The untouched item is absent.
	if len(names) != 2 || names[0] != "ข้าวสาร" || names[1] != "สบู่" {
		t.Errorf("items with history = %v, want [ข้าวสาร สบู่]", names)
	}
	for _, r := range got {
		if r.ID == untouched.ID {
			t.Error("an item with no history was offered as a filter")
		}
	}
}

// --- icons ------------------------------------------------------------------------

func TestAnItemCanBeCreatedWithAnIcon(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-icon-create", "พ่อ")

	item, err := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด", Icon: "🧴"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if item.Icon != "🧴" {
		t.Errorf("Icon = %q, want 🧴", item.Icon)
	}

	// ...and it survives the read path the list uses.
	items, err := db.ItemsForHousehold(ctx, h.ID, u.ID)
	if err != nil {
		t.Fatalf("ItemsForHousehold: %v", err)
	}
	if len(items) != 1 || items[0].Icon != "🧴" {
		t.Errorf("listed item = %+v, want icon 🧴", items)
	}
}

func TestAnItemWithoutAnIconHasNone(t *testing.T) {
	db := open(t)
	u, h := household(t, db, "U-icon-none", "พ่อ")

	item, err := db.CreateItem(context.Background(), h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if item.Icon != "" {
		t.Errorf("Icon = %q, want empty", item.Icon)
	}
}

func TestEditingCanSetAndClearAnIcon(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-icon-edit", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})

	set, err := db.UpdateItem(ctx, item.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด", Icon: "🍚"})
	if err != nil {
		t.Fatalf("UpdateItem set: %v", err)
	}
	if set.Icon != "🍚" {
		t.Errorf("Icon = %q, want 🍚", set.Icon)
	}

	// An empty icon is how one is removed, not "leave it alone".
	cleared, err := db.UpdateItem(ctx, item.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด"})
	if err != nil {
		t.Fatalf("UpdateItem clear: %v", err)
	}
	if cleared.Icon != "" {
		t.Errorf("Icon = %q after clearing, want empty", cleared.Icon)
	}
}

// Every read path returns the whole item, so the icon has to come back from the ones that
// are not obviously "reads" too — otherwise the row loses its picture the moment somebody
// taps plus.
func TestAnIconSurvivesAQuantityChangeAndAPhotoUpload(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	u, h := household(t, db, "U-icon-paths", "พ่อ")
	item, _ := db.CreateItem(ctx, h.ID, u.ID, store.ItemInput{Name: "น้ำปลา", Unit: "ขวด", Icon: "🧴"})

	one := 1
	adjusted, err := db.AdjustQuantity(ctx, item.ID, u.ID, store.QuantityChange{Delta: &one})
	if err != nil {
		t.Fatalf("AdjustQuantity: %v", err)
	}
	if adjusted.Icon != "🧴" {
		t.Errorf("after a quantity change Icon = %q, want 🧴", adjusted.Icon)
	}

	// A no-op change takes a different branch, and returns the item too.
	zero := 1
	noop, err := db.AdjustQuantity(ctx, item.ID, u.ID, store.QuantityChange{Absolute: &zero})
	if err != nil {
		t.Fatalf("AdjustQuantity no-op: %v", err)
	}
	if noop.Icon != "🧴" {
		t.Errorf("after a no-op change Icon = %q, want 🧴", noop.Icon)
	}

	withPhoto, err := db.SetItemImage(ctx, item.ID, u.ID, "items/"+item.ID+"/photo.jpg")
	if err != nil {
		t.Fatalf("SetItemImage: %v", err)
	}
	if withPhoto.Icon != "🧴" || !withPhoto.HasImage {
		t.Errorf("after a photo upload = %+v, want the icon kept and hasImage true", withPhoto)
	}
}

func TestAMultiCodePointIconRoundTrips(t *testing.T) {
	db := open(t)
	u, h := household(t, db, "U-icon-zwj", "พ่อ")

	// A joined emoji is several code points; storing it must not mangle it.
	item, err := db.CreateItem(context.Background(), h.ID, u.ID,
		store.ItemInput{Name: "ของใช้", Unit: "ชิ้น", Icon: "🧑‍🍳"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if item.Icon != "🧑‍🍳" {
		t.Errorf("Icon = %q, want the joined emoji back unchanged", item.Icon)
	}
}
