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
		if sendErr := sender.Send(ctx, job.envelope); sendErr != nil {
			failures = append(failures, fmt.Errorf("delivery job %d: %w", job.id, sendErr))
			if err := s.recordDeliveryFailure(ctx, job.id, job.attempts+1, sendErr); err != nil {
				return sent, err
			}
			continue
		}
		if err := s.recordDeliverySuccess(ctx, job.id, now); err != nil {
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

// recordDeliverySuccess drops the rendered body. The message is gone to the
// provider, so keeping a live invitation bearer token in a second table would
// widen the token's blast radius for no operational gain. Recipient, subject,
// and idempotency key remain for the audit trail.
func (s *Service) recordDeliverySuccess(ctx context.Context, id int64, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE mail_delivery_jobs
		SET state = ?, sent_at = ?, last_error = NULL, body_text = ?, body_html = ?
		WHERE id = ?`, DeliverySent, now.Unix(), redactedBody, redactedBody, id); err != nil {
		return fmt.Errorf("record delivery success: %w", err)
	}
	return nil
}

func (s *Service) recordDeliveryFailure(ctx context.Context, id int64, attempts int, cause error) error {
	state := DeliveryDelayed
	if attempts >= maxDeliveryAttempts {
		state = DeliveryFailed
	}
	if _, err := s.db.ExecContext(ctx,
		"UPDATE mail_delivery_jobs SET state = ?, last_error = ? WHERE id = ?",
		state, cause.Error(), id); err != nil {
		return fmt.Errorf("record delivery failure: %w", err)
	}
	return nil
}

func retryBackoff(attempts int) time.Duration {
	if attempts >= maxDeliveryAttempts {
		return retryBackoffCap
	}
	return min(retryBackoffBase<<(attempts-1), retryBackoffCap)
}
