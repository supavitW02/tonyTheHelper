-- +goose Up
-- An invite grants one person membership of one household. The token is high-entropy and
-- opaque rather than signed: because it is stored, it can be revoked and its use recorded,
-- which a self-contained signed token cannot offer without a revocation list anyway.
create table invites (
    id           uuid        primary key default gen_random_uuid(),
    household_id uuid        not null references households (id) on delete cascade,
    created_by   uuid        not null references users (id),
    token        text        not null unique,
    expires_at   timestamptz not null,
    -- Recorded on first use. A second open by the same person is not an error; it just
    -- opens the household.
    consumed_at  timestamptz,
    consumed_by  uuid        references users (id),
    created_at   timestamptz not null default now()
);

-- Every invite is looked up by its token, never by id.
create index invites_token_idx on invites (token);

-- +goose Down
drop table invites;
