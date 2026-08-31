-- +goose Up
-- Deleting a household revokes access to it; it does not erase what happened in it.
--
-- ADR-0001 makes the movement log append-only, and the schema enforces that:
-- stock_movements has no cascade from items, and is commented "never deleted". A true
-- cascade would therefore have to delete history explicitly, against the one rule the
-- storage design is built around.
--
-- So deletion drops the household's memberships and invites instead. Every authorisation
-- path in the app — items, quantities, movements, ownership — resolves through
-- household_members, so removing those rows makes the household unreachable to everyone
-- at once, with no query anywhere needing to learn about a new flag. The items and their
-- history stay on disk, orphaned but intact.
--
-- This column records that this is what happened. Nothing reads it: it is the difference
-- between "the owner deleted this" and "something went wrong and left a household with no
-- members", which is a question someone will eventually ask of the data.
alter table households add column deleted_at timestamptz;

-- +goose Down
alter table households drop column deleted_at;
