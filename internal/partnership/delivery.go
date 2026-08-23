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
	// maxDeliveryAttempts bounds retries before a job is reported as failed.
	// Reachability stays a visible signal; it never changes authorization or
	// a lifecycle deadline (S3-58).
	maxDeliveryAttempts = 8
	retryBackoffBase    = time.Minute
	retryBackoffCap     = time.Hour
)

// deliveryJob is one prepared message queued for exactly one recipient, in the
// same transaction as the mutation it reports (ADR 0002).
type deliveryJob struct {
	ActivityID     int64
	InvitationID   *int64
	PartnershipID  *int64
	Message        preparedMessage
	IdempotencyKey string
	CreatedAt      time.Time
}

func queueDelivery(ctx context.Context, tx *sql.Tx, job deliveryJob) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO mail_delivery_jobs(
			activity_id, invitation_id, partnership_id, message_kind,
			recipient_email, subject, body_text, body_html, reply_to,
			idempotency_key, state, attempts, next_attempt_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'queued', 0, ?, ?)`,
		job.ActivityID, job.InvitationID, job.PartnershipID, job.Message.Kind,
		job.Message.Recipient, job.Message.Subject, job.Message.Text, job.Message.HTML,
		job.Message.ReplyTo, job.IdempotencyKey, job.CreatedAt.UTC().Unix(), job.CreatedAt.UTC().Unix())
	if err != nil {
		return fmt.Errorf("queue delivery job: %w", err)
	}
	return nil
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, attempts, recipient_email, subject, body_text, body_html, reply_to, idempotency_key
		FROM mail_delivery_jobs
		WHERE state = 'queued' AND next_attempt_at <= ?
		ORDER BY next_attempt_at, id
		LIMIT ?`, now.Unix(), limit)
	if err != nil {
		return 0, fmt.Errorf("read due delivery jobs: %w", err)
	}
	type due struct {
		id       int64
		attempts int
		envelope mailmessage.Envelope
	}
	var jobs []due
	for rows.Next() {
		var job due
		if err := rows.Scan(&job.id, &job.attempts, &job.envelope.To, &job.envelope.Subject,
			&job.envelope.Text, &job.envelope.HTML, &job.envelope.ReplyTo, &job.envelope.IdempotencyKey); err != nil {
			rows.Close()
			return 0, fmt.Errorf("read due delivery job: %w", err)
		}
		jobs = append(jobs, job)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read due delivery jobs: %w", err)
	}

	sent := 0
	var failures []error
	for _, job := range jobs {
		claimed, err := s.claimDeliveryJob(ctx, job.id, job.attempts, now)
		if err != nil {
			return sent, err
		}
		if !claimed {
			continue
		}
		if err := sender.Send(ctx, job.envelope); err != nil {
			failures = append(failures, fmt.Errorf("delivery job %d: %w", job.id, err))
			if err := s.recordDeliveryFailure(ctx, job.id, job.attempts+1, err); err != nil {
				return sent, err
			}
			continue
		}
		if _, err := s.db.ExecContext(ctx,
			"UPDATE mail_delivery_jobs SET state = 'sent', sent_at = ?, last_error = NULL WHERE id = ?",
			now.Unix(), job.id); err != nil {
			return sent, fmt.Errorf("record delivery success: %w", err)
		}
		sent++
	}
	return sent, errors.Join(failures...)
}

// claimDeliveryJob takes ownership of one job by counting the attempt and
// pushing its next attempt past the backoff before any network call. A process
// that dies mid-send therefore retries later instead of stalling in a
// half-claimed state.
func (s *Service) claimDeliveryJob(ctx context.Context, id int64, attempts int, now time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE mail_delivery_jobs
		SET attempts = attempts + 1, next_attempt_at = ?
		WHERE id = ? AND state = 'queued' AND next_attempt_at <= ? AND attempts = ?`,
		now.Add(retryBackoff(attempts+1)).Unix(), id, now.Unix(), attempts)
	if err != nil {
		return false, fmt.Errorf("claim delivery job: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim delivery job: %w", err)
	}
	return affected == 1, nil
}

func (s *Service) recordDeliveryFailure(ctx context.Context, id int64, attempts int, cause error) error {
	state := "queued"
	if attempts >= maxDeliveryAttempts {
		state = "failed"
	}
	if _, err := s.db.ExecContext(ctx,
		"UPDATE mail_delivery_jobs SET state = ?, last_error = ? WHERE id = ?",
		state, cause.Error(), id); err != nil {
		return fmt.Errorf("record delivery failure: %w", err)
	}
	return nil
}

func retryBackoff(attempts int) time.Duration {
	backoff := retryBackoffBase << min(attempts-1, 10)
	return min(backoff, retryBackoffCap)
}
