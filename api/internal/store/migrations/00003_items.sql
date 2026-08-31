-- +goose Up
-- An item is a kind of thing a household keeps ("น้ำปลา"), not a single physical object.
create table items (
    id           uuid        primary key default gen_random_uuid(),
    household_id uuid        not null references households (id) on delete cascade,
    name         text        not null,
    -- Free text on purpose: people name units however they like. The UI offers the
    -- household's existing units as chips to nudge reuse, but never rejects a new one.
    unit         text        not null,
    description  text,
    quantity     integer     not null default 0 check (quantity >= 0),
    created_by   uuid        not null references users (id),
    created_at   timestamptz not null default now(),
    updated_at   timestamptz not null default now()
);

create index items_household_idx on items (household_id);

-- Append-only history of every quantity change (ADR-0001). Never updated, never deleted.
-- Stores the quantity before and after rather than a delta, so a row is self-describing
-- even if the items.quantity column is later corrected out of band.
create table stock_movements (
    id              uuid        primary key default gen_random_uuid(),
    item_id         uuid        not null references items (id),
    user_id         uuid        not null references users (id),
    quantity_before integer     not null,
    quantity_after  integer     not null,
    note            text,
    created_at      timestamptz not null default now()
);

create index stock_movements_item_idx on stock_movements (item_id, created_at desc);

-- +goose Down
drop table stock_movements;
drop table items;
