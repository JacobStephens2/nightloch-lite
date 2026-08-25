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
	// CoolingOffEndsAt is the deadline the Protectee already requested, and
	// zero when they have requested nothing. It turns the Partner's immediate
	// ending into the **End now** the Protectee asked them to consider (S3-08).
	CoolingOffEndsAt time.Time
}

// PartnerLockActive reports the Protectee's lock state after this ending.
func (e PartnershipEnding) PartnerLockActive() bool { return e.RemainingPartners > 0 }

// CoolingOffRunning reports whether this ending resolves a Cooling-off the
// Protectee already requested rather than starting from an unscheduled
// Partnership.
func (e PartnershipEnding) CoolingOffRunning() bool { return !e.CoolingOffEndsAt.IsZero() }

// livePartnership is one active Partnership with both parties' identities and
// whatever Cooling-off it currently carries. A scheduled ending is an ordinary
// attribute of a live Partnership: nothing about the relationship's powers
// changes until the deadline actually arrives.
type livePartnership struct {
	id                    int64
	protecteeID           int64
	protecteeEmail        string
	partnerEmail          string
	logSharing            bool
	since                 time.Time
	coolingOffRequestedAt time.Time
	coolingOffEndsAt      time.Time
}

// partnershipRole is which side of a Partnership a caller is claiming to be.
// Authorization is the query itself: the wrong side simply matches no row, so
// a Protectee, another Partner, or a stranger cannot act on — or probe for — a
// Partnership that is not theirs. Every role scopes the read to one User, so
// no value of this type can widen it.
type partnershipRole string

const (
	byPartner   partnershipRole = "partnership.partner_id = ?"
	byProtectee partnershipRole = "partnership.protectee_id = ?"
)

// livePartnershipProjection projects one live Partnership with both identities
// and whatever Cooling-off it carries, up to and including the WHERE keyword;
// each caller appends its own predicate.
const livePartnershipProjection = `
	SELECT partnership.id, partnership.protectee_id, protectee.email, partner.email,
	       partnership.log_sharing, partnership.accepted_at,
	       COALESCE(partnership.cooling_off_requested_at, 0),
	       COALESCE(partnership.cooling_off_ends_at, 0)
	FROM partnerships partnership
	JOIN users protectee ON protectee.id = partnership.protectee_id
	JOIN users partner ON partner.id = partnership.partner_id
	WHERE partnership.ended_at IS NULL AND `

// livePartnershipQuery names one Partnership by id, which is how every caller
// acting on a single relationship reaches it.
const livePartnershipQuery = livePartnershipProjection + `partnership.id = ? AND `

// readLivePartnership resolves the live Partnership the given side holds, or
// ErrPartnershipUnavailable. Every request path reads through here, so a
// caller always has to name which side of the relationship they are.
func readLivePartnership(ctx context.Context, queryer rowQueryer, role partnershipRole, partnershipID, userID int64) (livePartnership, error) {
	if partnershipID <= 0 || userID <= 0 {
		return livePartnership{}, ErrPartnershipUnavailable
	}
	return scanLivePartnership(queryer.QueryRowContext(ctx,
		livePartnershipQuery+string(role), partnershipID, userID))
}

// readDuePartnership resolves one live Partnership for Nightloch's own
// due-transition runner, which acts for no User and so scopes to neither side.
// It is a separate function rather than a third role precisely so that no
// request path can reach an unauthorized read by passing a role value.
func readDuePartnership(ctx context.Context, queryer rowQueryer, partnershipID int64) (livePartnership, error) {
	if partnershipID <= 0 {
		return livePartnership{}, ErrPartnershipUnavailable
	}
	return scanLivePartnership(queryer.QueryRowContext(ctx,
		livePartnershipQuery+"partnership.cooling_off_ends_at IS NOT NULL", partnershipID))
}

func scanLivePartnership(row rowScanner) (livePartnership, error) {
	var partnership livePartnership
	var logSharing int
	var acceptedAt, requestedAt, endsAt int64
	err := row.Scan(&partnership.id, &partnership.protecteeID, &partnership.protecteeEmail,
		&partnership.partnerEmail, &logSharing, &acceptedAt, &requestedAt, &endsAt)
	if errors.Is(err, sql.ErrNoRows) {
		return livePartnership{}, ErrPartnershipUnavailable
	}
	if err != nil {
		return livePartnership{}, fmt.Errorf("read Partnership: %w", err)
	}
	partnership.logSharing = intToBool(logSharing)
	partnership.since = time.Unix(acceptedAt, 0).UTC()
	if requestedAt != 0 {
		partnership.coolingOffRequestedAt = time.Unix(requestedAt, 0).UTC()
		partnership.coolingOffEndsAt = time.Unix(endsAt, 0).UTC()
	}
	return partnership, nil
}

// EndablePartnership is the read behind the confirmation screen: the same
// consequences EndPartnership will commit, without committing them. Only the
// Partner on the Partnership reaches it.
func (s *Service) EndablePartnership(ctx context.Context, partnershipID, partnerID int64) (PartnershipEnding, error) {
	partnership, err := readLivePartnership(ctx, s.db, byPartner, partnershipID, partnerID)
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
	partnership, err := readLivePartnership(ctx, tx, byPartner, command.PartnershipID, command.PartnerID)
	if err != nil {
		return PartnershipEnding{}, err
	}
	if strings.TrimSpace(command.Confirmation) != endConfirmationWord {
		return PartnershipEnding{}, ErrEndConfirmationRequired
	}
	// The same route serves both endings a Partner may perform. Ending an
	// unscheduled Partnership is their own independent act; ending one the
	// Protectee already asked to end is the **End now** they were invited to
	// consider, so the two are stored, audited, and announced apart (S3-08).
	reason := EndReasonPartnerEnded
	if !partnership.coolingOffEndsAt.IsZero() {
		reason = EndReasonPartnerAgreedNow
	}
	// First commit wins: only the transaction that still sees a live
	// Partnership may end it, and every later mutation hangs off that row.
	result, err := tx.ExecContext(ctx, `
		UPDATE partnerships SET ended_at = ?, ended_reason = ?
		WHERE id = ? AND ended_at IS NULL`, now.Unix(), reason, partnership.id)
	if err != nil {
		return PartnershipEnding{}, fmt.Errorf("end Partnership: %w", err)
	}
	if err := requireOneRow(result, ErrPartnershipUnavailable); err != nil {
		return PartnershipEnding{}, err
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
			"ended_reason":        reason,
			"log_sharing":         partnership.logSharing,
			"partner_count":       remaining,
			"partner_lock_active": ending.PartnerLockActive(),
		},
	})
	if err != nil {
		return PartnershipEnding{}, err
	}

	if err := s.queueEndingMessages(ctx, tx, partnership, remaining, now, reason, activityID); err != nil {
		return PartnershipEnding{}, err
	}
	// This ending may have left a Cooling-off elsewhere on the Roster holding
	// the Partner lock up on its own, which is its own transition with its own
	// Activity entry and alert.
	if err := s.announceFinalCoolingOff(ctx, tx, partnership, now); err != nil {
		return PartnershipEnding{}, err
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
	PartnershipID int64
	// ProtecteeEmail is empty once that Protectee's User is deleted, because
	// the receipt then identifies them only as Deleted User. Read it through
	// ProtecteeLabel rather than directly, so a screen cannot render a blank
	// where a person used to be.
	ProtecteeEmail string
	// ProtecteeDeleted marks the minimal retained receipt: the ending cause
	// and the exact instant, and nothing else about the relationship — no
	// address, no start, no Log sharing, no Roster, no Activity (S3-71).
	ProtecteeDeleted bool
	Since            time.Time
	EndedAt          time.Time
	LogSharing       bool
	EndedReason      string
}

// ProtecteeLabel is who this receipt is about, which is Deleted User once
// their User is gone.
func (e EndedPartnership) ProtecteeLabel() string {
	if e.ProtecteeDeleted {
		return DeletedUserLabel
	}
	return e.ProtecteeEmail
}

// EndedByUserDeletion reports the Partnership ending because a User on it was
// deleted, which is the one ending cause that outlives the identity it names.
func (e EndedPartnership) EndedByUserDeletion() bool { return e.EndedReason == EndReasonUserDeleted }

// PartnerEnded, AgreedToEndNow, and EndedAfterCoolingOff name the three ways a
// Partnership reaches its end, so a template can state the cause without
// restating the stored vocabulary.
func (e EndedPartnership) PartnerEnded() bool { return e.EndedReason == EndReasonPartnerEnded }

// AgreedToEndNow reports the Partner waiving a Cooling-off the Protectee had
// requested.
func (e EndedPartnership) AgreedToEndNow() bool { return e.EndedReason == EndReasonPartnerAgreedNow }

// EndedAfterCoolingOff reports Nightloch completing the Protectee's request on
// its deadline.
func (e EndedPartnership) EndedAfterCoolingOff() bool {
	return e.EndedReason == EndReasonCoolingOffCompleted
}

// endedReceiptQuery projects the receipts one former Partner holds. Both
// callers scope it to partner_id, so a receipt is unreachable by anyone else,
// including the Protectee and the Partners still on that Roster.
//
// A deleted Protectee is projected as the pseudonym here rather than filtered
// out or fixed up by a caller: the receipt survives their deletion, and the
// address, the start, and the Log sharing do not (S3-71).
const endedReceiptQuery = `
	SELECT partnership.id,
	       CASE WHEN protectee.deleted_at IS NULL THEN protectee.email ELSE '' END,
	       protectee.deleted_at IS NOT NULL,
	       partnership.accepted_at, partnership.ended_at, partnership.log_sharing,
	       partnership.ended_reason
	FROM partnerships partnership
	JOIN users protectee ON protectee.id = partnership.protectee_id
	WHERE partnership.ended_at IS NOT NULL AND partnership.partner_id = ?`

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface{ Scan(...any) error }

func scanEndedPartnership(scanner rowScanner) (EndedPartnership, error) {
	var receipt EndedPartnership
	var since, endedAt int64
	var logSharing int
	if err := scanner.Scan(&receipt.PartnershipID, &receipt.ProtecteeEmail,
		&receipt.ProtecteeDeleted, &since, &endedAt, &logSharing,
		&receipt.EndedReason); err != nil {
		return EndedPartnership{}, err
	}
	receipt.EndedAt = time.Unix(endedAt, 0).UTC()
	if receipt.ProtecteeDeleted {
		// Everything else the receipt would carry describes a relationship
		// with a person who no longer exists, so it is not read at all.
		return receipt, nil
	}
	receipt.Since = time.Unix(since, 0).UTC()
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

// queueEndingMessages queues the one ending signal, once per entitled
// recipient. Both parties and every remaining Partner are entitled to it
// (S3-20), each in their own envelope so the recipient list is never exposed
// (S3-21). reason selects the approved variant, which is the only thing that
// differs between an immediate end, an End now, and a completed Cooling-off.
func (s *Service) queueEndingMessages(ctx context.Context, tx *sql.Tx, partnership livePartnership,
	remaining int, endedAt time.Time, reason string, activityID int64) error {

	recipients := []string{partnership.protecteeEmail, partnership.partnerEmail}
	roster, err := livePartnerEmails(ctx, tx, partnership.protecteeID)
	if err != nil {
		return err
	}
	recipients = append(recipients, roster...)

	scope := partnershipScope(partnership.id, endedAt)
	for sequence, recipient := range recipients {
		message := renderPartnershipEnded(endingMessage{
			ProtecteeEmail: partnership.protecteeEmail,
			PartnerEmail:   partnership.partnerEmail,
			EndedAt:        endedAt,
			EndedReason:    reason,
			PartnerCount:   remaining,
			DashboardURL:   s.dashboardURL(),
			ReceiptURL:     s.receiptURL(partnership.id),
		}, recipient)
		message.IdempotencyKey = idempotencyKey(message.Kind, message.To, scope, sequence+1)
		if err := queueDelivery(ctx, tx, deliveryJob{
			ActivityID:    activityID,
			PartnershipID: &partnership.id,
			Message:       message,
			CreatedAt:     endedAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

// newEnding describes one Partnership at, or just before, its ending.
// remainingAfter is always the Protectee's Partner count once this Partnership
// is gone, so a preview and a committed ending state the same consequence.
func newEnding(partnership livePartnership, remainingAfter int, endedAt time.Time) PartnershipEnding {
	return PartnershipEnding{
		PartnershipID: partnership.id, ProtecteeEmail: partnership.protecteeEmail,
		Since: partnership.since, LogSharing: partnership.logSharing,
		RemainingPartners: remainingAfter, EndedAt: endedAt,
		CoolingOffEndsAt: partnership.coolingOffEndsAt,
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

// partnersAfterDeadlineBody counts the Partnerships that outlast one
// Partnership's scheduled ending: every other live Partnership except those
// already scheduled to end at or before it. Concurrent Cooling-off is what
// makes this different from the plain Partner count — several countdowns share
// one 72-hour horizon rather than running in series — so it, and not
// countPartnersAfterEnding, is what a scheduled ending's consequences are
// stated in. A zero deadline means "no ending is scheduled", and every other
// Partnership outlasts it.
//
// The question is asked two ways — correlated to a Roster row, and against a
// deadline a caller names — so the condition itself is written once here and
// both forms are derived from it.
const partnersAfterDeadlineBody = `COUNT(*) FROM partnerships later
	WHERE later.protectee_id = %[1]s AND later.id <> %[2]s AND later.ended_at IS NULL
	  AND (later.cooling_off_ends_at IS NULL OR later.cooling_off_ends_at > %[3]s)`

// partnersAfterDeadlineProjection correlates the count to a Partnership row
// aliased `partnerships`, using that row's own deadline.
var partnersAfterDeadlineProjection = "(SELECT " + fmt.Sprintf(partnersAfterDeadlineBody,
	"partnerships.protectee_id", "partnerships.id",
	"COALESCE(partnerships.cooling_off_ends_at, 0)") + ")"

// partnersAfterDeadlineQuery asks the same question about a Protectee, a
// Partnership, and a deadline the caller supplies — including one no
// Partnership carries yet, which is how a Cooling-off preview states the
// consequences of a request nobody has made.
var partnersAfterDeadlineQuery = "SELECT " + fmt.Sprintf(partnersAfterDeadlineBody, "?", "?", "?")

func countPartnersAfterDeadline(ctx context.Context, queryer rowQueryer,
	protecteeID, partnershipID int64, deadline time.Time) (int, error) {

	var partners int
	if err := queryer.QueryRowContext(ctx, partnersAfterDeadlineQuery,
		protecteeID, partnershipID, deadlineUnix(deadline)).Scan(&partners); err != nil {
		return 0, fmt.Errorf("read Partner count after a scheduled ending: %w", err)
	}
	return partners, nil
}

// scheduledEnding is one Partnership's Cooling-off as every view of it carries
// it: the deadline itself and the horizon it sits in. The Protectee's Roster
// row and their Partner's own row both embed it, so the two sides cannot
// disagree about what a countdown means (S3-07, S3-15).
type scheduledEnding struct {
	// CoolingOffEndsAt is the exact deadline a requested ending will complete
	// on, and zero when no ending is scheduled.
	CoolingOffEndsAt time.Time
	// PartnersAfterDeadline is how many Partners still protect the Protectee
	// once that deadline arrives. Concurrent countdowns share one horizon, so
	// this is not "every other Partner": a Partnership already scheduled to
	// end at or before this deadline is not there afterwards either. A
	// Partnership with no scheduled ending simply counts the others.
	PartnersAfterDeadline int
}

// CoolingOffRunning reports whether this Partnership is scheduled to end.
func (e scheduledEnding) CoolingOffRunning() bool { return !e.CoolingOffEndsAt.IsZero() }

// EndsPartnerLock reports whether this scheduled ending is the one that
// deactivates the Protectee's Partner lock and restores self-loosening. It is
// the dynamic final-Partnership question: an ending that is not final today
// becomes final when the Partnerships outlasting it end first, and stops being
// final when a new Partner accepts.
func (e scheduledEnding) EndsPartnerLock() bool {
	return e.CoolingOffRunning() && e.PartnersAfterDeadline == 0
}

// deadlineUnix is the stored form of a Cooling-off deadline, with the zero
// time — no scheduled ending — stored as 0 rather than as a 1754 timestamp.
func deadlineUnix(deadline time.Time) int64 {
	if deadline.IsZero() {
		return 0
	}
	return deadline.Unix()
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

// requireOneRow turns a guarded UPDATE that changed nothing into the caller's
// own refusal, which is how every first-commit-wins race in this package is
// settled.
func requireOneRow(result sql.Result, lost error) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected rows: %w", err)
	}
	if affected != 1 {
		return lost
	}
	return nil
}

// partnershipURL is where the affected Partner reviews a Cooling-off request
// and, if they agree, ends it now.
func (s *Service) partnershipURL(partnershipID int64) string {
	link := *s.baseURL
	link.Path = "/partners/partnerships/" + strconv.FormatInt(partnershipID, 10) + "/end"
	return link.String()
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

// activityScope keys a delivery job to the one Activity entry it reports on.
// It is what an event that can repeat on the same Partnership in the same
// second uses instead of partnershipScope: the entry id is durable, unique per
// event, and unchanged by a retry, so the key stays stable for exactly one
// send without two distinct signals ever colliding.
func activityScope(activityID int64) [32]byte {
	return sha256.Sum256(fmt.Appendf(nil, "activity\x00%d", activityID))
}
