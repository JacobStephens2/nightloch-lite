package partnership

import (
	"fmt"
	"html"
	"strings"
	"time"

	mailmessage "github.com/JacobStephens2/nightloch-lite/internal/mail"
)

// alertsAddress receives replies to Nightloch's own integrity messages.
// Partner invitations reply to the inviting Protectee instead (S3-24).
const alertsAddress = "alerts@nightloch.app"

// maxFactLabel and sentencePunctuation separate a `Label: value` fact line
// from a wrapped sentence that happens to contain a colon.
const (
	maxFactLabel        = 40
	sentencePunctuation = ".,;"
)

// utcStamp renders the explicit UTC date and time every message must use
// instead of a bare or viewer-relative timestamp (S3-27).
func utcStamp(at time.Time) string {
	return at.UTC().Format("2006-01-02 15:04") + " UTC"
}

// preparedMessage is one canonical message for exactly one recipient, already
// rendered so a queued delivery job can reproduce it verbatim after a restart.
type preparedMessage struct {
	// Kind is the stable message type, used for idempotency and for showing
	// authorized viewers which signal a delivery job carries.
	Kind string
	mailmessage.Envelope
}

type invitationMessage struct {
	ProtecteeEmail string
	InviteeEmail   string
	LogSharing     bool
	PartnerCount   int
	// Reciprocal is whether this Protectee already protects the invitee, so
	// accepting would form Reciprocal Partnerships. The message goes to the
	// invited address alone, which is the one party already inside that
	// reverse-direction relationship, so stating it discloses nothing new.
	Reciprocal bool
	ReviewURL  string
	ExpiresAt  time.Time
}

// renderPartnerInvitation produces the approved Partner invitation from
// docs/SLICE-3-PARTNERSHIP-EXPERIENCE.md.
func renderPartnerInvitation(message invitationMessage) preparedMessage {
	logSharing := "Log sharing: Off — you will not receive domain-log access."
	if message.LogSharing {
		logSharing = "Log sharing: On — you will receive domain-log access after acceptance."
	}

	text := fmt.Sprintf(`%[1]s invited you to become their Partner in Nightloch.

Nightloch is partner-locked DNS protection. It can block domains on a
Protectee’s enrolled Devices. While a Protectee has active Partners, they
cannot loosen their own Policy; their Partners can.

A Protectee can still turn off the VPN on a Device, which removes protection.

If you accept, you can:
- view Coverage and enforcement health;
- tighten or loosen %[1]s’s Policy;
- approve Allowlist entries;
- revoke their Devices;
- view their Roster and Activity; and
- view their DNS domain log only when Log sharing is on.

%[2]s

%[3]s%[6]s

You may end the Partnership immediately. If %[1]s requests to
end it, the Partnership remains active for 72 hours unless they cancel the
request or you agree to end it sooner.

Review Partner invitation:
%[4]s

This invitation expires %[5]s.

If you did not expect this invitation, do not accept it. Contact
%[1]s directly.

Replies to this email go directly to %[1]s.`,
		message.ProtecteeEmail,
		logSharing,
		rosterDisclosure(message),
		message.ReviewURL,
		utcStamp(message.ExpiresAt),
		reciprocalInvitationDisclosure(message),
	)

	return preparedMessage{
		Kind: MessagePartnerInvitation,
		Envelope: mailmessage.Envelope{
			To:      message.InviteeEmail,
			Subject: message.ProtecteeEmail + " invited you to be their Nightloch Partner",
			Text:    text,
			HTML:    textToHTML(text),
			ReplyTo: message.ProtecteeEmail,
		},
	}
}

// rosterDisclosure states how visible the invitee becomes. With no current
// Partners the approved paragraph's claims about telling them and revealing
// their identities would be false, so that half is omitted until there is
// someone to tell.
func rosterDisclosure(message invitationMessage) string {
	if message.PartnerCount == 0 {
		return fmt.Sprintf(`%s currently has 0 active Partners.

If you accept, your verified email will remain visible to current and future
Partners in the Roster and Activity.`, message.ProtecteeEmail)
	}
	return fmt.Sprintf(`%[1]s currently has %[2]d active Partner(s).
Their identities will be shown after you accept.

Nightloch has told those current Partners that this invitation was sent to
%[3]s. If you accept, your verified email will remain visible to
current and future Partners in the Roster and Activity.`,
		message.ProtecteeEmail, message.PartnerCount, message.InviteeEmail)
}

// reciprocalInvitationDisclosure names the reverse-direction consequence for an invitee
// the inviting Protectee already protects (S3-04). It is empty in the ordinary
// direction, where there is no second Partnership to describe and a
// "Reciprocal Partnerships: No" line would only invite the reader to wonder
// what merged authority they had just avoided.
func reciprocalInvitationDisclosure(message invitationMessage) string {
	if !message.Reciprocal {
		return ""
	}
	return fmt.Sprintf(`

%[1]s already protects you as your Partner. Accepting creates a
second, independent Partnership in the opposite direction, so you would be
their Partner too. The two Partnerships do not merge: each keeps its own Log
sharing, its own ending, and its own powers, and ending one does not end the
other.`, message.ProtecteeEmail)
}

// reciprocalAcceptanceDisclosure names the reverse direction for a Partner who
// has just accepted an invitation from a Protectee who already protects them
// (S3-04). Like reciprocalInvitationDisclosure it is empty in the ordinary
// direction: this Partner is already inside the reverse relationship, so what
// they need is not the fact but the assurance that the two do not merge.
func reciprocalAcceptanceDisclosure(message acceptanceMessage) string {
	if !message.Reciprocal {
		return ""
	}
	return fmt.Sprintf(`

Reciprocal Partnerships: Yes. %[1]s already protects you, and
you now protect them. These are two independent Partnerships: ending or
changing one does not change the other, and neither one gives either of you
authority over the other’s Partnership.`, message.ProtecteeEmail)
}

// invitationAlert is the Action alert every current Partner receives when
// their Protectee invites someone else. It states what acceptance would grant
// and that the reader cannot act on it, and it never names another recipient.
type invitationAlert struct {
	ProtecteeEmail string
	InviteeEmail   string
	LogSharing     bool
	// Reciprocal is whether accepting would also make the invitee a Protectee
	// of this Protectee, which is a consequence a current Partner is entitled
	// to see before it happens.
	Reciprocal  bool
	SentAt      time.Time
	ExpiresAt   time.Time
	ActivityURL string
}

// renderInvitationSentToPartner produces the approved invitation-sent Action
// alert for one current Partner. Every current Partner reads the same body in
// their own envelope, so the recipient list is never exposed (S3-21).
func renderInvitationSentToPartner(message invitationAlert, recipient string) preparedMessage {
	text := fmt.Sprintf(`%[1]s sent a Partner invitation to %[2]s on
%[3]s.

No Partner powers have been granted yet. If accepted, %[2]s will
be able to loosen %[1]s’s Policy, revoke their Devices, and
exercise the other Partner powers shown in Nightloch.

Log sharing after acceptance: %[4]s
Reciprocal Partnerships if accepted: %[5]s
Invitation expires: %[6]s

You can observe this invitation in Activity, but you cannot accept, cancel,
or decline it for either person.

Review Activity:
%[7]s

If this invitation is unexpected, contact %[1]s directly.

This is an integrity message from Nightloch. Replies go to the monitored
address %[8]s.`,
		message.ProtecteeEmail, message.InviteeEmail, utcStamp(message.SentAt),
		onOff(message.LogSharing), yesNo(message.Reciprocal), utcStamp(message.ExpiresAt),
		message.ActivityURL, alertsAddress)

	return preparedMessage{
		Kind: MessagePartnerInvitationSent,
		Envelope: mailmessage.Envelope{
			To: recipient,
			Subject: fmt.Sprintf("[Nightloch] %s invited %s as a Partner",
				message.ProtecteeEmail, message.InviteeEmail),
			Text:    text,
			HTML:    textToHTML(text),
			ReplyTo: alertsAddress,
		},
	}
}

// textToHTML mirrors the canonical plain text as HTML so the two parts cannot
// drift apart in meaning. Bare URL lines become descriptive links; the plain
// text keeps the full URL. Nothing loads remotely, so there is no tracking.
func textToHTML(text string) string {
	var builder strings.Builder
	for _, block := range strings.Split(text, "\n\n") {
		lines := strings.Split(block, "\n")
		var prose, bullets []string
		for _, line := range lines {
			if strings.HasPrefix(line, "- ") {
				bullets = append(bullets, strings.TrimPrefix(line, "- "))
				continue
			}
			prose = append(prose, line)
		}
		if link := len(prose) - 1; len(bullets) == 0 && link > 0 && isBareURL(prose[link]) {
			// Only the caption line directly above the URL becomes the link
			// text. Anything before it is ordinary prose and would change
			// meaning if it were swallowed into the anchor.
			if intro := strings.TrimSpace(strings.Join(prose[:link-1], " ")); intro != "" {
				fmt.Fprintf(&builder, "<p>%s</p>", html.EscapeString(intro))
			}
			label := strings.TrimSuffix(prose[link-1], ":")
			fmt.Fprintf(&builder, "<p><a href=\"%s\">%s</a></p>",
				html.EscapeString(prose[link]), html.EscapeString(label))
			continue
		}
		if isFactBlock(prose) {
			escaped := make([]string, 0, len(prose))
			for _, line := range prose {
				escaped = append(escaped, html.EscapeString(line))
			}
			fmt.Fprintf(&builder, "<p>%s</p>", strings.Join(escaped, "<br>"))
		} else if paragraph := strings.TrimSpace(strings.Join(prose, " ")); paragraph != "" {
			fmt.Fprintf(&builder, "<p>%s</p>", html.EscapeString(paragraph))
		}
		if len(bullets) > 0 {
			builder.WriteString("<ul>")
			for _, bullet := range bullets {
				fmt.Fprintf(&builder, "<li>%s</li>", html.EscapeString(trimListPunctuation(bullet)))
			}
			builder.WriteString("</ul>")
		}
	}
	return builder.String()
}

// trimListPunctuation drops the running punctuation the plain-text list needs
// but an HTML list item does not.
func trimListPunctuation(bullet string) string {
	for _, suffix := range []string{"; and", ";", "."} {
		if trimmed, found := strings.CutSuffix(bullet, suffix); found {
			return trimmed
		}
	}
	return bullet
}

// isFactBlock reports whether every line of a block is a `Label: value` fact
// rather than wrapped prose. Joining those onto one line would run unrelated
// facts together, so they keep their line breaks in the HTML part.
func isFactBlock(lines []string) bool {
	if len(lines) == 0 {
		return false
	}
	for _, line := range lines {
		label, value, found := strings.Cut(line, ": ")
		// A wrapped sentence can carry a colon too, so a fact label must stay
		// short and free of sentence punctuation.
		if !found || label == "" || len(label) > maxFactLabel ||
			strings.TrimSpace(value) == "" || strings.ContainsAny(label, sentencePunctuation) {
			return false
		}
	}
	return true
}

func isBareURL(line string) bool {
	return strings.HasPrefix(line, "https://") && !strings.Contains(line, " ")
}

type acceptanceMessage struct {
	ProtecteeEmail  string
	NewPartnerEmail string
	LogSharing      bool
	Reciprocal      bool
	// PartnerCount includes the Partner who just accepted.
	PartnerCount int
	AcceptedAt   time.Time
	DashboardURL string
	// LockEndsAt and LockEndingPartners are the instant the Protectee's Partner
	// lock was scheduled to deactivate before this acceptance, and the
	// Partnerships ending at it. They are set only when such an instant
	// existed, because that is the only case where a reader would otherwise
	// expect a deadline they already know about to end the lock.
	LockEndsAt         time.Time
	LockEndingPartners []string
}

// renderAcceptanceToProtecteeAndPriorPartners produces the approved acceptance
// alert for everyone who already had a relationship with this Protectee. They
// all read the same body, each in their own envelope, so no recipient learns
// who else was told (S3-20, S3-21).
func renderAcceptanceToProtecteeAndPriorPartners(message acceptanceMessage, recipient string) preparedMessage {
	lock := "The Partner lock remains active."
	if message.PartnerCount == 1 {
		lock = "The Partner lock is now active."
	}
	// The acceptance overtook the ending that would have deactivated the lock.
	// Saying so is the whole point: these readers were told that deadline
	// would end it, and it no longer does (S3-43).
	if surviving := survivingLockDisclosure(message); surviving != "" {
		lock += "\n\n" + surviving
	}
	text := fmt.Sprintf(`%[1]s accepted %[2]s’s Partner invitation on
%[3]s.

The Partnership is now active. %[1]s can loosen
%[2]s’s Policy, revoke their Devices, and exercise the other
Partner powers shown in Nightloch.

Log sharing: %[4]s
Reciprocal Partnerships: %[5]s
Active Partners now: %[6]d

%[7]s

Review the Roster and Activity:
%[8]s

This is an integrity message from Nightloch. Replies go to the monitored
address %[9]s.`,
		message.NewPartnerEmail, message.ProtecteeEmail, utcStamp(message.AcceptedAt),
		onOff(message.LogSharing), yesNo(message.Reciprocal), message.PartnerCount,
		lock, message.DashboardURL, alertsAddress)

	return preparedMessage{
		Kind: MessagePartnershipAcceptedExisting,
		Envelope: mailmessage.Envelope{
			To: recipient,
			Subject: fmt.Sprintf("[Nightloch] %s now protects %s",
				message.NewPartnerEmail, message.ProtecteeEmail),
			Text:    text,
			HTML:    textToHTML(text),
			ReplyTo: alertsAddress,
		},
	}
}

// survivingLockDisclosure states that the Partner lock outlives the ending it
// was scheduled to deactivate at. The approved sentence names the one final
// Partner; when several countdowns share that instant — the concurrent horizon
// a Protectee gets by scheduling every Partnership at once — naming one of
// them would be false, so the deadline itself carries the sentence instead.
func survivingLockDisclosure(message acceptanceMessage) string {
	switch len(message.LockEndingPartners) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("The Partner lock will remain active after %s’s\n"+
			"Partnership is scheduled to end on %s.",
			message.LockEndingPartners[0], utcStamp(message.LockEndsAt))
	default:
		return fmt.Sprintf("The Partner lock will remain active after the %d Partnerships\n"+
			"scheduled to end on %s.",
			len(message.LockEndingPartners), utcStamp(message.LockEndsAt))
	}
}

// renderAcceptanceToNewPartner confirms the Partnership to the accepting
// Partner. It names how many other Partners exist but never lists the Roster;
// that stays behind authentication (S3-21).
func renderAcceptanceToNewPartner(message acceptanceMessage) preparedMessage {
	logSharing := "Off — you cannot view their DNS domain log."
	if message.LogSharing {
		logSharing = "On — you can view their DNS domain log."
	}
	text := fmt.Sprintf(`You accepted the Partnership with %[1]s on %[2]s.

You can now view their protection status, change their Policy, approve
Allowlist entries, revoke Devices, and view their Roster and Activity.

Log sharing: %[3]s%[7]s

%[1]s has %[4]d other active Partner(s).
Sign in to see who they are:
%[5]s

Add %[6]s to your contacts so integrity messages are less
likely to be filtered.

You may end this Partnership immediately from Nightloch.

Replies to this message go to the monitored address %[6]s.`,
		message.ProtecteeEmail, utcStamp(message.AcceptedAt), logSharing,
		message.PartnerCount-1, message.DashboardURL, alertsAddress,
		reciprocalAcceptanceDisclosure(message))

	return preparedMessage{
		Kind: MessagePartnershipAcceptedPartner,
		Envelope: mailmessage.Envelope{
			To:      message.NewPartnerEmail,
			Subject: "You now protect " + message.ProtecteeEmail + " with Nightloch",
			Text:    text,
			HTML:    textToHTML(text),
			ReplyTo: alertsAddress,
		},
	}
}

func onOff(value bool) string {
	if value {
		return "On"
	}
	return "Off"
}

func yesNo(value bool) string {
	if value {
		return "Yes"
	}
	return "No"
}

// terminalMessage carries the two parties and the exact instant an invitation
// ended without becoming a Partnership.
type terminalMessage struct {
	ProtecteeEmail string
	InviteeEmail   string
	ResolvedAt     time.Time
}

// renderInvitationDeclined produces the approved decline alert. It goes only
// to the Protectee who sent the invitation (S3-20) and carries no review link,
// because there is nothing left to review.
func renderInvitationDeclined(message terminalMessage) preparedMessage {
	text := fmt.Sprintf(`%[1]s declined your Partner invitation on %[2]s.

No Partnership was created. You may send a new invitation later.`,
		message.InviteeEmail, utcStamp(message.ResolvedAt))

	return preparedMessage{
		Kind: MessageInvitationDeclined,
		Envelope: mailmessage.Envelope{
			To:      message.ProtecteeEmail,
			Subject: message.InviteeEmail + " declined your Nightloch Partner invitation",
			Text:    text,
			HTML:    textToHTML(text),
			ReplyTo: alertsAddress,
		},
	}
}

// renderInvitationCanceled produces the approved cancellation alert for the
// invitee, who loses a link rather than a relationship and so is told nothing
// about the Roster.
func renderInvitationCanceled(message terminalMessage) preparedMessage {
	text := fmt.Sprintf(`%[1]s canceled the Partner invitation sent to you on
%[2]s.

No Partnership was created and no Partner powers were granted.`,
		message.ProtecteeEmail, utcStamp(message.ResolvedAt))

	return preparedMessage{
		Kind: MessageInvitationCanceled,
		Envelope: mailmessage.Envelope{
			To:      message.InviteeEmail,
			Subject: message.ProtecteeEmail + " canceled your Nightloch Partner invitation",
			Text:    text,
			HTML:    textToHTML(text),
			ReplyTo: alertsAddress,
		},
	}
}

// endingMessage carries the two parties, the exact ending instant, the
// Protectee's resulting Partner count, and both closing destinations for the
// approved ending alert.
type endingMessage struct {
	ProtecteeEmail string
	PartnerEmail   string
	EndedAt        time.Time
	// EndedReason selects the approved subject and opening sentence. The rest
	// of the body is identical for all three causes.
	EndedReason string
	// PartnerCount is the Protectee's active Partners after the ending.
	PartnerCount int
	DashboardURL string
	// ReceiptURL is the departing Partner's retained receipt, and the only
	// destination their copy of the message offers (S3-16).
	ReceiptURL string
}

// renderPartnershipEnded produces the approved Partnership-ended alert for one
// recipient. Both parties and every remaining Partner read the same body, each
// in their own envelope, so no recipient ever learns who else was told
// (S3-20, S3-21). Only the closing link differs, because only one recipient
// keeps a receipt instead of the Roster. The zero-Partner consequence is
// always stated, because it changes who may loosen the Policy.
func renderPartnershipEnded(message endingMessage, recipient string) preparedMessage {
	subject, opening := endingVariant(message)
	lock := "The Partner lock remains active."
	if message.PartnerCount == 0 {
		lock = fmt.Sprintf("The Partner lock is now inactive. %s can loosen their own Policy.",
			message.ProtecteeEmail)
	}
	// The closing link names what this recipient actually retains. Everyone
	// still on the Roster reviews it; the Partner who just lost it is offered
	// their receipt instead, because inviting them to a Roster they can no
	// longer reach would be a false instruction.
	closing := "Review the Roster and Activity:\n" + message.DashboardURL
	if recipient == message.PartnerEmail {
		closing = "You keep a read-only receipt for this ended Partnership:\n" + message.ReceiptURL
	}
	text := fmt.Sprintf(`%[8]s

The Partnership ended on %[3]s.

%[1]s can no longer change %[2]s’s Policy, revoke
their Devices, view their Roster or ongoing Activity, or exercise any other
Partner power. They retain only a read-only receipt for this ended
Partnership.

%[2]s now has %[4]d active Partner(s).

%[5]s

%[6]s

This is an integrity message from Nightloch. Replies go to the monitored
address %[7]s.`,
		message.PartnerEmail, message.ProtecteeEmail, utcStamp(message.EndedAt),
		message.PartnerCount, lock, closing, alertsAddress, opening)

	return preparedMessage{
		Kind: MessagePartnershipEnded,
		Envelope: mailmessage.Envelope{
			To:      recipient,
			Subject: subject,
			Text:    text,
			HTML:    textToHTML(text),
			ReplyTo: alertsAddress,
		},
	}
}

// endingVariant is the approved subject and opening sentence for how this
// Partnership ended. A completed Cooling-off names Nightloch as the actor and
// the Protectee as the requester, because the execution was not their act
// (S3-18); the other two name the Partner who acted.
func endingVariant(message endingMessage) (subject, opening string) {
	switch message.EndedReason {
	case EndReasonPartnerAgreedNow:
		return fmt.Sprintf("[Nightloch] %s agreed to end their Partnership with %s now",
				message.PartnerEmail, message.ProtecteeEmail),
			fmt.Sprintf("%s agreed to end the Partnership immediately after\n%s requested Cooling-off.",
				message.PartnerEmail, message.ProtecteeEmail)
	case EndReasonCoolingOffCompleted:
		return fmt.Sprintf("[Nightloch] A Partnership for %s ended after Cooling-off",
				message.ProtecteeEmail),
			fmt.Sprintf("Nightloch ended the Partnership after the Cooling-off requested by\n%s completed.",
				message.ProtecteeEmail)
	default:
		return fmt.Sprintf("[Nightloch] %s ended their Partnership with %s",
				message.PartnerEmail, message.ProtecteeEmail),
			message.PartnerEmail + " ended the Partnership immediately."
	}
}

// coolingOffMessage carries the two parties, the instant the Protectee acted,
// the deadline where one applies, and both destinations the approved
// Cooling-off copy closes with.
type coolingOffMessage struct {
	ProtecteeEmail string
	PartnerEmail   string
	At             time.Time
	EndsAt         time.Time
	// PartnerCount is the Protectee's active Partners once this Partnership
	// ends, which is what makes the request final or not.
	PartnerCount int
	DashboardURL string
	// PartnershipURL is where the affected Partner reviews the request and, if
	// they agree, ends it now.
	PartnershipURL string
}

// renderCoolingOffStarted produces the approved Cooling-off start alert. The
// affected Partner reads that they keep every power and may agree to end
// sooner; every other current Partner reads that they may watch the countdown
// but cannot touch it. The Protectee initiated the request and gets an in-app
// receipt instead of mail (S3-20).
func renderCoolingOffStarted(message coolingOffMessage, recipient string) preparedMessage {
	if recipient != message.PartnerEmail {
		text := fmt.Sprintf(`%[1]s requested to end their Partnership with
%[2]s on %[3]s.

It is scheduled to end on %[4]s.

Until then, %[2]s remains an active Partner with all
Partner powers. You can observe the countdown, but you cannot cancel it or
end another Partner’s Partnership.

Review the Roster and Activity:
%[5]s

This is an integrity message from Nightloch. Replies go to the monitored
address %[6]s.`,
			message.ProtecteeEmail, message.PartnerEmail, utcStamp(message.At),
			utcStamp(message.EndsAt), message.DashboardURL, alertsAddress)

		return preparedMessage{
			Kind: MessageCoolingOffStarted,
			Envelope: mailmessage.Envelope{
				To:      recipient,
				Subject: "[Nightloch] " + message.ProtecteeEmail + " requested to end a Partnership",
				Text:    text,
				HTML:    textToHTML(text),
				ReplyTo: alertsAddress,
			},
		}
	}

	text := fmt.Sprintf(`%[1]s requested to end your Partnership on
%[2]s.

It is scheduled to end on %[3]s.

Until then, the Partnership remains active and you retain all Partner powers.
%[1]s may cancel the request. If you agree to end sooner, you
may choose “End now” in Nightloch.

Review the request:
%[4]s

This is an integrity message from Nightloch. Replies go to the monitored
address %[5]s.`,
		message.ProtecteeEmail, utcStamp(message.At), utcStamp(message.EndsAt),
		message.PartnershipURL, alertsAddress)

	return preparedMessage{
		Kind: MessageCoolingOffStarted,
		Envelope: mailmessage.Envelope{
			To:      recipient,
			Subject: "[Nightloch] " + message.ProtecteeEmail + " requested to end your Partnership",
			Text:    text,
			HTML:    textToHTML(text),
			ReplyTo: alertsAddress,
		},
	}
}

// renderCoolingOffCanceled produces the approved cancellation alert. The
// retained Partner and every other current Partner read the same body, each in
// their own envelope, because nothing about the relationship changed and there
// is nothing recipient-specific left to say.
func renderCoolingOffCanceled(message coolingOffMessage, recipient string) preparedMessage {
	text := fmt.Sprintf(`%[1]s canceled the request to end their Partnership with
%[2]s on %[3]s.

The Partnership remains active. No end is scheduled, and
%[2]s retains the same Partner powers.

Review the Roster and Activity:
%[4]s

This is an integrity message from Nightloch. Replies go to the monitored
address %[5]s.`,
		message.ProtecteeEmail, message.PartnerEmail, utcStamp(message.At),
		message.DashboardURL, alertsAddress)

	return preparedMessage{
		Kind: MessageCoolingOffCanceled,
		Envelope: mailmessage.Envelope{
			To:      recipient,
			Subject: "[Nightloch] " + message.ProtecteeEmail + " canceled a Partnership ending request",
			Text:    text,
			HTML:    textToHTML(text),
			ReplyTo: alertsAddress,
		},
	}
}

// finalCoolingOffMessage carries the ending that changed the Roster, the
// Partnership it left final, and that Partnership's unchanged deadline.
type finalCoolingOffMessage struct {
	ProtecteeEmail    string
	FinalPartnerEmail string
	// EndedPartnerEmail is the Partner whose ending caused the transition.
	// Both recipients were already entitled to that ending's own alert, so
	// naming them here discloses nothing new.
	EndedPartnerEmail string
	RosterChangedAt   time.Time
	// EndsAt is the deadline the Protectee already requested. The transition
	// never moves it, and the copy says so explicitly.
	EndsAt       time.Time
	DashboardURL string
}

// renderCoolingOffBecameFinal produces the approved alert for a Cooling-off
// that has become the final active Partnership. The Protectee and the final
// Partner read the same body, each in their own envelope (S3-21). It states
// the unchanged deadline, the lock consequence at it, and that nothing about
// the Partnership's powers has changed in the meantime.
func renderCoolingOffBecameFinal(message finalCoolingOffMessage, recipient string) preparedMessage {
	text := fmt.Sprintf(`%[1]s’s Partnership with %[2]s ended on
%[3]s.

As a result, %[4]s is now the final active Partner
protecting %[2]s. That Partnership is already in Cooling-off
and remains scheduled to end on %[5]s. Its deadline has not changed.

If no other Partner accepts an invitation before that deadline, the Partner
lock will become inactive and %[2]s will be able to loosen
their own Policy.

Until then, %[4]s retains all Partner powers and the
Partner lock remains active.

Review the Roster and Activity:
%[6]s

This is an integrity message from Nightloch. Replies go to the monitored
address %[7]s.`,
		message.EndedPartnerEmail, message.ProtecteeEmail, utcStamp(message.RosterChangedAt),
		message.FinalPartnerEmail, utcStamp(message.EndsAt), message.DashboardURL, alertsAddress)

	return preparedMessage{
		Kind: MessageCoolingOffBecameFinal,
		Envelope: mailmessage.Envelope{
			To: recipient,
			Subject: "[Nightloch] A scheduled Partnership end will deactivate " +
				message.ProtecteeEmail + "’s Partner lock",
			Text:    text,
			HTML:    textToHTML(text),
			ReplyTo: alertsAddress,
		},
	}
}

// cancellationAlert is the Action alert every current Partner receives when a
// pending invitation they were told about is withdrawn. Like invitationAlert
// it carries its own Activity destination, because a current Partner is sent
// to the chronology rather than to a link that no longer works.
type cancellationAlert struct {
	ProtecteeEmail string
	InviteeEmail   string
	CanceledAt     time.Time
	ActivityURL    string
}

// renderInvitationCanceledToPartner produces the approved cancellation alert
// for one current Partner. They were told the invitation was sent (S3-20), so
// they are told it ended; the body carries the integrity footer their alerts
// always use, and never another recipient's address.
func renderInvitationCanceledToPartner(message cancellationAlert, recipient string) preparedMessage {
	text := fmt.Sprintf(`%[1]s canceled the pending Partner invitation to
%[2]s on %[3]s.

No Partnership was created and no Partner powers were granted.

Review Activity:
%[4]s

This is an integrity message from Nightloch. Replies go to the monitored
address %[5]s.`,
		message.ProtecteeEmail, message.InviteeEmail, utcStamp(message.CanceledAt),
		message.ActivityURL, alertsAddress)

	return preparedMessage{
		Kind: MessageInvitationCanceledPartner,
		Envelope: mailmessage.Envelope{
			To: recipient,
			Subject: fmt.Sprintf("[Nightloch] %s canceled the invitation to %s",
				message.ProtecteeEmail, message.InviteeEmail),
			Text:    text,
			HTML:    textToHTML(text),
			ReplyTo: alertsAddress,
		},
	}
}
