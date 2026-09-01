# An item is low by its threshold or by a person's word

`items.is_low` is derived from two independent sources, either of which is enough on its
own: the quantity standing at or below the item's low-stock threshold, and a
`marked_low_at` timestamp set by a member who said so by hand. The shopping list is
exactly the set of low items — a view over the stock, not a list anyone keeps.

We added the second source because the first cannot reach the things a household keeps one
of. A bottle of shampoo reads 1 the day it is bought and 1 the day it runs out; no
threshold separates those two days, and setting one to 1 would mark the item low for its
whole life. The knowledge that it is nearly empty exists only in the head of whoever last
used it, so the app has to let them say it.

We chose a view over a separate list because a second list is a second copy of the truth.
Two members shopping at once would have to reconcile theirs, an item could sit on the list
after the cupboard was refilled, and "how much is left" would have to be answered twice.
Deriving it means the question is answered once, in the place it is already answered.

## Consequences

**Restocking clears the mark; using more up does not.** An increase in quantity is the
only thing that clears `marked_low_at`, and it does so inside the same transaction as the
quantity change. Nobody has to remember to un-mark anything after the shop, which matters
because the person this app is for will not. The cost is honest: someone who taps plus by
mistake has silently cleared the mark, and tapping minus does not bring it back. Re-marking
is one tap, and the alternative — a shopping list that only empties by hand — goes stale
in a week.

**Marking writes no movement.** A movement records a change to a *quantity* (ADR-0001) and
marking moves no stock, so the log stays a log of what was used rather than of what was
noticed. The consequence to be honest about: the history cannot answer "who put this on
the list", only `marked_low_at` can say when it happened at all.

**Setting the mark is idempotent, not a toggle.** `PUT /api/items/{id}/low` carries the
state it wants, so a retried request and two members noticing the same empty bottle both
leave it marked, rather than cancelling each other out. Marking an already-marked item
keeps the original timestamp, so the column keeps answering "since when".

**The derivation is written twice and must agree.** Once in SQL, in `itemColumns`, and once
in Go, in `isLow`, because `AdjustQuantity` computes the new state inside its transaction
rather than reading the row back. If the two drift, the badge on an item and the position
of that item in the list start disagreeing.
