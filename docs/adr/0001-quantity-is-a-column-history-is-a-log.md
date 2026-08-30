# Quantity is a mutable column; history is an append-only log

An item's `quantity` is a column we overwrite on every change, not a value derived by
summing a ledger. Alongside it, every change appends an immutable `stock_movements` row
recording the actor, the before and after quantity, and an optional note. Both writes
happen in one transaction.

We chose this over a counter alone because "who used the last one, and when" is the
question a shared household actually asks, and it is unanswerable retroactively — the
information has to be written at the time or it never exists. We chose it over deriving
quantity from the log because reading a household's item list is the hot path and should
be one cheap `SELECT`, not an aggregation.

## Consequences

The column and the log can disagree if a write path forgets the log. Every quantity change
must therefore go through one function that writes both; no handler updates `items.quantity`
directly. Movements store before *and* after quantities rather than a delta, so a movement
row is self-describing even if the column is later corrected out of band.
