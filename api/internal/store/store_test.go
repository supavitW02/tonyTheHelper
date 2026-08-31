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
		"truncate stock_movements, items, household_members, households, users restart identity cascade"); err != nil {
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
