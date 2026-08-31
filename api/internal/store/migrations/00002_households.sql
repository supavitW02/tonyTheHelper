-- +goose Up
-- A household is the unit of ownership: items belong to a household, never to a user.
create table households (
    id         uuid        primary key default gen_random_uuid(),
    name       text        not null,
    owner_id   uuid        not null references users (id),
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

-- Membership is a join table from the start. Nobody belongs to two households yet, but
-- migrating from a users.household_id column later would mean touching every query.
create table household_members (
    household_id uuid        not null references households (id) on delete cascade,
    user_id      uuid        not null references users (id),
    joined_at    timestamptz not null default now(),
    primary key (household_id, user_id)
);

-- Answering "which households does this user belong to" is done on every app open.
create index household_members_user_idx on household_members (user_id);

-- The household last opened, so a returning user lands straight back in it.
alter table users add column last_household_id uuid references households (id);

-- +goose Down
alter table users drop column last_household_id;
drop table household_members;
drop table households;
