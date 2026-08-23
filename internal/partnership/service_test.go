package partnership

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JacobStephens2/nightloch-lite/internal/database"
	mailmessage "github.com/JacobStephens2/nightloch-lite/internal/mail"
)

var testClock = time.Date(2026, 8, 23, 15, 4, 5, 0, time.UTC)

func newTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "nightloch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	baseURL, _ := url.Parse("https://lite.nightloch.app")
	service := NewService(db, baseURL)
	service.now = func() time.Time { return testClock }
	return service, db
}

func createUser(t *testing.T, db *sql.DB, email string) int64 {
	t.Helper()
	result, err := db.Exec("INSERT INTO users(email, created_at) VALUES (?, unixepoch())", email)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestInvitePartnerCommitsInvitationActivityAndDeliveryJobTogether(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")

	invitation, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: " Invitee@Example.com ", LogSharing: true,
	})
	if err != nil {
		t.Fatalf("InvitePartner: %v", err)
	}
	if invitation.InviteeEmail != "invitee@example.com" {
		t.Fatalf("invitee email = %q", invitation.InviteeEmail)
	}
	if want := testClock.Add(invitationTTL); !invitation.ExpiresAt.Equal(want) {
		t.Fatalf("expires at = %v, want %v", invitation.ExpiresAt, want)
	}

	var invitations, activities, jobs int
	row := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM partner_invitations WHERE state = 'pending'),
		(SELECT COUNT(*) FROM activity_entries WHERE action = 'partner_invitation_sent'),
		(SELECT COUNT(*) FROM mail_delivery_jobs WHERE state = 'queued')`)
	if err := row.Scan(&invitations, &activities, &jobs); err != nil {
		t.Fatal(err)
	}
	if invitations != 1 || activities != 1 || jobs != 1 {
		t.Fatalf("invitations=%d activities=%d jobs=%d, want 1/1/1", invitations, activities, jobs)
	}

	var recipient, replyTo string
	if err := db.QueryRow("SELECT recipient_email, reply_to FROM mail_delivery_jobs").Scan(&recipient, &replyTo); err != nil {
		t.Fatal(err)
	}
	if recipient != "invitee@example.com" || replyTo != "protectee@example.com" {
		t.Fatalf("job recipient=%q reply-to=%q", recipient, replyTo)
	}
}

func TestInvitePartnerStoresOnlyTheTokenHash(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")

	invitation, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "invitee@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if invitation.RawToken == "" {
		t.Fatal("InvitePartner returned no raw token")
	}

	var stored []byte
	var bodyText string
	if err := db.QueryRow("SELECT token_hash FROM partner_invitations").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 32 || strings.Contains(string(stored), invitation.RawToken) {
		t.Fatalf("stored token hash = %x", stored)
	}
	if err := db.QueryRow("SELECT body_text FROM mail_delivery_jobs").Scan(&bodyText); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bodyText, invitation.RawToken) {
		t.Fatal("prepared recipient message does not carry the raw token")
	}
}

func TestInvitePartnerRejectsSelfDuplicateAndAllowsReverseDirection(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	invitee := createUser(t, db, "invitee@example.com")
	ctx := context.Background()

	if _, err := service.InvitePartner(ctx, InviteCommand{ProtecteeID: protectee, InviteeEmail: "PROTECTEE@example.com"}); !errors.Is(err, ErrSelfInvitation) {
		t.Fatalf("self invitation error = %v", err)
	}
	if _, err := service.InvitePartner(ctx, InviteCommand{ProtecteeID: protectee, InviteeEmail: "not-an-email"}); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("invalid email error = %v", err)
	}
	if _, err := service.InvitePartner(ctx, InviteCommand{ProtecteeID: protectee, InviteeEmail: "invitee@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.InvitePartner(ctx, InviteCommand{ProtecteeID: protectee, InviteeEmail: "invitee@example.com"}); !errors.Is(err, ErrDuplicateInvitation) {
		t.Fatalf("duplicate invitation error = %v", err)
	}
	if _, err := service.InvitePartner(ctx, InviteCommand{ProtecteeID: protectee, InviteeEmail: "someone-else@example.com"}); !errors.Is(err, ErrOnePartnerLimit) {
		t.Fatalf("3A exposure gate error = %v", err)
	}
	if _, err := service.InvitePartner(ctx, InviteCommand{ProtecteeID: invitee, InviteeEmail: "protectee@example.com"}); err != nil {
		t.Fatalf("reverse direction rejected: %v", err)
	}
}

func TestInvitePartnerRejectsDuplicateOfActiveDirectionalPartnership(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")
	if _, err := db.Exec(`INSERT INTO partnerships(protectee_id, partner_id, edge_type, log_sharing, accepted_at)
		VALUES (?, ?, 'peer', 0, ?)`, protectee, partner, testClock.Unix()); err != nil {
		t.Fatal(err)
	}
	_, err := service.InvitePartner(context.Background(), InviteCommand{ProtecteeID: protectee, InviteeEmail: "partner@example.com"})
	if !errors.Is(err, ErrAlreadyPartnered) {
		t.Fatalf("duplicate active Partnership error = %v", err)
	}
}

func TestPendingInvitationLeavesPartnerLockInactiveAndCanLoosenUnchanged(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	ctx := context.Background()

	before, err := service.CanLoosen(ctx, protectee, protectee)
	if err != nil || !before {
		t.Fatalf("CanLoosen before invitation = %v, %v; want true", before, err)
	}
	if _, err := service.InvitePartner(ctx, InviteCommand{ProtecteeID: protectee, InviteeEmail: "invitee@example.com"}); err != nil {
		t.Fatal(err)
	}
	after, err := service.CanLoosen(ctx, protectee, protectee)
	if err != nil || !after {
		t.Fatalf("CanLoosen after invitation = %v, %v; want true", after, err)
	}

	view, err := service.ProtecteeView(ctx, protectee)
	if err != nil {
		t.Fatal(err)
	}
	if view.PartnerLockActive {
		t.Fatal("pending invitation activated the Partner lock")
	}
	if len(view.PendingInvitations) != 1 || view.PendingInvitations[0].InviteeEmail != "invitee@example.com" {
		t.Fatalf("pending invitations = %+v", view.PendingInvitations)
	}
}

func TestCanLoosenDeniesPartneredProtecteeAndPermitsActivePartner(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")
	stranger := createUser(t, db, "stranger@example.com")
	if _, err := db.Exec(`INSERT INTO partnerships(protectee_id, partner_id, edge_type, log_sharing, accepted_at)
		VALUES (?, ?, 'peer', 0, ?)`, protectee, partner, testClock.Unix()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, testCase := range []struct {
		name  string
		actor int64
		want  bool
	}{
		{"partnered Protectee", protectee, false},
		{"active Partner", partner, true},
		{"unrelated User", stranger, false},
	} {
		got, err := service.CanLoosen(ctx, testCase.actor, protectee)
		if err != nil {
			t.Fatalf("%s: %v", testCase.name, err)
		}
		if got != testCase.want {
			t.Fatalf("%s: CanLoosen = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

func TestDispatchDueSendsCanonicalInvitationAndResumesAfterRestart(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	ctx := context.Background()

	invitation, err := service.InvitePartner(ctx, InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "invitee@example.com", LogSharing: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A process that dies before dispatching leaves the job queued. A fresh
	// Service over the same database must pick it up.
	baseURL, _ := url.Parse("https://lite.nightloch.app")
	restarted := NewService(db, baseURL)
	restarted.now = func() time.Time { return testClock.Add(time.Minute) }
	sender := &mailmessage.RecordingSender{}

	sent, err := restarted.DispatchDue(ctx, sender, 10)
	if err != nil || sent != 1 {
		t.Fatalf("DispatchDue = %d, %v; want 1, nil", sent, err)
	}
	if len(sender.Envelopes) != 1 {
		t.Fatalf("envelopes = %+v", sender.Envelopes)
	}
	envelope := sender.Envelopes[0]
	if envelope.To != "invitee@example.com" || envelope.ReplyTo != "protectee@example.com" {
		t.Fatalf("envelope routing = %+v", envelope)
	}
	if envelope.Subject != "protectee@example.com invited you to be their Nightloch Partner" {
		t.Fatalf("subject = %q", envelope.Subject)
	}
	for _, want := range []string{
		"protectee@example.com invited you to become their Partner in Nightloch.",
		"Log sharing: On — you will receive domain-log access after acceptance.",
		"https://lite.nightloch.app/partners/invitations/" + invitation.RawToken,
		"This invitation expires 2026-08-30 15:04 UTC.",
		"Replies to this email go directly to protectee@example.com.",
	} {
		if !strings.Contains(envelope.Text, want) {
			t.Fatalf("plain text missing %q:\n%s", want, envelope.Text)
		}
	}
	if envelope.HTML == "" || strings.Contains(envelope.HTML, "<img") {
		t.Fatalf("HTML part = %q", envelope.HTML)
	}
	if envelope.IdempotencyKey == "" {
		t.Fatal("delivery job has no idempotency key")
	}

	// A second pass must not resend an already-sent job.
	sentAgain, err := restarted.DispatchDue(ctx, sender, 10)
	if err != nil || sentAgain != 0 || len(sender.Envelopes) != 1 {
		t.Fatalf("second DispatchDue = %d, %v; envelopes=%d", sentAgain, err, len(sender.Envelopes))
	}
}

func TestDispatchDueRetriesTransientFailureWithoutLosingTheJob(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	ctx := context.Background()
	if _, err := service.InvitePartner(ctx, InviteCommand{ProtecteeID: protectee, InviteeEmail: "invitee@example.com"}); err != nil {
		t.Fatal(err)
	}

	failing := &mailmessage.RecordingSender{SendError: errors.New("provider unavailable")}
	if _, err := service.DispatchDue(ctx, failing, 10); err == nil {
		t.Fatal("DispatchDue hid a delivery failure")
	}
	var state DeliveryState
	var attempts int
	if err := db.QueryRow("SELECT state, attempts FROM mail_delivery_jobs").Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != DeliveryDelayed || attempts != 1 {
		t.Fatalf("after failure state=%q attempts=%d", state, attempts)
	}

	later := NewService(db, service.baseURL)
	later.now = func() time.Time { return testClock.Add(time.Hour) }
	sender := &mailmessage.RecordingSender{}
	if sent, err := later.DispatchDue(ctx, sender, 10); err != nil || sent != 1 {
		t.Fatalf("retry DispatchDue = %d, %v", sent, err)
	}
}

func TestLapsedInvitationStopsBlockingTheNextInvitation(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	ctx := context.Background()

	if _, err := service.InvitePartner(ctx, InviteCommand{ProtecteeID: protectee, InviteeEmail: "invitee@example.com"}); err != nil {
		t.Fatal(err)
	}

	// A Protectee whose invitation lapsed must not be locked out of Add a
	// Partner while waiting for the sweep that marks it expired.
	lapsed := NewService(db, service.baseURL)
	lapsed.now = func() time.Time { return testClock.Add(invitationTTL + time.Hour) }

	view, err := lapsed.ProtecteeView(ctx, protectee)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.PendingInvitations) != 0 {
		t.Fatalf("lapsed invitation still listed as pending: %+v", view.PendingInvitations)
	}
	if _, err := lapsed.InvitePartner(ctx, InviteCommand{ProtecteeID: protectee, InviteeEmail: "someone-else@example.com"}); err != nil {
		t.Fatalf("lapsed invitation still blocked a new invitation: %v", err)
	}
	// The same address may also be invited again once the first one lapsed.
	if _, err := lapsed.InvitePartner(ctx, InviteCommand{ProtecteeID: protectee, InviteeEmail: "invitee@example.com"}); !errors.Is(err, ErrOnePartnerLimit) {
		t.Fatalf("second live invitation error = %v, want the 3A gate", err)
	}
}

func TestRetryingDeliveryReportsDelayedAndClearsOnSuccess(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	ctx := context.Background()
	if _, err := service.InvitePartner(ctx, InviteCommand{ProtecteeID: protectee, InviteeEmail: "invitee@example.com"}); err != nil {
		t.Fatal(err)
	}

	failing := &mailmessage.RecordingSender{SendError: errors.New("provider unavailable")}
	if _, err := service.DispatchDue(ctx, failing, 10); err == nil {
		t.Fatal("DispatchDue hid a delivery failure")
	}
	view, err := service.ProtecteeView(ctx, protectee)
	if err != nil {
		t.Fatal(err)
	}
	if got := view.PendingInvitations[0].DeliveryState; got != DeliveryDelayed {
		t.Fatalf("delivery state after a retryable failure = %q, want %q", got, DeliveryDelayed)
	}
	if got := view.PendingInvitations[0].DeliveryState.Label(); got != "Delivery delayed" {
		t.Fatalf("delivery label = %q", got)
	}

	later := NewService(db, service.baseURL)
	later.now = func() time.Time { return testClock.Add(time.Hour) }
	if sent, err := later.DispatchDue(ctx, &mailmessage.RecordingSender{}, 10); err != nil || sent != 1 {
		t.Fatalf("retry DispatchDue = %d, %v", sent, err)
	}
	recovered, err := later.ProtecteeView(ctx, protectee)
	if err != nil {
		t.Fatal(err)
	}
	if got := recovered.PendingInvitations[0].DeliveryState; got != DeliverySent {
		t.Fatalf("delivery state after recovery = %q, want %q", got, DeliverySent)
	}
}

func TestExhaustedDeliveryReportsInvitationNotDelivered(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	ctx := context.Background()
	if _, err := service.InvitePartner(ctx, InviteCommand{ProtecteeID: protectee, InviteeEmail: "invitee@example.com"}); err != nil {
		t.Fatal(err)
	}

	failing := &mailmessage.RecordingSender{SendError: errors.New("mailbox does not exist")}
	for attempt := 1; attempt <= maxDeliveryAttempts; attempt++ {
		service.now = func() time.Time { return testClock.Add(time.Duration(attempt) * 24 * time.Hour) }
		if _, err := service.DispatchDue(ctx, failing, 10); err == nil {
			t.Fatalf("attempt %d reported success", attempt)
		}
	}
	var state DeliveryState
	if err := db.QueryRow("SELECT state FROM mail_delivery_jobs").Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != DeliveryFailed || state.Label() != "Invitation not delivered" {
		t.Fatalf("exhausted job state = %q label = %q", state, state.Label())
	}
}

func TestSentDeliveryJobNoLongerHoldsTheRawBearerToken(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	ctx := context.Background()
	invitation, err := service.InvitePartner(ctx, InviteCommand{ProtecteeID: protectee, InviteeEmail: "invitee@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DispatchDue(ctx, &mailmessage.RecordingSender{}, 10); err != nil {
		t.Fatal(err)
	}

	var bodyText, bodyHTML, subject, recipient string
	if err := db.QueryRow("SELECT body_text, body_html, subject, recipient_email FROM mail_delivery_jobs").
		Scan(&bodyText, &bodyHTML, &subject, &recipient); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bodyText, invitation.RawToken) || strings.Contains(bodyHTML, invitation.RawToken) {
		t.Fatal("a sent delivery job still stores the raw invitation token")
	}
	if subject == "" || recipient != "invitee@example.com" {
		t.Fatalf("redaction removed the audit trail: subject=%q recipient=%q", subject, recipient)
	}
}
