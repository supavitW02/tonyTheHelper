-- +goose Up
-- Users are identified solely by their LINE user id (ADR-0002). There is no password
-- column, and there never will be.
create table users (
    id            uuid primary key default gen_random_uuid(),
    line_user_id  text        not null unique,
    display_name  text        not null,
    picture_url   text,
    created_at    timestamptz not null default now(),
    updated_at    timestamptz not null default now()
);

-- +goose Down
drop table users;
