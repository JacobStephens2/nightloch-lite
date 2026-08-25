package partnership

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// activityEntry is one append-only chronology entry for a Protectee. Entries
// are never edited; corrections append (S3-17). Details holds structured
// fields used to render authorized prose and is never product UI (S3-49).
type activityEntry struct {
	ProtecteeID   int64
	Action        string
	ActorUserID   *int64
	ActorEmail    string
	TargetEmail   string
	PartnershipID *int64
	InvitationID  *int64
	OccurredAt    time.Time
	EffectiveAt   *time.Time
	Details       map[string]any
}

func appendActivity(ctx context.Context, tx *sql.Tx, entry activityEntry) (int64, error) {
	details, err := json.Marshal(entry.Details)
	if err != nil {
		return 0, fmt.Errorf("encode Activity details: %w", err)
	}
	var effectiveAt any
	if entry.EffectiveAt != nil {
		effectiveAt = entry.EffectiveAt.UTC().Unix()
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO activity_entries(
			protectee_id, action, actor_user_id, actor_email, target_email,
			partnership_id, invitation_id, occurred_at, effective_at, details)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ProtecteeID, entry.Action, entry.ActorUserID, nullString(entry.ActorEmail),
		nullString(entry.TargetEmail), entry.PartnershipID, entry.InvitationID,
		entry.OccurredAt.UTC().Unix(), effectiveAt, string(details))
	if err != nil {
		return 0, fmt.Errorf("append Activity entry: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read Activity entry id: %w", err)
	}
	return id, nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// ErrActivityUnavailable is the single answer for a chronology this caller may
// not read: an unknown Protectee, a Protectee they have never partnered with,
// a Partnership that has already ended, and an unauthenticated visitor all
// receive it. It distinguishes none of those deliberately, so a direct
// identifier cannot be used to probe for the existence of a relationship.
var ErrActivityUnavailable = errors.New("that Activity is unavailable")

// ActivityEntry is one append-only entry as an authorized viewer reads it.
// The stored structured details never leave the module: a viewer reads the
// entry's own prose and the facts it is attributed by, never a raw payload
// (S3-49).
type ActivityEntry struct {
	ID int64
	// Action is the stable typed action, which is what a later category
	// filter and any caller-side grouping key off rather than the prose.
	Action string
	// ProtecteeEmail is whose chronology this entry belongs to, so an entry
	// describes itself without the view it came from.
	ProtecteeEmail string
	// ActorEmail is the User who acted, and is empty when Nightloch itself
	// acted — a completed timer, an expiry, a delivery outcome (S3-18).
	ActorEmail string
	// TargetEmail is who the action was about.
	TargetEmail string
	// OccurredAt is when the action happened, and EffectiveAt is when its
	// consequence lands where those differ, such as a Cooling-off deadline.
	OccurredAt  time.Time
	EffectiveAt time.Time
	// details is the decoded subset of the stored payload that the entry's own
	// prose needs. It stays unexported: details inform what a viewer reads,
	// they are never themselves what a viewer reads (S3-49).
	details activityDetails
}

// activityDetails is the typed view of a stored payload. It names only the
// fields some entry's prose turns on, so widening it is a deliberate act
// rather than a side effect of storing something new.
type activityDetails struct {
	// EndedReason separates the two endings a Partner performs on one action:
	// their own independent ending, and the **End now** that waives a
	// Cooling-off the Protectee requested. They are stored apart so they can
	// be audited apart (S3-08), so Activity has to read them apart too.
	EndedReason string `json:"ended_reason"`
}

// Effective reports whether this entry has a consequence instant distinct from
// when it was recorded.
func (e ActivityEntry) Effective() bool { return !e.EffectiveAt.IsZero() }

// ActivityView is one Protectee's chronology as one viewer is authorized to
// read it, newest first.
type ActivityView struct {
	ProtecteeEmail string
	// VisibleFrom is the instant this viewer's window opens, which is their
	// own Partnership's acceptance. It is zero for the Protectee, whose window
	// is their whole history while their User exists (S3-14).
	VisibleFrom time.Time
	Entries     []ActivityEntry
}

// Windowed reports whether this viewer reads a bounded window rather than the
// Protectee's complete chronology, so a screen can say so plainly instead of
// letting a Partner mistake their window for everything that happened.
func (v ActivityView) Windowed() bool { return !v.VisibleFrom.IsZero() }

// activityWindow is the span of one Protectee's chronology one viewer may
// read, resolved before a single entry is fetched. A Protectee's window has no
// opening instant; every other authorized viewer is a live Partner whose
// window opens at their own Partnership's acceptance and closes the moment
// that Partnership ends (ADR 0001).
type activityWindow struct {
	protecteeEmail string
	from           time.Time
	// fromEntry is the id of the acceptance entry that opens the window. A
	// second holds more than one event, so the instant alone cannot say
	// whether an entry stamped with the same second happened before or after
	// the acceptance; within that second, insertion order is the answer, and
	// the acceptance's own id is where the window starts.
	fromEntry int64
}

// activityWindowQuery resolves the Protectee and, in the same read, whichever
// live Partnership the viewer holds over them. Authorization is the query
// itself: a former Partner, a pending invitee, and a stranger all leave the
// acceptance NULL, and an unknown Protectee matches no row at all. A later
// Partnership between the same two Users is a different row with a later
// acceptance, so re-partnering opens a new window rather than reviving the old
// one, and the gap between them is never inside either.
const activityWindowQuery = `
	SELECT protectee.email, live.accepted_at,
	       (SELECT MIN(entry.id) FROM activity_entries entry
	         WHERE entry.partnership_id = live.id AND entry.action = ?3)
	FROM users protectee
	LEFT JOIN partnerships live ON live.protectee_id = protectee.id
	     AND live.partner_id = ?2 AND live.ended_at IS NULL
	WHERE protectee.id = ?1`

func (s *Service) activityWindow(ctx context.Context, protecteeID, viewerID int64) (activityWindow, error) {
	if protecteeID <= 0 || viewerID <= 0 {
		return activityWindow{}, ErrActivityUnavailable
	}
	var protecteeEmail string
	var acceptedAt, acceptanceEntry sql.NullInt64
	err := s.db.QueryRowContext(ctx, activityWindowQuery, protecteeID, viewerID, ActionInvitationAccepted).
		Scan(&protecteeEmail, &acceptedAt, &acceptanceEntry)
	if errors.Is(err, sql.ErrNoRows) {
		return activityWindow{}, ErrActivityUnavailable
	}
	if err != nil {
		return activityWindow{}, fmt.Errorf("read Activity window: %w", err)
	}
	if protecteeID == viewerID {
		return activityWindow{protecteeEmail: protecteeEmail}, nil
	}
	if !acceptedAt.Valid {
		return activityWindow{}, ErrActivityUnavailable
	}
	return activityWindow{
		protecteeEmail: protecteeEmail,
		from:           time.Unix(acceptedAt.Int64, 0).UTC(),
		// Every acceptance appends its entry in the same transaction that
		// creates the Partnership, so the id is always there; treating a
		// missing one as 0 would only widen the window to the whole second.
		fromEntry: acceptanceEntry.Int64,
	}, nil
}

// Activity returns protecteeID's chronology as viewerID is authorized to read
// it, newest first. Every audience projection lives here rather than in a
// caller, so the Protectee's complete history, a Partner's window, and the
// nothing a former Partner reads cannot drift apart between screens (S3-14).
func (s *Service) Activity(ctx context.Context, protecteeID, viewerID int64) (ActivityView, error) {
	window, err := s.activityWindow(ctx, protecteeID, viewerID)
	if err != nil {
		return ActivityView{}, err
	}
	// The window opens at an entry, not merely at an instant, so an event
	// recorded in the same second as the acceptance but before it stays
	// outside. The Protectee's zero instant is older than any recorded event,
	// so the same comparison gives them their whole history. Entries stamped
	// in the past — a sweep recording an expiry on the deadline it passed —
	// are placed by when they occurred rather than by when they were written,
	// which is why the instant leads and the id only settles ties.
	// The read is deliberately unbounded for now: newest-first pagination and
	// the category, actor, and date-range filters are their own ticket, and a
	// silent cap here would let a screen claim a complete chronology while
	// showing part of one.
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, action, COALESCE(actor_email, ''), COALESCE(target_email, ''),
		       occurred_at, COALESCE(effective_at, 0), details
		FROM activity_entries
		WHERE protectee_id = ?1
		  AND (occurred_at > ?2 OR (occurred_at = ?2 AND id >= ?3))
		ORDER BY occurred_at DESC, id DESC`,
		protecteeID, window.from.Unix(), window.fromEntry)
	if err != nil {
		return ActivityView{}, fmt.Errorf("read Activity: %w", err)
	}
	defer rows.Close()

	view := ActivityView{ProtecteeEmail: window.protecteeEmail, VisibleFrom: window.from}
	for rows.Next() {
		entry := ActivityEntry{ProtecteeEmail: window.protecteeEmail}
		var occurredAt, effectiveAt int64
		var details []byte
		if err := rows.Scan(&entry.ID, &entry.Action, &entry.ActorEmail,
			&entry.TargetEmail, &occurredAt, &effectiveAt, &details); err != nil {
			return ActivityView{}, fmt.Errorf("read Activity entry: %w", err)
		}
		if err := json.Unmarshal(details, &entry.details); err != nil {
			return ActivityView{}, fmt.Errorf("decode Activity details: %w", err)
		}
		entry.OccurredAt = time.Unix(occurredAt, 0).UTC()
		if effectiveAt != 0 {
			entry.EffectiveAt = time.Unix(effectiveAt, 0).UTC()
		}
		view.Entries = append(view.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return ActivityView{}, fmt.Errorf("read Activity: %w", err)
	}
	return view, nil
}

// Summary is the entry's stable active-voice prose. It names the actor — a
// verified email, or Nightloch for its own scheduled work — the target, and
// what changed. It is derived from the typed action and the attributed
// identities alone, so no stored structured payload can reach a screen through
// it (S3-49).
func (e ActivityEntry) Summary() string {
	actor := e.ActorEmail
	if actor == "" {
		actor = "Nightloch"
	}
	switch e.Action {
	case ActionInvitationSent:
		return fmt.Sprintf("%s invited %s as a Partner.", actor, e.TargetEmail)
	case ActionInvitationResent:
		return fmt.Sprintf("%s resent the Partner invitation to %s. The expiry did not move.",
			actor, e.TargetEmail)
	case ActionInvitationAccepted:
		return fmt.Sprintf("%s accepted %s’s Partner invitation and became their Partner.",
			actor, e.TargetEmail)
	case ActionInvitationDeclined:
		return fmt.Sprintf("%s declined %s’s Partner invitation. No Partnership was created.",
			actor, e.TargetEmail)
	case ActionInvitationCanceled:
		return fmt.Sprintf("%s canceled the Partner invitation to %s. No Partnership was created.",
			actor, e.TargetEmail)
	case ActionInvitationExpired:
		return fmt.Sprintf("Nightloch expired %s’s Partner invitation to %s after seven days.",
			e.ProtecteeEmail, e.TargetEmail)
	case ActionPartnershipEndedByPartner:
		if e.details.EndedReason == EndReasonPartnerAgreedNow {
			return fmt.Sprintf("%s agreed to end their Partnership with %s now, waiving the rest of the Cooling-off %s requested.",
				actor, e.TargetEmail, e.TargetEmail)
		}
		return fmt.Sprintf("%s ended their Partnership with %s immediately.", actor, e.TargetEmail)
	case ActionCoolingOffStarted:
		return fmt.Sprintf("%s requested to end their Partnership with %s after Cooling-off.",
			actor, e.TargetEmail)
	case ActionCoolingOffCanceled:
		return fmt.Sprintf("%s canceled their request to end the Partnership with %s.",
			actor, e.TargetEmail)
	case ActionPartnershipEndedAfterCoolingOff:
		return fmt.Sprintf("Nightloch ended %s’s Partnership with %s on the Cooling-off deadline %s requested.",
			e.ProtecteeEmail, e.TargetEmail, e.ProtecteeEmail)
	case ActionCoolingOffBecameFinal:
		return fmt.Sprintf("Nightloch recorded that the Partnership with %s is now the last one holding %s’s Partner lock active.",
			e.TargetEmail, e.ProtecteeEmail)
	case ActionDeliveryDelayed:
		return fmt.Sprintf("Nightloch has not yet delivered a message to %s.", e.TargetEmail)
	case ActionDeliveryFailed:
		return fmt.Sprintf("Nightloch could not reach %s by email.", e.TargetEmail)
	case ActionDeliveryRecovered:
		return fmt.Sprintf("Nightloch reached %s by email again.", e.TargetEmail)
	}
	// An action with no prose is a programming error rather than a viewer's
	// problem, so the entry still reads as an attributable event instead of
	// vanishing from a chronology that claims to be complete.
	return fmt.Sprintf("%s changed %s’s protection.", actor, e.ProtecteeEmail)
}
