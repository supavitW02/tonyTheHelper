-- +goose Up
-- Optional per-item threshold. NULL means the item is never "low" — most items are not
-- worth tracking that closely, so absence has to be the default.
alter table items add column low_stock_threshold integer;

-- Items are soft-deleted so their movement history stays readable. Movements are
-- append-only (ADR-0001), and a hard delete would either orphan them or destroy the
-- answer to "who used the last one".
alter table items add column deleted_at timestamptz;

-- Reads always filter out deleted items, so the index should too.
drop index if exists items_household_idx;
create index items_household_live_idx on items (household_id) where deleted_at is null;

-- +goose Down
drop index if exists items_household_live_idx;
create index items_household_idx on items (household_id);
alter table items drop column deleted_at;
alter table items drop column low_stock_threshold;
