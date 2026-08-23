package partnership

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	// ActionInvitationDeclined, ActionInvitationCanceled, and
	// ActionInvitationExpired are the stable Activity actions for the three
	// terminal outcomes that create no Partnership.
	ActionInvitationDeclined = "partner_invitation_declined"
	ActionInvitationCanceled = "partner_invitation_canceled"
	ActionInvitationExpired  = "partner_invitation_expired"

	// MessageInvitationDeclined goes to the Protectee who invited;
	// MessageInvitationCanceled goes to the invitee who was dropped. Expiry
	// sends nothing at all (S3-20).
	MessageInvitationDeclined = "partner_invitation_declined"
	MessageInvitationCanceled = "partner_invitation_canceled"
)

// InvitationState is the lifecycle state of one Partner invitation. Only
// InvitationPending can still be acted on; the rest are terminal.
type InvitationState string

const (
	InvitationPending  InvitationState = "pending"
	InvitationAccepted InvitationState = "accepted"
	InvitationDeclined InvitationState = "declined"
	InvitationCanceled InvitationState = "canceled"
	InvitationExpired  InvitationState = "expired"
)

// InvitationRef names the one invitation a caller means. An invitee reaches
// the same invitation two ways — the bearer link Nightloch emailed, and the
// **Partner invitations** list they see once signed in — and both paths carry
// the same disclosures and the same authorization.
type InvitationRef struct {
	token string
	id    int64
}

// ByToken names an invitation by the bearer token from its emailed link.
func ByToken(rawToken string) InvitationRef { return InvitationRef{token: rawToken} }

// ByID names an invitation by the id an authenticated invitee sees in their
// own list. An id is guessable, so it resolves only for the User the
// invitation was addressed to.
func ByID(id int64) InvitationRef { return InvitationRef{id: id} }

// resolvedInvitation is one invitation row with the identities and the derived
// state every lifecycle decision needs.
type resolvedInvitation struct {
	id             int64
	protecteeID    int64
	protecteeEmail string
	inviteeEmail   string
	logSharing     bool
	state          InvitationState
	tokenHash      [32]byte
	expiresAt      time.Time
	resolvedAt     time.Time
}

// invitationQuery is the projection every lifecycle decision reads, up to and
// including the WHERE keyword; each caller appends its own predicate.
const invitationQuery = `
	SELECT invitation.id, invitation.protectee_id, protectee.email,
	       invitation.invitee_email, invitation.log_sharing, invitation.state,
	       invitation.token_hash, invitation.expires_at,
	       COALESCE(invitation.resolved_at, 0)
	FROM partner_invitations invitation
	JOIN users protectee ON protectee.id = invitation.protectee_id
	WHERE `

// resolveInvitation finds the invitation ref names, or ErrInvitationUnavailable.
//
// A bearer token proves possession of the emailed link, so it resolves on its
// own; the disclosure the caller then builds is what stays masked. An id
// proves nothing, so it resolves only when viewerEmail is the invited address.
// That keeps invitations unenumerable by id while still letting an invitee act
// from their own **Partner invitations** list.
func resolveInvitation(ctx context.Context, queryer rowQueryer, ref InvitationRef, viewerEmail string, now time.Time) (resolvedInvitation, error) {
	switch {
	case ref.token != "":
		tokenHash, err := hashInvitationToken(ref.token)
		if err != nil {
			return resolvedInvitation{}, err
		}
		return scanInvitation(queryer.QueryRowContext(ctx,
			invitationQuery+"invitation.token_hash = ?", tokenHash[:]), now)
	case ref.id > 0 && viewerEmail != "":
		return scanInvitation(queryer.QueryRowContext(ctx,
			invitationQuery+"invitation.id = ? AND invitation.invitee_email = ?",
			ref.id, viewerEmail), now)
	default:
		return resolvedInvitation{}, ErrInvitationUnavailable
	}
}

// resolveInvitationByID skips the invitee scoping for the two callers that
// authorize by another route entirely: the Protectee withdrawing their own
// invitation, who is checked against protectee_id, and Nightloch's own expiry
// sweep, which acts for nobody.
func resolveInvitationByID(ctx context.Context, queryer rowQueryer, invitationID int64, now time.Time) (resolvedInvitation, error) {
	if invitationID <= 0 {
		return resolvedInvitation{}, ErrInvitationUnavailable
	}
	return scanInvitation(queryer.QueryRowContext(ctx, invitationQuery+"invitation.id = ?", invitationID), now)
}

// scanInvitation reads one row and derives its effective state. A pending
// invitation past its deadline resolves as expired even before the sweep
// records it, so a late reader is never told an expired invitation is live.
func scanInvitation(row *sql.Row, now time.Time) (resolvedInvitation, error) {
	var invitation resolvedInvitation
	var tokenHash []byte
	var logSharing int
	var expiresAt, resolvedAt int64
	err := row.Scan(&invitation.id, &invitation.protecteeID, &invitation.protecteeEmail,
		&invitation.inviteeEmail, &logSharing, &invitation.state, &tokenHash, &expiresAt, &resolvedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return resolvedInvitation{}, ErrInvitationUnavailable
	}
	if err != nil {
		return resolvedInvitation{}, fmt.Errorf("read Partner invitation: %w", err)
	}
	invitation.logSharing = intToBool(logSharing)
	invitation.expiresAt = time.Unix(expiresAt, 0).UTC()
	copy(invitation.tokenHash[:], tokenHash)
	if resolvedAt != 0 {
		invitation.resolvedAt = time.Unix(resolvedAt, 0).UTC()
	}
	if invitation.state == InvitationPending && !now.Before(invitation.expiresAt) {
		invitation.state = InvitationExpired
		invitation.resolvedAt = invitation.expiresAt
	}
	return invitation, nil
}

// ReceivedInvitation is one pending invitation addressed to the signed-in
// User's verified email, as listed under **Partner invitations**. It carries
// no bearer token: the invitee acts on it by id from their own session.
type ReceivedInvitation struct {
	ID             int64
	ProtecteeEmail string
	LogSharing     bool
	SentAt         time.Time
	ExpiresAt      time.Time
}

// ReceivedInvitations lists the live Partner invitations addressed to the
// signed-in User's verified email, so an invitee who never received or kept
// the email can still find and answer them.
func (s *Service) ReceivedInvitations(ctx context.Context, viewerID int64) ([]ReceivedInvitation, error) {
	if viewerID <= 0 {
		return nil, nil
	}
	now := s.now().UTC()
	rows, err := s.db.QueryContext(ctx, `
		SELECT invitation.id, protectee.email, invitation.log_sharing,
		       invitation.last_sent_at, invitation.expires_at
		FROM partner_invitations invitation
		JOIN users protectee ON protectee.id = invitation.protectee_id
		JOIN users invitee ON invitee.email = invitation.invitee_email
		WHERE invitee.id = ? AND invitation.state = 'pending' AND invitation.expires_at > ?
		ORDER BY invitation.created_at, invitation.id`, viewerID, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("read received Partner invitations: %w", err)
	}
	defer rows.Close()

	var received []ReceivedInvitation
	for rows.Next() {
		var invitation ReceivedInvitation
		var logSharing int
		var sentAt, expiresAt int64
		if err := rows.Scan(&invitation.ID, &invitation.ProtecteeEmail, &logSharing, &sentAt, &expiresAt); err != nil {
			return nil, fmt.Errorf("read received Partner invitation: %w", err)
		}
		invitation.LogSharing = intToBool(logSharing)
		invitation.SentAt = time.Unix(sentAt, 0).UTC()
		invitation.ExpiresAt = time.Unix(expiresAt, 0).UTC()
		received = append(received, invitation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read received Partner invitations: %w", err)
	}
	return received, nil
}

// Decline is the receipt an invitee gets for refusing an invitation.
type Decline struct {
	ProtecteeEmail string
	DeclinedAt     time.Time
}

// DeclineInvitation terminates a pending invitation at the invitee's request
// and alerts only the Protectee who sent it (S3-20). Nobody else may decline:
// the Protectee cancels instead, and a third party has no invitation controls
// at all.
func (s *Service) DeclineInvitation(ctx context.Context, ref InvitationRef, viewerID int64) (Decline, error) {
	if viewerID <= 0 {
		return Decline{}, ErrInvitationNotYours
	}
	now := s.now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Decline{}, fmt.Errorf("begin Partner invitation decline: %w", err)
	}
	defer tx.Rollback()

	viewerEmail, err := readUserEmail(ctx, tx, viewerID)
	if err != nil {
		return Decline{}, err
	}
	invitation, err := resolveInvitation(ctx, tx, ref, viewerEmail, now)
	if err != nil {
		return Decline{}, err
	}
	if viewerEmail == "" || viewerEmail != invitation.inviteeEmail {
		return Decline{}, ErrInvitationNotYours
	}
	if err := resolveState(ctx, tx, invitation, InvitationDeclined, now, &viewerID); err != nil {
		return Decline{}, err
	}

	activityID, err := appendActivity(ctx, tx, activityEntry{
		ProtecteeID:  invitation.protecteeID,
		Action:       ActionInvitationDeclined,
		ActorUserID:  &viewerID,
		ActorEmail:   invitation.inviteeEmail,
		TargetEmail:  invitation.protecteeEmail,
		InvitationID: &invitation.id,
		OccurredAt:   now,
		Details:      map[string]any{"log_sharing": invitation.logSharing},
	})
	if err != nil {
		return Decline{}, err
	}

	message := renderInvitationDeclined(terminalMessage{
		ProtecteeEmail: invitation.protecteeEmail,
		InviteeEmail:   invitation.inviteeEmail,
		ResolvedAt:     now,
	})
	message.IdempotencyKey = idempotencyKey(message.Kind, message.To, invitation.tokenHash, 1)
	if err := queueDelivery(ctx, tx, deliveryJob{
		ActivityID: activityID, InvitationID: &invitation.id, Message: message, CreatedAt: now,
	}); err != nil {
		return Decline{}, err
	}

	if err := tx.Commit(); err != nil {
		return Decline{}, fmt.Errorf("commit Partner invitation decline: %w", err)
	}
	return Decline{ProtecteeEmail: invitation.protecteeEmail, DeclinedAt: now}, nil
}

// Cancellation is the receipt a Protectee gets for withdrawing an invitation.
type Cancellation struct {
	InviteeEmail string
	CanceledAt   time.Time
}

// CancelInvitation withdraws a pending invitation the Protectee sent. It is
// also the only way to correct one: the invited address and the proposed Log
// sharing are immutable, so a correction is a cancellation plus a fresh
// invitation. A caller who is not that Protectee gets ErrInvitationUnavailable
// rather than a refusal that would confirm the invitation exists.
func (s *Service) CancelInvitation(ctx context.Context, invitationID, protecteeID int64) (Cancellation, error) {
	if invitationID <= 0 || protecteeID <= 0 {
		return Cancellation{}, ErrInvitationUnavailable
	}
	now := s.now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Cancellation{}, fmt.Errorf("begin Partner invitation cancellation: %w", err)
	}
	defer tx.Rollback()

	protecteeEmail, err := readUserEmail(ctx, tx, protecteeID)
	if err != nil {
		return Cancellation{}, err
	}
	invitation, err := resolveInvitationByID(ctx, tx, invitationID, now)
	if errors.Is(err, ErrInvitationUnavailable) || protecteeEmail == "" {
		return Cancellation{}, ErrInvitationUnavailable
	}
	if err != nil {
		return Cancellation{}, err
	}
	if invitation.protecteeID != protecteeID {
		return Cancellation{}, ErrInvitationUnavailable
	}
	if err := resolveState(ctx, tx, invitation, InvitationCanceled, now, &protecteeID); err != nil {
		return Cancellation{}, err
	}

	activityID, err := appendActivity(ctx, tx, activityEntry{
		ProtecteeID:  invitation.protecteeID,
		Action:       ActionInvitationCanceled,
		ActorUserID:  &protecteeID,
		ActorEmail:   invitation.protecteeEmail,
		TargetEmail:  invitation.inviteeEmail,
		InvitationID: &invitation.id,
		OccurredAt:   now,
		Details:      map[string]any{"log_sharing": invitation.logSharing},
	})
	if err != nil {
		return Cancellation{}, err
	}

	// The invitee is the only recipient today. 3B lifts the one-Partner gate
	// and adds the same signal separately to each current Partner (S3-20).
	message := renderInvitationCanceled(terminalMessage{
		ProtecteeEmail: invitation.protecteeEmail,
		InviteeEmail:   invitation.inviteeEmail,
		ResolvedAt:     now,
	})
	message.IdempotencyKey = idempotencyKey(message.Kind, message.To, invitation.tokenHash, 1)
	if err := queueDelivery(ctx, tx, deliveryJob{
		ActivityID: activityID, InvitationID: &invitation.id, Message: message, CreatedAt: now,
	}); err != nil {
		return Cancellation{}, err
	}

	if err := tx.Commit(); err != nil {
		return Cancellation{}, fmt.Errorf("commit Partner invitation cancellation: %w", err)
	}
	return Cancellation{InviteeEmail: invitation.inviteeEmail, CanceledAt: now}, nil
}

// ExpireDueInvitations records the invitations whose seven-day window has
// closed and reports how many it settled. Expiry is Nightloch's own act, so it
// appends Activity at the instant the invitation actually lapsed and sends no
// mail (S3-20). The work is driven from SQLite, so a process that dies
// mid-sweep leaves the rest for the next one, and a settled row is never
// re-resolved.
func (s *Service) ExpireDueInvitations(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	now := s.now().UTC()
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM partner_invitations
		WHERE state = 'pending' AND expires_at <= ?
		ORDER BY expires_at, id LIMIT ?`, now.Unix(), limit)
	if err != nil {
		return 0, fmt.Errorf("read expired Partner invitations: %w", err)
	}
	var due []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("read expired Partner invitation: %w", err)
		}
		due = append(due, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return 0, fmt.Errorf("read expired Partner invitations: %w", err)
	}

	expired := 0
	for _, id := range due {
		settled, err := s.expireInvitation(ctx, id, now)
		if err != nil {
			return expired, err
		}
		if settled {
			expired++
		}
	}
	return expired, nil
}

func (s *Service) expireInvitation(ctx context.Context, invitationID int64, now time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin Partner invitation expiry: %w", err)
	}
	defer tx.Rollback()

	invitation, err := resolveInvitationByID(ctx, tx, invitationID, now)
	if errors.Is(err, ErrInvitationUnavailable) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Someone may have accepted, declined, or canceled between the scan and
	// this transaction. First commit wins, so the sweep simply stands down.
	if err := resolveState(ctx, tx, invitation, InvitationExpired, invitation.expiresAt, nil); err != nil {
		if errors.Is(err, ErrInvitationUnavailable) {
			return false, nil
		}
		return false, err
	}
	if _, err := appendActivity(ctx, tx, activityEntry{
		// A NULL actor is Nightloch itself. The Protectee who sent the
		// invitation is the entry's own protectee_id, which is how Activity
		// links a system completion back to its original requester (S3-30);
		// the target is the address that was invited and never answered.
		ProtecteeID:  invitation.protecteeID,
		Action:       ActionInvitationExpired,
		TargetEmail:  invitation.inviteeEmail,
		InvitationID: &invitation.id,
		OccurredAt:   invitation.expiresAt,
		Details:      map[string]any{"log_sharing": invitation.logSharing},
	}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit Partner invitation expiry: %w", err)
	}
	return true, nil
}

// resolveState is the single first-commit-wins gate every terminal outcome
// passes through. The row's own pending state is the concurrency token, so a
// racing accept, decline, cancel, or expiry that arrives second changes
// nothing and answers ErrInvitationUnavailable.
func resolveState(ctx context.Context, tx *sql.Tx, invitation resolvedInvitation, state InvitationState, at time.Time, actorID *int64) error {
	// The SQL guard below sees the stored state; this one sees the derived
	// state, and they differ for exactly one row: still stored as pending, but
	// already past its deadline. Nobody may accept, decline, or cancel that
	// row — only the sweep that is about to record its expiry.
	if invitation.state != InvitationPending && state != InvitationExpired {
		return ErrInvitationUnavailable
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE partner_invitations
		SET state = ?, resolved_at = ?, resolved_by_user_id = ?
		WHERE id = ? AND state = 'pending'`, state, at.UTC().Unix(), actorID, invitation.id)
	if err != nil {
		return fmt.Errorf("resolve Partner invitation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("resolve Partner invitation: %w", err)
	}
	if affected != 1 {
		return ErrInvitationUnavailable
	}
	return nil
}

// readUserEmail answers "" for a User who no longer exists rather than an
// error, so a stale session degrades to an unauthorized read.
func readUserEmail(ctx context.Context, queryer rowQueryer, userID int64) (string, error) {
	if userID <= 0 {
		return "", nil
	}
	var email string
	err := queryer.QueryRowContext(ctx, "SELECT email FROM users WHERE id = ?", userID).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read User: %w", err)
	}
	return email, nil
}

// readUserID answers 0 for an address that has never signed in, which is the
// ordinary case for an invitee who has not yet created their User.
func readUserID(ctx context.Context, queryer rowQueryer, email string) (int64, error) {
	var id int64
	err := queryer.QueryRowContext(ctx, "SELECT id FROM users WHERE email = ?", email).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read User by address: %w", err)
	}
	return id, nil
}
