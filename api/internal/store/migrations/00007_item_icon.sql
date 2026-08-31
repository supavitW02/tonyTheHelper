-- +goose Up
-- An icon stands in for a photo.
--
-- Most things a household keeps are recognisable from a symbol, and photographing every
-- bottle is real work for the person this app is for: find the item, hold the phone
-- steady, wait for the upload. Picking a picture from a list is one tap and needs no
-- camera, no light and no network beyond saving the item.
--
-- Nullable and unconstrained at the database level: which symbols are offered is a
-- question about the picker, and changing that list should not need a migration.
alter table items add column icon text;

-- +goose Down
alter table items drop column icon;
