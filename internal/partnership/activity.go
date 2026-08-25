package partnership

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// ActivityPageSize is how many entries one page holds. A long chronology is
// read a page at a time rather than truncated, so a screen never shows part of
// a history while implying it is the whole of one (S3-48).
const ActivityPageSize = 25

// ActivityEntry is one append-only entry as an authorized viewer reads it.
// The stored structured details never leave the module: a viewer reads the
// entry's own prose and the typed facts it is attributed by, never a raw
// payload (S3-49).
type ActivityEntry struct {
	ID int64
	// Action is the stable typed action, which is what the category filter and
	// any caller-side grouping key off rather than the prose.
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
	// details is the decoded subset of the stored payload that this entry's
	// prose and typed detail rows need. It stays unexported: details inform
	// what a viewer reads, they are never themselves what a viewer reads.
	details activityDetails
	// visibleFrom is the instant this reader's window opens, carried on the
	// entry because a detail row may name an earlier instant than the entry
	// itself. Such a row is withheld from a windowed reader rather than
	// letting an in-window entry disclose an out-of-window event (S3-15).
	visibleFrom time.Time
}

// Effective reports whether this entry has a consequence instant distinct from
// when it was recorded.
func (e ActivityEntry) Effective() bool { return !e.EffectiveAt.IsZero() }

// Category is the entry's own filter category, so a screen labels a row with
// the same vocabulary the filter offers.
func (e ActivityEntry) Category() ActivityCategory { return categoryOfAction[e.Action] }

// Delivery reports whether this entry is a reachability signal rather than a
// lifecycle change. Delivery state is shown apart from lifecycle prose,
// because an unreachable Partner keeps every power and every clock keeps
// running: conflating the two would read as a change that never happened.
func (e ActivityEntry) Delivery() bool { return e.Category() == CategoryDelivery }

// ActivityView is one page of one Protectee's chronology as one viewer is
// authorized to read it, newest first.
type ActivityView struct {
	ProtecteeEmail string
	// VisibleFrom is the instant this viewer's window opens, which is their
	// own Partnership's acceptance. It is zero for the Protectee, whose window
	// is their whole history while their User exists (S3-14).
	VisibleFrom time.Time
	Entries     []ActivityEntry
	// Query is the filter and page this view answers, echoed back so a screen
	// renders its own controls from what was actually applied.
	Query ActivityQuery
	// NextPage is where the following page starts, and is zero on the last
	// page. Paging is by position rather than by offset, so an entry appended
	// while a viewer reads cannot shift a later page underneath them.
	NextPage ActivityCursor
	// Actors is every actor with an entry inside this viewer's window, in the
	// order a filter should offer them. It is derived from the window alone
	// rather than from the current page, so the choices do not change as the
	// viewer pages, and it never names anyone the window does not already
	// disclose.
	Actors []string
}

// Windowed reports whether this viewer reads a bounded window rather than the
// Protectee's complete chronology, so a screen can say so plainly instead of
// letting a Partner mistake their window for everything that happened.
func (v ActivityView) Windowed() bool { return !v.VisibleFrom.IsZero() }

// More reports whether another page follows this one.
func (v ActivityView) More() bool { return !v.NextPage.Zero() }

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
// one, and the gap between them is never inside either. A deleted Protectee
// matches no row at all: their chronology is removed with them, and a former
// Partner reads the receipt they keep and nothing else (S3-71).
const activityWindowQuery = `
	SELECT protectee.email, live.accepted_at,
	       (SELECT MIN(entry.id) FROM activity_entries entry
	         WHERE entry.partnership_id = live.id AND entry.action = ?3)
	FROM users protectee
	LEFT JOIN partnerships live ON live.protectee_id = protectee.id
	     AND live.partner_id = ?2 AND live.ended_at IS NULL
	WHERE protectee.id = ?1 AND protectee.deleted_at IS NULL`

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

// windowCondition is the SQL half of a resolved window and leads every read.
// The window opens at an entry, not merely at an instant, so an event recorded
// in the same second as the acceptance but before it stays outside. The
// Protectee's zero instant is older than any recorded event, so the same
// comparison gives them their whole history.
const windowCondition = `protectee_id = ?
	AND (occurred_at > ? OR (occurred_at = ? AND id >= ?))`

func (w activityWindow) args(protecteeID int64) []any {
	return []any{protecteeID, w.from.Unix(), w.from.Unix(), w.fromEntry}
}

// Activity returns one page of protecteeID's chronology as viewerID is
// authorized to read it, newest first and narrowed by query. Every audience
// projection lives here rather than in a caller, so the Protectee's complete
// history, a Partner's window, and the nothing a former Partner reads cannot
// drift apart between screens (S3-14). Authorization is resolved before the
// query is even validated, so a filter error can never be the thing that tells
// an unauthorized viewer a Protectee exists.
func (s *Service) Activity(ctx context.Context, protecteeID, viewerID int64, query ActivityQuery) (ActivityView, error) {
	window, err := s.activityWindow(ctx, protecteeID, viewerID)
	if err != nil {
		return ActivityView{}, err
	}
	if err := query.validate(); err != nil {
		return ActivityView{}, err
	}

	conditions := []string{windowCondition}
	args := window.args(protecteeID)
	if query.Category != AnyCategory {
		actions := actionsIn(query.Category)
		conditions = append(conditions,
			"action IN (?"+strings.Repeat(", ?", len(actions)-1)+")")
		for _, action := range actions {
			args = append(args, action)
		}
	}
	switch {
	// Nightloch's own work is stored as no actor at all, so its filter is the
	// absence of one rather than a reserved address that could be registered.
	case query.Actor == ActorNightloch:
		conditions = append(conditions, "actor_email IS NULL")
	case query.Actor != "":
		conditions = append(conditions, "actor_email = ?")
		args = append(args, query.Actor)
	}
	if !query.From.IsZero() {
		conditions = append(conditions, "occurred_at >= ?")
		args = append(args, query.From.UTC().Unix())
	}
	if !query.To.IsZero() {
		conditions = append(conditions, "occurred_at <= ?")
		args = append(args, query.To.UTC().Unix())
	}
	if !query.After.Zero() {
		// The page boundary is settled the same way the ordering is, so
		// entries sharing a second are neither repeated nor skipped across it.
		conditions = append(conditions, "(occurred_at < ? OR (occurred_at = ? AND id < ?))")
		args = append(args, query.After.OccurredAt.UTC().Unix(),
			query.After.OccurredAt.UTC().Unix(), query.After.ID)
	}
	// One row beyond the page answers whether another page exists without a
	// second count query, which could otherwise disagree with this read.
	args = append(args, ActivityPageSize+1)

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, action, COALESCE(actor_email, ''), COALESCE(target_email, ''),
		       occurred_at, COALESCE(effective_at, 0), details
		FROM activity_entries
		WHERE `+strings.Join(conditions, " AND ")+`
		ORDER BY occurred_at DESC, id DESC
		LIMIT ?`, args...)
	if err != nil {
		return ActivityView{}, fmt.Errorf("read Activity: %w", err)
	}
	defer rows.Close()

	view := ActivityView{
		ProtecteeEmail: window.protecteeEmail,
		VisibleFrom:    window.from,
		Query:          query,
	}
	for rows.Next() {
		entry := ActivityEntry{ProtecteeEmail: window.protecteeEmail, visibleFrom: window.from}
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
	if len(view.Entries) > ActivityPageSize {
		last := view.Entries[ActivityPageSize-1]
		view.Entries = view.Entries[:ActivityPageSize]
		view.NextPage = ActivityCursor{OccurredAt: last.OccurredAt, ID: last.ID}
	}

	view.Actors, err = s.activityActors(ctx, protecteeID, window)
	if err != nil {
		return ActivityView{}, err
	}
	return view, nil
}

// activityActors lists the actors a viewer may filter by. It reads the window
// rather than the page, so the choices are the same on page one and page four,
// and it discloses no one the window does not already carry entries for.
func (s *Service) activityActors(ctx context.Context, protecteeID int64, window activityWindow) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT COALESCE(actor_email, '')
		FROM activity_entries
		WHERE `+windowCondition+`
		ORDER BY 1`, window.args(protecteeID)...)
	if err != nil {
		return nil, fmt.Errorf("read Activity actors: %w", err)
	}
	defer rows.Close()
	var actors []string
	nightloch := false
	for rows.Next() {
		var actor string
		if err := rows.Scan(&actor); err != nil {
			return nil, fmt.Errorf("read Activity actor: %w", err)
		}
		if actor == "" {
			nightloch = true
			continue
		}
		actors = append(actors, actor)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read Activity actors: %w", err)
	}
	if nightloch {
		actors = append(actors, ActorNightloch)
	}
	return actors, nil
}

// Summary is the entry's stable active-voice prose. It names the actor — a
// verified email, or Nightloch for its own scheduled work — the target, what
// changed, and the consequence that follows from it, because an entry a viewer
// has to interpret is not an audit record (S3-30). The exact occurrence
// instant, and the effective instant where they differ, are carried as typed
// fields rather than baked into the sentence, so every screen labels them the
// same way (S3-27). It is derived from the typed action and the attributed
// identities alone, so no stored structured payload can reach a screen through
// it (S3-49).
func (e ActivityEntry) Summary() string {
	actor := e.ActorEmail
	if actor == "" {
		actor = "Nightloch"
	}
	switch e.Action {
	case ActionInvitationSent:
		return fmt.Sprintf("%s invited %s as a Partner. No Partner powers exist until they accept.",
			actor, e.TargetEmail)
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
		return fmt.Sprintf("%s requested to end their Partnership with %s after Cooling-off. The Partnership stays active and %s keeps every Partner power until the deadline.",
			actor, e.TargetEmail, e.TargetEmail)
	case ActionCoolingOffCanceled:
		return fmt.Sprintf("%s canceled their request to end the Partnership with %s. No ending is scheduled and the Partnership continues unchanged.",
			actor, e.TargetEmail)
	case ActionPartnershipEndedAfterCoolingOff:
		return fmt.Sprintf("Nightloch ended %s’s Partnership with %s on the Cooling-off deadline %s requested.",
			e.ProtecteeEmail, e.TargetEmail, e.ProtecteeEmail)
	case ActionPartnershipEndedByUserDeletion:
		return fmt.Sprintf("%s deleted their Nightloch User, so Nightloch ended their Partnership with %s immediately. %[1]s holds no Partner power over %[2]s.",
			e.TargetEmail, e.ProtecteeEmail)
	case ActionCoolingOffBecameFinal:
		return fmt.Sprintf("Nightloch recorded that the Partnership with %s is now the last one holding %s’s Partner lock active.",
			e.TargetEmail, e.ProtecteeEmail)
	case ActionDeliveryDelayed:
		return fmt.Sprintf("Nightloch has not yet delivered a message to %s. Their Partner powers and every deadline are unchanged.",
			e.TargetEmail)
	case ActionDeliveryFailed:
		return fmt.Sprintf("Nightloch could not reach %s by email. Their Partner powers and every deadline are unchanged.",
			e.TargetEmail)
	case ActionDeliveryRecovered:
		return fmt.Sprintf("Nightloch reached %s by email again. The earlier failure stays in this chronology.",
			e.TargetEmail)
	}
	// An action with no prose is a programming error rather than a viewer's
	// problem, so the entry still reads as an attributable event instead of
	// vanishing from a chronology that claims to be complete.
	return fmt.Sprintf("%s changed %s’s protection.", actor, e.ProtecteeEmail)
}
