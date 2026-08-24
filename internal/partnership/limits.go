package partnership

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// This file owns the three approved limits on Partner invitations and nothing
// else. Sending is in service.go and resending is in resend.go; both pass
// through the checks here so neither can be bounded without the other.

// The approved invitation limits (S3-13). They are deliberately three separate
// rules rather than one: the pending cap bounds how much exposure a Protectee
// can create at once, the rolling window bounds how much mail one Protectee
// can cause in a day, and the resend interval bounds how often any single
// invitee can be reminded.
const (
	maxPendingInvitations = 5
	maxSendsPerWindow     = 10
	sendWindow            = 24 * time.Hour
	resendInterval        = 24 * time.Hour
)

var (
	// ErrPendingInvitationLimit refuses a pending invitation past the approved
	// cap. It names the limit and the remedy without naming any address, so a
	// refusal never enumerates who a Protectee has already invited.
	ErrPendingInvitationLimit = fmt.Errorf(
		"you already have %d pending Partner invitations, which is the most Nightloch allows at once; cancel one before sending another",
		maxPendingInvitations)
	// ErrSendQuotaExhausted refuses a send or resend past the rolling window.
	// Its wording counts emails rather than invitations, because a resend and
	// an invitation since canceled each spend one: saying "invitations" would
	// overstate how many people the Protectee actually approached.
	ErrSendQuotaExhausted = fmt.Errorf(
		"Nightloch has already sent %d Partner invitation emails for you in the last 24 hours, which is the most it allows; try again later",
		maxSendsPerWindow)
	// ErrResendTooSoon refuses a second reminder for one invitation inside its
	// own 24-hour interval.
	ErrResendTooSoon = errors.New("this Partner invitation was already sent in the last 24 hours; you can resend it once every 24 hours")
)

// countSend records one accepted send against the Protectee's rolling window.
// Every accepted send and resend calls it, inside the same transaction as the
// mail it authorizes, so a rolled-back send never spends quota and a committed
// one always does.
func countSend(ctx context.Context, tx *sql.Tx, protecteeID, invitationID int64, at time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO partner_invitation_sends(protectee_id, invitation_id, sent_at)
		VALUES (?, ?, ?)`, protecteeID, invitationID, at.UTC().Unix()); err != nil {
		return fmt.Errorf("count Partner invitation send: %w", err)
	}
	return nil
}

// checkSendQuota refuses a send once the Protectee has already had
// maxSendsPerWindow of them accepted inside the trailing window. The window
// rolls rather than resetting on a boundary, so the capacity a Protectee
// regains is exactly the capacity that aged out.
//
// It reads and writes inside the caller's transaction on Nightloch's single
// SQLite connection, so two concurrent sends cannot both see the same
// remaining capacity and both spend it.
func checkSendQuota(ctx context.Context, tx *sql.Tx, protecteeID int64, now time.Time) error {
	var sends int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM partner_invitation_sends
		WHERE protectee_id = ? AND sent_at > ?`,
		protecteeID, now.Add(-sendWindow).Unix()).Scan(&sends); err != nil {
		return fmt.Errorf("read Partner invitation send quota: %w", err)
	}
	if sends >= maxSendsPerWindow {
		return ErrSendQuotaExhausted
	}
	return nil
}

// checkPendingInvitationLimit refuses a new invitation once the approved
// number are already outstanding. An invitation past its seven-day expiry has
// stopped being outstanding whether or not the sweep has recorded it yet, so
// it frees its place immediately, exactly as the Protectee's own list shows.
func checkPendingInvitationLimit(ctx context.Context, tx *sql.Tx, protecteeID int64, now time.Time) error {
	var pending int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM partner_invitations
		WHERE protectee_id = ? AND state = 'pending' AND expires_at > ?`,
		protecteeID, now.Unix()).Scan(&pending); err != nil {
		return fmt.Errorf("read pending Partner invitations: %w", err)
	}
	if pending >= maxPendingInvitations {
		return ErrPendingInvitationLimit
	}
	return nil
}
