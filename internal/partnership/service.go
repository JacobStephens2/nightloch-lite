// Package partnership owns Nightloch's relationship perimeter: Partner
// invitations, durable Partnerships, append-only Activity, the authorization
// decisions derived from them, and the per-recipient delivery jobs that carry
// their integrity signals. HTTP handlers and the background dispatcher both
// call this package instead of restating relationship rules.
package partnership

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/JacobStephens2/nightloch-lite/internal/emailaddr"
)

const (
	// invitationTTL is the approved seven-day acceptance window. Resending
	// never extends it.
	invitationTTL = 7 * 24 * time.Hour
	tokenBytes    = 32

	// MessagePartnerInvitation identifies the invitation sent to an invitee.
	MessagePartnerInvitation = "partner_invitation"
	// MessagePartnerInvitationSent is the separate Action alert each current
	// Partner receives about that same invitation. It is a different message
	// to a different audience, so it carries its own kind.
	MessagePartnerInvitationSent = "partner_invitation_sent_partner"

	// ActionInvitationSent is the stable Activity action for a sent invitation.
	ActionInvitationSent = "partner_invitation_sent"
)

var (
	// ErrInvalidEmail rejects an invitee address Nightloch cannot identify.
	ErrInvalidEmail = emailaddr.ErrInvalid
	// ErrSelfInvitation rejects a Protectee inviting their own address.
	ErrSelfInvitation = errors.New("you cannot invite your own address as a Partner")
	// ErrDuplicateInvitation rejects a second pending invitation to the same
	// address in the same direction.
	ErrDuplicateInvitation = errors.New("a Partner invitation to that address is already pending")
	// ErrAlreadyPartnered rejects an invitation duplicating a live directional
	// Partnership.
	ErrAlreadyPartnered = errors.New("that Partner already protects you")
)

// invitationRefusals are the rules a Protectee can see and correct. Callers
// use IsRefusal rather than restating this list.
var invitationRefusals = []error{
	ErrInvalidEmail, ErrSelfInvitation, ErrDuplicateInvitation, ErrAlreadyPartnered,
}

// IsRefusal reports whether err is the module refusing an invitation on a rule
// the Protectee can act on, rather than a failure. Its message is safe to show
// to the Protectee who submitted the invitation.
func IsRefusal(err error) bool {
	for _, refusal := range invitationRefusals {
		if errors.Is(err, refusal) {
			return true
		}
	}
	return false
}

// Service is the deep Partnership module. Callers pass authenticated User IDs
// and receive viewer-authorized views; transaction ordering, recipient
// selection, token hashing, and canonical rendering stay inside.
type Service struct {
	db      *sql.DB
	baseURL *url.URL
	now     func() time.Time
	random  io.Reader
}

// NewService builds a Service over the process's serialized SQLite handle.
func NewService(db *sql.DB, baseURL *url.URL) *Service {
	return &Service{db: db, baseURL: baseURL, now: time.Now, random: rand.Reader}
}

// InviteCommand is one authenticated Protectee's request to add a Partner.
type InviteCommand struct {
	ProtecteeID  int64
	InviteeEmail string
	LogSharing   bool
}

// Invitation is a created Partner invitation. RawToken is present only on the
// return path from InvitePartner; only its hash reaches SQLite.
type Invitation struct {
	ID           int64
	InviteeEmail string
	LogSharing   bool
	SentAt       time.Time
	ExpiresAt    time.Time
	RawToken     string
}

// InvitePartner validates the invitation, then commits it, its Activity
// entry, and its per-recipient delivery jobs in one SQLite transaction.
// Delivery itself is asynchronous and never rolls this back (ADR 0002).
func (s *Service) InvitePartner(ctx context.Context, command InviteCommand) (Invitation, error) {
	inviteeEmail, err := emailaddr.Normalize(command.InviteeEmail)
	if err != nil {
		return Invitation{}, err
	}
	rawToken, tokenHash, err := s.newToken()
	if err != nil {
		return Invitation{}, err
	}
	now := s.now().UTC()
	expiresAt := now.Add(invitationTTL)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Invitation{}, fmt.Errorf("begin Partner invitation: %w", err)
	}
	defer tx.Rollback()

	var protecteeEmail string
	if err := tx.QueryRowContext(ctx, "SELECT email FROM users WHERE id = ?", command.ProtecteeID).
		Scan(&protecteeEmail); err != nil {
		return Invitation{}, fmt.Errorf("read inviting Protectee: %w", err)
	}
	if err := checkInvitationAllowed(ctx, tx, command.ProtecteeID, protecteeEmail, inviteeEmail, now); err != nil {
		return Invitation{}, err
	}
	if err := settleLapsedInvitation(ctx, tx, command.ProtecteeID, inviteeEmail, now); err != nil {
		return Invitation{}, err
	}
	// The current Roster is both an audience and a disclosure: each Partner
	// receives their own Action alert, and the invitee is told how many
	// Partners already protect this Protectee without learning who they are.
	currentPartners, err := livePartnerEmails(ctx, tx, command.ProtecteeID)
	if err != nil {
		return Invitation{}, err
	}
	inviteeUserID, err := readUserID(ctx, tx, inviteeEmail)
	if err != nil {
		return Invitation{}, err
	}
	reciprocal, err := reciprocalWith(ctx, tx, command.ProtecteeID, inviteeUserID)
	if err != nil {
		return Invitation{}, err
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO partner_invitations(
			protectee_id, invitee_email, log_sharing, token_hash,
			created_at, expires_at, last_sent_at, send_count, state)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1, 'pending')`,
		command.ProtecteeID, inviteeEmail, boolToInt(command.LogSharing), tokenHash[:],
		now.Unix(), expiresAt.Unix(), now.Unix())
	if err != nil {
		return Invitation{}, fmt.Errorf("store Partner invitation: %w", err)
	}
	invitationID, err := result.LastInsertId()
	if err != nil {
		return Invitation{}, fmt.Errorf("read Partner invitation id: %w", err)
	}

	activityID, err := appendActivity(ctx, tx, activityEntry{
		ProtecteeID:  command.ProtecteeID,
		Action:       ActionInvitationSent,
		ActorUserID:  &command.ProtecteeID,
		ActorEmail:   protecteeEmail,
		TargetEmail:  inviteeEmail,
		InvitationID: &invitationID,
		OccurredAt:   now,
		Details: map[string]any{
			"log_sharing": command.LogSharing,
			"expires_at":  expiresAt.Unix(),
		},
	})
	if err != nil {
		return Invitation{}, err
	}

	// The invitee and every current Partner read different disclosures, so
	// each gets its own envelope and no recipient ever learns the others
	// (S3-20, S3-21).
	messages := []preparedMessage{renderPartnerInvitation(invitationMessage{
		ProtecteeEmail: protecteeEmail,
		InviteeEmail:   inviteeEmail,
		LogSharing:     command.LogSharing,
		PartnerCount:   len(currentPartners),
		Reciprocal:     reciprocal,
		ReviewURL:      s.reviewURL(rawToken),
		ExpiresAt:      expiresAt,
	})}
	alert := invitationAlert{
		ProtecteeEmail: protecteeEmail,
		InviteeEmail:   inviteeEmail,
		LogSharing:     command.LogSharing,
		Reciprocal:     reciprocal,
		SentAt:         now,
		ExpiresAt:      expiresAt,
		ActivityURL:    s.dashboardURL(),
	}
	for _, partnerEmail := range currentPartners {
		messages = append(messages, renderInvitationSentToPartner(alert, partnerEmail))
	}
	if err := queueInvitationMessages(ctx, tx, messages, invitationDelivery{
		ActivityID: activityID, InvitationID: invitationID, TokenHash: tokenHash, At: now,
	}); err != nil {
		return Invitation{}, err
	}

	if err := tx.Commit(); err != nil {
		return Invitation{}, fmt.Errorf("commit Partner invitation: %w", err)
	}
	return Invitation{
		ID: invitationID, InviteeEmail: inviteeEmail, LogSharing: command.LogSharing,
		SentAt: now, ExpiresAt: expiresAt, RawToken: rawToken,
	}, nil
}

// checkInvitationAllowed refuses only the two invitations that would duplicate
// a relationship the Protectee already has with this one address. Nightloch
// places no cap on how many Partners protect a Protectee, so a second address
// is always invitable. Every check is scoped to one direction, so the
// reverse-direction invitation that forms a Reciprocal Partnership stays open.
// An invitation past its seven-day expiry stops counting immediately, so a
// Protectee is never blocked by one while waiting for the sweep that marks it
// expired.
func checkInvitationAllowed(ctx context.Context, tx *sql.Tx, protecteeID int64, protecteeEmail, inviteeEmail string, now time.Time) error {
	if inviteeEmail == protecteeEmail {
		return ErrSelfInvitation
	}
	var duplicateInvitation, duplicatePartnership int
	err := tx.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM partner_invitations
			  WHERE protectee_id = ?1 AND state = 'pending' AND expires_at > ?3
			    AND invitee_email = ?2),
			(SELECT COUNT(*) FROM partnerships
			  JOIN users ON users.id = partnerships.partner_id
			  WHERE partnerships.protectee_id = ?1 AND partnerships.ended_at IS NULL
			    AND users.email = ?2)`,
		protecteeID, inviteeEmail, now.Unix()).
		Scan(&duplicateInvitation, &duplicatePartnership)
	if err != nil {
		return fmt.Errorf("check existing Partner relationships: %w", err)
	}
	switch {
	case duplicateInvitation > 0:
		return ErrDuplicateInvitation
	case duplicatePartnership > 0:
		return ErrAlreadyPartnered
	}
	return nil
}

// ProtecteeView is the authenticated Protectee's own protection state.
type ProtecteeView struct {
	// PartnerLockActive is true only while at least one Partnership is live.
	// A pending invitation never sets it (S3-42).
	PartnerLockActive  bool
	Partners           []RosterEntry
	PendingInvitations []PendingInvitation
}

// RosterEntry is one live Partnership as every entitled viewer sees it: the
// Protectee it protects and each of their active Partners read the same row
// (S3-15). It carries the six facts an active Roster row must show — verified
// email, since time, Log sharing, reciprocal state, Partner reachability, and
// any Cooling-off deadline — and no control for it, because who may act on a
// Partnership depends on who is reading, not on the row.
type RosterEntry struct {
	// PartnershipID names the one Partnership this row is, so the Protectee's
	// Cooling-off controls act on it without guessing from the two identities.
	PartnershipID int64
	PartnerEmail  string
	Since         time.Time
	LogSharing    bool
	// Reciprocal is true when this Partner is also protected by this
	// Protectee, which is a second, independent Partnership.
	Reciprocal bool
	// Reachability is whether Nightloch's integrity messages are reaching this
	// Partner. It is a visible warning only and never changes their powers or
	// any lifecycle deadline (S3-58).
	Reachability Reachability
	// CoolingOffEndsAt is the exact deadline a requested ending will complete
	// on, and zero when no ending is scheduled. Every entitled Roster viewer
	// sees the same instant (S3-07).
	CoolingOffEndsAt time.Time
	// PartnersAfterDeadline is how many Partners still protect the Protectee
	// once this row's deadline arrives. Concurrent countdowns share one
	// horizon, so this is not "every other Partner": a Partnership already
	// scheduled to end at or before this deadline is not there afterwards
	// either. A row with no scheduled ending simply counts the others.
	PartnersAfterDeadline int
}

// CoolingOffRunning reports whether this Partnership is scheduled to end.
func (e RosterEntry) CoolingOffRunning() bool { return !e.CoolingOffEndsAt.IsZero() }

// EndsPartnerLock reports whether this row's scheduled ending is the one that
// deactivates the Protectee's Partner lock and restores self-loosening. It is
// the dynamic final-Partnership question: an ending that is not final today
// becomes final when the Partnerships outlasting it end first.
func (e RosterEntry) EndsPartnerLock() bool {
	return e.CoolingOffRunning() && e.PartnersAfterDeadline == 0
}

// PendingInvitation is one outstanding Partner invitation as the Protectee who
// sent it and each of their current Partners see it. The Protectee never holds
// the bearer token, so ID is how they reach the one control anyone has:
// cancellation. A Partner reading the same row holds no control at all.
type PendingInvitation struct {
	ID int64
	// InviterEmail is the Protectee who sent it, so a Partner reading another
	// person's pending invitation is never left guessing who invited whom.
	InviterEmail string
	InviteeEmail string
	LogSharing   bool
	// Reciprocal is whether acceptance would also make the invitee a Protectee
	// of this Protectee.
	Reciprocal    bool
	SentAt        time.Time
	ExpiresAt     time.Time
	DeliveryState DeliveryState
}

// ProtecteeView returns the Partner lock, Roster, and pending invitations for
// the User's own protection.
func (s *Service) ProtecteeView(ctx context.Context, protecteeID int64) (ProtecteeView, error) {
	var view ProtecteeView
	partners, err := s.roster(ctx, protecteeID)
	if err != nil {
		return ProtecteeView{}, err
	}
	view.Partners = partners
	view.PartnerLockActive = len(view.Partners) > 0

	pending, err := s.pendingInvitations(ctx, protecteeID)
	if err != nil {
		return ProtecteeView{}, err
	}
	view.PendingInvitations = pending
	return view, nil
}

// pendingInvitations lists one Protectee's outstanding invitations. The
// Protectee and each of their current Partners are entitled to the same row
// (S3-15), so both views read it here rather than restating the projection.
func (s *Service) pendingInvitations(ctx context.Context, protecteeID int64) ([]PendingInvitation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT invitation.id, protectee.email, invitation.invitee_email,
		       invitation.log_sharing, invitation.last_sent_at, invitation.expires_at,
		       `+reciprocalOfInvitation+`,
		       COALESCE((SELECT job.state FROM mail_delivery_jobs job
		                  WHERE job.invitation_id = invitation.id
		                    AND job.recipient_email = invitation.invitee_email
		                  ORDER BY job.id DESC LIMIT 1), ?)
		FROM partner_invitations invitation
		JOIN users protectee ON protectee.id = invitation.protectee_id
		WHERE invitation.protectee_id = ? AND invitation.state = 'pending'
		  AND invitation.expires_at > ?
		ORDER BY invitation.created_at, invitation.id`,
		DeliveryQueued, protecteeID, s.now().UTC().Unix())
	if err != nil {
		return nil, fmt.Errorf("read pending Partner invitations: %w", err)
	}
	defer rows.Close()

	var pending []PendingInvitation
	for rows.Next() {
		var invitation PendingInvitation
		var logSharing, reciprocal int
		var sentAt, expiresAt int64
		if err := rows.Scan(&invitation.ID, &invitation.InviterEmail, &invitation.InviteeEmail,
			&logSharing, &sentAt, &expiresAt, &reciprocal, &invitation.DeliveryState); err != nil {
			return nil, fmt.Errorf("read pending Partner invitation: %w", err)
		}
		invitation.LogSharing = intToBool(logSharing)
		invitation.Reciprocal = intToBool(reciprocal)
		invitation.SentAt = time.Unix(sentAt, 0).UTC()
		invitation.ExpiresAt = time.Unix(expiresAt, 0).UTC()
		pending = append(pending, invitation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read pending Partner invitations: %w", err)
	}
	return pending, nil
}

// CanLoosen answers whether actorID may loosen protecteeID's Policy. It is
// deny-closed: an unpartnered Protectee may loosen their own Policy, a
// Protectee with any live Partnership may not, any live Partner of that
// Protectee may act independently, and an error or unknown relationship state
// never permits a loosen.
func (s *Service) CanLoosen(ctx context.Context, actorID, protecteeID int64) (bool, error) {
	if actorID <= 0 || protecteeID <= 0 {
		return false, nil
	}
	var livePartnerships, actorPartnerships int
	err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM partnerships WHERE protectee_id = ?1 AND ended_at IS NULL),
			(SELECT COUNT(*) FROM partnerships
			  WHERE protectee_id = ?1 AND partner_id = ?2 AND ended_at IS NULL)`,
		protecteeID, actorID).Scan(&livePartnerships, &actorPartnerships)
	if err != nil {
		return false, fmt.Errorf("read Partner lock state: %w", err)
	}
	if actorID == protecteeID {
		return livePartnerships == 0, nil
	}
	return actorPartnerships > 0, nil
}

func (s *Service) reviewURL(rawToken string) string {
	link := *s.baseURL
	link.Path = "/partners/invitations/" + rawToken
	return link.String()
}

func (s *Service) newToken() (string, [32]byte, error) {
	buffer := make([]byte, tokenBytes)
	if _, err := io.ReadFull(s.random, buffer); err != nil {
		return "", [32]byte{}, fmt.Errorf("generate invitation token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), sha256.Sum256(buffer), nil
}

// idempotencyKey is stable for one message to one recipient for one send, so
// a retry after a crashed process cannot become a second provider send.
func idempotencyKey(kind, recipient string, tokenHash [32]byte, sequence int) string {
	digest := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%x\x00%d", kind, recipient, tokenHash, sequence))
	return "nightloch-" + kind + "-" + hex.EncodeToString(digest[:])
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func intToBool(value int) bool {
	return value == 1
}
