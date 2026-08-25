package partnership

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// ActionPartnershipEndedByUserDeletion is the stable Activity action for a
	// Partnership Nightloch ended because one of its two Users was deleted. It
	// is stored apart from every other ending so a surviving Protectee's
	// chronology can name the cause after the identity behind it is gone.
	ActionPartnershipEndedByUserDeletion = "partnership_ended_by_user_deletion"

	// EndReasonUserDeleted is the stored ending cause for that route, and is
	// what a former Partner's retained receipt states once there is no
	// Protectee left to name (S3-71).
	EndReasonUserDeleted = "user_deleted"

	// MessageUserDeleted is the completion signal each Partner who still
	// protected the User receives, separately (S3-73).
	MessageUserDeleted = "user_deleted"

	// DeletedUserLabel is who a deleted User is in every retained product
	// view: their Activity attribution, a former Partner's receipt, and the
	// actor filter alike. It is a name rather than an address, so it can
	// never collide with a verified email, and it is stored rather than
	// substituted at render time so no screen can reach the address by
	// forgetting to substitute (S3-17, S3-78). Every deleted User reads as
	// this same pseudonym, including in the actor filter: two deleted people
	// are deliberately indistinguishable there, because a per-person
	// pseudonym would be a stable handle for the identity that was removed.
	DeletedUserLabel = "Deleted User"
)

// ErrUserUnavailable is the single answer for a User this operation cannot
// act on: an unknown id, or none at all.
var ErrUserUnavailable = errors.New("that User is unavailable")

// UserDeletion is what one completed teardown did. It is the whole return
// value of the hook, so the account-deletion slice can report the completion
// it just ran without reading any of the relationship state back.
type UserDeletion struct {
	UserID int64
	// DeletedAt is when the identity was replaced, which is this call's own
	// instant for the deletion that performed it and the original instant for
	// a repeat.
	DeletedAt time.Time
	// AlreadyDeleted marks a repeat: the User was already pseudonymized and
	// nothing changed. A retried teardown is a no-op, not a second ending and
	// not a second alert.
	AlreadyDeleted bool
	// EndedPartnerships is how many live Partnerships this teardown ended, in
	// both directions.
	EndedPartnerships int
}

// DeleteUser completes one User's teardown inside the relationship perimeter
// and replaces their identity with Deleted User, in one transaction. It is the
// hook the later account-deletion slice calls on its own 72-hour deadline; it
// implements no account-deletion UI, no confirmation, no Cooling-off, and no
// Device teardown, and it starts nothing — by the time it runs, the deleting
// User has already been told what it will do.
//
// What it preserves is the point. A deleted User's row stays, because another
// Protectee's Activity, a former Partner's receipt, and the Partnerships both
// hang off would otherwise cascade away and erase attributable events owned by
// people who still exist (S3-17). What every retained view loses is the
// identity: the verified email is replaced everywhere a product view reads it,
// the deleted User's own chronology and Roster access are removed outright,
// and a former Partner is left with the receipt alone (S3-71).
//
// Mail is external history: a message named the User when the event occurred,
// it stays that way whether it has been dispatched yet or not, and Nightloch
// does not claim it can be recalled or pseudonymized (S3-78).
//
// It is idempotent. A retried or replayed teardown reports AlreadyDeleted and
// changes nothing: no second ending, no second Activity entry, no second
// alert.
func (s *Service) DeleteUser(ctx context.Context, userID int64) (UserDeletion, error) {
	if userID <= 0 {
		return UserDeletion{}, ErrUserUnavailable
	}
	now := s.now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UserDeletion{}, fmt.Errorf("begin User deletion: %w", err)
	}
	defer tx.Rollback()

	var email string
	var deletedAt sql.NullInt64
	err = tx.QueryRowContext(ctx, "SELECT email, deleted_at FROM users WHERE id = ?", userID).
		Scan(&email, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UserDeletion{}, ErrUserUnavailable
	}
	if err != nil {
		return UserDeletion{}, fmt.Errorf("read User: %w", err)
	}
	if deletedAt.Valid {
		return UserDeletion{
			UserID: userID, DeletedAt: time.Unix(deletedAt.Int64, 0).UTC(), AlreadyDeleted: true,
		}, nil
	}

	relationships, err := readLivePartnershipsOf(ctx, tx, userID)
	if err != nil {
		return UserDeletion{}, err
	}
	for _, partnership := range relationships {
		if err := s.endForDeletion(ctx, tx, partnership, userID, now); err != nil {
			return UserDeletion{}, err
		}
	}
	if err := settleForDeletion(ctx, tx, userID, email, now); err != nil {
		return UserDeletion{}, err
	}
	if err := pseudonymize(ctx, tx, userID, email, now); err != nil {
		return UserDeletion{}, err
	}
	if err := tx.Commit(); err != nil {
		return UserDeletion{}, fmt.Errorf("commit User deletion: %w", err)
	}
	return UserDeletion{UserID: userID, DeletedAt: now, EndedPartnerships: len(relationships)}, nil
}

// readLivePartnershipsOf reads every live Partnership the User is on, in
// either direction. Both directions are read here rather than only the
// Partnerships protecting them, so a teardown cannot leave a live relationship
// pointing at an identity that no longer exists. The outbound direction is
// ordinarily already gone — account deletion ends a deleting Partner's own
// Partnerships when its Cooling-off starts (S3-67) — so finding one here is
// the safety net, not the usual path.
func readLivePartnershipsOf(ctx context.Context, tx *sql.Tx, userID int64) ([]livePartnership, error) {
	rows, err := tx.QueryContext(ctx, livePartnershipProjection+
		`(partnership.protectee_id = ?1 OR partnership.partner_id = ?1)
		 ORDER BY partnership.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("read the deleted User's Partnerships: %w", err)
	}
	defer rows.Close()
	var partnerships []livePartnership
	for rows.Next() {
		partnership, err := scanLivePartnership(rows)
		if err != nil {
			return nil, fmt.Errorf("read the deleted User's Partnership: %w", err)
		}
		partnerships = append(partnerships, partnership)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the deleted User's Partnerships: %w", err)
	}
	return partnerships, nil
}

// endForDeletion ends one Partnership because a User on it was deleted, and
// tells whoever survives it. The two directions are different events to
// different audiences: when the deleted User was the Protectee, the Partner
// keeps a pseudonymized receipt and reads no chronology, so the ending is
// recorded on the Partnership alone and announced to them by itself (S3-73).
// When the deleted User was a Partner, the Protectee survives, so their
// chronology gets the ending like any other and the ordinary ending signal
// goes to them and their remaining Roster (S3-72).
func (s *Service) endForDeletion(ctx context.Context, tx *sql.Tx, partnership livePartnership,
	userID int64, now time.Time) error {

	result, err := tx.ExecContext(ctx, `
		UPDATE partnerships SET ended_at = ?, ended_reason = ?
		WHERE id = ? AND ended_at IS NULL`, now.Unix(), EndReasonUserDeleted, partnership.id)
	if err != nil {
		return fmt.Errorf("end Partnership for User deletion: %w", err)
	}
	if err := requireOneRow(result, ErrPartnershipUnavailable); err != nil {
		return err
	}

	if partnership.protecteeID == userID {
		return s.queueDeletionCompletion(ctx, tx, partnership, now)
	}

	remaining, err := countPartnersAfterEnding(ctx, tx, partnership.protecteeID, partnership.id)
	if err != nil {
		return err
	}
	// Nightloch performed the ending; the deleted User asked for the deletion
	// that caused it. The entry therefore names no actor, exactly as every
	// other completion Nightloch carries out does (S3-18).
	activityID, err := appendActivity(ctx, tx, activityEntry{
		ProtecteeID:   partnership.protecteeID,
		Action:        ActionPartnershipEndedByUserDeletion,
		TargetEmail:   partnership.partnerEmail,
		PartnershipID: &partnership.id,
		OccurredAt:    now,
		Details: map[string]any{
			"ended_reason":        EndReasonUserDeleted,
			"log_sharing":         partnership.logSharing,
			"partner_count":       remaining,
			"partner_lock_active": remaining > 0,
		},
	})
	if err != nil {
		return err
	}
	if err := s.queueEndingMessages(ctx, tx, partnership, remaining, now,
		EndReasonUserDeleted, activityID); err != nil {
		return err
	}
	// This ending changes the Roster like any other, so it can leave a
	// Cooling-off elsewhere holding the Partner lock up on its own.
	return s.announceFinalCoolingOff(ctx, tx, partnership, now)
}

// queueDeletionCompletion sends the one Partner who still protected the
// deleted User their own completion signal. Each Partner gets a separate
// message naming only their own Partnership, so a completion never discloses
// the Roster it dissolves (S3-21, S3-73). The message names the verified email
// that identified the User while they existed, which is what makes it
// attributable real-time notice; the receipt it links to does not (S3-78).
func (s *Service) queueDeletionCompletion(ctx context.Context, tx *sql.Tx,
	partnership livePartnership, now time.Time) error {

	message := renderUserDeleted(userDeletionMessage{
		ProtecteeEmail: partnership.protecteeEmail,
		DeletedAt:      now,
		ReceiptURL:     s.receiptURL(partnership.id),
	}, partnership.partnerEmail)
	message.IdempotencyKey = idempotencyKey(message.Kind, message.To,
		partnershipScope(partnership.id, now), 1)
	return queueDelivery(ctx, tx, deliveryJob{
		PartnershipID: &partnership.id,
		Message:       message,
		CreatedAt:     now,
	})
}

// settleForDeletion closes what a deleted User can no longer be part of: their
// own chronology, which nobody may read once it is nobody's (S3-71), and the
// invitations either side of them, which must not stay acceptable into a
// relationship with a User who is gone (S3-70).
//
// The invitations are settled without alerts. Account deletion cancels pending
// invitations and tells the entitled parties when its Cooling-off starts, 72
// hours before this runs, so anything still pending here is a leftover rather
// than news.
//
// Queued mail is left exactly where it is. Those rows are the durable
// per-recipient delivery ledger (ADR 0002), each one reporting an event that
// really happened while the User existed; pruning them here would erase a
// delivery record to make a privacy claim the delivered copies already
// disprove.
func settleForDeletion(ctx context.Context, tx *sql.Tx, userID int64, email string, now time.Time) error {
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM activity_entries WHERE protectee_id = ?", userID); err != nil {
		return fmt.Errorf("remove the deleted User's Activity: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE partner_invitations
		SET state = 'canceled', resolved_at = ?, resolved_by_user_id = NULL
		WHERE state = 'pending'
		  AND (protectee_id = ? OR lower(invitee_email) = lower(?))`,
		now.Unix(), userID, email); err != nil {
		return fmt.Errorf("settle the deleted User's invitations: %w", err)
	}
	return nil
}

// pseudonymize replaces the identity everywhere a retained product view reads
// it, and marks the row deleted. The stored payloads are rewritten too: a
// detail row is rendered from named fields, and a field naming the deleted
// User by address would put it back on a screen the pseudonym was meant to
// keep it off.
func pseudonymize(ctx context.Context, tx *sql.Tx, userID int64, email string, now time.Time) error {
	for _, column := range []string{"actor_email", "target_email"} {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			"UPDATE activity_entries SET %s = ? WHERE lower(%s) = lower(?)", column, column),
			DeletedUserLabel, email); err != nil {
			return fmt.Errorf("pseudonymize retained Activity: %w", err)
		}
	}
	if err := pseudonymizeDetails(ctx, tx, email); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE partner_invitations SET invitee_email = ? WHERE lower(invitee_email) = lower(?)",
		DeletedUserLabel, email); err != nil {
		return fmt.Errorf("pseudonymize retained invitations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE users SET email = ?, deleted_at = ? WHERE id = ?",
		deletedUserEmail(userID), now.Unix(), userID); err != nil {
		return fmt.Errorf("pseudonymize the deleted User: %w", err)
	}
	return nil
}

// deletedUserEmail is what stands in the email column of a deleted User. It is
// unique, so the column's uniqueness still holds; it is not an address, so no
// sign-in can ever resolve to it; and it frees the real address, which the
// person may register again as a new User.
func deletedUserEmail(userID int64) string {
	return "deleted-user-" + strconv.FormatInt(userID, 10)
}

// pseudonymizeDetails rewrites the stored payloads that name the deleted User.
// Every occurrence of their address becomes the pseudonym, wherever in a value
// it sits and whichever field carries it, so a payload written before this
// operation existed is covered by the same rule as one written after.
func pseudonymizeDetails(ctx context.Context, tx *sql.Tx, email string) error {
	rows, err := tx.QueryContext(ctx,
		"SELECT id, details FROM activity_entries WHERE instr(lower(details), lower(?)) > 0", email)
	if err != nil {
		return fmt.Errorf("read Activity details to pseudonymize: %w", err)
	}
	// The rewrites are collected before any is applied: one SQLite connection
	// serves this transaction, so writing while the read is still open would
	// contend with it.
	rewritten := map[int64]string{}
	for rows.Next() {
		var id int64
		var payload []byte
		if err := rows.Scan(&id, &payload); err != nil {
			rows.Close()
			return fmt.Errorf("read Activity details to pseudonymize: %w", err)
		}
		details := map[string]any{}
		if err := json.Unmarshal(payload, &details); err != nil {
			rows.Close()
			return fmt.Errorf("decode Activity details to pseudonymize: %w", err)
		}
		changed := false
		for field, value := range details {
			text, ok := value.(string)
			if !ok {
				continue
			}
			scrubbed := replaceFold(text, email, DeletedUserLabel)
			if scrubbed != text {
				details[field] = scrubbed
				changed = true
			}
		}
		if !changed {
			continue
		}
		encoded, err := json.Marshal(details)
		if err != nil {
			rows.Close()
			return fmt.Errorf("encode pseudonymized Activity details: %w", err)
		}
		rewritten[id] = string(encoded)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("read Activity details to pseudonymize: %w", err)
	}
	for id, details := range rewritten {
		if _, err := tx.ExecContext(ctx,
			"UPDATE activity_entries SET details = ? WHERE id = ?", details, id); err != nil {
			return fmt.Errorf("pseudonymize Activity details: %w", err)
		}
	}
	return nil
}

// replaceFold replaces every case-insensitive occurrence of old in text. A
// stored payload holds identities Nightloch normalized, but a value that has
// an address inside it rather than as the whole of it must be scrubbed too:
// the pseudonym is worth nothing if one field can spell the address out.
func replaceFold(text, old, replacement string) string {
	if old == "" {
		return text
	}
	var scrubbed strings.Builder
	for {
		at := strings.Index(strings.ToLower(text), strings.ToLower(old))
		if at < 0 {
			scrubbed.WriteString(text)
			return scrubbed.String()
		}
		scrubbed.WriteString(text[:at])
		scrubbed.WriteString(replacement)
		text = text[at+len(old):]
	}
}
