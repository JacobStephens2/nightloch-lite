package partnership

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	mailmessage "github.com/JacobStephens2/nightloch-lite/internal/mail"
)

// firstPartnership drives an invitation through acceptance so an ending test
// starts from the same live Partnership the product does.
func firstPartnership(t *testing.T, service *Service, db *sql.DB, logSharing bool) (protectee, partner, partnership int64) {
	t.Helper()
	protectee = createUser(t, db, "protectee@example.com")
	invitation, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "partner@example.com", LogSharing: logSharing,
	})
	if err != nil {
		t.Fatal(err)
	}
	partner = createUser(t, db, "partner@example.com")
	accepted, err := service.AcceptInvitation(context.Background(), ByToken(invitation.RawToken), partner)
	if err != nil {
		t.Fatal(err)
	}
	return protectee, partner, accepted.PartnershipID
}

func TestEndPartnershipRequiresTheTypedConfirmationFromThatPartnerAlone(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, partnership := firstPartnership(t, service, db, true)
	stranger := createUser(t, db, "stranger@example.com")

	for _, refusal := range []struct {
		name    string
		command EndCommand
		want    error
	}{
		{"no confirmation", EndCommand{PartnershipID: partnership, PartnerID: partner}, ErrEndConfirmationRequired},
		{"wrong word", EndCommand{PartnershipID: partnership, PartnerID: partner, Confirmation: "end it"}, ErrEndConfirmationRequired},
		{"the Protectee", EndCommand{PartnershipID: partnership, PartnerID: protectee, Confirmation: "END"}, ErrPartnershipUnavailable},
		{"another User", EndCommand{PartnershipID: partnership, PartnerID: stranger, Confirmation: "END"}, ErrPartnershipUnavailable},
		// Authorization is checked first, so an unauthorized caller never
		// learns that their confirmation was the only thing missing.
		{"another User with no confirmation", EndCommand{PartnershipID: partnership, PartnerID: stranger}, ErrPartnershipUnavailable},
		{"unknown Partnership", EndCommand{PartnershipID: partnership + 99, PartnerID: partner, Confirmation: "END"}, ErrPartnershipUnavailable},
	} {
		if _, err := service.EndPartnership(context.Background(), refusal.command); !errors.Is(err, refusal.want) {
			t.Fatalf("%s: EndPartnership = %v, want %v", refusal.name, err, refusal.want)
		}
	}

	// None of those refusals may have touched the live Partnership.
	var live int
	if err := db.QueryRow("SELECT COUNT(*) FROM partnerships WHERE ended_at IS NULL").Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatalf("refused endings left %d live Partnerships", live)
	}
	if allowed, err := service.CanLoosen(context.Background(), partner, protectee); err != nil || !allowed {
		t.Fatalf("refused ending removed Partner powers: %v, %v", allowed, err)
	}

	// Only the Partner on the Partnership may end it, and their preview names
	// the same consequences the confirmation screen must show.
	preview, err := service.EndablePartnership(context.Background(), partnership, partner)
	if err != nil {
		t.Fatalf("EndablePartnership: %v", err)
	}
	if preview.ProtecteeEmail != "protectee@example.com" || preview.RemainingPartners != 0 || !preview.LogSharing {
		t.Fatalf("ending preview = %+v", preview)
	}
	if _, err := service.EndablePartnership(context.Background(), partnership, stranger); !errors.Is(err, ErrPartnershipUnavailable) {
		t.Fatalf("stranger preview = %v, want ErrPartnershipUnavailable", err)
	}
}

func TestEndPartnershipRemovesPowersRestoresZeroStateAndQueuesBothMessages(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, partnership := firstPartnership(t, service, db, true)

	ending, err := service.EndPartnership(context.Background(), EndCommand{
		PartnershipID: partnership, PartnerID: partner, Confirmation: " END ",
	})
	if err != nil {
		t.Fatalf("EndPartnership: %v", err)
	}
	if ending.ProtecteeEmail != "protectee@example.com" || !ending.EndedAt.Equal(testClock) || ending.RemainingPartners != 0 {
		t.Fatalf("ending = %+v", ending)
	}

	var endedAt int64
	var reason string
	var activity int
	if err := db.QueryRow(`SELECT
		(SELECT COALESCE(ended_at, 0) FROM partnerships),
		(SELECT COALESCE(ended_reason, '') FROM partnerships),
		(SELECT COUNT(*) FROM activity_entries WHERE action = ?)`, ActionPartnershipEndedByPartner).
		Scan(&endedAt, &reason, &activity); err != nil {
		t.Fatal(err)
	}
	if endedAt != testClock.Unix() || reason != EndReasonPartnerEnded || activity != 1 {
		t.Fatalf("ended row endedAt=%d reason=%q activity=%d", endedAt, reason, activity)
	}

	// Partner powers, Roster, and log access all go at the same instant.
	for _, check := range []struct {
		name string
		got  func() (bool, error)
		want bool
	}{
		{"former Partner may not loosen", func() (bool, error) { return service.CanLoosen(context.Background(), partner, protectee) }, false},
		{"Protectee loosens their own Policy again", func() (bool, error) { return service.CanLoosen(context.Background(), protectee, protectee) }, true},
		{"former Partner loses log access", func() (bool, error) { return service.CanViewLogs(context.Background(), partner, protectee) }, false},
	} {
		got, err := check.got()
		if err != nil || got != check.want {
			t.Fatalf("%s = %v, %v; want %v", check.name, got, err, check.want)
		}
	}
	protecting, err := service.ProtectingView(context.Background(), partner)
	if err != nil || len(protecting) != 0 {
		t.Fatalf("People you protect after ending = %+v, %v", protecting, err)
	}
	view, err := service.ProtecteeView(context.Background(), protectee)
	if err != nil || view.PartnerLockActive || len(view.Partners) != 0 {
		t.Fatalf("Protectee view after ending = %+v, %v", view, err)
	}

	// A replayed end wins nothing and duplicates neither Activity nor mail.
	if _, err := service.EndPartnership(context.Background(), EndCommand{
		PartnershipID: partnership, PartnerID: partner, Confirmation: "END",
	}); !errors.Is(err, ErrPartnershipUnavailable) {
		t.Fatalf("replayed ending = %v, want ErrPartnershipUnavailable", err)
	}
	var endings, jobs int
	if err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM activity_entries WHERE action = ?),
		(SELECT COUNT(*) FROM mail_delivery_jobs WHERE message_kind = ?)`,
		ActionPartnershipEndedByPartner, MessagePartnershipEnded).Scan(&endings, &jobs); err != nil {
		t.Fatal(err)
	}
	if endings != 1 || jobs != 2 {
		t.Fatalf("replayed ending duplicated work: activity=%d jobs=%d", endings, jobs)
	}

	sender := &mailmessage.RecordingSender{}
	if sent, err := service.DispatchDue(context.Background(), sender, 10); err != nil || sent != 5 {
		t.Fatalf("DispatchDue = %d, %v", sent, err)
	}
	ended := map[string]mailmessage.Envelope{}
	for _, envelope := range sender.Envelopes {
		if strings.Contains(envelope.Subject, "ended their Partnership") {
			if _, duplicate := ended[envelope.To]; duplicate {
				t.Fatalf("duplicate ending message to %s", envelope.To)
			}
			ended[envelope.To] = envelope
		}
	}
	if len(ended) != 2 {
		t.Fatalf("ending recipients = %v", ended)
	}
	for recipient, envelope := range ended {
		if envelope.Subject != "[Nightloch] partner@example.com ended their Partnership with protectee@example.com" {
			t.Fatalf("%s subject = %q", recipient, envelope.Subject)
		}
		for _, want := range []string{
			"partner@example.com ended the Partnership immediately.",
			"The Partnership ended on 2026-08-23 15:04 UTC.",
			"protectee@example.com now has 0 active Partner(s).",
			"The Partner lock is now inactive. protectee@example.com can loosen their own Policy.",
			"They retain only a read-only receipt for this ended",
			"alerts@nightloch.app",
		} {
			if !strings.Contains(envelope.Text, want) {
				t.Fatalf("%s message missing %q:\n%s", recipient, want, envelope.Text)
			}
		}
	}

	// The closing link names what each recipient actually retains. Offering
	// the Roster to the Partner who just lost it would be a false invitation.
	receiptLink := fmt.Sprintf("https://lite.nightloch.app/partners/ended-partnerships/%d", partnership)
	protecteeText, partnerText := ended["protectee@example.com"].Text, ended["partner@example.com"].Text
	if !strings.Contains(protecteeText, "Review the Roster and Activity:\nhttps://lite.nightloch.app/") ||
		strings.Contains(protecteeText, receiptLink) {
		t.Fatalf("Protectee closing link = %q", protecteeText)
	}
	if !strings.Contains(partnerText, "You keep a read-only receipt for this ended Partnership:\n"+receiptLink) {
		t.Fatalf("former Partner closing link = %q", partnerText)
	}
	if strings.Contains(partnerText, "Review the Roster and Activity") {
		t.Fatalf("the former Partner was offered the Roster they just lost:\n%s", partnerText)
	}
	if !strings.Contains(ended["partner@example.com"].HTML,
		fmt.Sprintf(`<a href="%s">You keep a read-only receipt for this ended Partnership</a>`, receiptLink)) {
		t.Fatalf("former Partner HTML part = %q", ended["partner@example.com"].HTML)
	}
}

func TestEndedPartnershipLeavesTheFormerPartnerOnlyTheirOwnReceipt(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, partnership := firstPartnership(t, service, db, true)
	stranger := createUser(t, db, "stranger@example.com")

	// While the Partnership is live there is no receipt to read.
	if _, err := service.EndedPartnershipReceipt(context.Background(), partnership, partner); !errors.Is(err, ErrPartnershipUnavailable) {
		t.Fatalf("receipt for a live Partnership = %v", err)
	}
	if _, err := service.EndPartnership(context.Background(), EndCommand{
		PartnershipID: partnership, PartnerID: partner, Confirmation: "END",
	}); err != nil {
		t.Fatal(err)
	}

	receipt, err := service.EndedPartnershipReceipt(context.Background(), partnership, partner)
	if err != nil {
		t.Fatalf("EndedPartnershipReceipt: %v", err)
	}
	if receipt.ProtecteeEmail != "protectee@example.com" || !receipt.EndedAt.Equal(testClock) ||
		!receipt.LogSharing || receipt.Since.IsZero() {
		t.Fatalf("receipt = %+v", receipt)
	}
	receipts, err := service.EndedPartnerships(context.Background(), partner)
	if err != nil || len(receipts) != 1 || receipts[0].PartnershipID != partnership {
		t.Fatalf("EndedPartnerships = %+v, %v", receipts, err)
	}

	// The receipt belongs to the former Partner alone.
	for _, viewer := range []struct {
		name string
		id   int64
	}{{"the Protectee", protectee}, {"another User", stranger}, {"nobody", 0}} {
		if _, err := service.EndedPartnershipReceipt(context.Background(), partnership, viewer.id); !errors.Is(err, ErrPartnershipUnavailable) {
			t.Fatalf("receipt for %s = %v, want ErrPartnershipUnavailable", viewer.name, err)
		}
		if others, err := service.EndedPartnerships(context.Background(), viewer.id); err != nil || len(others) != 0 {
			t.Fatalf("EndedPartnerships for %s = %+v, %v", viewer.name, others, err)
		}
	}
}

// addPartnership writes a second live Partnership directly. The 3A exposure
// gate refuses one through the invitation path, but the schema, the ending
// rules, and the Roster recipients are already many-to-many, so this is how a
// 3A test reaches the Roster behaviour 3B will open up.
func addPartnership(t *testing.T, db *sql.DB, protectee, partner int64) int64 {
	t.Helper()
	result, err := db.Exec(`
		INSERT INTO partnerships(protectee_id, partner_id, edge_type, log_sharing, accepted_at)
		VALUES (?, ?, 'peer', 0, ?)`, protectee, partner, testClock.Unix())
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestEndingOnePartnershipLeavesTheRosterLockedAndTellsTheRemainingPartner(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, partnership := firstPartnership(t, service, db, true)
	other := createUser(t, db, "other@example.com")
	otherPartnership := addPartnership(t, db, protectee, other)

	// A second Partner on the same Protectee holds a Partnership of their
	// own, and still may not touch this one.
	if _, err := service.EndablePartnership(context.Background(), partnership, other); !errors.Is(err, ErrPartnershipUnavailable) {
		t.Fatalf("another Partner previewed this ending = %v", err)
	}
	if _, err := service.EndPartnership(context.Background(), EndCommand{
		PartnershipID: partnership, PartnerID: other, Confirmation: "END",
	}); !errors.Is(err, ErrPartnershipUnavailable) {
		t.Fatalf("another Partner ended this Partnership = %v", err)
	}

	preview, err := service.EndablePartnership(context.Background(), partnership, partner)
	if err != nil || preview.RemainingPartners != 1 || !preview.PartnerLockActive() {
		t.Fatalf("preview with a remaining Partner = %+v, %v", preview, err)
	}
	ending, err := service.EndPartnership(context.Background(), EndCommand{
		PartnershipID: partnership, PartnerID: partner, Confirmation: "END",
	})
	if err != nil || ending.RemainingPartners != 1 || !ending.PartnerLockActive() {
		t.Fatalf("ending with a remaining Partner = %+v, %v", ending, err)
	}

	// The lock survives, so the Protectee still cannot loosen their own Policy.
	if allowed, err := service.CanLoosen(context.Background(), protectee, protectee); err != nil || allowed {
		t.Fatalf("Protectee loosened while a Partner remains: %v, %v", allowed, err)
	}
	if allowed, err := service.CanLoosen(context.Background(), other, protectee); err != nil || !allowed {
		t.Fatalf("remaining Partner lost their powers: %v, %v", allowed, err)
	}
	// The remaining Partner keeps their own Partnership and holds no receipt
	// for somebody else's.
	if _, err := service.EndedPartnershipReceipt(context.Background(), partnership, other); !errors.Is(err, ErrPartnershipUnavailable) {
		t.Fatalf("another Partner read this receipt = %v", err)
	}
	if _, err := service.EndablePartnership(context.Background(), otherPartnership, other); err != nil {
		t.Fatalf("remaining Partner lost their own Partnership: %v", err)
	}

	sender := &mailmessage.RecordingSender{}
	if _, err := service.DispatchDue(context.Background(), sender, 20); err != nil {
		t.Fatal(err)
	}
	// Both parties plus the remaining Roster, each in their own envelope.
	recipients := map[string]bool{}
	for _, envelope := range sender.Envelopes {
		if !strings.Contains(envelope.Subject, "ended their Partnership") {
			continue
		}
		if recipients[envelope.To] {
			t.Fatalf("duplicate ending message to %s", envelope.To)
		}
		recipients[envelope.To] = true
		for _, want := range []string{
			"protectee@example.com now has 1 active Partner(s).",
			"The Partner lock remains active.",
		} {
			if !strings.Contains(envelope.Text, want) {
				t.Fatalf("%s ending message missing %q:\n%s", envelope.To, want, envelope.Text)
			}
		}
	}
	if len(recipients) != 3 || !recipients["protectee@example.com"] ||
		!recipients["partner@example.com"] || !recipients["other@example.com"] {
		t.Fatalf("ending recipients = %v", recipients)
	}
	// A Partner who is still on the Roster keeps the Roster link; only the
	// departing Partner's copy swaps it for their receipt.
	for _, envelope := range sender.Envelopes {
		if envelope.To != "other@example.com" || !strings.Contains(envelope.Subject, "ended their Partnership") {
			continue
		}
		if !strings.Contains(envelope.Text, "Review the Roster and Activity:") ||
			strings.Contains(envelope.Text, "You keep a read-only receipt") {
			t.Fatalf("remaining Partner closing link = %q", envelope.Text)
		}
	}
}

func TestConcurrentEndAttemptsCommitExactlyOnce(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	_, partner, partnership := firstPartnership(t, service, db, false)

	const attempts = 4
	results := make(chan error, attempts)
	start := make(chan struct{})
	for range attempts {
		go func() {
			<-start
			_, err := service.EndPartnership(context.Background(), EndCommand{
				PartnershipID: partnership, PartnerID: partner, Confirmation: "END",
			})
			results <- err
		}()
	}
	close(start)

	committed := 0
	for range attempts {
		switch err := <-results; {
		case err == nil:
			committed++
		case errors.Is(err, ErrPartnershipUnavailable):
		default:
			t.Fatalf("concurrent ending = %v", err)
		}
	}
	if committed != 1 {
		t.Fatalf("%d concurrent endings committed, want exactly 1", committed)
	}

	var ended, activity, jobs int
	if err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM partnerships WHERE ended_at IS NOT NULL),
		(SELECT COUNT(*) FROM activity_entries WHERE action = ?),
		(SELECT COUNT(*) FROM mail_delivery_jobs WHERE message_kind = ?)`,
		ActionPartnershipEndedByPartner, MessagePartnershipEnded).Scan(&ended, &activity, &jobs); err != nil {
		t.Fatal(err)
	}
	if ended != 1 || activity != 1 || jobs != 2 {
		t.Fatalf("concurrent endings left ended=%d activity=%d jobs=%d", ended, activity, jobs)
	}
}
