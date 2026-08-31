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

Nothing deletes history, which decides what deletion means everywhere else in the app.
Deleting an item soft-deletes it. Deleting a *household* revokes access to it rather than
erasing it: the schema has no cascade from items to `stock_movements`, so a real cascade
would have to delete the log explicitly, against this decision. Instead the household's
memberships and invites are dropped. That is sufficient because every authorisation path
in the app — items, quantities, movements, ownership — resolves through
`household_members`, so removing those rows closes all of them at once, and no query needs
to learn about a deletion flag. The consequence to be honest about: the rows are still on
disk, so "delete" is not erasure, and anyone promising a user otherwise would be wrong.
