// Package store is the database. It holds queries and persistence only; decisions
// about what should happen belong above it.
package store

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/supavitW02/tonyTheHelper/api/internal/line"
)

//go:embed migrations/*.sql
var migrations embed.FS

var (
	// ErrNotFound is returned when a row that was asked for by id does not exist, and
	// also when the caller may not see it — whether a thing exists is not something an
	// outsider gets to learn by probing.
	ErrNotFound = errors.New("store: not found")
	// ErrNotOwner is for actions only a household's owner may take. Distinct from
	// ErrNotFound because the caller is a member and already knows the household exists.
	ErrNotOwner = errors.New("store: not the household owner")
	// ErrInviteExpired and ErrInviteUsed are separated so the UI can say which it was;
	// "try again" helps for neither, but the two mean different things to a person.
	ErrInviteExpired = errors.New("store: invite has expired")
	ErrInviteUsed    = errors.New("store: invite has already been used")
	// ErrCannotRemoveOwner guards the one removal that would orphan a household.
	ErrCannotRemoveOwner = errors.New("store: the owner cannot be removed")
)

// User is a person. Names and pictures are refreshed from LINE on each sign-in, so
// they are a cache of LINE's truth rather than something this app owns.
type User struct {
	ID          string    `json:"id"`
	LineUserID  string    `json:"-"` // never sent to the browser
	DisplayName string    `json:"displayName"`
	PictureURL  string    `json:"pictureUrl,omitempty"`
	CreatedAt   time.Time `json:"-"`

	// LastHouseholdID is the household this user last opened. Empty when they belong
	// to none yet, which is what puts a new user on the create-household screen.
	LastHouseholdID string `json:"lastHouseholdId,omitempty"`
}

// Household is a group of people who share one stock of items.
type Household struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	OwnerID string `json:"-"`
	// IsOwner is relative to the user who asked. Only the owner manages members.
	IsOwner bool `json:"isOwner"`
}

type DB struct {
	pool *pgxpool.Pool
}

// Open connects and verifies the connection, so a bad DATABASE_URL fails at startup
// rather than on someone's first sign-in.
func Open(ctx context.Context, databaseURL string) (*DB, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &DB{pool: pool}, nil
}

func (db *DB) Close() { db.pool.Close() }

// Migrate brings the schema up to date. Migrations are embedded in the binary, so the
// running image and the schema it expects can never drift apart.
func (db *DB) Migrate(ctx context.Context) error {
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("store: goose dialect: %w", err)
	}

	sqlDB := stdlib.OpenDBFromPool(db.pool)
	defer sqlDB.Close()

	if err := goose.UpContext(ctx, sqlDB, "migrations"); err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	return nil
}

const upsertUser = `
insert into users (line_user_id, display_name, picture_url)
values ($1, $2, nullif($3, ''))
on conflict (line_user_id) do update
    set display_name = excluded.display_name,
        picture_url  = excluded.picture_url,
        updated_at   = now()
returning id, line_user_id, display_name, coalesce(picture_url, ''), created_at,
          coalesce(last_household_id::text, '')`

// UpsertUser records the person behind a verified LINE identity, creating them on
// first sign-in and refreshing their name and picture on every one after.
func (db *DB) UpsertUser(ctx context.Context, identity line.Identity) (User, error) {
	row := db.pool.QueryRow(ctx, upsertUser, identity.UserID, identity.DisplayName, identity.PictureURL)

	var u User
	if err := row.Scan(&u.ID, &u.LineUserID, &u.DisplayName, &u.PictureURL, &u.CreatedAt, &u.LastHouseholdID); err != nil {
		return User{}, fmt.Errorf("store: upsert user: %w", err)
	}
	return u, nil
}

const selectUser = `
select id, line_user_id, display_name, coalesce(picture_url, ''), created_at,
       coalesce(last_household_id::text, '')
from users
where id = $1`

// UserByID looks a user up by our own id, which is what a session carries.
func (db *DB) UserByID(ctx context.Context, id string) (User, error) {
	var u User
	err := db.pool.QueryRow(ctx, selectUser, id).
		Scan(&u.ID, &u.LineUserID, &u.DisplayName, &u.PictureURL, &u.CreatedAt, &u.LastHouseholdID)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return User{}, ErrNotFound
	case err != nil:
		// An invalid uuid is a malformed session, not a server fault.
		if isInvalidUUID(err) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("store: user by id: %w", err)
	}
	return u, nil
}

// isInvalidUUID reports whether the error is Postgres refusing a malformed uuid
// literal (SQLSTATE 22P02), which happens when a session carries a bogus subject.
func isInvalidUUID(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "22P02"
}

const insertHousehold = `
insert into households (name, owner_id) values ($1, $2)
returning id, name, owner_id`

const insertMembership = `
insert into household_members (household_id, user_id) values ($1, $2)`

const setLastHousehold = `
update users set last_household_id = $2, updated_at = now() where id = $1`

// CreateHousehold creates a household, makes its creator the owner and first member, and
// makes it the user's current one.
//
// All three happen in one transaction: a household with no members would be invisible to
// the person who just created it, and there is no path in the app to recover from that.
func (db *DB) CreateHousehold(ctx context.Context, ownerID, name string) (Household, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return Household{}, fmt.Errorf("store: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	var h Household
	if err := tx.QueryRow(ctx, insertHousehold, name, ownerID).Scan(&h.ID, &h.Name, &h.OwnerID); err != nil {
		return Household{}, fmt.Errorf("store: insert household: %w", err)
	}
	if _, err := tx.Exec(ctx, insertMembership, h.ID, ownerID); err != nil {
		return Household{}, fmt.Errorf("store: insert membership: %w", err)
	}
	if _, err := tx.Exec(ctx, setLastHousehold, ownerID, h.ID); err != nil {
		return Household{}, fmt.Errorf("store: set last household: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Household{}, fmt.Errorf("store: commit: %w", err)
	}

	h.IsOwner = true
	return h, nil
}

const selectHouseholdsForUser = `
select h.id, h.name, h.owner_id
from households h
join household_members m on m.household_id = h.id
where m.user_id = $1
order by h.created_at`

// HouseholdsForUser lists the households a user belongs to, oldest first. IsOwner is
// resolved against that same user.
func (db *DB) HouseholdsForUser(ctx context.Context, userID string) ([]Household, error) {
	rows, err := db.pool.Query(ctx, selectHouseholdsForUser, userID)
	if err != nil {
		if isInvalidUUID(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: households for user: %w", err)
	}
	defer rows.Close()

	households := []Household{}
	for rows.Next() {
		var h Household
		if err := rows.Scan(&h.ID, &h.Name, &h.OwnerID); err != nil {
			return nil, fmt.Errorf("store: scan household: %w", err)
		}
		h.IsOwner = h.OwnerID == userID
		households = append(households, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: households for user: %w", err)
	}
	return households, nil
}

const selectMembership = `
select 1 from household_members where household_id = $1 and user_id = $2`

// IsMember reports whether a user belongs to a household. Every household-scoped request
// goes through this: a household id in a URL is a claim, not proof.
func (db *DB) IsMember(ctx context.Context, householdID, userID string) (bool, error) {
	var one int
	err := db.pool.QueryRow(ctx, selectMembership, householdID, userID).Scan(&one)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		if isInvalidUUID(err) {
			return false, nil
		}
		return false, fmt.Errorf("store: is member: %w", err)
	}
	return true, nil
}

// SetLastHousehold records the household a user is currently looking at, so the next open
// lands there. Refuses a household the user does not belong to.
func (db *DB) SetLastHousehold(ctx context.Context, userID, householdID string) error {
	member, err := db.IsMember(ctx, householdID, userID)
	if err != nil {
		return err
	}
	if !member {
		return ErrNotFound
	}
	if _, err := db.pool.Exec(ctx, setLastHousehold, userID, householdID); err != nil {
		return fmt.Errorf("store: set last household: %w", err)
	}
	return nil
}

// Item is a kind of thing a household keeps in stock, not a single physical object.
type Item struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Unit        string `json:"unit"`
	Description string `json:"description,omitempty"`
	Quantity    int    `json:"quantity"`

	// LowStockThreshold is optional. Absent means the item is never low: most things a
	// household keeps are not worth tracking that closely.
	LowStockThreshold *int `json:"lowStockThreshold,omitempty"`
	// IsLow is derived in SQL, both to sort low items to the top and so the UI never
	// has to repeat the comparison and risk disagreeing with the ordering.
	IsLow bool `json:"isLow"`
	// HasImage rather than the path itself: where an item's photo lives in object
	// storage is not the browser's business, and the URL is minted per request anyway.
	HasImage bool `json:"hasImage"`
}

// ItemInput is the writable half of an item, shared by create and update.
type ItemInput struct {
	Name              string
	Unit              string
	Description       string
	LowStockThreshold *int
}

// Movement is one recorded change to an item's quantity, with the person who made it.
type Movement struct {
	ID              string    `json:"id"`
	ActorName       string    `json:"actorName"`
	ActorPictureURL string    `json:"actorPictureUrl,omitempty"`
	QuantityBefore  int       `json:"quantityBefore"`
	QuantityAfter   int       `json:"quantityAfter"`
	Note            string    `json:"note,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}

// itemColumns is the projection every item read shares, including the derived low flag.
const itemColumns = `i.id, i.name, i.unit, coalesce(i.description, ''), i.quantity,
	i.low_stock_threshold,
	(i.low_stock_threshold is not null and i.quantity <= i.low_stock_threshold) as is_low,
	(i.image_path is not null) as has_image`

const insertItem = `
insert into items (household_id, name, unit, description, low_stock_threshold, created_by)
select $1, $2, $3, nullif($4, ''), $5, $6
where exists (select 1 from household_members where household_id = $1 and user_id = $6)
returning id, name, unit, coalesce(description, ''), quantity, low_stock_threshold,
          (low_stock_threshold is not null and quantity <= low_stock_threshold) as is_low,
          (image_path is not null) as has_image`

// CreateItem adds an item to a household.
//
// Membership is part of the insert rather than a separate check: a household id arriving
// from a client is a claim, and doing it in one statement leaves no window between the
// check and the write. A non-member gets ErrNotFound, not a permission error — whether
// the household exists is none of their business.
func (db *DB) CreateItem(ctx context.Context, householdID, userID string, in ItemInput) (Item, error) {
	var i Item
	err := db.pool.QueryRow(ctx, insertItem,
		householdID, in.Name, in.Unit, in.Description, in.LowStockThreshold, userID).
		Scan(&i.ID, &i.Name, &i.Unit, &i.Description, &i.Quantity, &i.LowStockThreshold, &i.IsLow, &i.HasImage)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Item{}, ErrNotFound
	case err != nil:
		if isInvalidUUID(err) {
			return Item{}, ErrNotFound
		}
		return Item{}, fmt.Errorf("store: create item: %w", err)
	}
	return i, nil
}

const selectItems = `
select ` + itemColumns + `
from items i
join household_members m on m.household_id = i.household_id and m.user_id = $2
where i.household_id = $1 and i.deleted_at is null
-- Low items first so they are noticed before they run out, then alphabetically.
order by is_low desc, i.name`

// ItemsForHousehold lists a household's items for one of its members.
func (db *DB) ItemsForHousehold(ctx context.Context, householdID, userID string) ([]Item, error) {
	rows, err := db.pool.Query(ctx, selectItems, householdID, userID)
	if err != nil {
		if isInvalidUUID(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: items for household: %w", err)
	}
	defer rows.Close()

	items := []Item{}
	for rows.Next() {
		var i Item
		if err := rows.Scan(&i.ID, &i.Name, &i.Unit, &i.Description, &i.Quantity,
			&i.LowStockThreshold, &i.IsLow, &i.HasImage); err != nil {
			return nil, fmt.Errorf("store: scan item: %w", err)
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: items for household: %w", err)
	}
	return items, nil
}

const selectUnits = `
select distinct i.unit
from items i
join household_members m on m.household_id = i.household_id and m.user_id = $2
where i.household_id = $1 and i.deleted_at is null
order by i.unit`

// UnitsForHousehold returns the units this household already uses, which the UI offers as
// tappable chips. Units stay free text; this only nudges people towards reusing a spelling
// rather than inventing a second one for the same thing.
func (db *DB) UnitsForHousehold(ctx context.Context, householdID, userID string) ([]string, error) {
	rows, err := db.pool.Query(ctx, selectUnits, householdID, userID)
	if err != nil {
		if isInvalidUUID(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: units for household: %w", err)
	}
	defer rows.Close()

	units := []string{}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, fmt.Errorf("store: scan unit: %w", err)
		}
		units = append(units, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: units for household: %w", err)
	}
	return units, nil
}

// QuantityChange describes a requested change. Exactly one of Delta and Absolute is set:
// Delta for the plus/minus buttons, Absolute for setting a total after shopping.
type QuantityChange struct {
	Delta    *int
	Absolute *int
	Note     string
}

const lockItemForUpdate = `
select i.quantity, i.name, i.unit, coalesce(i.description, ''), i.low_stock_threshold,
       (i.image_path is not null) as has_image
from items i
join household_members m on m.household_id = i.household_id and m.user_id = $2
where i.id = $1 and i.deleted_at is null
for update of i`

const updateItemQuantity = `
update items set quantity = $2, updated_at = now() where id = $1`

const insertMovement = `
insert into stock_movements (item_id, user_id, quantity_before, quantity_after, note)
values ($1, $2, $3, $4, nullif($5, ''))`

// AdjustQuantity is the only way an item's quantity ever changes.
//
// Per ADR-0001 the column is authoritative and the movement log is append-only, and both
// are written in one transaction — if a caller could update the column directly the two
// would drift and the history would silently start lying.
//
// The row is locked for the duration so two people tapping minus at the same moment cannot
// both read the same starting quantity.
func (db *DB) AdjustQuantity(ctx context.Context, itemID, userID string, change QuantityChange) (Item, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return Item{}, fmt.Errorf("store: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	var before int
	item := Item{ID: itemID}
	err = tx.QueryRow(ctx, lockItemForUpdate, itemID, userID).
		Scan(&before, &item.Name, &item.Unit, &item.Description, &item.LowStockThreshold, &item.HasImage)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Item{}, ErrNotFound
	case err != nil:
		if isInvalidUUID(err) {
			return Item{}, ErrNotFound
		}
		return Item{}, fmt.Errorf("store: lock item: %w", err)
	}

	after := targetQuantity(before, change)

	// Nothing changed — minus at zero, or setting the total to what it already is. There
	// is no movement to record, because nothing moved.
	if after == before {
		item.Quantity = before
		item.IsLow = isLow(item.LowStockThreshold, before)
		return item, tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, updateItemQuantity, itemID, after); err != nil {
		return Item{}, fmt.Errorf("store: update quantity: %w", err)
	}
	if _, err := tx.Exec(ctx, insertMovement, itemID, userID, before, after, change.Note); err != nil {
		return Item{}, fmt.Errorf("store: insert movement: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Item{}, fmt.Errorf("store: commit: %w", err)
	}

	item.Quantity = after
	item.IsLow = isLow(item.LowStockThreshold, after)
	return item, nil
}

// isLow mirrors the SQL expression in itemColumns. The two must agree, or the list
// ordering and the badge would disagree with each other.
func isLow(threshold *int, quantity int) bool {
	return threshold != nil && quantity <= *threshold
}

// targetQuantity resolves a requested change to a final quantity, floored at zero so that
// tapping minus at zero is a no-op rather than an error the user has to understand.
func targetQuantity(before int, change QuantityChange) int {
	after := before
	switch {
	case change.Absolute != nil:
		after = *change.Absolute
	case change.Delta != nil:
		after = before + *change.Delta
	}
	return max(after, 0)
}

const updateItemFields = `
update items i
set name = $3, unit = $4, description = nullif($5, ''), low_stock_threshold = $6, updated_at = now()
where i.id = $1
  and i.deleted_at is null
  and exists (select 1 from household_members m where m.household_id = i.household_id and m.user_id = $2)
returning i.id, i.name, i.unit, coalesce(i.description, ''), i.quantity, i.low_stock_threshold,
          (i.low_stock_threshold is not null and i.quantity <= i.low_stock_threshold) as is_low,
          (i.image_path is not null) as has_image`

// UpdateItem changes an item's editable fields. It deliberately cannot touch quantity:
// that only ever moves through AdjustQuantity, so every change is recorded (ADR-0001).
func (db *DB) UpdateItem(ctx context.Context, itemID, userID string, in ItemInput) (Item, error) {
	var i Item
	err := db.pool.QueryRow(ctx, updateItemFields,
		itemID, userID, in.Name, in.Unit, in.Description, in.LowStockThreshold).
		Scan(&i.ID, &i.Name, &i.Unit, &i.Description, &i.Quantity, &i.LowStockThreshold, &i.IsLow, &i.HasImage)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Item{}, ErrNotFound
	case err != nil:
		if isInvalidUUID(err) {
			return Item{}, ErrNotFound
		}
		return Item{}, fmt.Errorf("store: update item: %w", err)
	}
	return i, nil
}

const softDeleteItem = `
update items i
set deleted_at = now()
where i.id = $1
  and i.deleted_at is null
  and exists (select 1 from household_members m where m.household_id = i.household_id and m.user_id = $2)
returning i.id`

// DeleteItem marks an item deleted without removing the row.
//
// Its movements are append-only and must stay readable: a hard delete would either
// orphan them or destroy the answer to "who used the last one".
func (db *DB) DeleteItem(ctx context.Context, itemID, userID string) error {
	var id string
	err := db.pool.QueryRow(ctx, softDeleteItem, itemID, userID).Scan(&id)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case err != nil:
		if isInvalidUUID(err) {
			return ErrNotFound
		}
		return fmt.Errorf("store: delete item: %w", err)
	}
	return nil
}

const selectMovements = `
select sm.id, u.display_name, coalesce(u.picture_url, ''),
       sm.quantity_before, sm.quantity_after, coalesce(sm.note, ''), sm.created_at
from stock_movements sm
join items i on i.id = sm.item_id
join users u on u.id = sm.user_id
join household_members m on m.household_id = i.household_id and m.user_id = $2
where sm.item_id = $1
order by sm.created_at desc`

// MovementsForItem returns an item's history, newest first.
//
// Deleted items are deliberately not excluded: the history is the reason the item is
// soft-deleted rather than removed, so it stays readable afterwards.
func (db *DB) MovementsForItem(ctx context.Context, itemID, userID string) ([]Movement, error) {
	rows, err := db.pool.Query(ctx, selectMovements, itemID, userID)
	if err != nil {
		if isInvalidUUID(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: movements for item: %w", err)
	}
	defer rows.Close()

	movements := []Movement{}
	for rows.Next() {
		var m Movement
		if err := rows.Scan(&m.ID, &m.ActorName, &m.ActorPictureURL,
			&m.QuantityBefore, &m.QuantityAfter, &m.Note, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan movement: %w", err)
		}
		movements = append(movements, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: movements for item: %w", err)
	}
	return movements, nil
}

const setImagePath = `
update items i
set image_path = $3, updated_at = now()
where i.id = $1
  and i.deleted_at is null
  and exists (select 1 from household_members m where m.household_id = i.household_id and m.user_id = $2)
returning ` + itemColumns

// SetItemImage records where an item's photo lives, after the browser has uploaded it.
func (db *DB) SetItemImage(ctx context.Context, itemID, userID, path string) (Item, error) {
	var i Item
	err := db.pool.QueryRow(ctx, setImagePath, itemID, userID, path).
		Scan(&i.ID, &i.Name, &i.Unit, &i.Description, &i.Quantity, &i.LowStockThreshold, &i.IsLow, &i.HasImage)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Item{}, ErrNotFound
	case err != nil:
		if isInvalidUUID(err) {
			return Item{}, ErrNotFound
		}
		return Item{}, fmt.Errorf("store: set item image: %w", err)
	}
	return i, nil
}

const selectImagePath = `
select i.image_path
from items i
join household_members m on m.household_id = i.household_id and m.user_id = $2
where i.id = $1 and i.deleted_at is null and i.image_path is not null`

// ItemImagePath returns where an item's photo lives, for a member of its household.
func (db *DB) ItemImagePath(ctx context.Context, itemID, userID string) (string, error) {
	var path string
	err := db.pool.QueryRow(ctx, selectImagePath, itemID, userID).Scan(&path)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", ErrNotFound
	case err != nil:
		if isInvalidUUID(err) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("store: item image path: %w", err)
	}
	return path, nil
}

const selectItemAccess = `
select 1
from items i
join household_members m on m.household_id = i.household_id and m.user_id = $2
where i.id = $1 and i.deleted_at is null`

// ItemAccessible reports whether a user may act on an item, without reading it.
//
// Needed before minting an upload URL: a signed URL is a capability, so handing one out
// for an item id the caller cannot touch would let them overwrite another household's photo.
func (db *DB) ItemAccessible(ctx context.Context, itemID, userID string) error {
	var one int
	err := db.pool.QueryRow(ctx, selectItemAccess, itemID, userID).Scan(&one)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case err != nil:
		if isInvalidUUID(err) {
			return ErrNotFound
		}
		return fmt.Errorf("store: item accessible: %w", err)
	}
	return nil
}

// InviteTTL is how long an invite stays usable. Long enough that "I'll do it tonight"
// works, short enough that a link forwarded months later does not still let someone in.
const InviteTTL = 7 * 24 * time.Hour

// Invite is a one-person, time-limited grant of membership.
type Invite struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Member is a person in a household, as the members screen shows them.
type Member struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	PictureURL  string `json:"pictureUrl,omitempty"`
	IsOwner     bool   `json:"isOwner"`
}

const selectOwnership = `
select h.owner_id = $2 as is_owner
from households h
join household_members m on m.household_id = h.id and m.user_id = $2
where h.id = $1`

// requireOwner returns ErrNotFound for a non-member and ErrNotOwner for a member who is
// not the owner, so the two cases stay distinguishable to the caller.
func (db *DB) requireOwner(ctx context.Context, householdID, userID string) error {
	var isOwner bool
	err := db.pool.QueryRow(ctx, selectOwnership, householdID, userID).Scan(&isOwner)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case err != nil:
		if isInvalidUUID(err) {
			return ErrNotFound
		}
		return fmt.Errorf("store: ownership: %w", err)
	case !isOwner:
		return ErrNotOwner
	}
	return nil
}

const insertInvite = `
insert into invites (household_id, created_by, token, expires_at)
values ($1, $2, $3, $4)
returning token, expires_at`

// CreateInvite mints an invite for a household. Owner only: ownership is about people.
func (db *DB) CreateInvite(ctx context.Context, householdID, ownerID string, now time.Time) (Invite, error) {
	if err := db.requireOwner(ctx, householdID, ownerID); err != nil {
		return Invite{}, err
	}

	token, err := newToken()
	if err != nil {
		return Invite{}, err
	}

	var invite Invite
	if err := db.pool.QueryRow(ctx, insertInvite, householdID, ownerID, token, now.Add(InviteTTL)).
		Scan(&invite.Token, &invite.ExpiresAt); err != nil {
		return Invite{}, fmt.Errorf("store: create invite: %w", err)
	}
	return invite, nil
}

// newToken produces an opaque, unguessable invite token. 32 bytes so that guessing is not
// a strategy even though the token travels through a chat app.
func newToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("store: generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

const lockInvite = `
select household_id, expires_at, consumed_by
from invites
where token = $1
for update`

const consumeInvite = `
update invites set consumed_at = $2, consumed_by = $3
where token = $1 and consumed_by is null`

const insertMemberIfAbsent = `
insert into household_members (household_id, user_id) values ($1, $2)
on conflict (household_id, user_id) do nothing`

const selectHouseholdByID = `
select h.id, h.name, h.owner_id from households h where h.id = $1`

// AcceptInvite joins the caller to the invite's household and makes it their current one.
//
// Re-opening an invite you have already used is not an error: someone who taps the same
// LINE message twice should simply land in the household, not read a failure message.
func (db *DB) AcceptInvite(ctx context.Context, token, userID string, now time.Time) (Household, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return Household{}, fmt.Errorf("store: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	var householdID string
	var expiresAt time.Time
	var consumedBy *string

	err = tx.QueryRow(ctx, lockInvite, token).Scan(&householdID, &expiresAt, &consumedBy)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Household{}, ErrNotFound
	case err != nil:
		return Household{}, fmt.Errorf("store: load invite: %w", err)
	}

	if !expiresAt.After(now) {
		return Household{}, ErrInviteExpired
	}

	alreadyMember, err := db.isMemberTx(ctx, tx, householdID, userID)
	if err != nil {
		return Household{}, err
	}

	// Used by someone else, and the caller is not in the household: an invite grants
	// membership to one person, not to everyone the link is forwarded to.
	if !alreadyMember && consumedBy != nil && *consumedBy != userID {
		return Household{}, ErrInviteUsed
	}

	if !alreadyMember {
		if _, err := tx.Exec(ctx, insertMemberIfAbsent, householdID, userID); err != nil {
			return Household{}, fmt.Errorf("store: add member: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, consumeInvite, token, now, userID); err != nil {
		return Household{}, fmt.Errorf("store: consume invite: %w", err)
	}
	if _, err := tx.Exec(ctx, setLastHousehold, userID, householdID); err != nil {
		return Household{}, fmt.Errorf("store: set last household: %w", err)
	}

	var h Household
	if err := tx.QueryRow(ctx, selectHouseholdByID, householdID).Scan(&h.ID, &h.Name, &h.OwnerID); err != nil {
		return Household{}, fmt.Errorf("store: load household: %w", err)
	}
	h.IsOwner = h.OwnerID == userID

	if err := tx.Commit(ctx); err != nil {
		return Household{}, fmt.Errorf("store: commit: %w", err)
	}
	return h, nil
}

func (db *DB) isMemberTx(ctx context.Context, tx pgx.Tx, householdID, userID string) (bool, error) {
	var one int
	err := tx.QueryRow(ctx, selectMembership, householdID, userID).Scan(&one)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("store: is member: %w", err)
	}
	return true, nil
}

const selectMembers = `
select u.id, u.display_name, coalesce(u.picture_url, ''), (h.owner_id = u.id) as is_owner
from household_members m
join users u on u.id = m.user_id
join households h on h.id = m.household_id
where m.household_id = $1
order by is_owner desc, u.display_name`

// MembersOf lists a household's members for any of its members. The owner comes first.
func (db *DB) MembersOf(ctx context.Context, householdID, userID string) ([]Member, error) {
	member, err := db.IsMember(ctx, householdID, userID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrNotFound
	}

	rows, err := db.pool.Query(ctx, selectMembers, householdID)
	if err != nil {
		return nil, fmt.Errorf("store: members: %w", err)
	}
	defer rows.Close()

	members := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.DisplayName, &m.PictureURL, &m.IsOwner); err != nil {
			return nil, fmt.Errorf("store: scan member: %w", err)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: members: %w", err)
	}
	return members, nil
}

const deleteMember = `
delete from household_members where household_id = $1 and user_id = $2`

// RemoveMember revokes someone's access to a household.
//
// The user row and their movements are left alone: "แม่ took the last one" must still
// render next year, and history is append-only (ADR-0001).
func (db *DB) RemoveMember(ctx context.Context, householdID, ownerID, targetUserID string) error {
	if err := db.requireOwner(ctx, householdID, ownerID); err != nil {
		return err
	}
	// Removing the owner would leave a household nobody can manage.
	if targetUserID == ownerID {
		return ErrCannotRemoveOwner
	}

	tag, err := db.pool.Exec(ctx, deleteMember, householdID, targetUserID)
	if err != nil {
		if isInvalidUUID(err) {
			return ErrNotFound
		}
		return fmt.Errorf("store: remove member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
