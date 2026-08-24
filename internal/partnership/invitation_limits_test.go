package partnership

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTimedService is the deterministic clock every limit test runs on. Rolling
// windows and expiry are the whole subject here, so the tests move time by
// hand rather than sleeping.
func newTimedService(t *testing.T) (*Service, *sql.DB, func(time.Duration)) {
	t.Helper()
	service, db := newTestService(t)
	now := testClock
	service.now = func() time.Time { return now }
	return service, db, func(step time.Duration) { now = now.Add(step) }
}

// inviteAddress is one distinct invitable address per index, so a test can ask
// for more invitations than any limit allows without repeating an address and
// tripping the duplicate rule instead.
func inviteAddress(index int) string {
	return "invitee" + string(rune('a'+index)) + "@example.com"
}

// cancelInvite withdraws one invitation, failing the test on refusal.
func cancelInvite(t *testing.T, service *Service, invitationID, protecteeID int64) {
	t.Helper()
	if _, err := service.CancelInvitation(context.Background(), invitationID, protecteeID); err != nil {
		t.Fatalf("CancelInvitation: %v", err)
	}
}

// pendingCount is how many invitations the Protectee's own view still lists.
func pendingCount(t *testing.T, service *Service, protecteeID int64) int {
	t.Helper()
	view, err := service.ProtecteeView(context.Background(), protecteeID)
	if err != nil {
		t.Fatalf("ProtecteeView: %v", err)
	}
	return len(view.PendingInvitations)
}

func TestInvitePartnerAcceptsFivePendingInvitationsAndRefusesTheSixth(t *testing.T) {
	t.Parallel()
	service, db, _ := newTimedService(t)
	protectee := createUser(t, db, "protectee@example.com")

	for index := range maxPendingInvitations {
		invite(t, service, protectee, inviteAddress(index), false)
	}

	_, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: inviteAddress(maxPendingInvitations),
	})
	if !errors.Is(err, ErrPendingInvitationLimit) {
		t.Fatalf("sixth invitation error = %v, want ErrPendingInvitationLimit", err)
	}
	if !IsRefusal(err) {
		t.Fatal("the pending-invitation limit is a refusal the Protectee can correct")
	}
	// The refusal names the limit and the remedy, never another address.
	if strings.Contains(err.Error(), "@") {
		t.Fatalf("refusal enumerates an address: %q", err)
	}
	if got := pendingCount(t, service, protectee); got != maxPendingInvitations {
		t.Fatalf("pending invitations = %d, want %d", got, maxPendingInvitations)
	}
}

func TestPendingInvitationLimitCountsOnlyLiveInvitations(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	protectee := createUser(t, db, "protectee@example.com")

	var sent []Invitation
	for index := range maxPendingInvitations {
		sent = append(sent, invite(t, service, protectee, inviteAddress(index), false))
	}

	// A canceled invitation frees its place immediately.
	cancelInvite(t, service, sent[0].ID, protectee)
	replacement := invite(t, service, protectee, inviteAddress(maxPendingInvitations), false)
	if replacement.ID == 0 {
		t.Fatal("cancelling a pending invitation did not free a place")
	}

	// So does a lapsed one, before the sweep records it.
	advance(invitationTTL + time.Minute)
	if got := pendingCount(t, service, protectee); got != 0 {
		t.Fatalf("pending invitations after expiry = %d, want 0", got)
	}
	if _, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: inviteAddress(0),
	}); err != nil {
		t.Fatalf("InvitePartner after expiry: %v", err)
	}
}

// spendSendQuota consumes count send operations without leaving pending
// invitations behind, so a test can approach the rolling window's limit
// without also reaching the pending-invitation limit.
func spendSendQuota(t *testing.T, service *Service, protecteeID int64, count int) {
	t.Helper()
	for index := range count {
		sent := invite(t, service, protecteeID, inviteAddress(index), false)
		cancelInvite(t, service, sent.ID, protecteeID)
	}
}

func TestSendQuotaAcceptsTenOperationsPerRollingDay(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	protectee := createUser(t, db, "protectee@example.com")

	spendSendQuota(t, service, protectee, maxSendsPerWindow)

	_, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: inviteAddress(0),
	})
	if !errors.Is(err, ErrSendQuotaExhausted) {
		t.Fatalf("eleventh send error = %v, want ErrSendQuotaExhausted", err)
	}
	if !IsRefusal(err) {
		t.Fatal("the send quota is a refusal the Protectee can wait out")
	}

	// One second short of the window still refuses; crossing it admits the
	// oldest send's replacement.
	advance(sendWindow - time.Second)
	if _, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: inviteAddress(0),
	}); !errors.Is(err, ErrSendQuotaExhausted) {
		t.Fatalf("send one second before the window resets = %v, want ErrSendQuotaExhausted", err)
	}
	advance(time.Second)
	if _, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: inviteAddress(0),
	}); err != nil {
		t.Fatalf("InvitePartner once the window rolled: %v", err)
	}
}

func TestSendQuotaIsScopedToOneProtectee(t *testing.T) {
	t.Parallel()
	service, db, _ := newTimedService(t)
	exhausted := createUser(t, db, "exhausted@example.com")
	other := createUser(t, db, "other@example.com")
	spendSendQuota(t, service, exhausted, maxSendsPerWindow)

	if _, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: other, InviteeEmail: inviteAddress(0),
	}); err != nil {
		t.Fatalf("another Protectee's first invitation: %v", err)
	}
}

func TestResendRepeatsTheInvitationToTheInviteeAlone(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")
	acceptFor(t, service, invite(t, service, protectee, "partner@example.com", false), partner)
	sent := invite(t, service, protectee, "invitee@example.com", true)
	before := queuedRecipients(t, service)

	advance(resendInterval)
	resent, err := service.ResendInvitation(context.Background(), sent.ID, protectee)
	if err != nil {
		t.Fatalf("ResendInvitation: %v", err)
	}
	if resent.InviteeEmail != "invitee@example.com" {
		t.Fatalf("resend receipt = %+v", resent)
	}
	// The seven-day window belongs to the original invitation and never moves.
	if !resent.ExpiresAt.Equal(sent.ExpiresAt) {
		t.Fatalf("resend expiry = %v, want the original %v", resent.ExpiresAt, sent.ExpiresAt)
	}

	added := queuedRecipients(t, service)[len(before):]
	want := []string{MessagePartnerInvitationResent + " -> invitee@example.com"}
	if len(added) != 1 || added[0] != want[0] {
		t.Fatalf("resend queued %v, want exactly %v", added, want)
	}

	var storedExpiry int64
	var sendCount int
	if err := db.QueryRow("SELECT expires_at, send_count FROM partner_invitations WHERE id = ?", sent.ID).
		Scan(&storedExpiry, &sendCount); err != nil {
		t.Fatal(err)
	}
	if storedExpiry != sent.ExpiresAt.Unix() || sendCount != 2 {
		t.Fatalf("stored expiry=%d send_count=%d, want %d/2", storedExpiry, sendCount, sent.ExpiresAt.Unix())
	}

	var activities int
	if err := db.QueryRow("SELECT COUNT(*) FROM activity_entries WHERE action = ?",
		ActionInvitationResent).Scan(&activities); err != nil {
		t.Fatal(err)
	}
	if activities != 1 {
		t.Fatalf("resend Activity entries = %d, want 1", activities)
	}
}

func TestResendRefusesASecondSendWithinTwentyFourHours(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	protectee := createUser(t, db, "protectee@example.com")
	sent := invite(t, service, protectee, "invitee@example.com", false)

	// The original send starts the interval, so a resend is not due yet.
	advance(resendInterval - time.Second)
	_, err := service.ResendInvitation(context.Background(), sent.ID, protectee)
	if !errors.Is(err, ErrResendTooSoon) {
		t.Fatalf("resend one second early = %v, want ErrResendTooSoon", err)
	}
	if !IsRefusal(err) {
		t.Fatal("resending too soon is a refusal the Protectee can wait out")
	}

	advance(time.Second)
	if _, err := service.ResendInvitation(context.Background(), sent.ID, protectee); err != nil {
		t.Fatalf("ResendInvitation once due: %v", err)
	}
	queued := len(queuedRecipients(t, service))

	// A repeat of the same operation adds no second provider send.
	if _, err := service.ResendInvitation(context.Background(), sent.ID, protectee); !errors.Is(err, ErrResendTooSoon) {
		t.Fatalf("immediate repeat resend = %v, want ErrResendTooSoon", err)
	}
	if got := len(queuedRecipients(t, service)); got != queued {
		t.Fatalf("delivery jobs = %d after a refused resend, want %d", got, queued)
	}

	advance(resendInterval)
	if _, err := service.ResendInvitation(context.Background(), sent.ID, protectee); err != nil {
		t.Fatalf("ResendInvitation a day later: %v", err)
	}
	var sendCount int
	if err := db.QueryRow("SELECT send_count FROM partner_invitations WHERE id = ?", sent.ID).
		Scan(&sendCount); err != nil {
		t.Fatal(err)
	}
	if sendCount != 3 {
		t.Fatalf("send_count = %d, want 3", sendCount)
	}
}

func TestResendConsumesTheSameRollingSendQuota(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	protectee := createUser(t, db, "protectee@example.com")
	sent := invite(t, service, protectee, "invitee@example.com", false)

	// A day later the original send has rolled out of the window. Spend nine
	// of the ten operations it now has, so the resend is the tenth: it is
	// admitted, and it is what leaves nothing for an eleventh.
	advance(resendInterval)
	spendSendQuota(t, service, protectee, maxSendsPerWindow-1)
	if _, err := service.ResendInvitation(context.Background(), sent.ID, protectee); err != nil {
		t.Fatalf("ResendInvitation at the quota edge: %v", err)
	}
	if _, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "another@example.com",
	}); !errors.Is(err, ErrSendQuotaExhausted) {
		t.Fatalf("send after a quota-consuming resend = %v, want ErrSendQuotaExhausted", err)
	}
}

func TestResendRefusesForAnyoneButTheInvitingProtectee(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	protectee := createUser(t, db, "protectee@example.com")
	stranger := createUser(t, db, "stranger@example.com")
	sent := invite(t, service, protectee, "invitee@example.com", false)
	advance(resendInterval)

	if _, err := service.ResendInvitation(context.Background(), sent.ID, stranger); !errors.Is(err, ErrInvitationUnavailable) {
		t.Fatalf("stranger resend = %v, want ErrInvitationUnavailable", err)
	}
	cancelInvite(t, service, sent.ID, protectee)
	if _, err := service.ResendInvitation(context.Background(), sent.ID, protectee); !errors.Is(err, ErrInvitationUnavailable) {
		t.Fatalf("resend of a canceled invitation = %v, want ErrInvitationUnavailable", err)
	}
}

func TestResendReplacesTheBearerLinkWithoutChangingTheInvitation(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	protectee := createUser(t, db, "protectee@example.com")
	invitee := createUser(t, db, "invitee@example.com")
	sent := invite(t, service, protectee, "invitee@example.com", true)

	advance(resendInterval)
	resent, err := service.ResendInvitation(context.Background(), sent.ID, protectee)
	if err != nil {
		t.Fatalf("ResendInvitation: %v", err)
	}
	if resent.RawToken == "" || resent.RawToken == sent.RawToken {
		t.Fatal("resend did not mint a fresh bearer link")
	}
	if _, err := service.ReviewInvitation(context.Background(), ByToken(sent.RawToken), invitee); !errors.Is(err, ErrInvitationUnavailable) {
		t.Fatalf("superseded link = %v, want ErrInvitationUnavailable", err)
	}
	review, err := service.ReviewInvitation(context.Background(), ByToken(resent.RawToken), invitee)
	if err != nil {
		t.Fatalf("ReviewInvitation with the fresh link: %v", err)
	}
	// The invitation itself is immutable: same invitee, same proposed Log
	// sharing, same expiry (S3-66).
	if review.InviteeEmail != "invitee@example.com" || !review.LogSharing || !review.ExpiresAt.Equal(sent.ExpiresAt) {
		t.Fatalf("resend changed the invitation: %+v", review)
	}
}

func TestPendingInvitationRowReportsTheResendWithoutMovingExpiry(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	protectee := createUser(t, db, "protectee@example.com")
	sent := invite(t, service, protectee, "invitee@example.com", true)

	row := pendingRow(t, service, protectee, sent.ID)
	if !row.SentAt.Equal(sent.SentAt) || !row.ExpiresAt.Equal(sent.ExpiresAt) {
		t.Fatalf("pending row times = %+v", row)
	}
	if row.Resends != 0 || !row.LastSentAt.Equal(sent.SentAt) {
		t.Fatalf("unsent-again row reports a resend: %+v", row)
	}
	if want := sent.SentAt.Add(resendInterval); !row.ResendDueAt.Equal(want) {
		t.Fatalf("resend due at %v, want %v", row.ResendDueAt, want)
	}

	advance(resendInterval)
	if _, err := service.ResendInvitation(context.Background(), sent.ID, protectee); err != nil {
		t.Fatalf("ResendInvitation: %v", err)
	}

	row = pendingRow(t, service, protectee, sent.ID)
	if !row.SentAt.Equal(sent.SentAt) || !row.ExpiresAt.Equal(sent.ExpiresAt) {
		t.Fatalf("resend moved the row's own times: %+v", row)
	}
	if row.Resends != 1 || !row.LastSentAt.Equal(sent.SentAt.Add(resendInterval)) {
		t.Fatalf("pending row does not report the resend: %+v", row)
	}
	if row.DeliveryState != DeliveryQueued {
		t.Fatalf("delivery state = %q, want the resent job's own state", row.DeliveryState)
	}
}

func pendingRow(t *testing.T, service *Service, protecteeID, invitationID int64) PendingInvitation {
	t.Helper()
	view, err := service.ProtecteeView(context.Background(), protecteeID)
	if err != nil {
		t.Fatalf("ProtecteeView: %v", err)
	}
	for _, pending := range view.PendingInvitations {
		if pending.ID == invitationID {
			return pending
		}
	}
	t.Fatalf("invitation %d is not pending: %+v", invitationID, view.PendingInvitations)
	return PendingInvitation{}
}

// TestConcurrentSendsConsumeThePendingLimitExactlyOnce and its quota twin
// prove the limits are enforced by the committing transaction rather than by a
// read that a racing send could have invalidated.
func TestConcurrentSendsConsumeThePendingLimitExactlyOnce(t *testing.T) {
	t.Parallel()
	service, db, _ := newTimedService(t)
	protectee := createUser(t, db, "protectee@example.com")

	accepted, refusals := raceInvitations(t, service, protectee, maxPendingInvitations+3)
	if accepted != maxPendingInvitations {
		t.Fatalf("accepted %d concurrent invitations, want %d", accepted, maxPendingInvitations)
	}
	for _, err := range refusals {
		if !errors.Is(err, ErrPendingInvitationLimit) {
			t.Fatalf("concurrent refusal = %v, want ErrPendingInvitationLimit", err)
		}
	}
	if got := pendingCount(t, service, protectee); got != maxPendingInvitations {
		t.Fatalf("pending invitations = %d, want %d", got, maxPendingInvitations)
	}
}

func TestConcurrentSendsConsumeTheRollingQuotaExactlyOnce(t *testing.T) {
	t.Parallel()
	service, db, _ := newTimedService(t)
	protectee := createUser(t, db, "protectee@example.com")
	// Leave two operations in the window and no pending invitations, so the
	// quota is the only limit the racing sends can reach.
	spendSendQuota(t, service, protectee, maxSendsPerWindow-2)

	accepted, refusals := raceInvitations(t, service, protectee, 6)
	if accepted != 2 {
		t.Fatalf("accepted %d concurrent invitations, want 2", accepted)
	}
	for _, err := range refusals {
		if !errors.Is(err, ErrSendQuotaExhausted) {
			t.Fatalf("concurrent refusal = %v, want ErrSendQuotaExhausted", err)
		}
	}
	var sends int
	if err := db.QueryRow("SELECT COUNT(*) FROM partner_invitation_sends WHERE protectee_id = ?",
		protectee).Scan(&sends); err != nil {
		t.Fatal(err)
	}
	if sends != maxSendsPerWindow {
		t.Fatalf("recorded sends = %d, want %d", sends, maxSendsPerWindow)
	}
}

// TestConcurrentResendsOfOneInvitationSendOnce is the idempotency this
// operation actually has to hold: a Protectee who submits the same resend
// twice — a double click, a retried request — causes one email, not two. The
// compare-and-set in recordSend is what makes the loser stand down.
func TestConcurrentResendsOfOneInvitationSendOnce(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	protectee := createUser(t, db, "protectee@example.com")
	sent := invite(t, service, protectee, "invitee@example.com", false)
	advance(resendInterval)

	const attempts = 4
	var group sync.WaitGroup
	results := make([]error, attempts)
	for index := range attempts {
		group.Add(1)
		go func() {
			defer group.Done()
			_, results[index] = service.ResendInvitation(context.Background(), sent.ID, protectee)
		}()
	}
	group.Wait()

	accepted := 0
	for _, err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrResendTooSoon), errors.Is(err, ErrInvitationUnavailable):
			// Whichever guard the loser reached first; neither sends.
		default:
			t.Fatalf("concurrent resend failed: %v", err)
		}
	}
	if accepted != 1 {
		t.Fatalf("%d concurrent resends were accepted, want exactly 1", accepted)
	}

	var reminders, sendCount, sends int
	row := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM mail_delivery_jobs WHERE message_kind = ?),
		(SELECT send_count FROM partner_invitations WHERE id = ?),
		(SELECT COUNT(*) FROM partner_invitation_sends WHERE protectee_id = ?)`,
		MessagePartnerInvitationResent, sent.ID, protectee)
	if err := row.Scan(&reminders, &sendCount, &sends); err != nil {
		t.Fatal(err)
	}
	if reminders != 1 || sendCount != 2 || sends != 2 {
		t.Fatalf("reminders=%d send_count=%d recorded sends=%d, want 1/2/2", reminders, sendCount, sends)
	}
}

// raceInvitations sends count distinct invitations at once and reports how
// many were accepted alongside every refusal.
func raceInvitations(t *testing.T, service *Service, protecteeID int64, count int) (int, []error) {
	t.Helper()
	var group sync.WaitGroup
	results := make([]error, count)
	for index := range count {
		group.Add(1)
		go func() {
			defer group.Done()
			_, results[index] = service.InvitePartner(context.Background(), InviteCommand{
				ProtecteeID: protecteeID, InviteeEmail: "race" + string(rune('a'+index)) + "@example.com",
			})
		}()
	}
	group.Wait()

	accepted := 0
	var refusals []error
	for _, err := range results {
		switch {
		case err == nil:
			accepted++
		case IsRefusal(err):
			refusals = append(refusals, err)
		default:
			t.Fatalf("concurrent invitation failed: %v", err)
		}
	}
	return accepted, refusals
}
