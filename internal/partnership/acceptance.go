package partnership

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// ActionInvitationAccepted is the stable Activity action for the
	// acceptance that creates a durable Partnership.
	ActionInvitationAccepted = "partner_invitation_accepted"

	// MessagePartnershipAcceptedExisting and MessagePartnershipAcceptedPartner
	// are the two separate acceptance messages, named for their audiences: the
	// Protectee and their prior Partners read the first, the Partner who just
	// accepted reads the second. Recipients never share an envelope, so no
	// recipient list is ever exposed (S3-21).
	MessagePartnershipAcceptedExisting = "partnership_accepted_existing"
	MessagePartnershipAcceptedPartner  = "partnership_accepted_partner"

	// edgeTypePeer is the only relationship implemented in v1. The `guardian`
	// value stays reserved in the schema (F33).
	edgeTypePeer = "peer"
)

var (
	// ErrInvitationUnavailable is the single answer for a reference Nightloch
	// will not resolve: a malformed or unknown token, an id belonging to
	// someone else, or a mutation that lost its race. It deliberately
	// distinguishes none of those. A reference that does resolve reports its
	// precise state instead, so a valid token names its own outcome (S3-29).
	ErrInvitationUnavailable = errors.New("that Partner invitation is unavailable")
	// ErrInvitationNotYours refuses a signed-in User whose verified email is
	// not the invited address.
	ErrInvitationNotYours = errors.New("that Partner invitation was sent to a different address")
)

// InvitationReview is the disclosure for one invitation the viewer reached,
// pending or terminal. It is a pure read: opening a bearer link never mutates
// state (S3-28). The invited address stays masked until a matching verified
// User authenticates, so neither a leaked link nor a settled outcome discloses
// who was invited.
type InvitationReview struct {
	// ID lets an authenticated invitee act on this invitation from their own
	// **Partner invitations** list, without the bearer token.
	ID    int64
	State InvitationState
	// ResolvedAt is when a terminal invitation reached its outcome, and zero
	// while it is still pending.
	ResolvedAt         time.Time
	ProtecteeEmail     string
	MaskedInviteeEmail string
	// InviteeEmail is populated only once ViewerMatches is true.
	InviteeEmail string
	LogSharing   bool
	// OtherPartnerCount is how many Partners already protect the Protectee.
	// Their identities are withheld until acceptance (S3-06).
	OtherPartnerCount int
	// Reciprocal is whether accepting would create a second, independent
	// Partnership in the opposite direction, because the Protectee who sent
	// this invitation already protects the invitee. It names a relationship
	// the invitee is part of, so it stays behind ViewerMatches: a bearer link
	// alone never discloses it (S3-04).
	Reciprocal bool
	ExpiresAt  time.Time
	// ViewerEmail is the signed-in User's verified email, empty when nobody
	// is signed in.
	ViewerEmail   string
	ViewerMatches bool
}

// MayAccept and MayDecline report whether this viewer holds the invitee's
// controls. Only the invited User ever does; the Protectee cancels instead,
// and everyone else — including an existing Partner — may look but not act.
func (r InvitationReview) MayAccept() bool { return r.State == InvitationPending && r.ViewerMatches }

// MayDecline mirrors MayAccept: the two invitee controls always appear together.
func (r InvitationReview) MayDecline() bool { return r.MayAccept() }

// Outcome is the invitation's state as a plain string, so a template can pick
// the approved copy for it without importing this package's types.
func (r InvitationReview) Outcome() string { return string(r.State) }

// ReviewInvitation renders the approved disclosure for one invitation without
// touching any state. A pending invitation shows what accepting would mean; a
// terminal one names its precise outcome.
func (s *Service) ReviewInvitation(ctx context.Context, ref InvitationRef, viewerID int64) (InvitationReview, error) {
	now := s.now().UTC()
	viewerEmail, err := readUserEmail(ctx, s.db, viewerID)
	if err != nil {
		return InvitationReview{}, err
	}
	invitation, err := resolveInvitation(ctx, s.db, ref, viewerEmail, now)
	if err != nil {
		return InvitationReview{}, err
	}

	review := InvitationReview{
		ID:                 invitation.id,
		State:              invitation.state,
		ResolvedAt:         invitation.resolvedAt,
		ProtecteeEmail:     invitation.protecteeEmail,
		MaskedInviteeEmail: maskEmail(invitation.inviteeEmail),
		LogSharing:         invitation.logSharing,
		ExpiresAt:          invitation.expiresAt,
		ViewerEmail:        viewerEmail,
		ViewerMatches:      viewerEmail != "" && viewerEmail == invitation.inviteeEmail,
	}
	// Masking is the whole point of the review screen: withhold the invited
	// address from everyone except the User who was actually invited.
	if review.ViewerMatches {
		review.InviteeEmail = invitation.inviteeEmail
	}
	if invitation.state != InvitationPending {
		// A settled invitation discloses its outcome and nothing more; the
		// Roster shape describes a Partnership decision nobody can still make.
		return review, nil
	}

	// Before acceptance the invitee learns only how many Partners already
	// protect this Protectee, never who they are (S3-06).
	partnerCount, err := countLivePartners(ctx, s.db, invitation.protecteeID)
	if err != nil {
		return InvitationReview{}, err
	}
	review.OtherPartnerCount = partnerCount
	if review.ViewerMatches {
		// The reverse direction already exists or it does not; either way it
		// is the invitee's own relationship, so only they are told about it.
		reciprocal, err := reciprocalWith(ctx, s.db, invitation.protecteeID, viewerID)
		if err != nil {
			return InvitationReview{}, err
		}
		review.Reciprocal = reciprocal
	}
	return review, nil
}

// Acceptance is the durable Partnership an invitee just created.
type Acceptance struct {
	PartnershipID  int64
	ProtecteeID    int64
	ProtecteeEmail string
	AcceptedAt     time.Time
}

// AcceptInvitation atomically terminates the invitation, creates the durable
// Partnership, appends Activity, and queues one message per recipient. The
// invitation's pending state is the concurrency token, so a replayed or racing
// acceptance changes nothing and answers ErrInvitationUnavailable.
func (s *Service) AcceptInvitation(ctx context.Context, ref InvitationRef, viewerID int64) (Acceptance, error) {
	if viewerID <= 0 {
		return Acceptance{}, ErrInvitationNotYours
	}
	now := s.now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Acceptance{}, fmt.Errorf("begin Partnership acceptance: %w", err)
	}
	defer tx.Rollback()

	viewerEmail, err := readUserEmail(ctx, tx, viewerID)
	if err != nil {
		return Acceptance{}, err
	}
	invitation, err := resolveInvitation(ctx, tx, ref, viewerEmail, now)
	if err != nil {
		return Acceptance{}, err
	}
	if viewerEmail == "" || viewerEmail != invitation.inviteeEmail {
		return Acceptance{}, ErrInvitationNotYours
	}
	// First commit wins: only the transaction that still sees 'pending' may
	// resolve the invitation, and every later mutation hangs off that row.
	if err := resolveState(ctx, tx, invitation, InvitationAccepted, now, &viewerID); err != nil {
		return Acceptance{}, err
	}
	invitationID, protecteeID := invitation.id, invitation.protecteeID
	protecteeEmail, inviteeEmail := invitation.protecteeEmail, invitation.inviteeEmail
	logSharing := boolToInt(invitation.logSharing)
	tokenHash := invitation.tokenHash

	created, err := tx.ExecContext(ctx, `
		INSERT INTO partnerships(protectee_id, partner_id, edge_type, log_sharing, accepted_at)
		VALUES (?, ?, ?, ?, ?)`, protecteeID, viewerID, edgeTypePeer, logSharing, now.Unix())
	if err != nil {
		return Acceptance{}, fmt.Errorf("create Partnership: %w", err)
	}
	partnershipID, err := created.LastInsertId()
	if err != nil {
		return Acceptance{}, fmt.Errorf("read Partnership id: %w", err)
	}

	// The Roster now includes the Partner who just accepted, so it answers
	// both the count every recipient is told and who the prior Partners are.
	roster, err := livePartnerEmails(ctx, tx, protecteeID)
	if err != nil {
		return Acceptance{}, err
	}
	partnerCount := len(roster)
	// Reciprocal is the reverse direction: this Protectee also protects the
	// Partner who just accepted.
	reciprocal, err := reciprocalWith(ctx, tx, protecteeID, viewerID)
	if err != nil {
		return Acceptance{}, err
	}

	activityID, err := appendActivity(ctx, tx, activityEntry{
		ProtecteeID:   protecteeID,
		Action:        ActionInvitationAccepted,
		ActorUserID:   &viewerID,
		ActorEmail:    inviteeEmail,
		TargetEmail:   protecteeEmail,
		PartnershipID: &partnershipID,
		InvitationID:  &invitationID,
		OccurredAt:    now,
		Details: map[string]any{
			"log_sharing":   intToBool(logSharing),
			"reciprocal":    reciprocal,
			"partner_count": partnerCount,
		},
	})
	if err != nil {
		return Acceptance{}, err
	}

	acceptance := acceptanceMessage{
		ProtecteeEmail:  protecteeEmail,
		NewPartnerEmail: inviteeEmail,
		LogSharing:      intToBool(logSharing),
		Reciprocal:      reciprocal,
		PartnerCount:    partnerCount,
		AcceptedAt:      now,
		DashboardURL:    s.dashboardURL(),
	}
	// Excluding the Partnership just created asks the question as it stood a
	// moment ago: was the Partner lock already scheduled to deactivate? If it
	// was, this acceptance is exactly what keeps it alive past that instant,
	// and the alert says so rather than leaving recipients with the ending
	// they have already been told to expect.
	schedule, scheduled, err := readLockEndingSchedule(ctx, tx, protecteeID, partnershipID)
	if err != nil {
		return Acceptance{}, err
	}
	if scheduled && schedule.endsAt.After(now) {
		acceptance.LockEndsAt = schedule.endsAt
		acceptance.LockEndingPartners = schedule.partners
	}
	// The new Partner reads a different disclosure from everyone else, and
	// every recipient gets their own envelope, so no recipient learns who else
	// was told (S3-20, S3-21). The prior Partners are entitled to the same
	// alert as the Protectee: a new Partner acts independently of them.
	messages := []preparedMessage{
		renderAcceptanceToProtecteeAndPriorPartners(acceptance, protecteeEmail),
		renderAcceptanceToNewPartner(acceptance),
	}
	for _, priorPartner := range roster {
		if priorPartner != inviteeEmail {
			messages = append(messages,
				renderAcceptanceToProtecteeAndPriorPartners(acceptance, priorPartner))
		}
	}
	if err := queueInvitationMessages(ctx, tx, messages, invitationDelivery{
		ActivityID: activityID, InvitationID: invitationID,
		PartnershipID: &partnershipID, TokenHash: tokenHash, At: now,
	}); err != nil {
		return Acceptance{}, err
	}

	if err := tx.Commit(); err != nil {
		return Acceptance{}, fmt.Errorf("commit Partnership acceptance: %w", err)
	}
	return Acceptance{
		PartnershipID: partnershipID, ProtecteeID: protecteeID,
		ProtecteeEmail: protecteeEmail, AcceptedAt: now,
	}, nil
}

// ProtecteeEntry is one Protectee as their Partner sees them under **People
// you protect**, including that Protectee's full Roster. Both become visible
// only once the Partnership exists (S3-06, S3-15).
type ProtecteeEntry struct {
	// PartnershipID names the one Partnership this row is, so the Partner's
	// own ending control acts on it without guessing from the two identities.
	PartnershipID  int64
	ProtecteeID    int64
	ProtecteeEmail string
	Since          time.Time
	LogSharing     bool
	// Reciprocal is true when this Protectee is also one of the reader's own
	// Partners, which is a second, independent Partnership with its own Log
	// sharing, deadlines, and ending. Marking it in both areas is what keeps
	// a mixed-role dashboard readable without implying merged powers (S3-44).
	Reciprocal bool
	// scheduledEnding is whatever ending this Protectee has requested for this
	// Partnership. The Partner keeps every power until its deadline arrives,
	// and it is the same value their row on the Protectee's own Roster
	// carries, so the two sides never disagree about what it means.
	scheduledEnding
	Roster []RosterEntry
	// PendingInvitations is this Protectee's outstanding invitations. A
	// Partner may inspect them but holds no control over them (S3-15).
	PendingInvitations []PendingInvitation
}

// ProtectingView returns the Protectees partnerID actively protects.
func (s *Service) ProtectingView(ctx context.Context, partnerID int64) ([]ProtecteeEntry, error) {
	if partnerID <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT partnerships.id, partnerships.protectee_id, users.email,
		       partnerships.accepted_at, partnerships.log_sharing,
		       COALESCE(partnerships.cooling_off_ends_at, 0),
		       `+reciprocalOfPartnership+`
		FROM partnerships JOIN users ON users.id = partnerships.protectee_id
		WHERE partnerships.partner_id = ? AND partnerships.ended_at IS NULL
		ORDER BY partnerships.accepted_at`, partnerID)
	if err != nil {
		return nil, fmt.Errorf("read People you protect: %w", err)
	}
	defer rows.Close()

	var protecting []ProtecteeEntry
	for rows.Next() {
		var person ProtecteeEntry
		var since, endsAt int64
		var logSharing, reciprocal int
		if err := rows.Scan(&person.PartnershipID, &person.ProtecteeID, &person.ProtecteeEmail,
			&since, &logSharing, &endsAt, &reciprocal); err != nil {
			return nil, fmt.Errorf("read protected Protectee: %w", err)
		}
		person.Since = time.Unix(since, 0).UTC()
		person.LogSharing = intToBool(logSharing)
		person.Reciprocal = intToBool(reciprocal)
		if endsAt != 0 {
			person.CoolingOffEndsAt = time.Unix(endsAt, 0).UTC()
		}
		protecting = append(protecting, person)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read People you protect: %w", err)
	}
	for index, person := range protecting {
		roster, err := s.roster(ctx, person.ProtecteeID)
		if err != nil {
			return nil, err
		}
		pending, err := s.pendingInvitations(ctx, person.ProtecteeID)
		if err != nil {
			return nil, err
		}
		protecting[index].Roster = roster
		protecting[index].PendingInvitations = pending
		// The Partner's own row on that Roster already carries the horizon
		// this Partnership's deadline sits in, so it is read from there rather
		// than derived a second way that could disagree with it.
		for _, entry := range roster {
			if entry.PartnershipID == person.PartnershipID {
				protecting[index].scheduledEnding = entry.scheduledEnding
				break
			}
		}
	}
	return protecting, nil
}

// CanViewLogs answers whether actorID may view protecteeID's DNS domain log.
// It is deny-closed and reports only the authorization decision; no domain log
// passes through this module. A Protectee always reaches their own log; a
// Partner reaches it only through a live Partnership whose stored Log-sharing
// choice is on (S3-11).
func (s *Service) CanViewLogs(ctx context.Context, actorID, protecteeID int64) (bool, error) {
	if actorID <= 0 || protecteeID <= 0 {
		return false, nil
	}
	if actorID == protecteeID {
		return true, nil
	}
	var sharing int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM partnerships
		WHERE protectee_id = ? AND partner_id = ? AND ended_at IS NULL AND log_sharing = 1`,
		protecteeID, actorID).Scan(&sharing)
	if err != nil {
		return false, fmt.Errorf("read Log-sharing authorization: %w", err)
	}
	return sharing > 0, nil
}

// roster is the Protectee's active Roster, identical for every entitled
// viewer. Reachability reads the newest job with an actual outcome and skips
// queued ones: having more to say to an address is not evidence that anything
// reached it, so a fresh alert must not quiet a standing warning.
// Reciprocal state and reachability are derived here rather than by
// the caller, so the Protectee and each of their Partners cannot disagree
// about the same row. Reachability follows the address rather than this one
// Partnership, because the question a Roster row answers is whether Nightloch
// can reach that Partner at all — an alert about someone else's Partnership
// bounces from the same mailbox.
func (s *Service) roster(ctx context.Context, protecteeID int64) ([]RosterEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT partnerships.id, users.email, partnerships.accepted_at,
		       partnerships.log_sharing, COALESCE(partnerships.cooling_off_ends_at, 0),
		       `+reciprocalOfPartnership+`, `+partnersAfterDeadlineProjection+`,
		       COALESCE((SELECT job.state FROM mail_delivery_jobs job
		                  WHERE job.recipient_email = users.email
		                    AND job.state <> ?
		                  ORDER BY COALESCE(job.last_event_at, job.sent_at, job.created_at) DESC,
		                           job.id DESC
		                  LIMIT 1), ?)
		FROM partnerships JOIN users ON users.id = partnerships.partner_id
		WHERE partnerships.protectee_id = ? AND partnerships.ended_at IS NULL
		ORDER BY partnerships.accepted_at, partnerships.id`,
		DeliveryQueued, DeliveryQueued, protecteeID)
	if err != nil {
		return nil, fmt.Errorf("read Roster: %w", err)
	}
	defer rows.Close()
	var roster []RosterEntry
	for rows.Next() {
		var entry RosterEntry
		var since, endsAt int64
		var logSharing, reciprocal int
		var delivery DeliveryState
		if err := rows.Scan(&entry.PartnershipID, &entry.PartnerEmail, &since, &logSharing,
			&endsAt, &reciprocal, &entry.PartnersAfterDeadline, &delivery); err != nil {
			return nil, fmt.Errorf("read Roster row: %w", err)
		}
		entry.Since = time.Unix(since, 0).UTC()
		entry.LogSharing = intToBool(logSharing)
		entry.Reciprocal = intToBool(reciprocal)
		entry.Reachability = reachabilityOf(delivery)
		if endsAt != 0 {
			entry.CoolingOffEndsAt = time.Unix(endsAt, 0).UTC()
		}
		roster = append(roster, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read Roster: %w", err)
	}
	return roster, nil
}

// activityURL is where a current Partner reads one Protectee's chronology. It
// is the destination for the Action alerts whose approved copy offers Activity
// alone — the invitation a Protectee sent, and its cancellation — because the
// change those readers were told about belongs to that Protectee's history,
// not to their own dashboard. Alerts offering the Roster as well keep
// dashboardURL, which is where a Roster is read.
func (s *Service) activityURL(protecteeID int64) string {
	link := *s.baseURL
	link.Path = "/activity/" + strconv.FormatInt(protecteeID, 10)
	return link.String()
}

func (s *Service) dashboardURL() string {
	link := *s.baseURL
	link.Path = "/"
	return link.String()
}

// countLivePartners is how many Partners currently protect protecteeID. It is
// the one number an invitee learns before acceptance (S3-06) and the count
// every acceptance disclosure is stated in.
func countLivePartners(ctx context.Context, queryer rowQueryer, protecteeID int64) (int, error) {
	var partners int
	if err := queryer.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM partnerships WHERE protectee_id = ? AND ended_at IS NULL",
		protecteeID).Scan(&partners); err != nil {
		return 0, fmt.Errorf("read active Partner count: %w", err)
	}
	return partners, nil
}

// reciprocalWith reports whether a Partnership from inviteeID to protecteeID
// would be reciprocal — that is, whether protecteeID already holds a live
// Partnership over inviteeID in the opposite direction. Partnerships are
// directional, so this is the single question behind "reciprocal": the
// reverse-direction Partnership either exists or it does not. Every caller
// asks it in this order, invited party last, so no disclosure can state the
// relationship backwards. An inviteeID of zero is an address that has never
// signed in, which is protected by nobody.
func reciprocalWith(ctx context.Context, queryer rowQueryer, protecteeID, inviteeID int64) (bool, error) {
	if protecteeID <= 0 || inviteeID <= 0 {
		return false, nil
	}
	var live int
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM partnerships
		WHERE protectee_id = ? AND partner_id = ? AND ended_at IS NULL`,
		inviteeID, protecteeID).Scan(&live); err != nil {
		return false, fmt.Errorf("read reverse-direction Partnership: %w", err)
	}
	return live > 0, nil
}

// reciprocalOfPartnership and reciprocalOfInvitation are the same question the
// row projections ask in SQL, kept here beside reciprocalWith so the live-only
// condition that defines "reciprocal" is stated in one place. The first
// correlates to a Partnership row aliased `partnerships`; the second to an
// invitation row aliased `invitation`, matched by invited address rather than
// User id because an invitee need not have signed in yet.
const reciprocalOfPartnership = `EXISTS(SELECT 1 FROM partnerships reverse
	               WHERE reverse.protectee_id = partnerships.partner_id
	                 AND reverse.partner_id = partnerships.protectee_id
	                 AND reverse.ended_at IS NULL)`

const reciprocalOfInvitation = `EXISTS(SELECT 1 FROM partnerships reverse
	               JOIN users invited ON invited.id = reverse.protectee_id
	               WHERE invited.email = invitation.invitee_email
	                 AND reverse.partner_id = invitation.protectee_id
	                 AND reverse.ended_at IS NULL)`

// rowQueryer is satisfied by both *sql.DB and *sql.Tx.
type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// ValidToken reports whether raw has the shape of an invitation bearer token.
// The HTTP layer uses it to avoid persisting junk as a sign-in return
// destination; the token's real authority is only ever proven against SQLite.
func ValidToken(raw string) bool {
	_, err := hashInvitationToken(raw)
	return err == nil
}

// maskEmail hides most of an invited address while leaving enough for the
// invited person to recognize their own. It is the only form shown before a
// matching verified User authenticates.
func maskEmail(address string) string {
	local, domain, found := strings.Cut(address, "@")
	if !found || local == "" {
		return "•••"
	}
	masked := local[:1] + "•••"
	if len(local) > 1 {
		masked += local[len(local)-1:]
	}
	return masked + "@" + domain
}

// hashInvitationToken folds a malformed token into the same unavailable answer
// a stranger's guess receives, so the shape of a token is never confirmed.
func hashInvitationToken(rawToken string) ([32]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(rawToken)
	if err != nil || len(decoded) != tokenBytes {
		return [32]byte{}, ErrInvitationUnavailable
	}
	return sha256.Sum256(decoded), nil
}
