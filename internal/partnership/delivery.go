package partnership

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	mailmessage "github.com/JacobStephens2/nightloch-lite/internal/mail"
)

const (
	// maxDeliveryAttempts bounds retries before a job is reported as
	// undeliverable. Reachability stays a visible signal; it never changes
	// Partner powers or a lifecycle deadline (S3-58).
	maxDeliveryAttempts = 8
	retryBackoffBase    = time.Minute
	retryBackoffCap     = time.Hour

	// redactedBody replaces a delivered message body. A queued job must be
	// replayable verbatim after a restart (ADR 0002), but a sent one need not
	// be, and the invitation body carries a live bearer token.
	redactedBody = ""
)

// DeliveryState is the visible mail health of one per-recipient job.
type DeliveryState string

const (
	DeliveryQueued DeliveryState = "queued"
	// DeliveryDelayed marks a retryable failure that is still being retried.
	DeliveryDelayed DeliveryState = "delayed"
	DeliverySent    DeliveryState = "sent"
	// DeliveryDelivered and hard-bounce handling arrive with the provider
	// webhooks in 3B; the vocabulary is fixed here so no migration is needed.
	DeliveryDelivered DeliveryState = "delivered"
	DeliveryFailed    DeliveryState = "failed"
)

// Failed reports whether this delivery is over and unsuccessful, which is the
// one delivery state with a remedy: an invitation is immutable, so a Protectee
// corrects a bad address by cancelling and sending a new invitation (S3-47).
func (s DeliveryState) Failed() bool { return s == DeliveryFailed }

// Label is the approved product wording for this state (S3-47).
func (s DeliveryState) Label() string {
	switch s {
	case DeliverySent:
		return "Sent"
	case DeliveryDelivered:
		return "Delivered"
	case DeliveryDelayed:
		return "Delivery delayed"
	case DeliveryFailed:
		return "Invitation not delivered"
	default:
		return "Queued"
	}
}

// Reachability is what an active Roster row says about Nightloch's ability to
// reach one Partner by email. It is derived from that Partner's most recent
// delivery job, so a later success clears the warning while the failure itself
// stays in Activity. It is a visible signal only: an unreachable Partner keeps
// every power and every lifecycle clock keeps running (S3-58).
type Reachability string

const (
	// ReachabilityOK is the ordinary state and renders as no warning at all.
	ReachabilityOK          Reachability = ""
	ReachabilityDelayed     Reachability = "delayed"
	ReachabilityUnreachable Reachability = "unreachable"
)

// Warning reports whether this Roster row should show a reachability warning.
func (r Reachability) Warning() bool { return r != ReachabilityOK }

// Label is the approved product wording for this state (S3-47).
func (r Reachability) Label() string {
	switch r {
	case ReachabilityDelayed:
		return "Delivery delayed"
	case ReachabilityUnreachable:
		return "Nightloch cannot reach this Partner"
	default:
		return "Reachable"
	}
}

// reachabilityOf folds one delivery job's state into what the Roster shows. A
// queued job is not yet evidence of anything, so only an actual failure warns.
func reachabilityOf(state DeliveryState) Reachability {
	switch state {
	case DeliveryDelayed:
		return ReachabilityDelayed
	case DeliveryFailed:
		return ReachabilityUnreachable
	default:
		return ReachabilityOK
	}
}

// deliveryJob is one prepared message queued for exactly one recipient, in the
// same transaction as the mutation it reports (ADR 0002).
type deliveryJob struct {
	ActivityID    int64
	InvitationID  *int64
	PartnershipID *int64
	Message       preparedMessage
	CreatedAt     time.Time
}

func queueDelivery(ctx context.Context, tx *sql.Tx, job deliveryJob) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO mail_delivery_jobs(
			activity_id, invitation_id, partnership_id, message_kind,
			recipient_email, subject, body_text, body_html, reply_to,
			idempotency_key, state, attempts, next_attempt_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		job.ActivityID, job.InvitationID, job.PartnershipID, job.Message.Kind,
		job.Message.To, job.Message.Subject, job.Message.Text, job.Message.HTML,
		job.Message.ReplyTo, job.Message.IdempotencyKey, DeliveryQueued,
		job.CreatedAt.UTC().Unix(), job.CreatedAt.UTC().Unix())
	if err != nil {
		return fmt.Errorf("queue delivery job: %w", err)
	}
	return nil
}

// invitationDelivery is the scope every message about one invitation shares:
// the Activity entry the messages report, the invitation and any Partnership
// they hang off, and the token hash that keeps their idempotency keys stable.
type invitationDelivery struct {
	ActivityID    int64
	InvitationID  int64
	PartnershipID *int64
	TokenHash     [32]byte
	At            time.Time
}

// queueInvitationMessages queues one prepared message per recipient. Position
// within this scope is what separates two recipients' idempotency keys, so a
// retry after a crashed process cannot become a second provider send.
func queueInvitationMessages(ctx context.Context, tx *sql.Tx, messages []preparedMessage, scope invitationDelivery) error {
	for sequence, message := range messages {
		message.IdempotencyKey = idempotencyKey(message.Kind, message.To, scope.TokenHash, sequence+1)
		if err := queueDelivery(ctx, tx, deliveryJob{
			ActivityID:    scope.ActivityID,
			InvitationID:  &scope.InvitationID,
			PartnershipID: scope.PartnershipID,
			Message:       message,
			CreatedAt:     scope.At,
		}); err != nil {
			return err
		}
	}
	return nil
}

// dueJob is one delivery job the dispatcher may attempt now.
type dueJob struct {
	id       int64
	attempts int
	envelope mailmessage.Envelope
}

// DispatchDue transmits up to limit due delivery jobs and reports how many
// were sent. Jobs live in SQLite, so work queued before a restart is picked up
// by the next process; a job interrupted mid-send returns as due after its
// backoff, and its stable idempotency key keeps the provider from sending
// twice. A delivery failure is reported to the caller but never rolls back or
// pauses the Partnership state it reports on.
func (s *Service) DispatchDue(ctx context.Context, sender mailmessage.Sender, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	now := s.now().UTC()
	jobs, err := s.readDueJobs(ctx, now, limit)
	if err != nil {
		return 0, err
	}

	sent := 0
	var failures []error
	for _, job := range jobs {
		claimed, err := s.claimDeliveryJob(ctx, job, now)
		if err != nil {
			return sent, err
		}
		if !claimed {
			continue
		}
		receipt, sendErr := sender.Send(ctx, job.envelope)
		if sendErr != nil {
			failures = append(failures, fmt.Errorf("delivery job %d: %w", job.id, sendErr))
			if err := s.recordDeliveryFailure(ctx, job.id, job.attempts+1, sendErr); err != nil {
				return sent, err
			}
			continue
		}
		if err := s.recordDeliverySuccess(ctx, job.id, receipt.ProviderMessageID, now); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, errors.Join(failures...)
}

func (s *Service) readDueJobs(ctx context.Context, now time.Time, limit int) ([]dueJob, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, attempts, recipient_email, subject, body_text, body_html, reply_to, idempotency_key
		FROM mail_delivery_jobs
		WHERE state IN (?, ?) AND next_attempt_at <= ?
		ORDER BY next_attempt_at, id
		LIMIT ?`, DeliveryQueued, DeliveryDelayed, now.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("read due delivery jobs: %w", err)
	}
	defer rows.Close()

	var jobs []dueJob
	for rows.Next() {
		var job dueJob
		if err := rows.Scan(&job.id, &job.attempts, &job.envelope.To, &job.envelope.Subject,
			&job.envelope.Text, &job.envelope.HTML, &job.envelope.ReplyTo, &job.envelope.IdempotencyKey); err != nil {
			return nil, fmt.Errorf("read due delivery job: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read due delivery jobs: %w", err)
	}
	return jobs, nil
}

// claimDeliveryJob takes ownership of one job by counting the attempt and
// pushing its next attempt past the backoff before any network call. A process
// that dies mid-send therefore retries later instead of stalling in a
// half-claimed state.
func (s *Service) claimDeliveryJob(ctx context.Context, job dueJob, now time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE mail_delivery_jobs
		SET attempts = attempts + 1, next_attempt_at = ?
		WHERE id = ? AND state IN (?, ?) AND next_attempt_at <= ? AND attempts = ?`,
		now.Add(retryBackoff(job.attempts+1)).Unix(), job.id,
		DeliveryQueued, DeliveryDelayed, now.Unix(), job.attempts)
	if err != nil {
		return false, fmt.Errorf("claim delivery job: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim delivery job: %w", err)
	}
	return affected == 1, nil
}

// recordDeliverySuccess drops the rendered body and keeps the provider's own
// message id. The message is gone to the provider, so keeping a live
// invitation bearer token in a second table would widen the token's blast
// radius for no operational gain. Recipient, subject, idempotency key, and the
// provider message id remain: the first three are the audit trail, and the
// last is what a later delivery, bounce, or complaint callback names.
func (s *Service) recordDeliverySuccess(ctx context.Context, id int64, providerMessageID string, now time.Time) error {
	return s.inDeliveryTx(ctx, "record delivery success", func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			UPDATE mail_delivery_jobs
			SET sent_at = ?, last_error = NULL, body_text = ?, body_html = ?,
			    provider_message_id = ?
			WHERE id = ?`, now.Unix(), redactedBody, redactedBody,
			nullString(providerMessageID), id); err != nil {
			return err
		}
		return applyDeliveryState(ctx, tx, id, DeliverySent, now, "")
	})
}

func (s *Service) recordDeliveryFailure(ctx context.Context, id int64, attempts int, cause error) error {
	state := DeliveryDelayed
	if attempts >= maxDeliveryAttempts {
		state = DeliveryFailed
	}
	return s.inDeliveryTx(ctx, "record delivery failure", func(tx *sql.Tx) error {
		return applyDeliveryState(ctx, tx, id, state, s.now().UTC(), cause.Error())
	})
}

// inDeliveryTx runs one delivery-state change and its Activity entry together.
// Neither is worth having without the other: a warning nobody can trace back
// to an event, or an event that changed nothing, would both be misleading.
func (s *Service) inDeliveryTx(ctx context.Context, what string, apply func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	defer tx.Rollback()
	if err := apply(tx); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// applyDeliveryState moves one job to state and appends Activity when that
// changes what an authorized viewer is told about reachability. Only the
// change is worth an entry: retrying a delayed job eight times is one
// reachability story, not eight, and Activity is append-only (S3-17).
func applyDeliveryState(ctx context.Context, tx *sql.Tx, id int64, state DeliveryState, at time.Time, cause string) error {
	var previous DeliveryState
	if err := tx.QueryRowContext(ctx,
		"SELECT state FROM mail_delivery_jobs WHERE id = ?", id).Scan(&previous); err != nil {
		return fmt.Errorf("read delivery job state: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE mail_delivery_jobs SET state = ?, last_error = ? WHERE id = ?",
		state, nullString(cause), id); err != nil {
		return fmt.Errorf("store delivery job state: %w", err)
	}
	before, after := reachabilityOf(previous), reachabilityOf(state)
	if before == after {
		return nil
	}
	return appendDeliveryActivity(ctx, tx, id, after, at, cause)
}

// deliveryActivityAction is the Activity action for arriving at reachability.
// The three actions are stable strings, so a later Activity read path renders
// them without re-deriving anything from a delivery job.
func deliveryActivityAction(reachability Reachability) string {
	switch reachability {
	case ReachabilityDelayed:
		return ActionDeliveryDelayed
	case ReachabilityUnreachable:
		return ActionDeliveryFailed
	default:
		return ActionDeliveryRecovered
	}
}

// appendDeliveryActivity records one reachability change against the Protectee
// whose Activity the failing message already belonged to. A job whose own
// Activity entry is gone records nothing: there is no Protectee to attribute
// it to, and inventing one would put a delivery event on the wrong chronology.
func appendDeliveryActivity(ctx context.Context, tx *sql.Tx, id int64, reachability Reachability, at time.Time, cause string) error {
	var protecteeID sql.NullInt64
	var recipient, kind string
	var invitationID, partnershipID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT origin.protectee_id, job.recipient_email, job.message_kind,
		       job.invitation_id, job.partnership_id
		FROM mail_delivery_jobs job
		LEFT JOIN activity_entries origin ON origin.id = job.activity_id
		WHERE job.id = ?`, id).
		Scan(&protecteeID, &recipient, &kind, &invitationID, &partnershipID); err != nil {
		return fmt.Errorf("read delivery job origin: %w", err)
	}
	if !protecteeID.Valid {
		return nil
	}
	details := map[string]any{"message_kind": kind, "reachability": string(reachability)}
	// The provider's own words can name a mailbox or a policy Nightloch does
	// not control, so they stay out of Activity details; whether a delivery
	// failed is the fact a viewer is entitled to.
	if cause != "" {
		details["failed"] = true
	}
	_, err := appendActivity(ctx, tx, activityEntry{
		ProtecteeID:   protecteeID.Int64,
		Action:        deliveryActivityAction(reachability),
		TargetEmail:   recipient,
		PartnershipID: nullableID(partnershipID),
		InvitationID:  nullableID(invitationID),
		OccurredAt:    at,
		Details:       details,
	})
	return err
}

func nullableID(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func retryBackoff(attempts int) time.Duration {
	if attempts >= maxDeliveryAttempts {
		return retryBackoffCap
	}
	return min(retryBackoffBase<<(attempts-1), retryBackoffCap)
}
