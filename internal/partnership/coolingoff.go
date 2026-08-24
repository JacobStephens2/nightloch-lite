package partnership

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	// coolingOffDuration is the approved 72-hour delay on the one exit a
	// Protectee holds. It is the product's restraint: the deadline never
	// moves once requested, and only the Protectee's own cancellation or the
	// affected Partner's agreement resolves it sooner (F6, Q34′).
	coolingOffDuration = 72 * time.Hour

	// ActionCoolingOffStarted and ActionCoolingOffCanceled are the stable
	// Activity actions for the Protectee's request and their withdrawal of it.
	ActionCoolingOffStarted  = "partnership_cooling_off_started"
	ActionCoolingOffCanceled = "partnership_cooling_off_canceled"

	// ActionPartnershipEndedAfterCoolingOff is Nightloch's own completion of a
	// Cooling-off that ran its course. Its actor is NULL and its details link
	// back to the Protectee's original request, because the execution is not
	// the Protectee's act (S3-18).
	ActionPartnershipEndedAfterCoolingOff = "partnership_ended_after_cooling_off"

	// MessageCoolingOffStarted and MessageCoolingOffCanceled are the two
	// Cooling-off signals. Both go to the affected Partner and every other
	// current Partner, never to the Protectee who initiated them (S3-20).
	MessageCoolingOffStarted  = "partnership_cooling_off_started"
	MessageCoolingOffCanceled = "partnership_cooling_off_canceled"

	// EndReasonPartnerAgreedNow is the stored cause when the affected Partner
	// waives a running Cooling-off, and EndReasonCoolingOffCompleted is the
	// cause when the deadline itself arrives. Both are separate from
	// EndReasonPartnerEnded so a receipt always names how a Partnership ended.
	EndReasonPartnerAgreedNow    = "partner_agreed_now"
	EndReasonCoolingOffCompleted = "cooling_off_completed"
)

var (
	// ErrCoolingOffRunning refuses a second request for a Partnership that is
	// already scheduled to end. The first deadline stands.
	ErrCoolingOffRunning = errors.New("that Partnership is already scheduled to end")
	// ErrCoolingOffNotRunning refuses a cancellation with nothing to cancel.
	ErrCoolingOffNotRunning = errors.New("that Partnership has no scheduled ending to cancel")
)

// CoolingOffCommand is one authenticated Protectee acting on their own
// Partnership. Only that Protectee ever holds these two controls: a Partner
// ends immediately instead, and nobody else reaches the Partnership at all.
type CoolingOffCommand struct {
	PartnershipID int64
	ProtecteeID   int64
}

// CoolingOff is one Partnership's scheduled ending, or — from CoolingOffPreview
// — the same facts as they would stand if the Protectee confirmed now.
// RemainingPartners is the Protectee's active-Partner count once this
// Partnership is gone, so the final-Partnership consequence can be stated
// before the request as well as after it (S3-43).
type CoolingOff struct {
	PartnershipID     int64
	ProtecteeEmail    string
	PartnerEmail      string
	Since             time.Time
	LogSharing        bool
	RemainingPartners int
	RequestedAt       time.Time
	EndsAt            time.Time
	// CanceledAt is set only on the receipt CancelCoolingOff returns.
	CanceledAt time.Time
}

// FinalPartnership reports whether this Partnership's ending will deactivate
// the Protectee's Partner lock and restore self-loosening.
func (c CoolingOff) FinalPartnership() bool { return c.RemainingPartners == 0 }

// Remaining is how long is left before the deadline, floored at zero so a
// countdown never runs backwards past the moment the runner is due.
func (c CoolingOff) Remaining(now time.Time) time.Duration {
	return max(c.EndsAt.Sub(now.UTC()), 0)
}

// CoolingOffPreview is the read behind the Protectee's confirmation screen: the
// exact deadline the request would set and the consequences it would carry,
// without setting anything. Only the Protectee on the Partnership reaches it.
func (s *Service) CoolingOffPreview(ctx context.Context, partnershipID, protecteeID int64) (CoolingOff, error) {
	now := s.now().UTC()
	partnership, err := readLivePartnership(ctx, s.db, byProtectee, partnershipID, protecteeID)
	if err != nil {
		return CoolingOff{}, err
	}
	remaining, err := countPartnersAfterEnding(ctx, s.db, partnership.protecteeID, partnership.id)
	if err != nil {
		return CoolingOff{}, err
	}
	preview := newCoolingOff(partnership, remaining)
	// A Cooling-off already running keeps its own deadline; only an
	// unscheduled Partnership previews a fresh one from this instant.
	if preview.EndsAt.IsZero() {
		preview.RequestedAt, preview.EndsAt = now, now.Add(coolingOffDuration)
	}
	return preview, nil
}

// StartCoolingOff schedules the Protectee's own Partnership to end in 72 hours.
// Nothing else changes: the Partnership stays active, the Partner keeps every
// power, and the Protectee's Partner lock stays exactly as it was until the
// deadline actually arrives. The unscheduled state is the concurrency token,
// so a replayed or racing request answers ErrCoolingOffRunning and queues no
// second alert.
func (s *Service) StartCoolingOff(ctx context.Context, command CoolingOffCommand) (CoolingOff, error) {
	now := s.now().UTC()
	endsAt := now.Add(coolingOffDuration)

	return s.commitCoolingOff(ctx, command, func(tx *sql.Tx, partnership livePartnership, remaining int) (CoolingOff, error) {
		result, err := tx.ExecContext(ctx, `
			UPDATE partnerships SET cooling_off_requested_at = ?, cooling_off_ends_at = ?
			WHERE id = ? AND ended_at IS NULL AND cooling_off_requested_at IS NULL`,
			now.Unix(), endsAt.Unix(), partnership.id)
		if err != nil {
			return CoolingOff{}, fmt.Errorf("start Cooling-off: %w", err)
		}
		if err := requireOneRow(result, ErrCoolingOffRunning); err != nil {
			return CoolingOff{}, err
		}

		request := newCoolingOff(partnership, remaining)
		request.RequestedAt, request.EndsAt = now, endsAt
		activityID, err := appendActivity(ctx, tx, activityEntry{
			ProtecteeID:   partnership.protecteeID,
			Action:        ActionCoolingOffStarted,
			ActorUserID:   &command.ProtecteeID,
			ActorEmail:    partnership.protecteeEmail,
			TargetEmail:   partnership.partnerEmail,
			PartnershipID: &partnership.id,
			OccurredAt:    now,
			EffectiveAt:   &endsAt,
			Details: map[string]any{
				"log_sharing":       partnership.logSharing,
				"partner_count":     remaining,
				"final_partnership": request.FinalPartnership(),
				"cooling_off_hours": int(coolingOffDuration / time.Hour),
			},
		})
		if err != nil {
			return CoolingOff{}, err
		}
		if err := s.queueCoolingOffMessages(ctx, tx, coolingOffMessages{
			partnership: partnership, activityID: activityID, at: now,
			render: func(recipient string) preparedMessage {
				return renderCoolingOffStarted(coolingOffMessage{
					ProtecteeEmail: partnership.protecteeEmail,
					PartnerEmail:   partnership.partnerEmail,
					At:             now,
					EndsAt:         endsAt,
					PartnerCount:   remaining,
					DashboardURL:   s.dashboardURL(),
					PartnershipURL: s.partnershipURL(partnership.id),
				}, recipient)
			},
		}); err != nil {
			return CoolingOff{}, err
		}
		return request, nil
	})
}

// CancelCoolingOff withdraws the Protectee's own request. The Partnership was
// never anything but active, so cancelling restores no powers and revokes
// none; it only removes the scheduled ending. The scheduled state is the
// concurrency token, so a cancellation racing the deadline or an End now
// answers ErrCoolingOffNotRunning rather than undoing a committed ending.
func (s *Service) CancelCoolingOff(ctx context.Context, command CoolingOffCommand) (CoolingOff, error) {
	now := s.now().UTC()

	return s.commitCoolingOff(ctx, command, func(tx *sql.Tx, partnership livePartnership, remaining int) (CoolingOff, error) {
		result, err := tx.ExecContext(ctx, `
			UPDATE partnerships SET cooling_off_requested_at = NULL, cooling_off_ends_at = NULL
			WHERE id = ? AND ended_at IS NULL AND cooling_off_requested_at IS NOT NULL`,
			partnership.id)
		if err != nil {
			return CoolingOff{}, fmt.Errorf("cancel Cooling-off: %w", err)
		}
		if err := requireOneRow(result, ErrCoolingOffNotRunning); err != nil {
			return CoolingOff{}, err
		}

		cancellation := newCoolingOff(partnership, remaining)
		cancellation.RequestedAt, cancellation.EndsAt = time.Time{}, time.Time{}
		cancellation.CanceledAt = now
		activityID, err := appendActivity(ctx, tx, activityEntry{
			ProtecteeID:   partnership.protecteeID,
			Action:        ActionCoolingOffCanceled,
			ActorUserID:   &command.ProtecteeID,
			ActorEmail:    partnership.protecteeEmail,
			TargetEmail:   partnership.partnerEmail,
			PartnershipID: &partnership.id,
			OccurredAt:    now,
			Details: map[string]any{
				"log_sharing":       partnership.logSharing,
				"partner_count":     remaining,
				"canceled_deadline": partnership.coolingOffEndsAt.Unix(),
			},
		})
		if err != nil {
			return CoolingOff{}, err
		}
		if err := s.queueCoolingOffMessages(ctx, tx, coolingOffMessages{
			partnership: partnership, activityID: activityID, at: now,
			render: func(recipient string) preparedMessage {
				return renderCoolingOffCanceled(coolingOffMessage{
					ProtecteeEmail: partnership.protecteeEmail,
					PartnerEmail:   partnership.partnerEmail,
					At:             now,
					DashboardURL:   s.dashboardURL(),
					PartnershipURL: s.partnershipURL(partnership.id),
				}, recipient)
			},
		}); err != nil {
			return CoolingOff{}, err
		}
		return cancellation, nil
	})
}

// commitCoolingOff is the shared transaction both Protectee controls run in:
// resolve and authorize the Partnership, count the Partners its ending would
// leave, then let the caller commit its own mutation, Activity, and messages
// together (ADR 0002).
func (s *Service) commitCoolingOff(ctx context.Context, command CoolingOffCommand,
	commit func(*sql.Tx, livePartnership, int) (CoolingOff, error)) (CoolingOff, error) {

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CoolingOff{}, fmt.Errorf("begin Cooling-off change: %w", err)
	}
	defer tx.Rollback()

	partnership, err := readLivePartnership(ctx, tx, byProtectee, command.PartnershipID, command.ProtecteeID)
	if err != nil {
		return CoolingOff{}, err
	}
	remaining, err := countPartnersAfterEnding(ctx, tx, partnership.protecteeID, partnership.id)
	if err != nil {
		return CoolingOff{}, err
	}
	result, err := commit(tx, partnership, remaining)
	if err != nil {
		return CoolingOff{}, err
	}
	if err := tx.Commit(); err != nil {
		return CoolingOff{}, fmt.Errorf("commit Cooling-off change: %w", err)
	}
	return result, nil
}

// coolingOffMessages carries what every Cooling-off signal needs: the affected
// Partner, the rest of the current Roster, and one render per recipient.
type coolingOffMessages struct {
	partnership livePartnership
	activityID  int64
	at          time.Time
	render      func(recipient string) preparedMessage
}

// queueCoolingOffMessages queues one envelope per entitled recipient. The
// Protectee initiated the change and reads an in-app receipt instead, so they
// are never on this list (S3-20). Today the 3A exposure gate means the wider
// Roster is always empty; the query is written for the Roster 3B opens up.
func (s *Service) queueCoolingOffMessages(ctx context.Context, tx *sql.Tx, messages coolingOffMessages) error {
	roster, err := livePartnerEmails(ctx, tx, messages.partnership.protecteeID)
	if err != nil {
		return err
	}
	recipients := []string{messages.partnership.partnerEmail}
	for _, email := range roster {
		if email != messages.partnership.partnerEmail {
			recipients = append(recipients, email)
		}
	}
	scope := partnershipScope(messages.partnership.id, messages.at)
	for sequence, recipient := range recipients {
		message := messages.render(recipient)
		message.IdempotencyKey = idempotencyKey(message.Kind, message.To, scope, sequence+1)
		if err := queueDelivery(ctx, tx, deliveryJob{
			ActivityID:    messages.activityID,
			PartnershipID: &messages.partnership.id,
			Message:       message,
			CreatedAt:     messages.at,
		}); err != nil {
			return err
		}
	}
	return nil
}

// CompleteDueCoolingOff ends the Partnerships whose deadline has arrived and
// reports how many it settled. The work is driven from SQLite, so a process
// that dies mid-sweep leaves the rest for the next one and a restart resumes a
// deadline that passed while nothing was running. Each Partnership ends at its
// own stored deadline rather than at the instant the runner happened to see
// it, so a late sweep cannot move the ending Nightloch already promised.
func (s *Service) CompleteDueCoolingOff(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	now := s.now().UTC()
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM partnerships
		WHERE ended_at IS NULL AND cooling_off_ends_at IS NOT NULL AND cooling_off_ends_at <= ?
		ORDER BY cooling_off_ends_at, id LIMIT ?`, now.Unix(), limit)
	if err != nil {
		return 0, fmt.Errorf("read due Cooling-off Partnerships: %w", err)
	}
	var due []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("read due Cooling-off Partnership: %w", err)
		}
		due = append(due, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return 0, fmt.Errorf("read due Cooling-off Partnerships: %w", err)
	}

	ended := 0
	for _, id := range due {
		settled, err := s.completeCoolingOff(ctx, id)
		if err != nil {
			return ended, err
		}
		if settled {
			ended++
		}
	}
	return ended, nil
}

// completeCoolingOff ends one Partnership at its own deadline. A cancellation,
// an End now, or another runner may have won between the scan and this
// transaction; first commit wins, so the runner simply stands down.
func (s *Service) completeCoolingOff(ctx context.Context, partnershipID int64) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin Cooling-off completion: %w", err)
	}
	defer tx.Rollback()

	partnership, err := readLivePartnership(ctx, tx, byAnyone, partnershipID, 0)
	if errors.Is(err, ErrPartnershipUnavailable) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if partnership.coolingOffEndsAt.IsZero() {
		return false, nil
	}
	endedAt := partnership.coolingOffEndsAt

	result, err := tx.ExecContext(ctx, `
		UPDATE partnerships SET ended_at = ?, ended_reason = ?
		WHERE id = ? AND ended_at IS NULL AND cooling_off_ends_at = ?`,
		endedAt.Unix(), EndReasonCoolingOffCompleted, partnership.id, endedAt.Unix())
	if err != nil {
		return false, fmt.Errorf("complete Cooling-off: %w", err)
	}
	if err := requireOneRow(result, ErrPartnershipUnavailable); err != nil {
		return false, nil
	}

	remaining, err := countPartnersAfterEnding(ctx, tx, partnership.protecteeID, partnership.id)
	if err != nil {
		return false, err
	}
	// The completion names Nightloch as the actor and points back at the
	// Protectee's original request, so Activity reads as a system completion
	// of something a person asked for rather than as the person's own act
	// (S3-18, S3-30).
	requestID, err := coolingOffRequestActivityID(ctx, tx, partnership.id)
	if err != nil {
		return false, err
	}
	activityID, err := appendActivity(ctx, tx, activityEntry{
		ProtecteeID:   partnership.protecteeID,
		Action:        ActionPartnershipEndedAfterCoolingOff,
		TargetEmail:   partnership.partnerEmail,
		PartnershipID: &partnership.id,
		OccurredAt:    endedAt,
		Details: map[string]any{
			"ended_reason":          EndReasonCoolingOffCompleted,
			"requested_by":          partnership.protecteeEmail,
			"requested_at":          partnership.coolingOffRequestedAt.Unix(),
			"requested_activity_id": requestID,
			"log_sharing":           partnership.logSharing,
			"partner_count":         remaining,
			"partner_lock_active":   remaining > 0,
		},
	})
	if err != nil {
		return false, err
	}
	if err := s.queueEndingMessages(ctx, tx, partnership, remaining, endedAt,
		EndReasonCoolingOffCompleted, activityID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit Cooling-off completion: %w", err)
	}
	return true, nil
}

// coolingOffRequestActivityID finds the Protectee's most recent Cooling-off
// request for this Partnership, which is the entry a completion links back to.
func coolingOffRequestActivityID(ctx context.Context, queryer rowQueryer, partnershipID int64) (int64, error) {
	var id int64
	err := queryer.QueryRowContext(ctx, `
		SELECT id FROM activity_entries
		WHERE partnership_id = ? AND action = ?
		ORDER BY id DESC LIMIT 1`, partnershipID, ActionCoolingOffStarted).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read Cooling-off request entry: %w", err)
	}
	return id, nil
}

// newCoolingOff describes one Partnership's scheduled ending, using whatever
// deadline the row already carries.
func newCoolingOff(partnership livePartnership, remainingAfter int) CoolingOff {
	return CoolingOff{
		PartnershipID: partnership.id, ProtecteeEmail: partnership.protecteeEmail,
		PartnerEmail: partnership.partnerEmail, Since: partnership.since,
		LogSharing: partnership.logSharing, RemainingPartners: remainingAfter,
		RequestedAt: partnership.coolingOffRequestedAt, EndsAt: partnership.coolingOffEndsAt,
	}
}

// requireOneRow turns a guarded UPDATE that changed nothing into the caller's
// own refusal, which is how every first-commit-wins race is settled here.
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
	link.Path = fmt.Sprintf("/partners/partnerships/%d/end", partnershipID)
	return link.String()
}
