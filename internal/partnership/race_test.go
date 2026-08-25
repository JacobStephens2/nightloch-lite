package partnership

import (
	"context"
	"errors"
	"testing"
	"time"
)

// raceAll runs every attempt from one release point and returns their errors
// in order, so a scenario can state which outcomes are acceptable rather than
// which goroutine happened to win.
func raceAll(attempts ...func() error) []error {
	results := make([]error, len(attempts))
	start := make(chan struct{})
	done := make(chan struct{}, len(attempts))
	for index, attempt := range attempts {
		go func() {
			defer func() { done <- struct{}{} }()
			<-start
			results[index] = attempt()
		}()
	}
	close(start)
	for range attempts {
		<-done
	}
	return results
}

func TestExpirySweepRacingAnAcceptanceSettlesTheInvitationOnce(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	invitee := createUser(t, db, "invitee@example.com")
	invitation := invite(t, service, protectee, "invitee@example.com", false)
	// At the deadline itself the invitee is answering the same row Nightloch's
	// sweep is about to expire, which is the only moment the two can collide.
	at(service, testClock.Add(invitationTTL))

	results := raceAll(
		func() error {
			_, err := service.AcceptInvitation(context.Background(), ByToken(invitation.RawToken), invitee)
			return err
		},
		func() error {
			_, err := service.ExpireDueInvitations(context.Background(), 10)
			return err
		},
		func() error {
			_, err := service.ExpireDueInvitations(context.Background(), 10)
			return err
		},
	)
	for index, err := range results {
		switch {
		case err == nil, errors.Is(err, ErrInvitationUnavailable):
		default:
			t.Fatalf("attempt %d = %v", index, err)
		}
	}

	// Exactly one terminal state, and the Partnership exists only if the
	// acceptance is the transition that won.
	state := invitationState(t, service, invitation.ID)
	if state == string(InvitationPending) {
		t.Fatal("the invitation survived its own deadline as pending")
	}
	partnerships := countRows(t, db, "SELECT COUNT(*) FROM partnerships")
	if (state == string(InvitationAccepted)) != (partnerships == 1) {
		t.Fatalf("state %q with %d Partnerships", state, partnerships)
	}
	// Accepted and expired are both terminal, and the losing sweep must not
	// have appended a second terminal entry for the same invitation.
	terminal := countRows(t, db, `SELECT COUNT(*) FROM activity_entries
		WHERE invitation_id = ? AND action IN (?, ?)`,
		invitation.ID, ActionInvitationAccepted, ActionInvitationExpired)
	if terminal != 1 {
		t.Fatalf("the invitation recorded %d terminal Activity entries", terminal)
	}
}

func TestConcurrentCoolingOffRequestsScheduleExactlyOneDeadline(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, _, partnership := firstPartnership(t, service, db, false)

	request := func() error {
		_, err := service.StartCoolingOff(context.Background(), CoolingOffCommand{
			PartnershipID: partnership, ProtecteeID: protectee,
		})
		return err
	}
	results := raceAll(request, request, request, request)

	committed := 0
	for index, err := range results {
		switch {
		case err == nil:
			committed++
		case errors.Is(err, ErrCoolingOffRunning), errors.Is(err, ErrPartnershipUnavailable):
		default:
			t.Fatalf("attempt %d = %v", index, err)
		}
	}
	if committed != 1 {
		t.Fatalf("%d Cooling-off requests committed, want 1", committed)
	}

	// One deadline, one Activity entry, and one alert for the Partner the
	// request affects — the losers left nothing behind.
	requests := countRows(t, db,
		"SELECT COUNT(*) FROM activity_entries WHERE partnership_id = ? AND action = ?",
		partnership, ActionCoolingOffStarted)
	alerts := countRows(t, db,
		"SELECT COUNT(*) FROM mail_delivery_jobs WHERE message_kind = ?", MessageCoolingOffStarted)
	if requests != 1 || alerts != 1 {
		t.Fatalf("requests=%d alerts=%d, want 1 and 1", requests, alerts)
	}
	var deadline int64
	if err := db.QueryRow("SELECT cooling_off_ends_at FROM partnerships WHERE id = ?", partnership).
		Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	if want := testClock.Add(coolingOffDuration).Unix(); deadline != want {
		t.Fatalf("deadline = %d, want %d", deadline, want)
	}
}

func TestAPartnersEndingRacingTheProtecteesRequestLeavesOneSettledPartnership(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, partnership := firstPartnership(t, service, db, false)

	results := raceAll(
		func() error {
			_, err := service.StartCoolingOff(context.Background(), CoolingOffCommand{
				PartnershipID: partnership, ProtecteeID: protectee,
			})
			return err
		},
		func() error {
			_, err := service.EndPartnership(context.Background(), EndCommand{
				PartnershipID: partnership, PartnerID: partner, Confirmation: "END",
			})
			return err
		},
	)
	for index, err := range results {
		switch {
		case err == nil, errors.Is(err, ErrPartnershipUnavailable), errors.Is(err, ErrCoolingOffRunning):
		default:
			t.Fatalf("attempt %d = %v", index, err)
		}
	}

	// The Partner's ending is independent of the request, so it lands either
	// way; what may never happen is two endings or an ending Nightloch could
	// still complete again on a deadline.
	endings := countRows(t, db,
		"SELECT COUNT(*) FROM activity_entries WHERE partnership_id = ? AND action IN (?, ?)",
		partnership, ActionPartnershipEndedByPartner, ActionPartnershipEndedAfterCoolingOff)
	if endings != 1 {
		t.Fatalf("the Partnership recorded %d endings", endings)
	}
	endingJobs := countRows(t, db,
		"SELECT COUNT(*) FROM mail_delivery_jobs WHERE message_kind = ?", MessagePartnershipEnded)
	if endingJobs != 2 {
		t.Fatalf("the ending queued %d envelopes, want one for each side", endingJobs)
	}
	// A settled Partnership is never due again, however the race resolved.
	at(service, testClock.Add(coolingOffDuration+time.Hour))
	if ended, err := service.CompleteDueCoolingOff(context.Background(), 10); err != nil || ended != 0 {
		t.Fatalf("the deadline runner re-ended a settled Partnership: %d, %v", ended, err)
	}
	live := countRows(t, db, "SELECT COUNT(*) FROM partnerships WHERE ended_at IS NULL")
	if live != 0 {
		t.Fatalf("%d Partnerships survived the race", live)
	}
}

func TestDeletionRacingAnEndingEndsEachPartnershipExactlyOnce(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, partnership := firstPartnership(t, service, db, false)

	results := raceAll(
		func() error {
			_, err := service.DeleteUser(context.Background(), protectee)
			return err
		},
		func() error {
			_, err := service.EndPartnership(context.Background(), EndCommand{
				PartnershipID: partnership, PartnerID: partner, Confirmation: "END",
			})
			return err
		},
	)
	for index, err := range results {
		switch {
		case err == nil, errors.Is(err, ErrPartnershipUnavailable), errors.Is(err, ErrUserUnavailable):
		default:
			t.Fatalf("attempt %d = %v", index, err)
		}
	}

	// Whichever landed first, the Partnership ended once, and the former
	// Partner keeps exactly one receipt naming exactly one cause.
	var ended, causes int
	if err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM partnerships WHERE id = ?1 AND ended_at IS NOT NULL),
		(SELECT COUNT(DISTINCT ended_reason) FROM partnerships WHERE id = ?1)`,
		partnership).Scan(&ended, &causes); err != nil {
		t.Fatal(err)
	}
	if ended != 1 || causes != 1 {
		t.Fatalf("ended=%d distinct causes=%d", ended, causes)
	}
	endingJobs := countRows(t, db,
		"SELECT COUNT(*) FROM mail_delivery_jobs WHERE recipient_email = ? AND message_kind IN (?, ?)",
		"partner@example.com", MessagePartnershipEnded, MessageUserDeleted)
	if endingJobs != 1 {
		t.Fatalf("the former Partner was told %d times that the Partnership ended", endingJobs)
	}
}

func TestARequestCancelledAndMadeAgainInTheSameSecondStillSends(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	protectee, _, partnership := firstPartnership(t, service, db, false)
	command := CoolingOffCommand{PartnershipID: partnership, ProtecteeID: protectee}

	startEnding(t, service, partnership, protectee)
	if _, err := service.CancelCoolingOff(ctx, command); err != nil {
		t.Fatalf("CancelCoolingOff: %v", err)
	}
	// The clock has not moved. The second request is a separate signal that
	// owes the Partner their own envelope, so it must not collide with the
	// first request's.
	if _, err := service.StartCoolingOff(ctx, command); err != nil {
		t.Fatalf("a re-request inside the same second: %v", err)
	}

	requests := countRows(t, db,
		"SELECT COUNT(*) FROM activity_entries WHERE partnership_id = ? AND action = ?",
		partnership, ActionCoolingOffStarted)
	alerts := countRows(t, db,
		"SELECT COUNT(*) FROM mail_delivery_jobs WHERE message_kind = ?", MessageCoolingOffStarted)
	if requests != 2 || alerts != 2 {
		t.Fatalf("requests=%d alerts=%d, want the Partner told about each one", requests, alerts)
	}
}
