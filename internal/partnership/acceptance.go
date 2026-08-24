package partnership

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// ActionInvitationAccepted is the stable Activity action for the
	// acceptance that creates a durable Partnership.
	ActionInvitationAccepted = "partner_invitation_accepted"

	// MessagePartnershipAcceptedProtectee and MessagePartnershipAcceptedPartner
	// are the two separate acceptance messages. Recipients never share an
	// envelope, so no recipient list is ever exposed (S3-21).
	MessagePartnershipAcceptedProtectee = "partnership_accepted_protectee"
	MessagePartnershipAcceptedPartner   = "partnership_accepted_partner"

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
	ExpiresAt         time.Time
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

	// The reverse-direction count belongs to the acceptance disclosure, which
	// 3B adds once a Reciprocal Partnership is reachable.
	inviteeUserID, err := readUserID(ctx, s.db, invitation.inviteeEmail)
	if err != nil {
		return InvitationReview{}, err
	}
	partnerCount, _, err := readRosterShape(ctx, s.db, invitation.protecteeID, inviteeUserID)
	if err != nil {
		return InvitationReview{}, err
	}
	review.OtherPartnerCount = partnerCount
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

	partnerCount, reciprocal, err := readRosterShape(ctx, tx, protecteeID, viewerID)
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
	// The Protectee and the new Partner receive different disclosures, so each
	// gets its own envelope; 3B adds one more per prior Partner (S3-20).
	messages := []preparedMessage{
		renderAcceptanceToProtectee(acceptance),
		renderAcceptanceToNewPartner(acceptance),
	}
	for sequence, message := range messages {
		message.IdempotencyKey = idempotencyKey(message.Kind, message.To, tokenHash, sequence+1)
		if err := queueDelivery(ctx, tx, deliveryJob{
			ActivityID:    activityID,
			InvitationID:  &invitationID,
			PartnershipID: &partnershipID,
			Message:       message,
			CreatedAt:     now,
		}); err != nil {
			return Acceptance{}, err
		}
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
	// CoolingOffEndsAt is the deadline this Protectee requested for this
	// Partnership, and zero when they have requested nothing. The Partner
	// keeps every power until it arrives.
	CoolingOffEndsAt time.Time
	Roster           []RosterEntry
}

// CoolingOffRunning reports whether this Protectee has asked to end this
// Partnership, which turns the Partner's ending control into **End now**.
func (e ProtecteeEntry) CoolingOffRunning() bool { return !e.CoolingOffEndsAt.IsZero() }

// ProtectingView returns the Protectees partnerID actively protects.
func (s *Service) ProtectingView(ctx context.Context, partnerID int64) ([]ProtecteeEntry, error) {
	if partnerID <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT partnerships.id, partnerships.protectee_id, users.email,
		       partnerships.accepted_at, partnerships.log_sharing,
		       COALESCE(partnerships.cooling_off_ends_at, 0)
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
		var logSharing int
		if err := rows.Scan(&person.PartnershipID, &person.ProtecteeID, &person.ProtecteeEmail,
			&since, &logSharing, &endsAt); err != nil {
			return nil, fmt.Errorf("read protected Protectee: %w", err)
		}
		person.Since = time.Unix(since, 0).UTC()
		person.LogSharing = intToBool(logSharing)
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
		protecting[index].Roster = roster
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

func (s *Service) roster(ctx context.Context, protecteeID int64) ([]RosterEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT partnerships.id, users.email, partnerships.accepted_at,
		       partnerships.log_sharing, COALESCE(partnerships.cooling_off_ends_at, 0)
		FROM partnerships JOIN users ON users.id = partnerships.partner_id
		WHERE partnerships.protectee_id = ? AND partnerships.ended_at IS NULL
		ORDER BY partnerships.accepted_at`, protecteeID)
	if err != nil {
		return nil, fmt.Errorf("read Roster: %w", err)
	}
	defer rows.Close()
	var roster []RosterEntry
	for rows.Next() {
		var entry RosterEntry
		var since, endsAt int64
		var logSharing int
		if err := rows.Scan(&entry.PartnershipID, &entry.PartnerEmail, &since, &logSharing, &endsAt); err != nil {
			return nil, fmt.Errorf("read Roster row: %w", err)
		}
		entry.Since = time.Unix(since, 0).UTC()
		entry.LogSharing = intToBool(logSharing)
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

func (s *Service) dashboardURL() string {
	link := *s.baseURL
	link.Path = "/"
	return link.String()
}

// readRosterShape reports how many Partners currently protect protecteeID and
// whether partnerID already protects them in the opposite direction. Both the
// review disclosure and the acceptance messages need the same two numbers, and
// acceptance needs them inside its transaction, so it takes either handle.
func readRosterShape(ctx context.Context, queryer rowQueryer, protecteeID, partnerID int64) (partners int, reciprocal bool, err error) {
	var reverse int
	if err := queryer.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM partnerships WHERE protectee_id = ?1 AND ended_at IS NULL),
			(SELECT COUNT(*) FROM partnerships
			  WHERE protectee_id = ?2 AND partner_id = ?1 AND ended_at IS NULL)`,
		protecteeID, partnerID).Scan(&partners, &reverse); err != nil {
		return 0, false, fmt.Errorf("read Roster shape: %w", err)
	}
	return partners, reverse > 0, nil
}

// rowQueryer is satisfied by both *sql.DB and *sql.Tx.
type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
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
