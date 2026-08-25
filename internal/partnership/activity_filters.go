package partnership

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ActivityCategory is the small stable vocabulary a viewer narrows Activity
// by. It groups typed actions rather than prose, so wording can change without
// changing what a saved filter means, and a new action joins a category
// deliberately rather than by resembling an old sentence (S3-48).
type ActivityCategory string

const (
	// AnyCategory is the zero value and narrows nothing.
	AnyCategory ActivityCategory = ""
	// CategoryInvitations covers an invitation's whole life before it becomes
	// a Partnership: sent, resent, declined, canceled, expired.
	CategoryInvitations ActivityCategory = "invitations"
	// CategoryPartnerships covers a Partnership beginning and ending.
	CategoryPartnerships ActivityCategory = "partnerships"
	// CategoryCoolingOff covers the Protectee's 72-hour exit and everything
	// that happens to a running one short of the ending itself.
	CategoryCoolingOff ActivityCategory = "cooling-off"
	// CategoryDelivery covers reachability alone, which is an integrity
	// signal rather than a lifecycle change and is read as such (S3-58).
	CategoryDelivery ActivityCategory = "delivery"
)

// ActorNightloch is the actor filter matching Nightloch's own work — a
// completed timer, an expiry, a delivery outcome — which is stored as no actor
// at all. It is a name rather than an address, so it can never collide with a
// verified email (S3-18).
const ActorNightloch = "Nightloch"

// categoryOfAction is the one place an action is assigned a category. Both the
// filter and each entry's own category read it, so a filtered page cannot
// disagree with the label on the rows it returned.
var categoryOfAction = map[string]ActivityCategory{
	ActionInvitationSent:                  CategoryInvitations,
	ActionInvitationResent:                CategoryInvitations,
	ActionInvitationDeclined:              CategoryInvitations,
	ActionInvitationCanceled:              CategoryInvitations,
	ActionInvitationExpired:               CategoryInvitations,
	ActionInvitationAccepted:              CategoryPartnerships,
	ActionPartnershipEndedByPartner:       CategoryPartnerships,
	ActionPartnershipEndedAfterCoolingOff: CategoryPartnerships,
	ActionCoolingOffStarted:               CategoryCoolingOff,
	ActionCoolingOffCanceled:              CategoryCoolingOff,
	ActionCoolingOffBecameFinal:           CategoryCoolingOff,
	ActionDeliveryDelayed:                 CategoryDelivery,
	ActionDeliveryFailed:                  CategoryDelivery,
	ActionDeliveryRecovered:               CategoryDelivery,
}

// ActivityCategories is every category a viewer may filter by, in the order a
// screen should offer them. The order is stated here because no useful one
// falls out of the action table; that the two agree — every category offered
// covers actions, and every action's category is offered — is held by a test
// rather than by construction.
func ActivityCategories() []ActivityCategory {
	return []ActivityCategory{
		CategoryInvitations, CategoryPartnerships, CategoryCoolingOff, CategoryDelivery,
	}
}

// Label is the product wording for this category.
func (c ActivityCategory) Label() string {
	switch c {
	case CategoryInvitations:
		return "Invitations"
	case CategoryPartnerships:
		return "Partnerships"
	case CategoryCoolingOff:
		return "Cooling-off"
	case CategoryDelivery:
		return "Delivery"
	default:
		return "Everything"
	}
}

// actionsIn is every action the category covers, sorted so the generated SQL
// is stable and a query plan is comparable between runs.
func actionsIn(category ActivityCategory) []string {
	actions := make([]string, 0, len(categoryOfAction))
	for action, of := range categoryOfAction {
		if of == category {
			actions = append(actions, action)
		}
	}
	sort.Strings(actions)
	return actions
}

// ActivityCursor is one viewer's exact place in a chronology: the instant and
// the entry id of the last row they read. Both halves are needed because a
// second holds more than one event, and paging on the instant alone would
// either repeat or skip whatever shares it (S3-48).
type ActivityCursor struct {
	OccurredAt time.Time
	ID         int64
}

// Zero reports whether this cursor names no place, which is the first page.
func (c ActivityCursor) Zero() bool { return c.ID == 0 }

// String renders the cursor for a link. It is a position, not a capability:
// the viewer's window is re-resolved on every read, so an edited or borrowed
// cursor still reads nothing the viewer is not authorized to read.
func (c ActivityCursor) String() string {
	if c.Zero() {
		return ""
	}
	return fmt.Sprintf("%d-%d", c.OccurredAt.UTC().Unix(), c.ID)
}

// ParseActivityCursor reads a cursor back from a link. A malformed one is
// refused rather than silently treated as the first page, because a viewer
// paging through a chronology should not be quietly returned to its top.
func ParseActivityCursor(raw string) (ActivityCursor, error) {
	if raw == "" {
		return ActivityCursor{}, nil
	}
	instant, id, found := strings.Cut(raw, "-")
	if !found {
		return ActivityCursor{}, ErrActivityFilterInvalid
	}
	seconds, err := strconv.ParseInt(instant, 10, 64)
	if err != nil {
		return ActivityCursor{}, ErrActivityFilterInvalid
	}
	entry, err := strconv.ParseInt(id, 10, 64)
	if err != nil || entry <= 0 {
		return ActivityCursor{}, ErrActivityFilterInvalid
	}
	return ActivityCursor{OccurredAt: time.Unix(seconds, 0).UTC(), ID: entry}, nil
}

// ErrActivityFilterInvalid is a filter Nightloch cannot honour: an unknown
// category, an inverted date range, or a malformed cursor. It is deliberately
// not an empty page — a screen that answered a typo with "nothing happened"
// would be claiming a fact about the chronology it does not have.
var ErrActivityFilterInvalid = errors.New("that Activity filter is unavailable")

// ActivityQuery narrows one authorized chronology. Every field is optional and
// the zero value is the newest page of everything. Narrowing happens strictly
// inside the viewer's own window: a filter can only ever subtract from what
// they were already entitled to read (S3-48).
type ActivityQuery struct {
	// Category keeps only entries of one kind.
	Category ActivityCategory
	// Actor keeps only entries one User performed, or ActorNightloch for the
	// work Nightloch did itself.
	Actor string
	// From and To bound the occurrence instant inclusively. Either may be zero
	// for an open end.
	From, To time.Time
	// After is the place the previous page stopped.
	After ActivityCursor
}

// Filtered reports whether this query narrows anything, which lets a screen
// distinguish an empty chronology from a filter that matched nothing.
func (q ActivityQuery) Filtered() bool {
	return q.Category != AnyCategory || q.Actor != "" || !q.From.IsZero() || !q.To.IsZero()
}

// validate refuses a query that cannot be honoured as written.
func (q ActivityQuery) validate() error {
	if q.Category != AnyCategory && len(actionsIn(q.Category)) == 0 {
		return fmt.Errorf("%w: unknown category", ErrActivityFilterInvalid)
	}
	if !q.From.IsZero() && !q.To.IsZero() && q.To.Before(q.From) {
		return fmt.Errorf("%w: the range ends before it starts", ErrActivityFilterInvalid)
	}
	return nil
}
