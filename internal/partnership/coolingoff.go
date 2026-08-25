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

	// ActionCoolingOffBecameFinal is Nightloch's own record that a Roster
	// change left an already-running Cooling-off as the last thing holding the
	// Partner lock up. Like a completion its actor is NULL: nobody asked for
	// this transition, it is a consequence of somebody else's ending (S3-18).
	ActionCoolingOffBecameFinal = "partnership_cooling_off_became_final"

	// MessageCoolingOffStarted and MessageCoolingOffCanceled are the two
	// Cooling-off signals. Both go to the affected Partner and every other
	// current Partner, never to the Protectee who initiated them (S3-20).
	MessageCoolingOffStarted  = "partnership_cooling_off_started"
	MessageCoolingOffCanceled = "partnership_cooling_off_canceled"

	// MessageCoolingOffBecameFinal is the separate alert for that transition.
	// Unlike the other two Cooling-off signals it does go to the Protectee:
	// they did not cause it, and it is their own Partner lock that will
	// deactivate at a deadline they can no longer be assumed to remember.
	MessageCoolingOffBecameFinal = "partnership_cooling_off_became_final"

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
// PartnersAfterDeadline is the Protectee's Partner count once EndsAt arrives,
// so the final-Partnership consequence can be stated before the request as
// well as after it (S3-43).
type CoolingOff struct {
	PartnershipID  int64
	ProtecteeEmail string
	PartnerEmail   string
	Since          time.Time
	LogSharing     bool
	// PartnersAfterDeadline counts the Partnerships that outlive this
	// deadline, not merely the other Partnerships that exist today: countdowns
	// run concurrently, so one already scheduled to end at or before EndsAt is
	// gone by then too.
	PartnersAfterDeadline int
	RequestedAt           time.Time
	EndsAt                time.Time
	// CanceledAt is set only on the receipt CancelCoolingOff returns.
	CanceledAt time.Time
}

// FinalPartnership reports whether this Partnership's ending will deactivate
// the Protectee's Partner lock and restore self-loosening. It is a dynamic
// answer: the same request stops being final when someone else accepts, and
// becomes final when the Partnerships outlasting it end first.
func (c CoolingOff) FinalPartnership() bool { return c.PartnersAfterDeadline == 0 }

// Running reports whether an ending is actually scheduled. It is false for the
// preview of an unscheduled Partnership, whose EndsAt is the deadline a
// request would set rather than one Nightloch has committed to.
func (c CoolingOff) Running() bool { return !c.RequestedAt.IsZero() }

// CoolingOffPreview is the read behind the Protectee's confirmation screen: the
// exact deadline the request would set and the consequences it would carry,
// without setting anything. Only the Protectee on the Partnership reaches it.
func (s *Service) CoolingOffPreview(ctx context.Context, partnershipID, protecteeID int64) (CoolingOff, error) {
	now := s.now().UTC()
	partnership, err := readLivePartnership(ctx, s.db, byProtectee, partnershipID, protecteeID)
	if err != nil {
		return CoolingOff{}, err
	}
	// A Cooling-off already running keeps its own request time and deadline.
	// An unscheduled Partnership previews the deadline a request would set,
	// and keeps a zero RequestedAt, so no screen can mistake the proposal for
	// something Nightloch has already committed to.
	deadline := partnership.coolingOffEndsAt
	if deadline.IsZero() {
		deadline = now.Add(coolingOffDuration)
	}
	surviving, err := countPartnersAfterDeadline(ctx, s.db,
		partnership.protecteeID, partnership.id, deadline)
	if err != nil {
		return CoolingOff{}, err
	}
	preview := newCoolingOff(partnership, surviving)
	preview.EndsAt = deadline
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

		// The deadline is committed above, so the surviving count is read with
		// this Partnership's own ending already in place: the answer is the
		// horizon the Protectee is told about, not a serial delay per Partner.
		surviving, err := countPartnersAfterDeadline(ctx, tx,
			partnership.protecteeID, partnership.id, endsAt)
		if err != nil {
			return CoolingOff{}, err
		}
		request := newCoolingOff(partnership, surviving)
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
				"log_sharing": partnership.logSharing,
				// Both counts describe the same instant the ending does, so a
				// reader of this entry cannot be told that one Partner remains
				// and that the request is final at the same time.
				"partner_count":     surviving,
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
// are never on this list (S3-20).
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
	// The scope is this event's own Activity entry rather than the instant it
	// happened. A Protectee may request an ending, cancel it, and request it
	// again inside one second, and those are two separate signals that owe
	// the same Partner two envelopes; keyed on the instant they would collide
	// and the second request would fail rather than send.
	scope := activityScope(messages.activityID)
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

	// One Partnership that cannot be settled must not starve the rest: the
	// scan is ordered by deadline, so aborting the batch would re-select the
	// same failing row first on every later sweep and no other deadline would
	// ever complete. Each failure is reported and the sweep moves on.
	ended := 0
	var failures []error
	for _, id := range due {
		settled, err := s.completeCoolingOff(ctx, id)
		if err != nil {
			failures = append(failures, fmt.Errorf("Partnership %d: %w", id, err))
			continue
		}
		if settled {
			ended++
		}
	}
	return ended, errors.Join(failures...)
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

	partnership, err := readDuePartnership(ctx, tx, partnershipID)
	if errors.Is(err, ErrPartnershipUnavailable) {
		return false, nil
	}
	if err != nil {
		return false, err
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
	// A completion changes the Roster exactly as any other ending does, so a
	// Cooling-off it leaves behind can become final here too — including when
	// the Protectee scheduled every Partnership inside one horizon.
	if err := s.announceFinalCoolingOff(ctx, tx, partnership, endedAt); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit Cooling-off completion: %w", err)
	}
	return true, nil
}

// announceFinalCoolingOff records and announces the one Roster change a
// scheduled ending cannot announce for itself: another Partnership ending has
// left an already-running Cooling-off as the final active Partnership, so that
// unchanged deadline is now the instant the Protectee's Partner lock
// deactivates. The countdown is not restarted, extended, or otherwise touched;
// only the consequence changed.
//
// It is called from every path that ends a Partnership, after that ending's
// own messages, so the recipients read what happened before they read what it
// leaves behind.
func (s *Service) announceFinalCoolingOff(ctx context.Context, tx *sql.Tx,
	ended livePartnership, at time.Time) error {

	final, transitioned, err := readNewlyFinalCoolingOff(ctx, tx, ended, at)
	if err != nil || !transitioned {
		return err
	}

	activityID, err := appendActivity(ctx, tx, activityEntry{
		ProtecteeID:   final.protecteeID,
		Action:        ActionCoolingOffBecameFinal,
		TargetEmail:   final.partnerEmail,
		PartnershipID: &final.id,
		OccurredAt:    at,
		EffectiveAt:   &final.coolingOffEndsAt,
		Details: map[string]any{
			"ended_partner":       ended.partnerEmail,
			"requested_at":        final.coolingOffRequestedAt.Unix(),
			"log_sharing":         final.logSharing,
			"partner_count":       1,
			"partner_lock_active": true,
		},
	})
	if err != nil {
		return err
	}

	// The Protectee and every current Partner are entitled to it; after this
	// ending the current Roster is the final Partner alone. The Partner who
	// just left is not on it, and their own ending message already told them
	// everything they retain (S3-16, S3-20).
	message := finalCoolingOffMessage{
		ProtecteeEmail:    final.protecteeEmail,
		FinalPartnerEmail: final.partnerEmail,
		EndedPartnerEmail: ended.partnerEmail,
		RosterChangedAt:   at,
		EndsAt:            final.coolingOffEndsAt,
		DashboardURL:      s.dashboardURL(),
	}
	// Keyed on this transition's own Activity entry for the same reason the
	// request and cancellation signals are: a Roster change can make the same
	// Partnership final more than once, and two announcements in one second
	// must not collide on one key.
	scope := activityScope(activityID)
	for sequence, recipient := range []string{final.protecteeEmail, final.partnerEmail} {
		envelope := renderCoolingOffBecameFinal(message, recipient)
		envelope.IdempotencyKey = idempotencyKey(envelope.Kind, envelope.To, scope, sequence+1)
		if err := queueDelivery(ctx, tx, deliveryJob{
			ActivityID:    activityID,
			PartnershipID: &final.id,
			Message:       envelope,
			CreatedAt:     at,
		}); err != nil {
			return err
		}
	}
	return nil
}

// readNewlyFinalCoolingOff resolves the Partnership the ending just left as
// the Protectee's final active one, when that Partnership is already scheduled
// to end and its deadline is still ahead. Anything else — several survivors,
// one survivor with no Cooling-off, or a deadline that has already arrived —
// is not a transition anyone can be told about, and reports false.
//
// The last of those is the equal-deadline case: the sweep settling this ending
// is about to settle that one too, so calling it "scheduled to end" at a past
// instant would be false. It resolves the way every other race in this package
// does — the ending that commits wins.
func readNewlyFinalCoolingOff(ctx context.Context, queryer rowQueryer,
	ended livePartnership, at time.Time) (livePartnership, bool, error) {

	survivors, err := countPartnersAfterEnding(ctx, queryer, ended.protecteeID, ended.id)
	if err != nil {
		return livePartnership{}, false, err
	}
	if survivors != 1 {
		return livePartnership{}, false, nil
	}
	final, err := scanLivePartnership(queryer.QueryRowContext(ctx, livePartnershipProjection+
		`partnership.protectee_id = ? AND partnership.id <> ?
		 AND partnership.cooling_off_ends_at IS NOT NULL`, ended.protecteeID, ended.id))
	if errors.Is(err, ErrPartnershipUnavailable) {
		return livePartnership{}, false, nil
	}
	if err != nil {
		return livePartnership{}, false, err
	}
	return final, final.coolingOffEndsAt.After(at), nil
}

// lockEndingSchedule is the instant a Protectee's Partner lock is currently
// scheduled to deactivate, and the Partnerships that end at it. There is one
// only when every live Partnership is already in Cooling-off: a Partnership
// with no scheduled ending outlasts every deadline, so the lock has no
// scheduled end at all. Several Partnerships share the instant when their
// countdowns were started inside one horizon.
type lockEndingSchedule struct {
	endsAt   time.Time
	partners []string
}

// readLockEndingSchedule answers, for the Roster as it stands with excluding
// left out, when the Partner lock is scheduled to deactivate. It is the
// question behind the acceptance disclosure: a new Partner arriving before
// that instant is exactly what keeps the lock alive past it, and the people
// who were told the deadline would end the lock are entitled to hear so.
func readLockEndingSchedule(ctx context.Context, queryer rowQueryer,
	protecteeID, excluding int64) (lockEndingSchedule, bool, error) {

	var live, unscheduled, latest int64
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(*) - COUNT(cooling_off_ends_at),
		       COALESCE(MAX(cooling_off_ends_at), 0)
		FROM partnerships
		WHERE protectee_id = ? AND id <> ? AND ended_at IS NULL`,
		protecteeID, excluding).Scan(&live, &unscheduled, &latest); err != nil {
		return lockEndingSchedule{}, false, fmt.Errorf("read scheduled Partner-lock ending: %w", err)
	}
	if live == 0 || unscheduled > 0 {
		return lockEndingSchedule{}, false, nil
	}
	rows, err := queryer.QueryContext(ctx, `
		SELECT users.email FROM partnerships
		JOIN users ON users.id = partnerships.partner_id
		WHERE partnerships.protectee_id = ? AND partnerships.id <> ?
		  AND partnerships.ended_at IS NULL AND partnerships.cooling_off_ends_at = ?
		ORDER BY partnerships.accepted_at, partnerships.id`, protecteeID, excluding, latest)
	if err != nil {
		return lockEndingSchedule{}, false, fmt.Errorf("read scheduled Partner-lock ending: %w", err)
	}
	defer rows.Close()
	schedule := lockEndingSchedule{endsAt: time.Unix(latest, 0).UTC()}
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return lockEndingSchedule{}, false, fmt.Errorf("read scheduled Partner-lock ending: %w", err)
		}
		schedule.partners = append(schedule.partners, email)
	}
	if err := rows.Err(); err != nil {
		return lockEndingSchedule{}, false, fmt.Errorf("read scheduled Partner-lock ending: %w", err)
	}
	return schedule, true, nil
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
// deadline the row already carries. surviving is the Partner count that
// outlives that deadline.
func newCoolingOff(partnership livePartnership, surviving int) CoolingOff {
	return CoolingOff{
		PartnershipID: partnership.id, ProtecteeEmail: partnership.protecteeEmail,
		PartnerEmail: partnership.partnerEmail, Since: partnership.since,
		LogSharing: partnership.logSharing, PartnersAfterDeadline: surviving,
		RequestedAt: partnership.coolingOffRequestedAt, EndsAt: partnership.coolingOffEndsAt,
	}
}
