package partnership

import (
	"strconv"
	"time"
)

// activityDetails is the typed view of a stored payload. It names only the
// fields some entry's prose or detail rows turn on, so widening it is a
// deliberate act rather than a side effect of storing something new. Nothing
// outside this file reads a stored payload, and nothing anywhere renders one:
// a field with no typed home here simply never reaches a screen (S3-49).
type activityDetails struct {
	// EndedReason separates the two endings a Partner performs on one action:
	// their own independent ending, and the **End now** that waives a
	// Cooling-off the Protectee requested. They are stored apart so they can
	// be audited apart (S3-08), so Activity has to read them apart too.
	EndedReason string `json:"ended_reason"`
	// LogSharing is the Log sharing this invitation offered or this
	// Partnership carried, which is the one Partner power a Protectee chooses.
	LogSharing *bool `json:"log_sharing"`
	// Reciprocal marks an acceptance that put a second, independent
	// Partnership in the opposite direction.
	Reciprocal *bool `json:"reciprocal"`
	// PartnerCount is how many Partners the entry's own instant leaves
	// active, and PartnerLockActive whether the lock survives it.
	PartnerCount      *int  `json:"partner_count"`
	PartnerLockActive *bool `json:"partner_lock_active"`
	// FinalPartnership marks a Cooling-off request over the last Partnership
	// holding the lock, whose deadline therefore deactivates it (S3-43).
	FinalPartnership *bool `json:"final_partnership"`
	// ExpiresAt is the invitation deadline a resend deliberately does not move.
	ExpiresAt int64 `json:"expires_at"`
	// SendCount is how many times that invitation has now been sent.
	SendCount int `json:"send_count"`
	// RequestedBy, RequestedAt, and RequestedActivityID are the original
	// Protectee request a completed timer carries out. Nightloch runs the
	// timer; the Protectee asked for it, and the entry links back to where
	// they did rather than attributing the execution to them (S3-18).
	RequestedBy         string `json:"requested_by"`
	RequestedAt         int64  `json:"requested_at"`
	RequestedActivityID int64  `json:"requested_activity_id"`
	// CanceledDeadline is the deadline a canceled Cooling-off would have hit.
	CanceledDeadline int64 `json:"canceled_deadline"`
	// EndedPartner is the Partnership whose ending left another one final.
	EndedPartner string `json:"ended_partner"`
	// MessageKind and Reachability describe a delivery outcome. The kind is a
	// stable internal identifier and is never rendered as stored; it is
	// translated below, and an unknown kind renders nothing at all.
	MessageKind  string `json:"message_kind"`
	Reachability string `json:"reachability"`
}

// ActivityDetail is one typed, human-readable fact behind an entry, which a
// screen may offer as expandable detail. Each row is a label and either
// rendered text or an instant — never a stored payload, and never a field
// this viewer's window does not cover (S3-49).
type ActivityDetail struct {
	Label string
	// Text is the rendered value, and is empty when the value is an instant.
	Text string
	// At is an instant value, which a screen renders with the same explicit
	// UTC and browser-local labelling every other time gets (S3-27).
	At time.Time
}

// Instant reports whether this row's value is a time rather than text.
func (d ActivityDetail) Instant() bool { return !d.At.IsZero() }

// messageKindLabels translates a stored message kind into what a viewer is
// told a delivery outcome was about. A kind with no entry here is omitted
// rather than shown raw, which is the whole point of the table: the stored
// identifier is Nightloch's, not a viewer's.
var messageKindLabels = map[string]string{
	MessagePartnerInvitation:           "Partner invitation",
	MessagePartnerInvitationSent:       "Partner invitation alert",
	MessagePartnerInvitationResent:     "Partner invitation resend",
	MessageInvitationDeclined:          "Invitation declined alert",
	MessageInvitationCanceled:          "Invitation canceled alert",
	MessageInvitationCanceledPartner:   "Invitation canceled alert",
	MessagePartnershipAcceptedExisting: "Partnership accepted alert",
	MessagePartnershipAcceptedPartner:  "Partnership acceptance confirmation",
	MessageCoolingOffStarted:           "Cooling-off started alert",
	MessageCoolingOffCanceled:          "Cooling-off canceled alert",
	MessageCoolingOffBecameFinal:       "Final Partnership alert",
	MessagePartnershipEnded:            "Partnership ended alert",
}

// Details is the entry's typed detail rows for the viewer who read it. It is
// built from named fields alone, in a fixed order per action, so an expandable
// detail can only ever show something this module decided to render for this
// audience. The two audiences are the Protectee, who reads their complete
// chronology, and a Partner, who reads a window: a row naming an instant from
// before that window is withheld, because an entry inside the window must not
// become a way to read an event outside it (S3-15).
func (e ActivityEntry) Details() []ActivityDetail {
	var rows []ActivityDetail
	add := func(label, text string) {
		if text != "" {
			rows = append(rows, ActivityDetail{Label: label, Text: text})
		}
	}
	addInstant := func(label string, seconds int64) {
		if seconds == 0 {
			return
		}
		at := time.Unix(seconds, 0).UTC()
		if e.outsideWindow(at) {
			return
		}
		rows = append(rows, ActivityDetail{Label: label, At: at})
	}
	details := e.details

	switch e.Action {
	case ActionInvitationSent, ActionInvitationDeclined, ActionInvitationCanceled,
		ActionInvitationExpired:
		add("Log sharing offered", onOffOf(details.LogSharing))
		addInstant("Invitation expiry", details.ExpiresAt)
	case ActionInvitationResent:
		add("Log sharing offered", onOffOf(details.LogSharing))
		addInstant("Invitation expiry, unchanged", details.ExpiresAt)
		add("Times sent", countOf(&details.SendCount))
	case ActionInvitationAccepted:
		add("Log sharing", onOffOf(details.LogSharing))
		add("Reciprocal Partnership", yesNoOf(details.Reciprocal))
		add("Active Partners afterwards", countOf(details.PartnerCount))
	case ActionCoolingOffStarted:
		add("Log sharing while it runs", onOffOf(details.LogSharing))
		add("Active Partners after the deadline", countOf(details.PartnerCount))
		add("Partner lock at the deadline", lockAtDeadline(details.FinalPartnership))
	case ActionCoolingOffCanceled:
		add("Log sharing", onOffOf(details.LogSharing))
		add("Active Partners", countOf(details.PartnerCount))
		addInstant("Deadline no longer scheduled", details.CanceledDeadline)
	case ActionPartnershipEndedByPartner:
		add("Log sharing while active", onOffOf(details.LogSharing))
		add("Active Partners remaining", countOf(details.PartnerCount))
		add("Partner lock", lockState(details.PartnerLockActive))
	case ActionPartnershipEndedByUserDeletion:
		add("Log sharing while active", onOffOf(details.LogSharing))
		add("Active Partners remaining", countOf(details.PartnerCount))
		add("Partner lock", lockState(details.PartnerLockActive))
	case ActionPartnershipEndedAfterCoolingOff:
		add("Requested by", details.RequestedBy)
		addInstant("Requested on", details.RequestedAt)
		add("Log sharing while active", onOffOf(details.LogSharing))
		add("Active Partners remaining", countOf(details.PartnerCount))
		add("Partner lock", lockState(details.PartnerLockActive))
	case ActionCoolingOffBecameFinal:
		add("Partnership that ended", details.EndedPartner)
		addInstant("Cooling-off requested on", details.RequestedAt)
		add("Partner lock", lockState(details.PartnerLockActive))
	case ActionDeliveryDelayed, ActionDeliveryFailed, ActionDeliveryRecovered:
		add("Message", messageKindLabels[details.MessageKind])
		add("Reachability", reachabilityRow(details.Reachability))
	}
	return rows
}

// reachabilityRow renders a stored reachability, and renders nothing for one
// this module does not know. Reachability.Label falls back to "Reachable",
// which is the right answer for a Roster row derived from live jobs and the
// wrong one here: an entry that recorded a failure must never be the thing
// that claims a Partner is reachable.
func reachabilityRow(stored string) string {
	reachability := Reachability(stored)
	switch reachability {
	case ReachabilityOK, ReachabilityDelayed, ReachabilityUnreachable:
		return reachability.Label()
	default:
		return ""
	}
}

// outsideWindow reports whether an instant predates this reader's window. The
// Protectee's zero window puts nothing outside it.
func (e ActivityEntry) outsideWindow(at time.Time) bool {
	return !e.visibleFrom.IsZero() && at.Before(e.visibleFrom)
}

// RequestEntryID is the Activity entry a completed timer carries out, so a
// viewer reaches the Protectee's own request from Nightloch's execution of it
// rather than being left to search for it (S3-18). It is zero when there is no
// such entry, and when the request happened before this reader's window: the
// link is a way to read that entry, so it obeys the same rule its details do.
func (e ActivityEntry) RequestEntryID() int64 {
	if e.details.RequestedActivityID == 0 {
		return 0
	}
	if e.details.RequestedAt != 0 && e.outsideWindow(time.Unix(e.details.RequestedAt, 0).UTC()) {
		return 0
	}
	return e.details.RequestedActivityID
}

// onOffOf and yesNoOf render a stored flag, and render nothing when the field
// is absent from an entry that never carried it. They are the pointer halves
// of the message renderers' own onOff and yesNo, so a detail row and an alert
// cannot describe the same flag with different words.
// RequestPage is the page position that holds the request entry, so a reader
// can be taken to it rather than to wherever the chronology happens to be
// open. A Cooling-off deadline is 72 hours after the request that set it, so
// the two entries are routinely pages apart and often in different categories;
// a link that only worked when they shared a page would usually do nothing.
// The position is one past the request's own id, which puts that entry at the
// top of the page it opens.
func (e ActivityEntry) RequestPage() ActivityCursor {
	id := e.RequestEntryID()
	if id == 0 || e.details.RequestedAt == 0 {
		return ActivityCursor{}
	}
	return ActivityCursor{OccurredAt: time.Unix(e.details.RequestedAt, 0).UTC(), ID: id + 1}
}

func onOffOf(value *bool) string {
	if value == nil {
		return ""
	}
	return onOff(*value)
}

func yesNoOf(value *bool) string {
	if value == nil {
		return ""
	}
	return yesNo(*value)
}

// lockState is what a Partner lock consequence reads as. A missing value says
// nothing rather than guessing, because claiming the lock is inactive when it
// is not would be the one wrong answer that matters (S3-42).
func lockState(active *bool) string {
	if active == nil {
		return ""
	}
	if *active {
		return "Still active afterwards"
	}
	return "Inactive afterwards"
}

func lockAtDeadline(final *bool) string {
	if final == nil {
		return ""
	}
	if *final {
		return "Becomes inactive at the deadline"
	}
	return "Stays active after the deadline"
}

// countOf renders a stored count, and renders nothing at all when the field is
// absent. A missing count is not a count of zero: "0 Partners remaining" is a
// claim about the Partner lock, and an entry that never recorded one must not
// make it.
func countOf(value *int) string {
	if value == nil {
		return ""
	}
	return strconv.Itoa(*value)
}
