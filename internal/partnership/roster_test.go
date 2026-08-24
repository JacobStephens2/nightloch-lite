package partnership

import (
	"context"
	"errors"
	"strings"
	"testing"

	mailmessage "github.com/JacobStephens2/nightloch-lite/internal/mail"
)

// acceptFor accepts an invitation as the invitee, failing the test on refusal.
func acceptFor(t *testing.T, service *Service, invitation Invitation, inviteeID int64) Acceptance {
	t.Helper()
	acceptance, err := service.AcceptInvitation(context.Background(), ByToken(invitation.RawToken), inviteeID)
	if err != nil {
		t.Fatalf("AcceptInvitation: %v", err)
	}
	return acceptance
}

// lastJobID is the newest queued delivery job, so a test can scope the next
// mutation's recipients to the jobs that mutation alone produced.
func lastJobID(t *testing.T, service *Service) int64 {
	t.Helper()
	var id int64
	if err := service.db.QueryRow("SELECT COALESCE(MAX(id), 0) FROM mail_delivery_jobs").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// queuedBodies maps recipient to the one body of kind queued after since. A
// recipient appearing twice fails the test: every signal is exactly one
// envelope per recipient (S3-21).
func queuedBodies(t *testing.T, service *Service, kind string, since int64) map[string]string {
	t.Helper()
	rows, err := service.db.Query(
		"SELECT recipient_email, subject, body_text FROM mail_delivery_jobs WHERE message_kind = ? AND id > ? ORDER BY id",
		kind, since)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	bodies := map[string]string{}
	for rows.Next() {
		var recipient, subject, body string
		if err := rows.Scan(&recipient, &subject, &body); err != nil {
			t.Fatal(err)
		}
		if _, duplicate := bodies[recipient]; duplicate {
			t.Fatalf("%s was queued twice for %s", kind, recipient)
		}
		bodies[recipient] = subject + "\n" + body
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return bodies
}

// partnerUp drives one Protectee to one accepted Partnership, which is
// the 3A state every 3B scenario starts from.
func partnerUp(t *testing.T, service *Service, protectee, partner int64, partnerEmail string, logSharing bool) {
	t.Helper()
	acceptFor(t, service, invite(t, service, protectee, partnerEmail, logSharing), partner)
}

func TestSecondInvitationIsPermittedAndAlertsEveryCurrentPartner(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	first := createUser(t, db, "first@example.com")
	createUser(t, db, "second@example.com")
	partnerUp(t, service, protectee, first, "first@example.com", false)

	since := lastJobID(t, service)
	if _, err := service.InvitePartner(ctx, InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "second@example.com", LogSharing: true,
	}); err != nil {
		t.Fatalf("second invitation refused: %v", err)
	}

	// The invitee gets the invitation; every current Partner gets a separate
	// Action alert. Neither envelope names the other recipient.
	invitations := queuedBodies(t, service, MessagePartnerInvitation, since)
	if len(invitations) != 1 || invitations["second@example.com"] == "" {
		t.Fatalf("invitee messages = %v", invitations)
	}
	if !strings.Contains(invitations["second@example.com"], "currently has 1 active Partner(s)") {
		t.Fatalf("second invitee was not told the Partner count:\n%s", invitations["second@example.com"])
	}
	if strings.Contains(invitations["second@example.com"], "first@example.com") {
		t.Fatalf("the invitation leaked a Roster identity:\n%s", invitations["second@example.com"])
	}

	alerts := queuedBodies(t, service, MessagePartnerInvitationSent, since)
	if len(alerts) != 1 || alerts["first@example.com"] == "" {
		t.Fatalf("current-Partner alerts = %v", alerts)
	}
	for _, want := range []string{
		"[Nightloch] protectee@example.com invited second@example.com as a Partner",
		"No Partner powers have been granted yet.",
		"Log sharing after acceptance: On",
		"Reciprocal Partnerships if accepted: No",
		"Invitation expires: 2026-08-30 15:04 UTC",
		"you cannot accept, cancel,\nor decline it for either person",
		"alerts@nightloch.app",
	} {
		if !strings.Contains(alerts["first@example.com"], want) {
			t.Fatalf("current-Partner alert missing %q:\n%s", want, alerts["first@example.com"])
		}
	}
}

func TestSecondAcceptanceAlertsProtecteePriorPartnersAndNewPartnerSeparately(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	first := createUser(t, db, "first@example.com")
	second := createUser(t, db, "second@example.com")
	partnerUp(t, service, protectee, first, "first@example.com", false)
	pending := invite(t, service, protectee, "second@example.com", true)
	since := lastJobID(t, service)
	acceptFor(t, service, pending, second)

	roster := queuedBodies(t, service, MessagePartnershipAcceptedExisting, since)
	if len(roster) != 2 {
		t.Fatalf("Roster acceptance alerts went to %v, want the Protectee and the prior Partner", roster)
	}
	for _, recipient := range []string{"protectee@example.com", "first@example.com"} {
		body := roster[recipient]
		for _, want := range []string{
			"[Nightloch] second@example.com now protects protectee@example.com",
			"Active Partners now: 2", "The Partner lock remains active.",
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("acceptance alert to %s missing %q:\n%s", recipient, want, body)
			}
		}
	}
	// The prior Partner's own acceptance produced the first-Partnership copy;
	// this second acceptance must not tell the new Partner the Roster.
	newPartner := queuedBodies(t, service, MessagePartnershipAcceptedPartner, since)["second@example.com"]
	if !strings.Contains(newPartner, "has 1 other active Partner(s)") {
		t.Fatalf("new Partner was not told the other-Partner count:\n%s", newPartner)
	}
	if strings.Contains(newPartner, "first@example.com") {
		t.Fatalf("the acceptance email leaked the Roster:\n%s", newPartner)
	}
}

func TestCancelingAnInvitationAlertsTheInviteeAndEveryCurrentPartner(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	first := createUser(t, db, "first@example.com")
	partnerUp(t, service, protectee, first, "first@example.com", false)
	pending := invite(t, service, protectee, "second@example.com", false)

	since := lastJobID(t, service)
	if _, err := service.CancelInvitation(context.Background(), pending.ID, protectee); err != nil {
		t.Fatalf("CancelInvitation: %v", err)
	}
	invitee := queuedBodies(t, service, MessageInvitationCanceled, since)
	if len(invitee) != 1 || !strings.Contains(invitee["second@example.com"], "No Partnership was created") {
		t.Fatalf("invitee cancellation = %v", invitee)
	}
	alerts := queuedBodies(t, service, MessageInvitationCanceledPartner, since)
	if len(alerts) != 1 {
		t.Fatalf("current-Partner cancellation alerts = %v", alerts)
	}
	for _, want := range []string{
		"[Nightloch] protectee@example.com canceled the invitation to second@example.com",
		"No Partnership was created and no Partner powers were granted.",
		"alerts@nightloch.app",
	} {
		if !strings.Contains(alerts["first@example.com"], want) {
			t.Fatalf("cancellation alert missing %q:\n%s", want, alerts["first@example.com"])
		}
	}
}

func TestEveryActivePartnerSeesTheFullRosterAndPendingInvitations(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	first := createUser(t, db, "first@example.com")
	second := createUser(t, db, "second@example.com")
	partnerUp(t, service, protectee, first, "first@example.com", true)
	acceptFor(t, service, invite(t, service, protectee, "second@example.com", false), second)
	// A Reciprocal Partnership in the other direction must read as reciprocal
	// on the Roster row it belongs to, and only on that one.
	partnerUp(t, service, first, protectee, "protectee@example.com", false)
	invite(t, service, protectee, "third@example.com", true)

	for _, partner := range []int64{first, second} {
		protecting, err := service.ProtectingView(ctx, partner)
		if err != nil {
			t.Fatal(err)
		}
		var entry ProtecteeEntry
		for _, candidate := range protecting {
			if candidate.ProtecteeEmail == "protectee@example.com" {
				entry = candidate
			}
		}
		if len(entry.Roster) != 2 {
			t.Fatalf("Partner %d saw Roster %+v, want both Partners", partner, entry.Roster)
		}
		if entry.Roster[0].PartnerEmail != "first@example.com" || !entry.Roster[0].LogSharing ||
			!entry.Roster[0].Reciprocal || entry.Roster[0].Since.IsZero() {
			t.Fatalf("first Roster row = %+v", entry.Roster[0])
		}
		if entry.Roster[1].PartnerEmail != "second@example.com" || entry.Roster[1].LogSharing ||
			entry.Roster[1].Reciprocal {
			t.Fatalf("second Roster row = %+v", entry.Roster[1])
		}
		if len(entry.PendingInvitations) != 1 {
			t.Fatalf("Partner %d saw pending invitations %+v", partner, entry.PendingInvitations)
		}
		pending := entry.PendingInvitations[0]
		if pending.InviteeEmail != "third@example.com" || pending.InviterEmail != "protectee@example.com" ||
			!pending.LogSharing || pending.Reciprocal || pending.SentAt.IsZero() ||
			pending.ExpiresAt.IsZero() || pending.DeliveryState != DeliveryQueued {
			t.Fatalf("pending invitation as seen by a Partner = %+v", pending)
		}
	}

	// The Protectee's own view reports the same Roster shape.
	view, err := service.ProtecteeView(ctx, protectee)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Partners) != 2 || !view.Partners[0].Reciprocal || view.Partners[1].Reciprocal {
		t.Fatalf("Protectee Roster = %+v", view.Partners)
	}
}

func TestRosterReportsPartnerReachabilityWithoutChangingPowers(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")
	partnerUp(t, service, protectee, partner, "partner@example.com", false)

	reachability := func() Reachability {
		t.Helper()
		view, err := service.ProtecteeView(ctx, protectee)
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Partners) != 1 {
			t.Fatalf("Roster = %+v", view.Partners)
		}
		return view.Partners[0].Reachability
	}
	if got := reachability(); got != ReachabilityOK {
		t.Fatalf("a queued acceptance alert already reads as %q", got.Label())
	}

	failing := &mailmessage.RecordingSender{SendError: errors.New("provider unavailable")}
	if _, err := service.DispatchDue(ctx, failing, 10); err == nil {
		t.Fatal("DispatchDue hid the delivery failure")
	}
	if got := reachability(); got != ReachabilityDelayed || got.Label() != "Delivery delayed" {
		t.Fatalf("retryable failure reads as %q", got.Label())
	}
	if _, err := db.Exec("UPDATE mail_delivery_jobs SET state = ? WHERE recipient_email = ?",
		DeliveryFailed, "partner@example.com"); err != nil {
		t.Fatal(err)
	}
	if got := reachability(); got != ReachabilityUnreachable ||
		got.Label() != "Nightloch cannot reach this Partner" {
		t.Fatalf("exhausted delivery reads as %q", got.Label())
	}

	// Reachability is a visible signal only: it never suspends powers (S3-58).
	allowed, err := service.CanLoosen(ctx, partner, protectee)
	if err != nil || !allowed {
		t.Fatalf("an unreachable Partner lost their powers: %v, %v", allowed, err)
	}
	if _, err := db.Exec("UPDATE mail_delivery_jobs SET state = ? WHERE recipient_email = ?",
		DeliverySent, "partner@example.com"); err != nil {
		t.Fatal(err)
	}
	if got := reachability(); got != ReachabilityOK {
		t.Fatalf("later delivery did not clear the warning: %q", got.Label())
	}
}
