package partnership

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	// ActionDeliveryDelayed, ActionDeliveryFailed, and ActionDeliveryRecovered
	// are the stable Activity actions for Partner reachability. They record
	// what Nightloch could or could not tell someone; none of them is a
	// Partnership mutation, and none changes a power or a deadline (S3-58).
	ActionDeliveryDelayed   = "partner_delivery_delayed"
	ActionDeliveryFailed    = "partner_delivery_failed"
	ActionDeliveryRecovered = "partner_delivery_recovered"
)

// DeliveryEventType is one provider report about a message already accepted
// for delivery. The values are Resend's own event names, so a callback needs
// no translation table on the way in.
type DeliveryEventType string

const (
	EventSent      DeliveryEventType = "email.sent"
	EventDelivered DeliveryEventType = "email.delivered"
	// EventDelayed is Resend's temporary-issue event and the only one that
	// says a message is still on its way.
	EventDelayed DeliveryEventType = "email.delivery_delayed"
	// EventBounced is a mail server permanently rejecting the message, and
	// EventComplained is a delivered message the recipient marked as spam.
	// Both mean Nightloch cannot reach that Partner (S3-47).
	EventBounced    DeliveryEventType = "email.bounced"
	EventComplained DeliveryEventType = "email.complained"
	// EventFailed is a send that errored outright, and EventSuppressed is
	// Resend refusing to send at all — an address it already knows bounced or
	// complained. Neither message exists anywhere, so both are as final as a
	// bounce: pretending they were merely delayed would promise a retry that
	// is never coming.
	EventFailed     DeliveryEventType = "email.failed"
	EventSuppressed DeliveryEventType = "email.suppressed"
)

// ErrUnknownDeliveryEvent rejects an event kind this module does not act on.
// The caller answers the provider successfully anyway: an event Nightloch
// ignores is not a callback the provider should keep retrying.
var ErrUnknownDeliveryEvent = errors.New("unrecognized delivery event type")

// DeliveryEvent is one authenticated provider report about one message. The
// transport authenticates it; this module decides what it means.
type DeliveryEvent struct {
	// ProviderEventID is the provider's own id for this event and is the unit
	// of idempotency: the same id applied twice changes state once.
	ProviderEventID string
	// ProviderMessageID names the message, and therefore the single recipient,
	// the event reports on.
	ProviderMessageID string
	Type              DeliveryEventType
	// Permanent separates a hard bounce from a transient one. A hard bounce
	// means the address is unreachable; a transient one is still being tried.
	Permanent bool
	// OccurredAt is the provider's clock. Providers do not promise ordering,
	// so it — not arrival — decides whether this event is the newest word on
	// the message.
	OccurredAt time.Time
}

// state is what this event says the job's delivery state now is.
func (e DeliveryEvent) state() (DeliveryState, error) {
	switch e.Type {
	case EventSent:
		return DeliverySent, nil
	case EventDelivered:
		return DeliveryDelivered, nil
	case EventDelayed:
		return DeliveryDelayed, nil
	case EventComplained, EventFailed, EventSuppressed:
		return DeliveryFailed, nil
	case EventBounced:
		// Resend raises email.bounced only for a permanent rejection and has
		// email.delivery_delayed for the temporary case, so a bounce is
		// terminal unless its own type says otherwise.
		if e.Permanent {
			return DeliveryFailed, nil
		}
		return DeliveryDelayed, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownDeliveryEvent, e.Type)
	}
}

// RecordDeliveryEvent applies one provider delivery event to the message it
// names. It is idempotent on ProviderEventID, so a callback the provider
// redelivers changes state exactly once, and it is ordered on the provider's
// own clock, so a delivery stamped before a bounce cannot clear it.
//
// An event naming a message this control plane never queued — a sign-in link,
// or anything sent from the same provider account — is recorded and otherwise
// ignored. Nothing here touches a Partnership, a power, or a Cooling-off
// deadline: reachability is a visible signal only (S3-58).
func (s *Service) RecordDeliveryEvent(ctx context.Context, event DeliveryEvent) error {
	if event.ProviderEventID == "" || event.ProviderMessageID == "" {
		return errors.New("delivery event names no provider event or message")
	}
	state, err := event.state()
	if err != nil {
		return err
	}
	now := s.now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delivery event: %w", err)
	}
	defer tx.Rollback()

	job, found, err := readJobForMessage(ctx, tx, event.ProviderMessageID)
	if err != nil {
		return err
	}
	applied := found && job.supersededBy(event.OccurredAt.UTC(), state)
	if err := recordEventReceipt(ctx, tx, event, sql.NullInt64{Int64: job.id, Valid: found}, applied, now); err != nil {
		// A duplicate is the provider doing exactly what it promises. It is
		// not a failure, and the first copy already did the work.
		if errors.Is(err, errDuplicateDeliveryEvent) {
			return nil
		}
		return err
	}
	if applied {
		if _, err := tx.ExecContext(ctx,
			"UPDATE mail_delivery_jobs SET last_event_at = ? WHERE id = ?",
			event.OccurredAt.UTC().Unix(), job.id); err != nil {
			return fmt.Errorf("store delivery event time: %w", err)
		}
		if err := applyDeliveryState(ctx, tx, job.id, state, event.OccurredAt.UTC(), failureCause(state)); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delivery event: %w", err)
	}
	return nil
}

// failureCause marks a job whose delivery ended badly without storing the
// provider's own prose, which can name a mailbox or a policy that is not
// Nightloch's to disclose.
func failureCause(state DeliveryState) string {
	if state == DeliveryFailed || state == DeliveryDelayed {
		return "reported by the delivery provider"
	}
	return ""
}

// eventJob is the delivery job a provider event names, its current delivery
// state, and the provider clock of the newest event already applied to it.
type eventJob struct {
	id          int64
	state       DeliveryState
	lastEventAt time.Time
}

// supersededBy reports whether event is the newer word on this job. Providers
// stamp events to the second and do not promise ordering, so two events can
// tie: a message accepted and delivered in the same second is ordinary. On a
// tie the more conclusive outcome wins, which is what keeps a same-second
// delivery from being dropped behind its own send, and a same-second stale
// delivery from clearing a bounce.
func (j eventJob) supersededBy(at time.Time, state DeliveryState) bool {
	switch {
	case at.After(j.lastEventAt):
		return true
	case at.Equal(j.lastEventAt):
		return state.meaning().precedence >= j.state.meaning().precedence
	default:
		return false
	}
}

func readJobForMessage(ctx context.Context, tx *sql.Tx, providerMessageID string) (eventJob, bool, error) {
	var job eventJob
	var lastEventAt sql.NullInt64
	err := tx.QueryRowContext(ctx,
		"SELECT id, state, last_event_at FROM mail_delivery_jobs WHERE provider_message_id = ?",
		providerMessageID).Scan(&job.id, &job.state, &lastEventAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return eventJob{}, false, nil
	case err != nil:
		return eventJob{}, false, fmt.Errorf("read delivery job for provider message: %w", err)
	}
	if lastEventAt.Valid {
		job.lastEventAt = time.Unix(lastEventAt.Int64, 0).UTC()
	}
	return job, true, nil
}

// errDuplicateDeliveryEvent is the provider redelivering a callback Nightloch
// has already applied.
var errDuplicateDeliveryEvent = errors.New("delivery event already recorded")

// recordEventReceipt claims this event id. The UNIQUE index is the idempotency
// barrier: two concurrent copies of one callback race here rather than both
// reaching the job. The row is kept even when it changed nothing, so an
// operator can tell an ignored event from one that never arrived.
func recordEventReceipt(ctx context.Context, tx *sql.Tx, event DeliveryEvent, jobID sql.NullInt64, applied bool, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO mail_delivery_events(
			provider_event_id, provider_message_id, job_id, event_type,
			occurred_at, received_at, applied)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		event.ProviderEventID, event.ProviderMessageID, nullableID(jobID), event.Type,
		event.OccurredAt.UTC().Unix(), now.Unix(), boolToInt(applied))
	if err != nil {
		var already int
		if scanErr := tx.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM mail_delivery_events WHERE provider_event_id = ?",
			event.ProviderEventID).Scan(&already); scanErr == nil && already > 0 {
			return errDuplicateDeliveryEvent
		}
		return fmt.Errorf("record delivery event: %w", err)
	}
	return nil
}
