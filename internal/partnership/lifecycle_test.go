package partnership

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	mailmessage "github.com/JacobStephens2/nightloch-lite/internal/mail"
)

// invite sends one invitation and returns it, failing the test on refusal.
func invite(t *testing.T, service *Service, protecteeID int64, inviteeEmail string, logSharing bool) Invitation {
	t.Helper()
	invitation, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protecteeID, InviteeEmail: inviteeEmail, LogSharing: logSharing,
	})
	if err != nil {
		t.Fatalf("InvitePartner: %v", err)
	}
	return invitation
}

// queuedRecipients lists the recipients of every delivery job queued after the
// invitation's own, newest last.
func queuedRecipients(t *testing.T, service *Service) []string {
	t.Helper()
	rows, err := service.db.Query(
		"SELECT recipient_email, message_kind FROM mail_delivery_jobs ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var recipients []string
	for rows.Next() {
		var recipient, kind string
		if err := rows.Scan(&recipient, &kind); err != nil {
			t.Fatal(err)
		}
		recipients = append(recipients, kind+" -> "+recipient)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return recipients
}

func invitationState(t *testing.T, service *Service, id int64) string {
	t.Helper()
	var state string
	if err := service.db.QueryRow("SELECT state FROM partner_invitations WHERE id = ?", id).
		Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestReceivedInvitationsFindsInvitationsWithoutTheBearerLink(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	first := createUser(t, db, "first@example.com")
	second := createUser(t, db, "second@example.com")
	invitee := createUser(t, db, "invitee@example.com")
	stranger := createUser(t, db, "stranger@example.com")
	invite(t, service, first, "invitee@example.com", true)
	invite(t, service, second, "Invitee@Example.com", false)

	received, err := service.ReceivedInvitations(context.Background(), invitee)
	if err != nil {
		t.Fatalf("ReceivedInvitations: %v", err)
	}
	if len(received) != 2 {
		t.Fatalf("received %d invitations, want 2: %+v", len(received), received)
	}
	if received[0].ProtecteeEmail != "first@example.com" || !received[0].LogSharing {
		t.Fatalf("first received invitation = %+v", received[0])
	}
	if received[1].ProtecteeEmail != "second@example.com" || received[1].LogSharing {
		t.Fatalf("second received invitation = %+v", received[1])
	}
	if received[0].ID == 0 || received[0].ExpiresAt.IsZero() || received[0].SentAt.IsZero() {
		t.Fatalf("received invitation is not actionable: %+v", received[0])
	}

	empty, err := service.ReceivedInvitations(context.Background(), stranger)
	if err != nil {
		t.Fatalf("ReceivedInvitations for a stranger: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("a stranger saw %d invitations: %+v", len(empty), empty)
	}
}

func TestReceivedInvitationsOmitsTerminalAndExpiredInvitations(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	invitee := createUser(t, db, "invitee@example.com")
	invitation := invite(t, service, protectee, "invitee@example.com", false)

	service.now = func() time.Time { return testClock.Add(invitationTTL + time.Minute) }
	received, err := service.ReceivedInvitations(context.Background(), invitee)
	if err != nil {
		t.Fatalf("ReceivedInvitations: %v", err)
	}
	if len(received) != 0 {
		t.Fatalf("an expired invitation is still offered: %+v", received)
	}

	service.now = func() time.Time { return testClock }
	if _, err := service.DeclineInvitation(context.Background(), ByID(invitation.ID), invitee); err != nil {
		t.Fatalf("DeclineInvitation: %v", err)
	}
	received, err = service.ReceivedInvitations(context.Background(), invitee)
	if err != nil {
		t.Fatalf("ReceivedInvitations: %v", err)
	}
	if len(received) != 0 {
		t.Fatalf("a declined invitation is still offered: %+v", received)
	}
}

func TestDeclineInvitationAlertsOnlyTheProtectee(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	invitee := createUser(t, db, "invitee@example.com")
	invitation := invite(t, service, protectee, "invitee@example.com", true)

	declined, err := service.DeclineInvitation(context.Background(), ByToken(invitation.RawToken), invitee)
	if err != nil {
		t.Fatalf("DeclineInvitation: %v", err)
	}
	if declined.ProtecteeEmail != "protectee@example.com" || !declined.DeclinedAt.Equal(testClock) {
		t.Fatalf("decline receipt = %+v", declined)
	}
	if state := invitationState(t, service, invitation.ID); state != "declined" {
		t.Fatalf("invitation state = %q", state)
	}
	if _, err := db.Exec("SELECT 1"); err != nil {
		t.Fatal(err)
	}

	recipients := queuedRecipients(t, service)
	want := []string{
		MessagePartnerInvitation + " -> invitee@example.com",
		MessageInvitationDeclined + " -> protectee@example.com",
	}
	if strings.Join(recipients, ", ") != strings.Join(want, ", ") {
		t.Fatalf("queued %v, want %v", recipients, want)
	}

	var action, actorEmail, targetEmail string
	if err := db.QueryRow(`SELECT action, actor_email, target_email FROM activity_entries
		WHERE invitation_id = ? ORDER BY id DESC LIMIT 1`, invitation.ID).
		Scan(&action, &actorEmail, &targetEmail); err != nil {
		t.Fatal(err)
	}
	if action != ActionInvitationDeclined || actorEmail != "invitee@example.com" ||
		targetEmail != "protectee@example.com" {
		t.Fatalf("Activity = %s by %s to %s", action, actorEmail, targetEmail)
	}

	// No Partnership exists, so the Partner lock stays inactive.
	view, err := service.ProtecteeView(context.Background(), protectee)
	if err != nil {
		t.Fatal(err)
	}
	if view.PartnerLockActive || len(view.PendingInvitations) != 0 {
		t.Fatalf("Protectee view after decline = %+v", view)
	}
}

func TestDeclineInvitationRefusesEveryoneButTheInvitee(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	stranger := createUser(t, db, "stranger@example.com")
	invitation := invite(t, service, protectee, "invitee@example.com", false)

	for name, viewer := range map[string]int64{"anonymous": 0, "protectee": protectee, "stranger": stranger} {
		if _, err := service.DeclineInvitation(context.Background(), ByToken(invitation.RawToken), viewer); !errors.Is(err, ErrInvitationNotYours) {
			t.Fatalf("DeclineInvitation as %s = %v, want ErrInvitationNotYours", name, err)
		}
		if state := invitationState(t, service, invitation.ID); state != "pending" {
			t.Fatalf("%s mutated the invitation to %q", name, state)
		}
	}
	// The id path never resolves for a User the invitation was not sent to,
	// so ids stay unenumerable.
	if _, err := service.DeclineInvitation(context.Background(), ByID(invitation.ID), stranger); !errors.Is(err, ErrInvitationUnavailable) {
		t.Fatalf("DeclineInvitation by id as a stranger = %v, want ErrInvitationUnavailable", err)
	}
}

func TestCancelInvitationAlertsOnlyTheInvitee(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	invitation := invite(t, service, protectee, "invitee@example.com", false)

	canceled, err := service.CancelInvitation(context.Background(), invitation.ID, protectee)
	if err != nil {
		t.Fatalf("CancelInvitation: %v", err)
	}
	if canceled.InviteeEmail != "invitee@example.com" || !canceled.CanceledAt.Equal(testClock) {
		t.Fatalf("cancellation receipt = %+v", canceled)
	}
	if state := invitationState(t, service, invitation.ID); state != "canceled" {
		t.Fatalf("invitation state = %q", state)
	}
	recipients := queuedRecipients(t, service)
	want := []string{
		MessagePartnerInvitation + " -> invitee@example.com",
		MessageInvitationCanceled + " -> invitee@example.com",
	}
	if strings.Join(recipients, ", ") != strings.Join(want, ", ") {
		t.Fatalf("queued %v, want %v", recipients, want)
	}
	var action string
	if err := db.QueryRow(`SELECT action FROM activity_entries
		WHERE invitation_id = ? ORDER BY id DESC LIMIT 1`, invitation.ID).Scan(&action); err != nil {
		t.Fatal(err)
	}
	if action != ActionInvitationCanceled {
		t.Fatalf("Activity action = %q", action)
	}
}

func TestCancelInvitationRefusesEveryoneButTheProtectee(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	invitee := createUser(t, db, "invitee@example.com")
	stranger := createUser(t, db, "stranger@example.com")
	invitation := invite(t, service, protectee, "invitee@example.com", false)

	for name, viewer := range map[string]int64{"anonymous": 0, "invitee": invitee, "stranger": stranger} {
		if _, err := service.CancelInvitation(context.Background(), invitation.ID, viewer); !errors.Is(err, ErrInvitationUnavailable) {
			t.Fatalf("CancelInvitation as %s = %v, want ErrInvitationUnavailable", name, err)
		}
		if state := invitationState(t, service, invitation.ID); state != "pending" {
			t.Fatalf("%s mutated the invitation to %q", name, state)
		}
	}
}

func TestCorrectingAnInvitationRequiresCancellationAndANewInvitation(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	first := invite(t, service, protectee, "typo@example.com", false)

	// The invited address and the proposed Log sharing are immutable, so the
	// typo cannot be edited; it can only be withdrawn.
	if _, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "typo@example.com", LogSharing: true,
	}); !errors.Is(err, ErrDuplicateInvitation) {
		t.Fatalf("re-inviting the same address = %v, want ErrDuplicateInvitation", err)
	}
	if _, err := service.CancelInvitation(context.Background(), first.ID, protectee); err != nil {
		t.Fatalf("CancelInvitation: %v", err)
	}
	second := invite(t, service, protectee, "correct@example.com", true)
	if second.InviteeEmail != "correct@example.com" || !second.LogSharing {
		t.Fatalf("corrected invitation = %+v", second)
	}
	var invitees string
	if err := db.QueryRow("SELECT invitee_email FROM partner_invitations WHERE id = ?", first.ID).
		Scan(&invitees); err != nil {
		t.Fatal(err)
	}
	if invitees != "typo@example.com" {
		t.Fatalf("the canceled invitation was edited to %q", invitees)
	}
}

func TestExpireDueInvitationsAppendsActivityWithoutMail(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	invitation := invite(t, service, protectee, "invitee@example.com", false)

	expired, err := service.ExpireDueInvitations(context.Background(), 10)
	if err != nil {
		t.Fatalf("ExpireDueInvitations: %v", err)
	}
	if expired != 0 || invitationState(t, service, invitation.ID) != "pending" {
		t.Fatalf("expired %d invitations before the deadline", expired)
	}

	service.now = func() time.Time { return testClock.Add(invitationTTL + time.Hour) }
	expired, err = service.ExpireDueInvitations(context.Background(), 10)
	if err != nil {
		t.Fatalf("ExpireDueInvitations: %v", err)
	}
	if expired != 1 || invitationState(t, service, invitation.ID) != "expired" {
		t.Fatalf("expired %d invitations, state %q", expired, invitationState(t, service, invitation.ID))
	}

	// Expiry is Nightloch's own act and sends nothing (S3-20).
	if recipients := queuedRecipients(t, service); len(recipients) != 1 {
		t.Fatalf("expiry queued mail: %v", recipients)
	}
	var action string
	var actorUserID, occurredAt any
	if err := db.QueryRow(`SELECT action, actor_user_id, occurred_at FROM activity_entries
		WHERE invitation_id = ? ORDER BY id DESC LIMIT 1`, invitation.ID).
		Scan(&action, &actorUserID, &occurredAt); err != nil {
		t.Fatal(err)
	}
	if action != ActionInvitationExpired || actorUserID != nil {
		t.Fatalf("expiry Activity = %q by %v", action, actorUserID)
	}
	if occurred, ok := occurredAt.(int64); !ok || occurred != invitation.ExpiresAt.Unix() {
		t.Fatalf("expiry Activity occurred at %v, want the expiry instant", occurredAt)
	}

	// The sweep is durable and idempotent: a second pass finds nothing.
	again, err := service.ExpireDueInvitations(context.Background(), 10)
	if err != nil || again != 0 {
		t.Fatalf("second sweep expired %d invitations: %v", again, err)
	}
}

func TestReviewInvitationShowsPreciseTerminalOutcomes(t *testing.T) {
	t.Parallel()
	resolve := map[string]func(t *testing.T, service *Service, invitation Invitation, protectee, invitee int64){
		"accepted": func(t *testing.T, service *Service, invitation Invitation, _, invitee int64) {
			if _, err := service.AcceptInvitation(context.Background(), ByToken(invitation.RawToken), invitee); err != nil {
				t.Fatal(err)
			}
		},
		"declined": func(t *testing.T, service *Service, invitation Invitation, _, invitee int64) {
			if _, err := service.DeclineInvitation(context.Background(), ByToken(invitation.RawToken), invitee); err != nil {
				t.Fatal(err)
			}
		},
		"canceled": func(t *testing.T, service *Service, invitation Invitation, protectee, _ int64) {
			if _, err := service.CancelInvitation(context.Background(), invitation.ID, protectee); err != nil {
				t.Fatal(err)
			}
		},
		"expired": func(t *testing.T, service *Service, _ Invitation, _, _ int64) {
			service.now = func() time.Time { return testClock.Add(invitationTTL + time.Hour) }
			if _, err := service.ExpireDueInvitations(context.Background(), 10); err != nil {
				t.Fatal(err)
			}
		},
	}
	for outcome, resolveInvitation := range resolve {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			service, db := newTestService(t)
			protectee := createUser(t, db, "protectee@example.com")
			invitee := createUser(t, db, "invitee@example.com")
			invitation := invite(t, service, protectee, "invitee@example.com", false)
			resolveInvitation(t, service, invitation, protectee, invitee)

			review, err := service.ReviewInvitation(context.Background(), ByToken(invitation.RawToken), 0)
			if err != nil {
				t.Fatalf("ReviewInvitation after %s: %v", outcome, err)
			}
			if string(review.State) != outcome {
				t.Fatalf("state = %q, want %q", review.State, outcome)
			}
			if review.ResolvedAt.IsZero() {
				t.Fatalf("%s outcome has no time", outcome)
			}
			// A terminal outcome still masks the invited address.
			if review.InviteeEmail != "" || strings.Contains(review.MaskedInviteeEmail, "invitee") {
				t.Fatalf("terminal review exposed the invitee: %+v", review)
			}
			if review.ProtecteeEmail != "protectee@example.com" {
				t.Fatalf("terminal review = %+v", review)
			}
		})
	}
}

func TestReviewInvitationByIDServesOnlyTheInvitedUser(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	invitee := createUser(t, db, "invitee@example.com")
	stranger := createUser(t, db, "stranger@example.com")
	invitation := invite(t, service, protectee, "invitee@example.com", true)

	review, err := service.ReviewInvitation(context.Background(), ByID(invitation.ID), invitee)
	if err != nil {
		t.Fatalf("ReviewInvitation by id: %v", err)
	}
	if !review.ViewerMatches || review.InviteeEmail != "invitee@example.com" ||
		review.State != InvitationPending || !review.LogSharing {
		t.Fatalf("invitee review by id = %+v", review)
	}

	for name, viewer := range map[string]int64{"anonymous": 0, "protectee": protectee, "stranger": stranger} {
		if _, err := service.ReviewInvitation(context.Background(), ByID(invitation.ID), viewer); !errors.Is(err, ErrInvitationUnavailable) {
			t.Fatalf("ReviewInvitation by id as %s = %v, want ErrInvitationUnavailable", name, err)
		}
	}
}

func TestReviewInvitationHidesUnknownTokensAndIDs(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	invitee := createUser(t, db, "invitee@example.com")
	for _, ref := range []InvitationRef{
		ByToken(""), ByToken("not-base64-!!"), ByToken(strings.Repeat("A", 43)), ByID(0), ByID(4242),
	} {
		if _, err := service.ReviewInvitation(context.Background(), ref, invitee); !errors.Is(err, ErrInvitationUnavailable) {
			t.Fatalf("ReviewInvitation(%+v) = %v, want ErrInvitationUnavailable", ref, err)
		}
	}
}

func TestRacingLifecycleActionsAreFirstCommitWins(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	invitee := createUser(t, db, "invitee@example.com")
	invitation := invite(t, service, protectee, "invitee@example.com", false)
	// Race a second before the deadline, so accept, decline, and cancel all
	// still see a live row and genuinely compete for it.
	service.now = func() time.Time { return testClock.Add(invitationTTL - time.Second) }

	attempts := []func() error{
		func() error {
			_, err := service.AcceptInvitation(context.Background(), ByToken(invitation.RawToken), invitee)
			return err
		},
		func() error {
			_, err := service.DeclineInvitation(context.Background(), ByToken(invitation.RawToken), invitee)
			return err
		},
		func() error {
			_, err := service.CancelInvitation(context.Background(), invitation.ID, protectee)
			return err
		},
	}
	results := make([]error, len(attempts))
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for index, attempt := range attempts {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			results[index] = attempt()
		}()
	}
	start.Done()
	done.Wait()

	winners := 0
	for index, err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrInvitationUnavailable):
		default:
			t.Fatalf("attempt %d lost with %v, want ErrInvitationUnavailable", index, err)
		}
	}
	if winners != 1 {
		t.Fatalf("%d attempts committed, want exactly 1: %v", winners, results)
	}

	// Whatever won, the row resolved once and no second Partnership, Activity
	// entry, or message followed from the losers.
	var state string
	var partnerships, entries int
	if err := db.QueryRow(`SELECT
		(SELECT state FROM partner_invitations WHERE id = ?1),
		(SELECT COUNT(*) FROM partnerships),
		(SELECT COUNT(*) FROM activity_entries WHERE invitation_id = ?1)`, invitation.ID).
		Scan(&state, &partnerships, &entries); err != nil {
		t.Fatal(err)
	}
	if state == "pending" || entries != 2 {
		t.Fatalf("state %q with %d Activity entries", state, entries)
	}
	if (state == "accepted") != (partnerships == 1) {
		t.Fatalf("state %q with %d Partnerships", state, partnerships)
	}

	// The losers are quiet, and the sweep cannot re-resolve a settled row.
	service.now = func() time.Time { return testClock.Add(invitationTTL + time.Hour) }
	if expired, err := service.ExpireDueInvitations(context.Background(), 10); err != nil || expired != 0 {
		t.Fatalf("sweep expired %d settled invitations: %v", expired, err)
	}
	if state != "accepted" {
		if recipients := queuedRecipients(t, service); len(recipients) != 2 {
			t.Fatalf("losing actions queued mail: %v", recipients)
		}
	}
}

func TestDeclineAndCancelMessagesCarryTheApprovedCopy(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	invitee := createUser(t, db, "invitee@example.com")
	declinedInvitation := invite(t, service, protectee, "invitee@example.com", false)
	if _, err := service.DeclineInvitation(context.Background(), ByID(declinedInvitation.ID), invitee); err != nil {
		t.Fatal(err)
	}
	other := createUser(t, db, "other@example.com")
	canceledInvitation := invite(t, service, other, "invitee@example.com", false)
	if _, err := service.CancelInvitation(context.Background(), canceledInvitation.ID, other); err != nil {
		t.Fatal(err)
	}

	sender := &mailmessage.RecordingSender{}
	if _, err := service.DispatchDue(context.Background(), sender, 10); err != nil {
		t.Fatal(err)
	}
	byRecipient := map[string][]mailmessage.Envelope{}
	for _, envelope := range sender.Envelopes {
		byRecipient[envelope.To] = append(byRecipient[envelope.To], envelope)
	}

	decline := byRecipient["protectee@example.com"][0]
	if decline.Subject != "invitee@example.com declined your Nightloch Partner invitation" {
		t.Fatalf("decline subject = %q", decline.Subject)
	}
	for _, want := range []string{
		"invitee@example.com declined your Partner invitation on 2026-08-23 15:04 UTC.",
		"No Partnership was created. You may send a new invitation later.",
	} {
		if !strings.Contains(decline.Text, want) {
			t.Fatalf("decline message missing %q:\n%s", want, decline.Text)
		}
	}

	var cancel mailmessage.Envelope
	for _, envelope := range byRecipient["invitee@example.com"] {
		if strings.Contains(envelope.Subject, "canceled") {
			cancel = envelope
		}
	}
	if cancel.Subject != "other@example.com canceled your Nightloch Partner invitation" {
		t.Fatalf("cancellation subject = %q", cancel.Subject)
	}
	for _, want := range []string{
		"other@example.com canceled the Partner invitation sent to you on\n2026-08-23 15:04 UTC.",
		"No Partnership was created and no Partner powers were granted.",
	} {
		if !strings.Contains(cancel.Text, want) {
			t.Fatalf("cancellation message missing %q:\n%s", want, cancel.Text)
		}
	}
	// Neither message carries a bearer link, so a forwarded alert grants nothing.
	for _, envelope := range []mailmessage.Envelope{decline, cancel} {
		if strings.Contains(envelope.Text, "/partners/invitations/") {
			t.Fatalf("terminal alert leaked a review link:\n%s", envelope.Text)
		}
	}
}
