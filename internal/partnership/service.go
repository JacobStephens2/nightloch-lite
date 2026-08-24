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
	// ErrOnePartnerLimit is the temporary 3A exposure gate from S3-60. The
	// schema is already many-to-many; this limit is application behavior and
	// is removed in 3B.
	ErrOnePartnerLimit = errors.New("Nightloch currently supports one Partner per Protectee")
)

// invitationRefusals are the rules a Protectee can see and correct. Callers
// use IsRefusal rather than restating this list.
var invitationRefusals = []error{
	ErrInvalidEmail, ErrSelfInvitation, ErrDuplicateInvitation,
	ErrAlreadyPartnered, ErrOnePartnerLimit,
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
	livePartners, err := checkInvitationAllowed(ctx, tx, command.ProtecteeID, protecteeEmail, inviteeEmail, now)
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

	message := renderPartnerInvitation(invitationMessage{
		ProtecteeEmail: protecteeEmail,
		InviteeEmail:   inviteeEmail,
		LogSharing:     command.LogSharing,
		// The 3A exposure gate means this is always zero today. 3B lifts the
		// gate and adds one Action alert per current Partner (S3-20).
		PartnerCount: livePartners,
		ReviewURL:    s.reviewURL(rawToken),
		ExpiresAt:    expiresAt,
	})
	message.IdempotencyKey = idempotencyKey(message.Kind, message.To, tokenHash, 1)
	if err := queueDelivery(ctx, tx, deliveryJob{
		ActivityID:   activityID,
		InvitationID: &invitationID,
		Message:      message,
		CreatedAt:    now,
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

// checkInvitationAllowed applies the pending-invitation, live-Partnership, and
// temporary 3A exposure rules, and returns the Protectee's live Partner count.
// Every check is scoped to one direction, so the reverse-direction invitation
// that forms a Reciprocal Partnership stays open. An invitation past its
// seven-day expiry stops counting immediately, so a Protectee is never locked
// out of Add a Partner while waiting for the sweep that marks it expired.
func checkInvitationAllowed(ctx context.Context, tx *sql.Tx, protecteeID int64, protecteeEmail, inviteeEmail string, now time.Time) (int, error) {
	if inviteeEmail == protecteeEmail {
		return 0, ErrSelfInvitation
	}
	var duplicateInvitation, duplicatePartnership, livePartners, liveInvitations int
	err := tx.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM partner_invitations
			  WHERE protectee_id = ?1 AND state = 'pending' AND expires_at > ?3
			    AND invitee_email = ?2),
			(SELECT COUNT(*) FROM partnerships
			  JOIN users ON users.id = partnerships.partner_id
			  WHERE partnerships.protectee_id = ?1 AND partnerships.ended_at IS NULL
			    AND users.email = ?2),
			(SELECT COUNT(*) FROM partnerships
			  WHERE protectee_id = ?1 AND ended_at IS NULL),
			(SELECT COUNT(*) FROM partner_invitations
			  WHERE protectee_id = ?1 AND state = 'pending' AND expires_at > ?3)`,
		protecteeID, inviteeEmail, now.Unix()).
		Scan(&duplicateInvitation, &duplicatePartnership, &livePartners, &liveInvitations)
	if err != nil {
		return 0, fmt.Errorf("check existing Partner relationships: %w", err)
	}
	switch {
	case duplicateInvitation > 0:
		return 0, ErrDuplicateInvitation
	case duplicatePartnership > 0:
		return 0, ErrAlreadyPartnered
	case livePartners+liveInvitations > 0:
		return 0, ErrOnePartnerLimit
	}
	return livePartners, nil
}

// ProtecteeView is the authenticated Protectee's own protection state.
type ProtecteeView struct {
	// PartnerLockActive is true only while at least one Partnership is live.
	// A pending invitation never sets it (S3-42).
	PartnerLockActive  bool
	Partners           []RosterEntry
	PendingInvitations []PendingInvitation
}

// RosterEntry is one live Partnership as its Protectee sees it.
type RosterEntry struct {
	// PartnershipID names the one Partnership this row is, so the Protectee's
	// Cooling-off controls act on it without guessing from the two identities.
	PartnershipID int64
	PartnerEmail  string
	Since         time.Time
	LogSharing    bool
	// CoolingOffEndsAt is the exact deadline a requested ending will complete
	// on, and zero when no ending is scheduled. Every entitled Roster viewer
	// sees the same instant (S3-07).
	CoolingOffEndsAt time.Time
}

// CoolingOffRunning reports whether this Partnership is scheduled to end.
func (e RosterEntry) CoolingOffRunning() bool { return !e.CoolingOffEndsAt.IsZero() }

// PendingInvitation is one outstanding Partner invitation as its Protectee
// sees it. The Protectee never holds the bearer token, so ID is how they
// reach the one control they have: cancellation.
type PendingInvitation struct {
	ID            int64
	InviteeEmail  string
	LogSharing    bool
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

	invitations, err := s.db.QueryContext(ctx, `
		SELECT invitation.id, invitation.invitee_email, invitation.log_sharing,
		       invitation.last_sent_at, invitation.expires_at,
		       COALESCE((SELECT job.state FROM mail_delivery_jobs job
		                  WHERE job.invitation_id = invitation.id
		                    AND job.recipient_email = invitation.invitee_email
		                  ORDER BY job.id DESC LIMIT 1), ?)
		FROM partner_invitations invitation
		WHERE invitation.protectee_id = ? AND invitation.state = 'pending'
		  AND invitation.expires_at > ?
		ORDER BY invitation.created_at`, DeliveryQueued, protecteeID, s.now().UTC().Unix())
	if err != nil {
		return ProtecteeView{}, fmt.Errorf("read pending Partner invitations: %w", err)
	}
	defer invitations.Close()
	for invitations.Next() {
		var pending PendingInvitation
		var logSharing int
		var sentAt, expiresAt int64
		if err := invitations.Scan(&pending.ID, &pending.InviteeEmail, &logSharing, &sentAt, &expiresAt, &pending.DeliveryState); err != nil {
			return ProtecteeView{}, fmt.Errorf("read pending Partner invitation: %w", err)
		}
		pending.LogSharing = intToBool(logSharing)
		pending.SentAt = time.Unix(sentAt, 0).UTC()
		pending.ExpiresAt = time.Unix(expiresAt, 0).UTC()
		view.PendingInvitations = append(view.PendingInvitations, pending)
	}
	if err := invitations.Err(); err != nil {
		return ProtecteeView{}, fmt.Errorf("read pending Partner invitations: %w", err)
	}
	return view, nil
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
