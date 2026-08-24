package partnership

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// ActionPartnershipEndedByPartner is the stable Activity action for a
	// Partner exercising their independent right to end immediately (S3-08).
	ActionPartnershipEndedByPartner = "partnership_ended_by_partner"

	// MessagePartnershipEnded is the one ending signal. Every recipient reads
	// the same approved body, but each gets their own envelope, so the
	// recipient list is never exposed (S3-21).
	MessagePartnershipEnded = "partnership_ended"

	// EndReasonPartnerEnded is the stored ending cause for this route. Every
	// other cause — Cooling-off completion, End now, account deletion — is a
	// separate value, so a receipt can always name how the Partnership ended.
	EndReasonPartnerEnded = "partner_ended"

	// endConfirmationWord is the exact word a Partner types to end a
	// Partnership immediately (S3-46). Surrounding whitespace is forgiven;
	// the word itself is not.
	endConfirmationWord = "END"
)

var (
	// ErrPartnershipUnavailable is the single answer for a Partnership this
	// caller may not act on: unknown, already ended, or somebody else's. It
	// deliberately distinguishes none of those, so a Partner cannot probe for
	// relationships they are not part of.
	ErrPartnershipUnavailable = errors.New("that Partnership is unavailable")
	// ErrEndConfirmationRequired refuses an ending whose typed confirmation is
	// missing or misspelled. It is a refusal the Partner can correct, so its
	// message is safe to show them.
	ErrEndConfirmationRequired = errors.New("type END to end this Partnership")
)

// EndCommand is one Partner's request to end their own Partnership
// immediately. Confirmation carries the word they typed, so no caller can
// reach the mutation without the friction S3-46 requires.
type EndCommand struct {
	PartnershipID int64
	PartnerID     int64
	Confirmation  string
}

// PartnershipEnding is one Partnership at the moment it ends, or — with a zero
// EndedAt — the same consequences as a preview before the Partner confirms.
// RemainingPartners is the Protectee's active-Partner count once this
// Partnership is gone, so the zero-Partner self-loosening consequence can be
// stated explicitly wherever the ending appears (S3-43).
type PartnershipEnding struct {
	PartnershipID     int64
	ProtecteeEmail    string
	Since             time.Time
	LogSharing        bool
	RemainingPartners int
	EndedAt           time.Time
}

// PartnerLockActive reports the Protectee's lock state after this ending.
func (e PartnershipEnding) PartnerLockActive() bool { return e.RemainingPartners > 0 }

// livePartnership is one active Partnership with both parties' identities.
type livePartnership struct {
	id             int64
	protecteeID    int64
	protecteeEmail string
	partnerEmail   string
	logSharing     bool
	since          time.Time
}

// readLivePartnership resolves the live Partnership partnerID holds, or
// ErrPartnershipUnavailable. Authorization is the query: a Protectee, another
// Partner, or a stranger simply matches no row, so none of them can end or
// edit a Partnership that is not theirs.
func readLivePartnership(ctx context.Context, queryer rowQueryer, partnershipID, partnerID int64) (livePartnership, error) {
	if partnershipID <= 0 || partnerID <= 0 {
		return livePartnership{}, ErrPartnershipUnavailable
	}
	var partnership livePartnership
	var logSharing int
	var acceptedAt int64
	err := queryer.QueryRowContext(ctx, `
		SELECT partnership.id, partnership.protectee_id, protectee.email, partner.email,
		       partnership.log_sharing, partnership.accepted_at
		FROM partnerships partnership
		JOIN users protectee ON protectee.id = partnership.protectee_id
		JOIN users partner ON partner.id = partnership.partner_id
		WHERE partnership.id = ? AND partnership.partner_id = ? AND partnership.ended_at IS NULL`,
		partnershipID, partnerID).
		Scan(&partnership.id, &partnership.protecteeID, &partnership.protecteeEmail,
			&partnership.partnerEmail, &logSharing, &acceptedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return livePartnership{}, ErrPartnershipUnavailable
	}
	if err != nil {
		return livePartnership{}, fmt.Errorf("read Partnership: %w", err)
	}
	partnership.logSharing = intToBool(logSharing)
	partnership.since = time.Unix(acceptedAt, 0).UTC()
	return partnership, nil
}

// EndablePartnership is the read behind the confirmation screen: the same
// consequences EndPartnership will commit, without committing them. Only the
// Partner on the Partnership reaches it.
func (s *Service) EndablePartnership(ctx context.Context, partnershipID, partnerID int64) (PartnershipEnding, error) {
	partnership, err := readLivePartnership(ctx, s.db, partnershipID, partnerID)
	if err != nil {
		return PartnershipEnding{}, err
	}
	remaining, err := countPartnersAfterEnding(ctx, s.db, partnership.protecteeID, partnership.id)
	if err != nil {
		return PartnershipEnding{}, err
	}
	return newEnding(partnership, remaining, time.Time{}), nil
}

// EndPartnership ends one Partner's own Partnership immediately. The
// Partnership's live state is the concurrency token, so a replayed or racing
// end changes nothing, appends no second Activity entry, and queues no second
// delivery job; it answers ErrPartnershipUnavailable instead. Powers, Roster
// access, and the Protectee's Partner lock all move in the same transaction as
// the Activity entry and the per-recipient messages (ADR 0002).
func (s *Service) EndPartnership(ctx context.Context, command EndCommand) (PartnershipEnding, error) {
	now := s.now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PartnershipEnding{}, fmt.Errorf("begin Partnership ending: %w", err)
	}
	defer tx.Rollback()

	// Authorization comes first, so a caller who holds no such Partnership
	// learns only that it is unavailable — never that they typed the
	// confirmation wrongly for a relationship they are not part of.
	partnership, err := readLivePartnership(ctx, tx, command.PartnershipID, command.PartnerID)
	if err != nil {
		return PartnershipEnding{}, err
	}
	if strings.TrimSpace(command.Confirmation) != endConfirmationWord {
		return PartnershipEnding{}, ErrEndConfirmationRequired
	}
	// First commit wins: only the transaction that still sees a live
	// Partnership may end it, and every later mutation hangs off that row.
	result, err := tx.ExecContext(ctx, `
		UPDATE partnerships SET ended_at = ?, ended_reason = ?
		WHERE id = ? AND ended_at IS NULL`, now.Unix(), EndReasonPartnerEnded, partnership.id)
	if err != nil {
		return PartnershipEnding{}, fmt.Errorf("end Partnership: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return PartnershipEnding{}, fmt.Errorf("end Partnership: %w", err)
	}
	if affected != 1 {
		return PartnershipEnding{}, ErrPartnershipUnavailable
	}

	remaining, err := countPartnersAfterEnding(ctx, tx, partnership.protecteeID, partnership.id)
	if err != nil {
		return PartnershipEnding{}, err
	}
	ending := newEnding(partnership, remaining, now)

	activityID, err := appendActivity(ctx, tx, activityEntry{
		ProtecteeID:   partnership.protecteeID,
		Action:        ActionPartnershipEndedByPartner,
		ActorUserID:   &command.PartnerID,
		ActorEmail:    partnership.partnerEmail,
		TargetEmail:   partnership.protecteeEmail,
		PartnershipID: &partnership.id,
		OccurredAt:    now,
		Details: map[string]any{
			"ended_reason":        EndReasonPartnerEnded,
			"log_sharing":         partnership.logSharing,
			"partner_count":       remaining,
			"partner_lock_active": ending.PartnerLockActive(),
		},
	})
	if err != nil {
		return PartnershipEnding{}, err
	}

	// Both parties and every remaining Partner are entitled to the ending
	// signal (S3-20). Today the 3A exposure gate means the remaining Roster is
	// always empty; the query is written for the Roster 3B opens up.
	recipients := []string{partnership.protecteeEmail, partnership.partnerEmail}
	roster, err := livePartnerEmails(ctx, tx, partnership.protecteeID)
	if err != nil {
		return PartnershipEnding{}, err
	}
	recipients = append(recipients, roster...)

	scope := partnershipScope(partnership.id, now)
	for sequence, recipient := range recipients {
		message := renderPartnershipEnded(endingMessage{
			ProtecteeEmail: partnership.protecteeEmail,
			PartnerEmail:   partnership.partnerEmail,
			EndedAt:        now,
			PartnerCount:   remaining,
			DashboardURL:   s.dashboardURL(),
			ReceiptURL:     s.receiptURL(partnership.id),
		}, recipient)
		message.IdempotencyKey = idempotencyKey(message.Kind, message.To, scope, sequence+1)
		if err := queueDelivery(ctx, tx, deliveryJob{
			ActivityID:    activityID,
			PartnershipID: &partnership.id,
			Message:       message,
			CreatedAt:     now,
		}); err != nil {
			return PartnershipEnding{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return PartnershipEnding{}, fmt.Errorf("commit Partnership ending: %w", err)
	}
	return ending, nil
}

// EndedPartnership is the read-only receipt a former Partner keeps for their
// own ended Partnership. It carries the relationship's own facts and nothing
// else: no Roster, no remaining-Partner count, and nothing that happened after
// the ending instant (S3-16).
type EndedPartnership struct {
	PartnershipID  int64
	ProtecteeEmail string
	Since          time.Time
	EndedAt        time.Time
	LogSharing     bool
	EndedReason    string
}

// PartnerEnded reports whether this receipt records the Partner's own
// immediate ending, so a template can name the cause without restating the
// stored vocabulary.
func (e EndedPartnership) PartnerEnded() bool { return e.EndedReason == EndReasonPartnerEnded }

// endedReceiptQuery projects the receipts one former Partner holds. Both
// callers scope it to partner_id, so a receipt is unreachable by anyone else,
// including the Protectee and the Partners still on that Roster.
const endedReceiptQuery = `
	SELECT partnership.id, protectee.email, partnership.accepted_at,
	       partnership.ended_at, partnership.log_sharing, partnership.ended_reason
	FROM partnerships partnership
	JOIN users protectee ON protectee.id = partnership.protectee_id
	WHERE partnership.ended_at IS NOT NULL AND partnership.partner_id = ?`

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface{ Scan(...any) error }

func scanEndedPartnership(scanner rowScanner) (EndedPartnership, error) {
	var receipt EndedPartnership
	var since, endedAt int64
	var logSharing int
	if err := scanner.Scan(&receipt.PartnershipID, &receipt.ProtecteeEmail, &since,
		&endedAt, &logSharing, &receipt.EndedReason); err != nil {
		return EndedPartnership{}, err
	}
	receipt.Since = time.Unix(since, 0).UTC()
	receipt.EndedAt = time.Unix(endedAt, 0).UTC()
	receipt.LogSharing = intToBool(logSharing)
	return receipt, nil
}

// EndedPartnershipReceipt returns one receipt, authorized to the former
// Partner alone. A live Partnership, another User's receipt, and an unknown id
// all answer ErrPartnershipUnavailable.
func (s *Service) EndedPartnershipReceipt(ctx context.Context, partnershipID, partnerID int64) (EndedPartnership, error) {
	if partnershipID <= 0 || partnerID <= 0 {
		return EndedPartnership{}, ErrPartnershipUnavailable
	}
	receipt, err := scanEndedPartnership(s.db.QueryRowContext(ctx,
		endedReceiptQuery+" AND partnership.id = ?", partnerID, partnershipID))
	if errors.Is(err, sql.ErrNoRows) {
		return EndedPartnership{}, ErrPartnershipUnavailable
	}
	if err != nil {
		return EndedPartnership{}, fmt.Errorf("read ended Partnership receipt: %w", err)
	}
	return receipt, nil
}

// EndedPartnerships lists the receipts partnerID still holds, newest first, so
// a former Partner can find one without the link they were sent.
func (s *Service) EndedPartnerships(ctx context.Context, partnerID int64) ([]EndedPartnership, error) {
	if partnerID <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, endedReceiptQuery+
		" ORDER BY partnership.ended_at DESC, partnership.id DESC", partnerID)
	if err != nil {
		return nil, fmt.Errorf("read ended Partnership receipts: %w", err)
	}
	defer rows.Close()

	var receipts []EndedPartnership
	for rows.Next() {
		receipt, err := scanEndedPartnership(rows)
		if err != nil {
			return nil, fmt.Errorf("read ended Partnership receipt: %w", err)
		}
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read ended Partnership receipts: %w", err)
	}
	return receipts, nil
}

// newEnding describes one Partnership at, or just before, its ending.
// remainingAfter is always the Protectee's Partner count once this Partnership
// is gone, so a preview and a committed ending state the same consequence.
func newEnding(partnership livePartnership, remainingAfter int, endedAt time.Time) PartnershipEnding {
	return PartnershipEnding{
		PartnershipID: partnership.id, ProtecteeEmail: partnership.protecteeEmail,
		Since: partnership.since, LogSharing: partnership.logSharing,
		RemainingPartners: remainingAfter, EndedAt: endedAt,
	}
}

// countPartnersAfterEnding is the Protectee's active-Partner count with this
// Partnership excluded, which is what every ending consequence is stated in
// terms of. Excluding it explicitly means the same query answers correctly
// before the ending commits and after.
func countPartnersAfterEnding(ctx context.Context, queryer rowQueryer, protecteeID, partnershipID int64) (int, error) {
	var partners int
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM partnerships
		WHERE protectee_id = ? AND id <> ? AND ended_at IS NULL`,
		protecteeID, partnershipID).Scan(&partners); err != nil {
		return 0, fmt.Errorf("read active Partner count: %w", err)
	}
	return partners, nil
}

// livePartnerEmails is the remaining Roster entitled to an ending signal.
func livePartnerEmails(ctx context.Context, tx *sql.Tx, protecteeID int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT users.email FROM partnerships
		JOIN users ON users.id = partnerships.partner_id
		WHERE partnerships.protectee_id = ? AND partnerships.ended_at IS NULL
		ORDER BY partnerships.accepted_at, partnerships.id`, protecteeID)
	if err != nil {
		return nil, fmt.Errorf("read remaining Roster: %w", err)
	}
	defer rows.Close()
	var emails []string
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, fmt.Errorf("read remaining Roster: %w", err)
		}
		emails = append(emails, email)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read remaining Roster: %w", err)
	}
	return emails, nil
}

// receiptURL is where a departing Partner reads the one thing they retain.
func (s *Service) receiptURL(partnershipID int64) string {
	link := *s.baseURL
	link.Path = "/partners/ended-partnerships/" + strconv.FormatInt(partnershipID, 10)
	return link.String()
}

// partnershipScope stands in for an invitation's token hash when deriving a
// delivery job's idempotency key. A Partnership has no bearer token, so the
// scope is the relationship plus the instant it ended — stable for one ending
// and different for a later one on a re-partnered relationship.
func partnershipScope(partnershipID int64, at time.Time) [32]byte {
	return sha256.Sum256(fmt.Appendf(nil, "partnership\x00%d\x00%d", partnershipID, at.UTC().Unix()))
}
