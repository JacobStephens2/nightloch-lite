package partnership

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MessagePartnerInvitationResent is the reminder the invitee alone receives.
// It carries its own kind because it is a different message from the original
// send, and because the pending row shows the delivery state of whichever send
// is most recent (S3-20).
const MessagePartnerInvitationResent = "partner_invitation_resent"

// ActionInvitationResent is the stable Activity action for a resend.
const ActionInvitationResent = "partner_invitation_resent"

// Resend is the receipt a Protectee gets for reminding one invitee. ExpiresAt
// is the original invitation's unchanged deadline, which is the fact the
// receipt exists to state.
type Resend struct {
	InvitationID int64
	InviteeEmail string
	ResentAt     time.Time
	ExpiresAt    time.Time
	// RawToken is the fresh bearer link, present only on this return path.
	RawToken string
}

// ResendInvitation puts a pending invitation in front of its invitee again
// without changing anything about the invitation itself. The invited address,
// the proposed Log sharing, and the seven-day expiry are immutable, so a
// resend is a repeat rather than a correction; correcting either field is
// still a cancellation plus a fresh invitation (S3-66).
//
// Only the invitee is written to. Every current Partner was already told this
// invitation exists, and a reminder is not a new Action for them to observe,
// so the invitation-sent alert is not repeated (S3-20).
//
// A caller who is not the inviting Protectee, or an invitation that is no
// longer pending, answers ErrInvitationUnavailable rather than a refusal that
// would confirm the invitation exists.
func (s *Service) ResendInvitation(ctx context.Context, invitationID, protecteeID int64) (Resend, error) {
	if invitationID <= 0 || protecteeID <= 0 {
		return Resend{}, ErrInvitationUnavailable
	}
	rawToken, tokenHash, err := s.newToken()
	if err != nil {
		return Resend{}, err
	}
	now := s.now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Resend{}, fmt.Errorf("begin Partner invitation resend: %w", err)
	}
	defer tx.Rollback()

	protecteeEmail, err := readUserEmail(ctx, tx, protecteeID)
	if err != nil {
		return Resend{}, err
	}
	invitation, err := resolveInvitationByID(ctx, tx, invitationID, now)
	if errors.Is(err, ErrInvitationUnavailable) || protecteeEmail == "" {
		return Resend{}, ErrInvitationUnavailable
	}
	if err != nil {
		return Resend{}, err
	}
	if invitation.protecteeID != protecteeID || invitation.state != InvitationPending {
		return Resend{}, ErrInvitationUnavailable
	}
	// The per-invitation interval is checked before the Protectee-wide window,
	// because it is the more specific rule and the one whose remedy — wait for
	// this invitation's own next due time — is the more useful answer.
	if now.Before(invitation.lastSentAt.Add(resendInterval)) {
		return Resend{}, ErrResendTooSoon
	}
	if err := checkSendQuota(ctx, tx, protecteeID, now); err != nil {
		return Resend{}, err
	}

	// The invitee's own bearer link is a credential, not part of the
	// invitation: replacing it on every send keeps exactly one live link per
	// invitation, and the superseded email says so. Everything the invitee is
	// deciding about — who invited them, the proposed Log sharing, the
	// deadline — is untouched.
	if err := recordSend(ctx, tx, invitation, tokenHash, now); err != nil {
		return Resend{}, err
	}

	inviteeUserID, err := readUserID(ctx, tx, invitation.inviteeEmail)
	if err != nil {
		return Resend{}, err
	}
	reciprocal, err := reciprocalWith(ctx, tx, protecteeID, inviteeUserID)
	if err != nil {
		return Resend{}, err
	}
	roster, err := livePartnerEmails(ctx, tx, protecteeID)
	if err != nil {
		return Resend{}, err
	}

	activityID, err := appendActivity(ctx, tx, activityEntry{
		ProtecteeID:  protecteeID,
		Action:       ActionInvitationResent,
		ActorUserID:  &protecteeID,
		ActorEmail:   protecteeEmail,
		TargetEmail:  invitation.inviteeEmail,
		InvitationID: &invitation.id,
		OccurredAt:   now,
		Details: map[string]any{
			"log_sharing": invitation.logSharing,
			// The unchanged deadline is the point of the entry: Activity has
			// to show that a reminder did not buy the invitee more time.
			"expires_at": invitation.expiresAt.Unix(),
			"send_count": invitation.sendCount + 1,
		},
	})
	if err != nil {
		return Resend{}, err
	}

	message := renderPartnerInvitation(invitationMessage{
		ProtecteeEmail: protecteeEmail,
		InviteeEmail:   invitation.inviteeEmail,
		LogSharing:     invitation.logSharing,
		PartnerCount:   len(roster),
		Reciprocal:     reciprocal,
		ReviewURL:      s.reviewURL(rawToken),
		ExpiresAt:      invitation.expiresAt,
		Resent:         true,
	})
	if err := queueInvitationMessages(ctx, tx, []preparedMessage{message}, invitationDelivery{
		ActivityID: activityID, InvitationID: invitation.id,
		TokenHash: tokenHash, At: now,
	}); err != nil {
		return Resend{}, err
	}

	if err := tx.Commit(); err != nil {
		return Resend{}, fmt.Errorf("commit Partner invitation resend: %w", err)
	}
	return Resend{
		InvitationID: invitation.id, InviteeEmail: invitation.inviteeEmail,
		ResentAt: now, ExpiresAt: invitation.expiresAt, RawToken: rawToken,
	}, nil
}

// recordSend re-arms one pending invitation for another delivery: a fresh
// bearer link, a new last-sent instant, and one more counted send. The UPDATE
// names the send the caller read, so a resend that races another resend of the
// same invitation makes no second send.
func recordSend(ctx context.Context, tx *sql.Tx, invitation resolvedInvitation, tokenHash [32]byte, now time.Time) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE partner_invitations
		SET token_hash = ?, last_sent_at = ?, send_count = send_count + 1
		WHERE id = ? AND state = 'pending' AND last_sent_at = ? AND send_count = ?`,
		tokenHash[:], now.Unix(), invitation.id, invitation.lastSentAt.Unix(), invitation.sendCount)
	if err != nil {
		return fmt.Errorf("resend Partner invitation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("resend Partner invitation: %w", err)
	}
	if affected != 1 {
		return ErrInvitationUnavailable
	}
	return countSend(ctx, tx, invitation.protecteeID, invitation.id, now)
}
