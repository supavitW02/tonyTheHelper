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
returning id, line_user_id, display_name, coalesce(picture_url, ''), created_at`

// UpsertUser records the person behind a verified LINE identity, creating them on
// first sign-in and refreshing their name and picture on every one after.
func (db *DB) UpsertUser(ctx context.Context, identity line.Identity) (User, error) {
	row := db.pool.QueryRow(ctx, upsertUser, identity.UserID, identity.DisplayName, identity.PictureURL)

	var u User
	if err := row.Scan(&u.ID, &u.LineUserID, &u.DisplayName, &u.PictureURL, &u.CreatedAt); err != nil {
		return User{}, fmt.Errorf("store: upsert user: %w", err)
	}
	return u, nil
}

const selectUser = `
select id, line_user_id, display_name, coalesce(picture_url, ''), created_at
from users
where id = $1`

// UserByID looks a user up by our own id, which is what a session carries.
func (db *DB) UserByID(ctx context.Context, id string) (User, error) {
	var u User
	err := db.pool.QueryRow(ctx, selectUser, id).
		Scan(&u.ID, &u.LineUserID, &u.DisplayName, &u.PictureURL, &u.CreatedAt)

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
