package store_test

import (
	"context"
	"errors"
	"os"
	"testing"

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
	return db
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
