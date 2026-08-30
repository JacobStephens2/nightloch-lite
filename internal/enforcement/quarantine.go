package enforcement

import (
	"fmt"
	"net/netip"
	"time"
)

// A tunnel address is not merely an address. AdGuard Home keys its per-client
// rules and every row of its query log on it, so it is the security principal
// a Protectee's browsing history is filed under. Handing a removed Device's
// address to a new occupant while the previous occupant's queries are still
// retained would disclose one Protectee's browsing to another, however
// carefully the later per-Protectee log projection is written.
//
// Quarantine is what closes that: a removed address is held out of the pool
// until every query it could still resolve to has aged out of retention. Only
// the removal instant is recorded and the window is derived from it, so it
// cannot drift from the retention actually in force
// (docs/adr/0005-derive-tunnel-address-quarantine-from-the-removal-instant.md).
const (
	// QueryLogRetention is the query-log retention Nightloch configures on
	// AdGuard Home. It is stated here, next to the rule that depends on it,
	// because it is a correctness constraint on address allocation before it
	// is a privacy setting — AdGuard Home's own default is 90 days, so the
	// slice that writes its configuration must set this value rather than
	// inherit one (DECISIONS.md 33).
	QueryLogRetention = 7 * 24 * time.Hour

	// quarantineMargin is how much longer than retention an address is held.
	// It is what makes the window strictly longer rather than exactly equal:
	// retention is enforced by another process on another clock, and an
	// address released at the same instant the last query expired would be
	// betting the isolation on the two agreeing.
	quarantineMargin = 24 * time.Hour

	// QuarantineWindow is how long a removed Device's address is held out of
	// the pool, counted from the removal instant.
	QuarantineWindow = QueryLogRetention + quarantineMargin
)

// ReusableAt is the instant a Device removed at removedAt releases its tunnel
// address back to the pool. It is a function rather than a stored column so
// that it cannot drift from the retention actually in force: change the
// retention and every quarantine in flight moves with it.
func ReusableAt(removedAt time.Time) time.Time {
	return removedAt.Add(QuarantineWindow).UTC()
}

// ResolverQueryLogRetention is the AdGuard Home duration Nightloch renders
// into the Resolver's configuration.  Keeping this translation here makes
// QueryLogRetention the only source of the value: the deployment posture asks
// the application for it instead of restating seven days in shell or YAML.
func ResolverQueryLogRetention() string {
	return fmt.Sprintf("%dh", QueryLogRetention/time.Hour)
}

// quarantineOf reports the hold a Device removed at removedAt still has on its
// tunnel address at now, and whether it still has one. Allocation and
// reporting both ask this rather than each comparing instants themselves, so
// an address cannot be free to one and held by the other.
func quarantineOf(address netip.Addr, removedAt, now time.Time) (QuarantinedAddress, bool) {
	reusableAt := ReusableAt(removedAt)
	if !now.Before(reusableAt) {
		return QuarantinedAddress{}, false
	}
	return QuarantinedAddress{Address: address, RemovedAt: removedAt, ReusableAt: reusableAt}, true
}

// QuarantinedAddress is one tunnel address a removed Device still holds, and
// the wait before it can be allocated again. An Operator refused a mint for
// exhaustion needs to see these to know whether the remedy is to remove a
// Device or to wait.
type QuarantinedAddress struct {
	Address    netip.Addr
	RemovedAt  time.Time
	ReusableAt time.Time
}
