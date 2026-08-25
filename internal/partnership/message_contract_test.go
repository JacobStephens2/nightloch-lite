package partnership

import (
	"context"
	"database/sql"
	"html"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// queuedMessage is one row of the durable per-recipient delivery ledger, read
// back as the contract test reads it: what was addressed to whom, and what it
// said in both parts.
type queuedMessage struct {
	kind      string
	recipient string
	subject   string
	text      string
	html      string
	replyTo   string
}

// everyCanonicalMessage drives one Protectee through every relationship event
// that sends mail, and returns every envelope those events queued. Running
// them in one scenario is what makes the contract a matrix rather than a set
// of unrelated examples: the same actors appear in every kind, so a leak in
// one is visible against what the others were entitled to say.
func everyCanonicalMessage(t *testing.T, service *Service, db *sql.DB) []queuedMessage {
	t.Helper()
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	partnerA := createUser(t, db, "partner-a@example.com")
	partnerB := createUser(t, db, "partner-b@example.com")
	// The withdrawn invitee has a User of their own, which is the strongest
	// an uninvited outsider can be when the leak rule is checked.
	createUser(t, db, "withdrawn@example.com")
	declining := createUser(t, db, "declining@example.com")

	// Invitation, acceptance, and the alerts each of them owes a Roster.
	firstAccepted := acceptFor(t, service,
		invite(t, service, protectee, "partner-a@example.com", true), partnerA).PartnershipID
	second := invite(t, service, protectee, "partner-b@example.com", false)
	// A resend repeats the same invitation on a fresh bearer link, a day later
	// so it clears the once-a-day rule.
	at(service, testClock.Add(25*time.Hour))
	if _, err := service.ResendInvitation(ctx, second.ID, protectee); err != nil {
		t.Fatalf("ResendInvitation: %v", err)
	}
	// The resend superseded the first bearer link (ADR 0003), so the invitee
	// answers from their own Partner invitations list instead.
	accepted, err := service.AcceptInvitation(ctx, ByID(second.ID), partnerB)
	if err != nil {
		t.Fatalf("AcceptInvitation: %v", err)
	}
	secondPartnership := accepted.PartnershipID

	// An invitation the Protectee withdraws, and one the invitee declines.
	pending := invite(t, service, protectee, "withdrawn@example.com", false)
	if _, err := service.CancelInvitation(ctx, pending.ID, protectee); err != nil {
		t.Fatalf("CancelInvitation: %v", err)
	}
	declined := invite(t, service, protectee, "declining@example.com", false)
	if _, err := service.DeclineInvitation(ctx, ByToken(declined.RawToken), declining); err != nil {
		t.Fatalf("DeclineInvitation: %v", err)
	}

	// Cooling-off started, cancelled, restarted, and then made final by the
	// other Partner ending their own Partnership.
	startEnding(t, service, firstAccepted, protectee)
	if _, err := service.CancelCoolingOff(ctx, CoolingOffCommand{
		PartnershipID: firstAccepted, ProtecteeID: protectee,
	}); err != nil {
		t.Fatalf("CancelCoolingOff: %v", err)
	}
	startEnding(t, service, firstAccepted, protectee)
	if _, err := service.EndPartnership(ctx, EndCommand{
		PartnershipID: secondPartnership, PartnerID: partnerB, Confirmation: "END",
	}); err != nil {
		t.Fatalf("EndPartnership: %v", err)
	}
	// The deadline arrives, and afterwards the Protectee deletes their User
	// while one Partnership still stands.
	at(service, testClock.Add(25*time.Hour+coolingOffDuration))
	if ended, err := service.CompleteDueCoolingOff(ctx, 10); err != nil || ended != 1 {
		t.Fatalf("CompleteDueCoolingOff = %d, %v", ended, err)
	}
	restored := acceptFor(t, service,
		invite(t, service, protectee, "partner-a@example.com", false), partnerA)
	if restored.PartnershipID == 0 {
		t.Fatal("the re-partnered Protectee has no Partnership")
	}
	if _, err := service.DeleteUser(ctx, protectee); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	rows, err := db.Query(`SELECT message_kind, recipient_email, subject, body_text, body_html, reply_to
		FROM mail_delivery_jobs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var messages []queuedMessage
	for rows.Next() {
		var message queuedMessage
		if err := rows.Scan(&message.kind, &message.recipient, &message.subject,
			&message.text, &message.html, &message.replyTo); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return messages
}

func TestEveryCanonicalMessageReachesExactlyItsOwnRecipients(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	messages := everyCanonicalMessage(t, service, db)

	// Every kind, and for each the addresses it was queued to across the whole
	// scenario. A recipient listed twice is two separate events, never two
	// copies of one: each event queues one envelope per recipient (S3-21).
	want := map[string][]string{
		// The invitation goes to the invitee alone, however many Partners the
		// Protectee already has.
		MessagePartnerInvitation: {
			"partner-a@example.com", "partner-b@example.com",
			"withdrawn@example.com", "declining@example.com", "partner-a@example.com",
		},
		MessagePartnerInvitationResent: {"partner-b@example.com"},
		// Every current Partner is told what their Protectee did, one envelope
		// each; the first invitation predates any Partner, so it alerts nobody.
		MessagePartnerInvitationSent: {
			"partner-a@example.com", "partner-a@example.com", "partner-b@example.com",
			"partner-a@example.com", "partner-b@example.com",
		},
		MessageInvitationCanceled:        {"withdrawn@example.com"},
		MessageInvitationCanceledPartner: {"partner-a@example.com", "partner-b@example.com"},
		MessageInvitationDeclined:        {"protectee@example.com"},
		// An acceptance tells the Protectee and every prior Partner in the
		// same words, and the new Partner in words of their own.
		MessagePartnershipAcceptedExisting: {
			"protectee@example.com", "protectee@example.com",
			"partner-a@example.com", "protectee@example.com",
		},
		MessagePartnershipAcceptedPartner: {
			"partner-a@example.com", "partner-b@example.com", "partner-a@example.com",
		},
		// A Cooling-off signal reaches the affected Partner and every other
		// current Partner; the Protectee acted and reads it in-app.
		MessageCoolingOffStarted:  {"partner-a@example.com", "partner-b@example.com", "partner-a@example.com", "partner-b@example.com"},
		MessageCoolingOffCanceled: {"partner-a@example.com", "partner-b@example.com"},
		// The transition to a final Partnership is news for the Protectee and
		// the Partner whose countdown it now is.
		MessageCoolingOffBecameFinal: {"protectee@example.com", "partner-a@example.com"},
		// An ending tells both sides of the Partnership that ended, and every
		// Partner who remains.
		MessagePartnershipEnded: {
			"protectee@example.com", "partner-b@example.com", "partner-a@example.com",
			"protectee@example.com", "partner-a@example.com",
		},
		MessageUserDeleted: {"partner-a@example.com"},
	}

	got := map[string][]string{}
	for _, message := range messages {
		got[message.kind] = append(got[message.kind], message.recipient)
	}
	for kind, recipients := range want {
		sort.Strings(recipients)
		actual := append([]string(nil), got[kind]...)
		sort.Strings(actual)
		if strings.Join(recipients, ",") != strings.Join(actual, ",") {
			t.Errorf("%s reached %v, want %v", kind, actual, recipients)
		}
	}
	for kind := range got {
		if _, known := want[kind]; !known {
			t.Errorf("%s is queued by the module but has no recipient contract", kind)
		}
	}
}

var (
	// instantPattern is the one stamp format every canonical message uses.
	instantPattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}(:\d{2})?(?: ([A-Za-z]+))?`)
	linkPattern    = regexp.MustCompile(`https?://\S+`)
	hrefPattern    = regexp.MustCompile(`href="([^"]+)"`)
	tagPattern     = regexp.MustCompile(`<[^>]+>`)
)

func TestEveryCanonicalMessageCarriesTheSameContractInBothParts(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	messages := everyCanonicalMessage(t, service, db)
	if len(messages) == 0 {
		t.Fatal("the scenario queued nothing")
	}

	for _, message := range messages {
		label := message.kind + " -> " + message.recipient
		// Every subject names Nightloch on one line, so the message is
		// recognizable in a crowded inbox without being opened.
		if !strings.Contains(message.subject, "Nightloch") || strings.Contains(message.subject, "\n") {
			t.Errorf("%s subject = %q", label, message.subject)
		}
		// Reply routing is part of the contract: an invitation is answered to
		// the Protectee who sent it, and every other message is Nightloch's
		// own, answered at its monitored address.
		wantReplyTo := alertsAddress
		if message.kind == MessagePartnerInvitation || message.kind == MessagePartnerInvitationResent {
			wantReplyTo = "protectee@example.com"
		}
		if message.replyTo != wantReplyTo {
			t.Errorf("%s replies to %q, want %q", label, message.replyTo, wantReplyTo)
		}

		// Every instant is stamped UTC in both parts, so no reader has to
		// guess an offset whichever part their client renders.
		for part, content := range map[string]string{"text": message.text, "HTML": message.html} {
			for _, stamp := range instantPattern.FindAllStringSubmatch(content, -1) {
				if stamp[2] != "UTC" {
					t.Errorf("%s stamps %q outside UTC in its %s part", label, stamp[0], part)
				}
			}
		}
		// Every link is Nightloch's own over TLS, and nothing else is fetched.
		for _, link := range linkPattern.FindAllString(message.text, -1) {
			if !strings.HasPrefix(link, "https://lite.nightloch.app/") {
				t.Errorf("%s links away to %q", label, link)
			}
		}
		if strings.Contains(message.html, "<img") || strings.Contains(message.html, "<script") {
			t.Errorf("%s loads something remote:\n%s", label, message.html)
		}
		assertPartsAgree(t, label, message)
	}
}

// assertPartsAgree proves the HTML part says what the plain-text part says: no
// sentence is dropped, and every URL the text spells out is reachable from the
// HTML as a link rather than only as prose.
func assertPartsAgree(t *testing.T, label string, message queuedMessage) {
	t.Helper()
	visible := normalizeMeaning(html.UnescapeString(tagPattern.ReplaceAllString(message.html, " ")))
	var hrefs []string
	for _, match := range hrefPattern.FindAllStringSubmatch(message.html, -1) {
		hrefs = append(hrefs, html.UnescapeString(match[1]))
	}
	for _, line := range strings.Split(message.text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if isBareURL(line) {
			if !slices.Contains(hrefs, line) {
				t.Errorf("%s spells out %s in text but does not link it: %v", label, line, hrefs)
			}
			continue
		}
		if bullet, isBullet := strings.CutPrefix(line, "- "); isBullet {
			// An HTML list item drops the running punctuation the plain-text
			// list needs, which is presentation rather than meaning.
			line = trimListPunctuation(bullet)
		}
		if wanted := normalizeMeaning(line); wanted != "" && !strings.Contains(visible, wanted) {
			t.Errorf("%s drops %q from its HTML part:\n%s", label, line, message.html)
		}
	}
}

// normalizeMeaning reduces a line to its letters and digits, so wrapping,
// punctuation the HTML list drops, and escaping cannot make two identical
// sentences look different.
func normalizeMeaning(text string) string {
	var builder strings.Builder
	for _, symbol := range strings.ToLower(text) {
		if (symbol >= 'a' && symbol <= 'z') || (symbol >= '0' && symbol <= '9') {
			builder.WriteRune(symbol)
		}
	}
	return builder.String()
}

func TestNoMessageDisclosesARosterItsRecipientHasNotJoined(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	messages := everyCanonicalMessage(t, service, db)

	// An invitee who never accepted, and a Protectee's own withdrawn invitee,
	// are entitled to exactly two addresses: their own and the Protectee's who
	// wrote to them. Anybody else's is a Roster disclosure they never earned.
	outsiders := map[string]bool{
		"withdrawn@example.com": true,
		"declining@example.com": true,
	}
	everyone := []string{
		"protectee@example.com", "partner-a@example.com", "partner-b@example.com",
		"withdrawn@example.com", "declining@example.com",
	}
	// A rule about what outsiders may read is worth nothing if no outsider was
	// written to, so count them as the rule is applied.
	audited := map[string]int{}
	for _, message := range messages {
		if !outsiders[message.recipient] {
			continue
		}
		audited[message.recipient]++
		for _, address := range everyone {
			if address == message.recipient || address == "protectee@example.com" {
				continue
			}
			if strings.Contains(message.text, address) || strings.Contains(message.html, address) {
				t.Errorf("%s disclosed %s to %s:\n%s",
					message.kind, address, message.recipient, message.text)
			}
		}
	}

	for outsider := range outsiders {
		if audited[outsider] == 0 {
			t.Fatalf("%s received nothing, so the disclosure rule was never applied", outsider)
		}
	}
}
