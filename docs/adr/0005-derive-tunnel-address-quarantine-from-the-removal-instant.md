# Derive tunnel address quarantine from the removal instant

A tunnel address is not merely an address. AdGuard Home keys its per-client rules and every
row of its query log on the client's IP, so the tunnel address is the principal a Protectee's
browsing history is filed under. Returning a removed Device's address straight to the pool
would let a later Device — belonging to a different Protectee — resolve to the previous
occupant's retained queries, no matter how carefully the per-Protectee log projection in a
later slice is written. The isolation has to hold at allocation, not at rendering.

So a removed Device's address is held out of the pool for a window strictly longer than the
query-log retention. The question this records is where that window lives.

The obvious implementation is a quarantine table, or a `reusable_after` column, written at
removal. That is a second source of truth. Retention is a value Nightloch configures on
AdGuard Home, and AdGuard Home's own default is 90 days rather than the seven Nightloch sets
(DECISIONS.md 33); the day retention is changed, every stored expiry becomes a claim about a
retention that is no longer in force. A stored expiry can also only ever be too short — the
failure it produces is a silent disclosure rather than a visible error.

Only the removal instant is recorded. `removed_at` on the Device row is the whole of the
state, and `ReusableAt` is a function of it and `QuarantineWindow`. Nothing sweeps
quarantine: an address leaves it because its window has passed, so there is no job to fall
behind and nothing to reconcile. Change the retention and every quarantine in flight moves
with it, in the safe direction as readily as the unsafe one.

`QueryLogRetention` therefore lives in `internal/enforcement` beside the allocation rule that
depends on it, rather than in the AdGuard Home configuration it is also written to. That is
deliberate and is the constraint this ADR binds on the AdGuard Home slice: the retention
configured on AdGuard Home must be read from this constant, never stated separately. The two
values agreeing today is not the property that matters; that they cannot disagree tomorrow is.

`QuarantineWindow` is retention plus one day rather than retention exactly. The margin is
not caution about arithmetic — it is that retention is enforced by another process on another
clock, and releasing an address at the same instant the last query expired would stake the
isolation on the two agreeing. The cost is one day of pool pressure across 253 addresses,
which at this deployment's scale is not a cost. The window being strictly longer than
retention is asserted as its own test, because it is the one relationship the whole scheme
rests on.
