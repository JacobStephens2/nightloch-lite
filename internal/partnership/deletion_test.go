package partnership

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

// deletionClock is when a User deletion completes in these tests, an hour
// after everything the deletion has to preserve.
var deletionClock = testClock.Add(time.Hour)

// deleteUser completes one User's teardown at deletionClock, which is what the
// later account-deletion slice will call on its own 72-hour deadline.
func deleteUser(t *testing.T, service *Service, userID int64) UserDeletion {
	t.Helper()
	restore := service.now
	service.now = func() time.Time { return deletionClock }
	defer func() { service.now = restore }()
	deletion, err := service.DeleteUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	return deletion
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestDeletingAnActorPseudonymizesAnotherProtecteesRetainedActivity(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, partnership := firstPartnership(t, service, db, true)

	// The Partner acts on the Protectee's chronology, and only then deletes
	// their own User. What they did, and exactly when, has to survive.
	if _, err := service.EndPartnership(context.Background(), EndCommand{
		PartnershipID: partnership, PartnerID: partner, Confirmation: "END",
	}); err != nil {
		t.Fatal(err)
	}
	before, err := service.Activity(context.Background(), protectee, protectee, ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}

	deleteUser(t, service, partner)

	after, err := service.Activity(context.Background(), protectee, protectee, ActivityQuery{})
	if err != nil {
		t.Fatalf("Activity after deletion: %v", err)
	}
	if len(after.Entries) != len(before.Entries) {
		t.Fatalf("deletion changed the surviving Protectee's chronology from %d to %d entries",
			len(before.Entries), len(after.Entries))
	}
	for index, entry := range after.Entries {
		was := before.Entries[index]
		if entry.Action != was.Action || !entry.OccurredAt.Equal(was.OccurredAt) {
			t.Fatalf("entry %d changed action or instant: %+v, was %+v", index, entry, was)
		}
		if entry.ActorEmail == "partner@example.com" || entry.TargetEmail == "partner@example.com" {
			t.Fatalf("entry %d still names the deleted User: %+v", index, entry)
		}
		if was.ActorEmail == "partner@example.com" && entry.ActorEmail != DeletedUserLabel {
			t.Fatalf("entry %d lost its actor instead of pseudonymizing it: %+v", index, entry)
		}
		// An actor that was Nightloch stays Nightloch: pseudonymization must
		// not attribute Nightloch's own work to a deleted person.
		if was.ActorEmail == "" && entry.ActorEmail != "" {
			t.Fatalf("entry %d attributed Nightloch's work to %q", index, entry.ActorEmail)
		}
	}
	// The filter offers the pseudonym rather than the address it replaced.
	for _, actor := range after.Actors {
		if actor == "partner@example.com" {
			t.Fatalf("actor filter still offers the deleted User: %v", after.Actors)
		}
	}
	filtered, err := service.Activity(context.Background(), protectee, protectee,
		ActivityQuery{Actor: DeletedUserLabel})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Entries) == 0 {
		t.Fatal("no entry is attributable to the deleted User after pseudonymization")
	}
}

func TestDeletingTheProtecteeLeavesAFormerPartnerOnlyAPseudonymizedReceipt(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, partnership := firstPartnership(t, service, db, true)

	deletion := deleteUser(t, service, protectee)
	if deletion.EndedPartnerships != 1 || !deletion.DeletedAt.Equal(deletionClock) {
		t.Fatalf("deletion = %+v", deletion)
	}

	receipt, err := service.EndedPartnershipReceipt(context.Background(), partnership, partner)
	if err != nil {
		t.Fatalf("EndedPartnershipReceipt: %v", err)
	}
	if !receipt.ProtecteeDeleted || receipt.ProtecteeLabel() != DeletedUserLabel {
		t.Fatalf("receipt still identifies the Protectee: %+v", receipt)
	}
	if receipt.ProtecteeEmail != "" {
		t.Fatalf("receipt kept the deleted verified email: %q", receipt.ProtecteeEmail)
	}
	if !receipt.EndedByUserDeletion() || !receipt.EndedAt.Equal(deletionClock) {
		t.Fatalf("receipt lost the ending cause or instant: %+v", receipt)
	}
	// Nothing else about the relationship survives: no Log sharing, and not
	// even when it started.
	if !receipt.Since.IsZero() || receipt.LogSharing {
		t.Fatalf("receipt retained more than the cause and the instant: %+v", receipt)
	}

	// The former Partner reads nothing else about them anywhere.
	if _, err := service.Activity(context.Background(), protectee, partner, ActivityQuery{}); !errors.Is(err, ErrActivityUnavailable) {
		t.Fatalf("Activity for a deleted Protectee = %v, want ErrActivityUnavailable", err)
	}
	protecting, err := service.ProtectingView(context.Background(), partner)
	if err != nil {
		t.Fatal(err)
	}
	if len(protecting) != 0 {
		t.Fatalf("deleted Protectee still appears under People you protect: %+v", protecting)
	}
	if allowed, err := service.CanLoosen(context.Background(), partner, protectee); err != nil || allowed {
		t.Fatalf("former Partner kept powers over a deleted Protectee: %v, %v", allowed, err)
	}
}

func TestDeletionRemovesTheDeletedUsersEmailRosterAndBroaderActivity(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, _ := firstPartnership(t, service, db, true)
	// A second pending invitation and a second Partner give the deleted User
	// a Roster and a chronology worth withholding.
	other := createUser(t, db, "other@example.com")
	second, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "other@example.com", LogSharing: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcceptInvitation(context.Background(), ByToken(second.RawToken), other); err != nil {
		t.Fatal(err)
	}
	pending, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "invitee@example.com", LogSharing: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	deleteUser(t, service, protectee)

	if left := countRows(t, db, "SELECT COUNT(*) FROM activity_entries WHERE protectee_id = ?", protectee); left != 0 {
		t.Fatalf("the deleted User's own Activity survived: %d entries", left)
	}
	if live := countRows(t, db,
		"SELECT COUNT(*) FROM partnerships WHERE ended_at IS NULL AND (protectee_id = ? OR partner_id = ?)",
		protectee, protectee); live != 0 {
		t.Fatalf("%d Partnerships still name the deleted User as live", live)
	}
	if open := countRows(t, db,
		"SELECT COUNT(*) FROM partner_invitations WHERE state = 'pending' AND protectee_id = ?", protectee); open != 0 {
		t.Fatalf("%d invitations from the deleted User are still pending", open)
	}
	// The verified email is gone from every table a product view reads.
	for _, query := range []string{
		"SELECT COUNT(*) FROM users WHERE email = ?",
		"SELECT COUNT(*) FROM activity_entries WHERE actor_email = ? OR target_email = ?1",
		"SELECT COUNT(*) FROM partner_invitations WHERE invitee_email = ?",
		"SELECT COUNT(*) FROM activity_entries WHERE instr(lower(details), ?) > 0",
	} {
		if found := countRows(t, db, query, "protectee@example.com"); found != 0 {
			t.Fatalf("%d rows still carry the deleted verified email: %s", found, query)
		}
	}
	// Neither former Partner can reach the Roster they once read.
	for _, viewer := range []int64{partner, other} {
		if _, err := service.Activity(context.Background(), protectee, viewer, ActivityQuery{}); !errors.Is(err, ErrActivityUnavailable) {
			t.Fatalf("Activity for viewer %d = %v, want ErrActivityUnavailable", viewer, err)
		}
	}
	// The invitee learns nothing from the invitation they never answered.
	received, err := service.ReceivedInvitations(context.Background(), createUser(t, db, "invitee@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if len(received) != 0 {
		t.Fatalf("a deleted User's invitation is still offered: %+v", received)
	}
	if _, err := service.ReviewInvitation(context.Background(), ByToken(pending.RawToken), other); err == nil {
		t.Fatal("a deleted User's invitation is still reviewable")
	}
}

func TestDeletionIsIdempotentAndKeepsAnotherProtecteesAttributableEvents(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	// The deleted User is a Partner here, so every entry they appear in
	// belongs to a Protectee who survives them.
	protectee, partner, _ := firstPartnership(t, service, db, true)

	first := deleteUser(t, service, partner)
	entries := countRows(t, db, "SELECT COUNT(*) FROM activity_entries WHERE protectee_id = ?", protectee)
	jobs := countRows(t, db, "SELECT COUNT(*) FROM mail_delivery_jobs")

	second := deleteUser(t, service, partner)
	if !second.AlreadyDeleted || first.AlreadyDeleted {
		t.Fatalf("repeated deletion = %+v, first = %+v", second, first)
	}
	if !second.DeletedAt.Equal(first.DeletedAt) {
		t.Fatalf("repeated deletion moved the deletion instant to %v from %v", second.DeletedAt, first.DeletedAt)
	}
	if after := countRows(t, db, "SELECT COUNT(*) FROM activity_entries WHERE protectee_id = ?", protectee); after != entries {
		t.Fatalf("repeated deletion changed the surviving chronology from %d to %d", entries, after)
	}
	if after := countRows(t, db, "SELECT COUNT(*) FROM mail_delivery_jobs"); after != jobs {
		t.Fatalf("repeated deletion queued mail again: %d jobs, was %d", after, jobs)
	}
	if entries == 0 {
		t.Fatal("the surviving Protectee lost every attributable event")
	}
	if _, err := service.DeleteUser(context.Background(), 0); !errors.Is(err, ErrUserUnavailable) {
		t.Fatalf("DeleteUser(0) = %v, want ErrUserUnavailable", err)
	}
	if _, err := service.DeleteUser(context.Background(), partner+900); !errors.Is(err, ErrUserUnavailable) {
		t.Fatal("an unknown User id was accepted as a deletion")
	}
}

func TestDeletionLeavesAuthorizationIntactForEveryOtherPartnership(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	// Two Partners protect one Protectee, one of them reciprocally, and a
	// third Partnership between the same two Users has already ended and been
	// formed again. Deleting an unrelated Partner may change none of it.
	protectee, keeper, _ := firstPartnership(t, service, db, true)
	leaving := createUser(t, db, "leaving@example.com")
	second, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "leaving@example.com", LogSharing: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcceptInvitation(context.Background(), ByToken(second.RawToken), leaving); err != nil {
		t.Fatal(err)
	}
	// The keeper is also a Protectee whom the original Protectee protects.
	reciprocal, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: keeper, InviteeEmail: "protectee@example.com", LogSharing: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcceptInvitation(context.Background(), ByToken(reciprocal.RawToken), protectee); err != nil {
		t.Fatal(err)
	}

	deleteUser(t, service, leaving)

	for _, authorized := range []struct {
		name             string
		actor, protectee int64
		want             bool
	}{
		{"the surviving Partner still loosens", keeper, protectee, true},
		{"the reciprocal direction is untouched", protectee, keeper, true},
		{"the deleted Partner loosens nothing", leaving, protectee, false},
		{"the Protectee still cannot loosen their own", protectee, protectee, false},
	} {
		allowed, err := service.CanLoosen(context.Background(), authorized.actor, authorized.protectee)
		if err != nil || allowed != authorized.want {
			t.Fatalf("%s: CanLoosen = %v, %v; want %v", authorized.name, allowed, err, authorized.want)
		}
	}
	logs, err := service.CanViewLogs(context.Background(), keeper, protectee)
	if err != nil || !logs {
		t.Fatalf("Log sharing changed for an unrelated Partner: %v, %v", logs, err)
	}
	view, err := service.ProtecteeView(context.Background(), protectee)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Partners) != 1 || view.Partners[0].PartnerEmail != "partner@example.com" {
		t.Fatalf("Roster after an unrelated deletion = %+v", view.Partners)
	}
	// A Partner deleted mid-relationship is a Partnership ending, so the
	// Protectee reads it in their own chronology.
	activity, err := service.Activity(context.Background(), protectee, protectee, ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	newest := activity.Entries[0]
	if newest.Action != ActionPartnershipEndedByUserDeletion || !newest.OccurredAt.Equal(deletionClock) {
		t.Fatalf("newest entry after a Partner's deletion = %+v", newest)
	}
	if !strings.Contains(newest.Summary(), DeletedUserLabel) || strings.Contains(newest.Summary(), "leaving@example.com") {
		t.Fatalf("ending prose = %q", newest.Summary())
	}
	if newest.Category() != CategoryPartnerships {
		t.Fatalf("ending category = %q", newest.Category())
	}
}

func TestDeletionTreatsAlreadyDeliveredMailAsExternalHistory(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, _ := firstPartnership(t, service, db, true)

	// The invitation Nightloch already handed to the provider named the
	// Protectee, and no deletion can reach into a delivered message.
	sent := countRows(t, db,
		"SELECT COUNT(*) FROM mail_delivery_jobs WHERE instr(body_text, ?) > 0", "protectee@example.com")
	if sent == 0 {
		t.Fatal("no earlier message named the Protectee")
	}
	if _, err := db.Exec("UPDATE mail_delivery_jobs SET state = 'sent', sent_at = ?", testClock.Unix()); err != nil {
		t.Fatal(err)
	}

	deleteUser(t, service, protectee)

	if after := countRows(t, db,
		"SELECT COUNT(*) FROM mail_delivery_jobs WHERE instr(body_text, ?) > 0", "protectee@example.com"); after < sent {
		t.Fatalf("deletion rewrote or removed %d already-delivered messages", sent-after)
	}
	// The completion signal reaches the Partner who still protected them, and
	// names the verified email that identified the User when it happened.
	var subject, body, recipient string
	if err := db.QueryRow(`
		SELECT recipient_email, subject, body_text FROM mail_delivery_jobs
		WHERE message_kind = ? AND state = 'queued'`, MessageUserDeleted).
		Scan(&recipient, &subject, &body); err != nil {
		t.Fatalf("completion signal: %v", err)
	}
	if recipient != "partner@example.com" || !strings.Contains(subject, "protectee@example.com") {
		t.Fatalf("completion signal to %q with subject %q", recipient, subject)
	}
	if !strings.Contains(body, "Deleted User") || !strings.Contains(body, service.receiptURL(1)) {
		t.Fatalf("completion body = %q", body)
	}

	_ = partner
}

func TestDeletionLeavesTheDurableDeliveryLedgerAlone(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, _, _ := firstPartnership(t, service, db, true)

	// Every message queued before the deletion reported an event that really
	// happened while the User existed. Those rows are the durable
	// per-recipient ledger (ADR 0002), so the teardown neither prunes them nor
	// rewrites what they say.
	addressed := countRows(t, db,
		"SELECT COUNT(*) FROM mail_delivery_jobs WHERE recipient_email = ?", "protectee@example.com")
	if addressed == 0 {
		t.Fatal("no message was queued to the Protectee before their deletion")
	}
	total := countRows(t, db, "SELECT COUNT(*) FROM mail_delivery_jobs")

	deleteUser(t, service, protectee)

	if after := countRows(t, db,
		"SELECT COUNT(*) FROM mail_delivery_jobs WHERE recipient_email = ?",
		"protectee@example.com"); after != addressed {
		t.Fatalf("the teardown pruned %d messages from the delivery ledger", addressed-after)
	}
	// It only ever adds: the completion signal to the Partner who still
	// protected them.
	if after := countRows(t, db, "SELECT COUNT(*) FROM mail_delivery_jobs"); after != total+1 {
		t.Fatalf("delivery ledger holds %d jobs, want %d", after, total+1)
	}
}

func TestAReceiptKeepsItsOwnEndingCauseWhenTheProtecteeIsDeletedLater(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, partnership := firstPartnership(t, service, db, true)

	// The Partner ends the Partnership first. The Protectee's later deletion
	// pseudonymizes who the receipt is about; it does not rewrite how the
	// Partnership actually ended.
	if _, err := service.EndPartnership(context.Background(), EndCommand{
		PartnershipID: partnership, PartnerID: partner, Confirmation: "END",
	}); err != nil {
		t.Fatal(err)
	}
	deleteUser(t, service, protectee)

	receipt, err := service.EndedPartnershipReceipt(context.Background(), partnership, partner)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.ProtecteeDeleted || receipt.ProtecteeLabel() != DeletedUserLabel {
		t.Fatalf("receipt still identifies the Protectee: %+v", receipt)
	}
	if receipt.EndedByUserDeletion() || !receipt.PartnerEnded() {
		t.Fatalf("the later deletion rewrote the ending cause: %+v", receipt)
	}
	if !receipt.EndedAt.Equal(testClock) {
		t.Fatalf("the later deletion moved the ending instant to %v", receipt.EndedAt)
	}
}
