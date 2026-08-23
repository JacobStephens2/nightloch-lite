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
	ReviewURL      string
	ExpiresAt      time.Time
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

%[3]s

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
			label := strings.TrimSuffix(strings.Join(prose[:link], " "), ":")
			fmt.Fprintf(&builder, "<p><a href=\"%s\">%s</a></p>",
				html.EscapeString(prose[link]), html.EscapeString(label))
			continue
		}
		if paragraph := strings.TrimSpace(strings.Join(prose, " ")); paragraph != "" {
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

func isBareURL(line string) bool {
	return strings.HasPrefix(line, "https://") && !strings.Contains(line, " ")
}
