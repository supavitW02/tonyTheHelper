// Package store is the database. It holds queries and persistence only; decisions
// about what should happen belong above it.
package store

import (
	"context"
	"embed"
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

// ErrNotFound is returned when a row that was asked for by id does not exist.
var ErrNotFound = errors.New("store: not found")

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
