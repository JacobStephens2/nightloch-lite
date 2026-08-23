package partnership

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	mailmessage "github.com/JacobStephens2/nightloch-lite/internal/mail"
)

func TestReviewInvitationMasksTheInviteeUntilTheMatchingUserAuthenticates(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	invitation, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "invitee@example.com", LogSharing: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	anonymous, err := service.ReviewInvitation(context.Background(), ByToken(invitation.RawToken), 0)
	if err != nil {
		t.Fatalf("ReviewInvitation: %v", err)
	}
	if anonymous.ViewerMatches || anonymous.InviteeEmail != "" {
		t.Fatalf("anonymous review exposed the invitee: %+v", anonymous)
	}
	if strings.Contains(anonymous.MaskedInviteeEmail, "invitee") {
		t.Fatalf("masked invitee = %q", anonymous.MaskedInviteeEmail)
	}
	if anonymous.ProtecteeEmail != "protectee@example.com" || !anonymous.LogSharing {
		t.Fatalf("review disclosure = %+v", anonymous)
	}

	stranger := createUser(t, db, "stranger@example.com")
	wrong, err := service.ReviewInvitation(context.Background(), ByToken(invitation.RawToken), stranger)
	if err != nil {
		t.Fatalf("ReviewInvitation for a wrong session: %v", err)
	}
	if wrong.ViewerMatches || wrong.ViewerEmail != "stranger@example.com" ||
		strings.Contains(wrong.MaskedInviteeEmail, "invitee") {
		t.Fatalf("wrong session review = %+v", wrong)
	}

	invitee := createUser(t, db, "invitee@example.com")
	matching, err := service.ReviewInvitation(context.Background(), ByToken(invitation.RawToken), invitee)
	if err != nil {
		t.Fatalf("ReviewInvitation for the invitee: %v", err)
	}
	if !matching.ViewerMatches || matching.InviteeEmail != "invitee@example.com" {
		t.Fatalf("matching review = %+v", matching)
	}

	// Reviewing is a read; it must not resolve the invitation or send mail.
	var pending, jobs int
	if err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM partner_invitations WHERE state = 'pending'),
		(SELECT COUNT(*) FROM mail_delivery_jobs)`).Scan(&pending, &jobs); err != nil {
		t.Fatal(err)
	}
	if pending != 1 || jobs != 1 {
		t.Fatalf("review mutated state: pending=%d jobs=%d", pending, jobs)
	}
}

func TestAcceptInvitationCommitsPartnershipActivityAndBothMessages(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	invitation, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "invitee@example.com", LogSharing: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	invitee := createUser(t, db, "invitee@example.com")

	stranger := createUser(t, db, "stranger@example.com")
	if _, err := service.AcceptInvitation(context.Background(), ByToken(invitation.RawToken), stranger); !errors.Is(err, ErrInvitationNotYours) {
		t.Fatalf("stranger acceptance = %v, want ErrInvitationNotYours", err)
	}

	accepted, err := service.AcceptInvitation(context.Background(), ByToken(invitation.RawToken), invitee)
	if err != nil {
		t.Fatalf("AcceptInvitation: %v", err)
	}
	if accepted.PartnershipID == 0 || !accepted.AcceptedAt.Equal(testClock) {
		t.Fatalf("acceptance = %+v", accepted)
	}

	var state string
	var resolvedBy int64
	var partnerships, activity int
	if err := db.QueryRow(`SELECT
		(SELECT state FROM partner_invitations),
		(SELECT resolved_by_user_id FROM partner_invitations),
		(SELECT COUNT(*) FROM partnerships WHERE ended_at IS NULL AND edge_type = 'peer' AND log_sharing = 1),
		(SELECT COUNT(*) FROM activity_entries WHERE action = ?)`, ActionInvitationAccepted).
		Scan(&state, &resolvedBy, &partnerships, &activity); err != nil {
		t.Fatal(err)
	}
	if state != "accepted" || resolvedBy != invitee || partnerships != 1 || activity != 1 {
		t.Fatalf("acceptance state=%q resolvedBy=%d partnerships=%d activity=%d",
			state, resolvedBy, partnerships, activity)
	}

	// A second attempt on a spent token wins nothing and duplicates nothing.
	if _, err := service.AcceptInvitation(context.Background(), ByToken(invitation.RawToken), invitee); !errors.Is(err, ErrInvitationUnavailable) {
		t.Fatalf("replayed acceptance = %v, want ErrInvitationUnavailable", err)
	}

	sender := &mailmessage.RecordingSender{}
	if sent, err := service.DispatchDue(context.Background(), sender, 10); err != nil || sent != 3 {
		t.Fatalf("DispatchDue = %d, %v", sent, err)
	}
	byRecipient := map[string]string{}
	for _, envelope := range sender.Envelopes {
		if _, duplicate := byRecipient[envelope.To+envelope.Subject]; duplicate {
			t.Fatalf("duplicate message to %s", envelope.To)
		}
		byRecipient[envelope.To+envelope.Subject] = envelope.Text
	}
	protecteeText := byRecipient["protectee@example.com[Nightloch] invitee@example.com now protects protectee@example.com"]
	partnerText := byRecipient["invitee@example.comYou now protect protectee@example.com with Nightloch"]
	if protecteeText == "" || partnerText == "" {
		t.Fatalf("acceptance messages = %+v", sender.Envelopes)
	}
	for _, want := range []string{"The Partner lock is now active.", "Active Partners now: 1", "Log sharing: On"} {
		if !strings.Contains(protecteeText, want) {
			t.Fatalf("Protectee acceptance missing %q:\n%s", want, protecteeText)
		}
	}
	for _, want := range []string{
		"You accepted the Partnership with protectee@example.com",
		"has 0 other active Partner(s)",
		"On — you can view their DNS domain log.",
	} {
		if !strings.Contains(partnerText, want) {
			t.Fatalf("Partner acceptance missing %q:\n%s", want, partnerText)
		}
	}
	if strings.Contains(partnerText, "stranger@example.com") {
		t.Fatalf("acceptance message leaked an unrelated Roster:\n%s", partnerText)
	}

	// The HTML part must mirror the plain text without changing meaning: the
	// facts block keeps its line breaks and no statement is swallowed into a
	// link label.
	for _, envelope := range sender.Envelopes {
		if envelope.To != "protectee@example.com" || !strings.HasPrefix(envelope.Subject, "[Nightloch]") {
			continue
		}
		if !strings.Contains(envelope.HTML, "<p>Log sharing: On<br>Reciprocal Partnerships: No<br>Active Partners now: 1</p>") ||
			!strings.Contains(envelope.HTML, `<a href="https://lite.nightloch.app/">Review the Roster and Activity</a>`) {
			t.Fatalf("acceptance HTML part = %q", envelope.HTML)
		}
	}
}

func TestAcceptedPartnershipDrivesLockRosterAndAuthorization(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	invitation, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "invitee@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	invitee := createUser(t, db, "invitee@example.com")

	before, err := service.CanLoosen(context.Background(), protectee, protectee)
	if err != nil || !before {
		t.Fatalf("unpartnered self loosen = %v, %v", before, err)
	}
	if _, err := service.AcceptInvitation(context.Background(), ByToken(invitation.RawToken), invitee); err != nil {
		t.Fatal(err)
	}

	view, err := service.ProtecteeView(context.Background(), protectee)
	if err != nil {
		t.Fatal(err)
	}
	if !view.PartnerLockActive || len(view.Partners) != 1 ||
		view.Partners[0].PartnerEmail != "invitee@example.com" || len(view.PendingInvitations) != 0 {
		t.Fatalf("Protectee view after acceptance = %+v", view)
	}

	protecting, err := service.ProtectingView(context.Background(), invitee)
	if err != nil {
		t.Fatal(err)
	}
	if len(protecting) != 1 || protecting[0].ProtecteeEmail != "protectee@example.com" ||
		len(protecting[0].Roster) != 1 || protecting[0].Roster[0].PartnerEmail != "invitee@example.com" {
		t.Fatalf("People you protect = %+v", protecting)
	}

	stranger := createUser(t, db, "stranger@example.com")
	for _, check := range []struct {
		name             string
		actor, protectee int64
		want             bool
	}{
		{"partnered Protectee is denied closed", protectee, protectee, false},
		{"active Partner may loosen", invitee, protectee, true},
		{"stranger may not loosen", stranger, protectee, false},
		{"unknown actor may not loosen", 0, protectee, false},
		{"unknown Protectee may not be loosened", invitee, 0, false},
	} {
		got, err := service.CanLoosen(context.Background(), check.actor, check.protectee)
		if err != nil || got != check.want {
			t.Fatalf("%s: CanLoosen = %v, %v; want %v", check.name, got, err, check.want)
		}
	}

	// Log sharing was left off, so the accepted Partnership grants no log access.
	for _, check := range []struct {
		name             string
		actor, protectee int64
		want             bool
	}{
		{"Partner without Log sharing", invitee, protectee, false},
		{"Protectee's own log", protectee, protectee, true},
		{"stranger", stranger, protectee, false},
		{"unknown actor", 0, protectee, false},
	} {
		got, err := service.CanViewLogs(context.Background(), check.actor, check.protectee)
		if err != nil || got != check.want {
			t.Fatalf("%s: CanViewLogs = %v, %v; want %v", check.name, got, err, check.want)
		}
	}
}

func TestCanViewLogsFollowsTheAcceptedLogSharingChoice(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	invitation, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "invitee@example.com", LogSharing: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	invitee := createUser(t, db, "invitee@example.com")
	if _, err := service.AcceptInvitation(context.Background(), ByToken(invitation.RawToken), invitee); err != nil {
		t.Fatal(err)
	}
	allowed, err := service.CanViewLogs(context.Background(), invitee, protectee)
	if err != nil || !allowed {
		t.Fatalf("CanViewLogs with Log sharing on = %v, %v", allowed, err)
	}
}

func TestAcceptInvitationRefusesAnExpiredInvitation(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	invitation, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "invitee@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	invitee := createUser(t, db, "invitee@example.com")
	service.now = func() time.Time { return testClock.Add(invitationTTL + time.Minute) }

	// The bearer token is still valid, so the review names the precise
	// outcome rather than hiding behind "unavailable" (S3-29).
	review, err := service.ReviewInvitation(context.Background(), ByToken(invitation.RawToken), invitee)
	if err != nil {
		t.Fatalf("expired review = %v", err)
	}
	if review.State != InvitationExpired || !review.ResolvedAt.Equal(invitation.ExpiresAt) {
		t.Fatalf("expired review = %+v", review)
	}
	if _, err := service.AcceptInvitation(context.Background(), ByToken(invitation.RawToken), invitee); !errors.Is(err, ErrInvitationUnavailable) {
		t.Fatalf("expired acceptance = %v", err)
	}
	var partnerships int
	if err := db.QueryRow("SELECT COUNT(*) FROM partnerships").Scan(&partnerships); err != nil {
		t.Fatal(err)
	}
	if partnerships != 0 {
		t.Fatalf("expired invitation created %d Partnerships", partnerships)
	}
}
