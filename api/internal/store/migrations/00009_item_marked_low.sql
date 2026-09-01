-- +goose Up
-- A member's own declaration that an item is running out.
--
-- The threshold added in 00004 only fires on a comparison, which leaves out exactly the
-- things a household keeps one of: a bottle of shampoo sits at 1 until the day it is
-- empty, and no threshold makes "1" mean "nearly gone" without also meaning it the day
-- it was bought. Marking it by hand is the only signal that carries that knowledge.
--
-- A timestamp rather than a boolean, matching deleted_at: it is set once and cleared once,
-- and when it was set is worth keeping for free.
alter table items add column marked_low_at timestamptz;

-- +goose Down
alter table items drop column marked_low_at;
