-- +goose Up
-- Where the item's photo lives in object storage. NULL means no photo — images are
-- optional and most items will never have one. The bytes never touch this database.
alter table items add column image_path text;

-- +goose Down
alter table items drop column image_path;
