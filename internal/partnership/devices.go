package partnership

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	// MessageDeviceEnrolled is the Action alert sent to active Partners when a
	// Protectee enrolls a Device.
	MessageDeviceEnrolled = "device_enrolled"

	// MessageDeviceRemoved is the Action alert sent when a Device is removed:
	// - If removed by Protectee: to all active Partners.
	// - If removed by Partner: to the Protectee and all other active Partners.
	MessageDeviceRemoved = "device_removed"
)

// RecordDeviceEnrolled appends an Activity entry under CategoryDevices and queues
// Action alert emails to all current active Partners of protecteeID.
// If the Protectee is unpartnered, no mail is queued and no error is raised.
func (s *Service) RecordDeviceEnrolled(ctx context.Context, protecteeID int64, deviceName string) error {
	if protecteeID <= 0 {
		return ErrUserUnavailable
	}
	now := s.now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin RecordDeviceEnrolled: %w", err)
	}
	defer tx.Rollback()

	protecteeEmail, err := userEmailTx(ctx, tx, protecteeID)
	if err != nil {
		return err
	}

	activityID, err := appendActivity(ctx, tx, activityEntry{
		ProtecteeID: protecteeID,
		Action:      ActionDeviceEnrolled,
		ActorUserID: &protecteeID,
		ActorEmail:  protecteeEmail,
		TargetEmail: protecteeEmail,
		OccurredAt:  now,
		Details: map[string]any{
			"device_name": deviceName,
		},
	})
	if err != nil {
		return err
	}

	partners, err := livePartnerEmails(ctx, tx, protecteeID)
	if err != nil {
		return err
	}

	if len(partners) > 0 {
		message := deviceEnrolledMessage{
			ProtecteeEmail: protecteeEmail,
			DeviceName:     deviceName,
			EnrolledAt:     now,
			DashboardURL:   s.dashboardURL(),
		}
		if err := queueAlertEnvelopes(ctx, tx, activityID, now, partners, func(recipient string) preparedMessage {
			return renderDeviceEnrolled(message, recipient)
		}); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit RecordDeviceEnrolled: %w", err)
	}
	return nil
}

// ErrUnauthorizedDeviceRemoval is returned when an actor who is neither the
// Protectee nor an active Partner attempts to record a Device removal.
var ErrUnauthorizedDeviceRemoval = errors.New("unauthorized to remove device")

// RecordDeviceRemoved appends an Activity entry under CategoryDevices and queues
// Action alert emails:
// - If removed by Protectee: to all current active Partners.
// - If removed by Partner: to the Protectee and all other active Partners (naming the actor).
// If 0 recipients exist, no mail is queued and no error is raised.
func (s *Service) RecordDeviceRemoved(ctx context.Context, actorID, protecteeID int64, deviceName string) error {
	if actorID <= 0 || protecteeID <= 0 {
		return ErrUserUnavailable
	}

	now := s.now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin RecordDeviceRemoved: %w", err)
	}
	defer tx.Rollback()

	protecteeEmail, err := userEmailTx(ctx, tx, protecteeID)
	if err != nil {
		return err
	}

	var actorEmail string
	if actorID == protecteeID {
		actorEmail = protecteeEmail
	} else {
		actorEmail, err = userEmailTx(ctx, tx, actorID)
		if err != nil {
			return err
		}

		var activePartnership int
		err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM partnerships WHERE protectee_id = ? AND partner_id = ? AND ended_at IS NULL", protecteeID, actorID).Scan(&activePartnership)
		if err != nil {
			return fmt.Errorf("check removal authorization: %w", err)
		}
		if activePartnership == 0 {
			return ErrUnauthorizedDeviceRemoval
		}
	}

	activityID, err := appendActivity(ctx, tx, activityEntry{
		ProtecteeID: protecteeID,
		Action:      ActionDeviceRemoved,
		ActorUserID: &actorID,
		ActorEmail:  actorEmail,
		TargetEmail: protecteeEmail,
		OccurredAt:  now,
		Details: map[string]any{
			"device_name": deviceName,
		},
	})
	if err != nil {
		return err
	}

	partners, err := livePartnerEmails(ctx, tx, protecteeID)
	if err != nil {
		return err
	}

	var recipients []string
	if actorID == protecteeID {
		recipients = partners
	} else {
		for _, p := range partners {
			if p != actorEmail {
				recipients = append(recipients, p)
			}
		}
		recipients = append(recipients, protecteeEmail)
	}

	if len(recipients) > 0 {
		message := deviceRemovedMessage{
			ActorEmail:     actorEmail,
			ProtecteeEmail: protecteeEmail,
			DeviceName:     deviceName,
			RemovedAt:      now,
			DashboardURL:   s.dashboardURL(),
		}
		if err := queueAlertEnvelopes(ctx, tx, activityID, now, recipients, func(recipient string) preparedMessage {
			return renderDeviceRemoved(message, recipient)
		}); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit RecordDeviceRemoved: %w", err)
	}
	return nil
}

func userEmailTx(ctx context.Context, tx *sql.Tx, userID int64) (string, error) {
	var email string
	err := tx.QueryRowContext(ctx, "SELECT email FROM users WHERE id = ? AND deleted_at IS NULL", userID).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUserUnavailable
	}
	if err != nil {
		return "", fmt.Errorf("read user email: %w", err)
	}
	return email, nil
}

func queueAlertEnvelopes(
	ctx context.Context,
	tx *sql.Tx,
	activityID int64,
	now time.Time,
	recipients []string,
	render func(recipient string) preparedMessage,
) error {
	scope := activityScope(activityID)
	for sequence, recipient := range recipients {
		envelope := render(recipient)
		envelope.IdempotencyKey = idempotencyKey(envelope.Kind, envelope.To, scope, sequence+1)
		if err := queueDelivery(ctx, tx, deliveryJob{
			ActivityID: activityID,
			Message:    envelope,
			CreatedAt:  now,
		}); err != nil {
			return err
		}
	}
	return nil
}
