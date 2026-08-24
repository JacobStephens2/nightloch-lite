package webapp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// deliveredTo collects the envelopes each recipient received after index,
// failing when one signal reaches the same recipient twice.
func deliveredTo(t *testing.T, env *testEnv, since int) map[string]string {
	t.Helper()
	if _, err := env.partnerships.DispatchDue(context.Background(), env.sender, 20); err != nil {
		t.Fatal(err)
	}
	messages := map[string]string{}
	for _, envelope := range env.sender.Envelopes[since:] {
		if existing, repeated := messages[envelope.To]; repeated &&
			strings.Contains(existing, envelope.Subject) {
			t.Fatalf("%q reached %s twice", envelope.Subject, envelope.To)
		}
		messages[envelope.To] += envelope.Subject + "\n" + envelope.Text + "\n"
	}
	return messages
}

func partnershipIDFor(t *testing.T, env *testEnv, protecteeEmail, partnerEmail string) int64 {
	t.Helper()
	var id int64
	if err := env.db.QueryRow(`SELECT partnerships.id FROM partnerships
		JOIN users protectee ON protectee.id = partnerships.protectee_id
		JOIN users partner ON partner.id = partnerships.partner_id
		WHERE protectee.email = ? AND partner.email = ? AND partnerships.ended_at IS NULL`,
		protecteeEmail, partnerEmail).Scan(&id); err != nil {
		t.Fatalf("no live Partnership %s -> %s: %v", partnerEmail, protecteeEmail, err)
	}
	return id
}

// TestThreeUsersReachTheFullRosterThroughASecondPartnership is the 3B gate: a
// Protectee who already has one Partner adds a second one end to end, and
// every recipient, disclosure, and control is checked against the approved
// recipient matrix at each step.
func TestThreeUsersReachTheFullRosterThroughASecondPartnership(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	ctx := context.Background()

	protecteeSession, protecteeCSRF := signIn(t, env, "protectee@example.com")
	firstReview, _ := sendInvitation(t, env, protecteeSession, protecteeCSRF, "first@example.com", true)
	firstSession, firstCSRF := signIn(t, env, "first@example.com")
	if recorder := post(t, env, firstReview+"/accept",
		url.Values{"csrf_token": []string{firstCSRF.Value}}, firstSession, firstCSRF); recorder.Code != http.StatusSeeOther {
		t.Fatalf("first acceptance = %d", recorder.Code)
	}

	// The 3A gate is lifted: a second invitation is permitted while the first
	// Partnership is live, and it alerts the current Partner separately.
	if _, err := env.partnerships.DispatchDue(ctx, env.sender, 20); err != nil {
		t.Fatal(err)
	}
	beforeInvite := len(env.sender.Envelopes)
	secondReview, _ := sendInvitation(t, env, protecteeSession, protecteeCSRF, "second@example.com", false)
	invitationMail := deliveredTo(t, env, beforeInvite)
	if len(invitationMail) != 2 {
		t.Fatalf("the second invitation reached %v, want the invitee and the current Partner", invitationMail)
	}
	for _, want := range []string{
		"[Nightloch] protectee@example.com invited second@example.com as a Partner",
		"No Partner powers have been granted yet.",
		"Log sharing after acceptance: Off",
		"Reciprocal Partnerships if accepted: No",
		"you cannot accept, cancel,\nor decline it for either person",
	} {
		if !strings.Contains(invitationMail["first@example.com"], want) {
			t.Fatalf("prior-Partner alert missing %q:\n%s", want, invitationMail["first@example.com"])
		}
	}
	if strings.Contains(invitationMail["second@example.com"], "first@example.com") {
		t.Fatalf("the invitation leaked the Roster to the invitee:\n%s", invitationMail["second@example.com"])
	}
	if !strings.Contains(invitationMail["second@example.com"], "currently has 1 active Partner(s)") {
		t.Fatalf("the invitee was not told the Partner count:\n%s", invitationMail["second@example.com"])
	}

	// The current Partner inspects the pending invitation in full, and holds
	// no control over it.
	priorPartnerBody := get(t, env, "https://lite.nightloch.app/", firstSession, firstCSRF).Body.String()
	for _, want := range []string{
		"Invitations protectee@example.com sent",
		"second@example.com", "Invited by protectee@example.com",
		"Log sharing if accepted: Off", "Reciprocal Partnership if accepted: No",
		"Delivery: Sent",
		"You can observe this invitation, but you cannot accept, cancel, or decline it.",
	} {
		if !strings.Contains(priorPartnerBody, want) {
			t.Fatalf("prior Partner's view of the pending invitation missing %q:\n%s", want, priorPartnerBody)
		}
	}
	for _, forbidden := range []string{"/cancel", "Cancel invitation", "Accept Partnership", "Decline invitation"} {
		if strings.Contains(priorPartnerBody, forbidden) {
			t.Fatalf("prior Partner was offered %q over someone else's invitation:\n%s", forbidden, priorPartnerBody)
		}
	}

	// Before acceptance the invitee still sees only a count, never identities.
	secondSession, secondCSRF := signIn(t, env, "second@example.com")
	reviewBody := get(t, env, secondReview, secondSession, secondCSRF).Body.String()
	if !strings.Contains(reviewBody, "1 other Partner(s) currently protect protectee@example.com") {
		t.Fatalf("review disclosure missing the existing-Partner count:\n%s", reviewBody)
	}
	if strings.Contains(reviewBody, "first@example.com") {
		t.Fatalf("the review screen disclosed a Roster identity before acceptance:\n%s", reviewBody)
	}
	if !strings.Contains(reviewBody, "Every Partner can act independently. Only add someone you trust.") {
		t.Fatalf("review screen missing the approved trust warning:\n%s", reviewBody)
	}

	beforeAccept := len(env.sender.Envelopes)
	if recorder := post(t, env, secondReview+"/accept",
		url.Values{"csrf_token": []string{secondCSRF.Value}}, secondSession, secondCSRF); recorder.Code != http.StatusSeeOther {
		t.Fatalf("second acceptance = %d:\n%s", recorder.Code, recorder.Body.String())
	}
	acceptanceMail := deliveredTo(t, env, beforeAccept)
	if len(acceptanceMail) != 3 {
		t.Fatalf("acceptance reached %v, want the Protectee, the prior Partner, and the new Partner", acceptanceMail)
	}
	for _, recipient := range []string{"protectee@example.com", "first@example.com"} {
		for _, want := range []string{
			"[Nightloch] second@example.com now protects protectee@example.com",
			"Active Partners now: 2", "The Partner lock remains active.",
		} {
			if !strings.Contains(acceptanceMail[recipient], want) {
				t.Fatalf("acceptance alert to %s missing %q:\n%s", recipient, want, acceptanceMail[recipient])
			}
		}
	}
	if !strings.Contains(acceptanceMail["second@example.com"], "You now protect protectee@example.com with Nightloch") ||
		!strings.Contains(acceptanceMail["second@example.com"], "has 1 other active Partner(s)") {
		t.Fatalf("new Partner acceptance = %q", acceptanceMail["second@example.com"])
	}
	if strings.Contains(acceptanceMail["second@example.com"], "first@example.com") {
		t.Fatalf("the acceptance email leaked the Roster:\n%s", acceptanceMail["second@example.com"])
	}

	// Every active Partner now sees the whole Roster, and only their own
	// Partnership's control.
	firstPartnership := partnershipIDFor(t, env, "protectee@example.com", "first@example.com")
	secondPartnership := partnershipIDFor(t, env, "protectee@example.com", "second@example.com")
	for _, viewer := range []struct {
		name        string
		session     *http.Cookie
		csrf        *http.Cookie
		own, others int64
	}{
		{"first", firstSession, firstCSRF, firstPartnership, secondPartnership},
		{"second", secondSession, secondCSRF, secondPartnership, firstPartnership},
	} {
		body := get(t, env, "https://lite.nightloch.app/", viewer.session, viewer.csrf).Body.String()
		for _, want := range []string{
			"People you protect", "protectee@example.com",
			"first@example.com", "second@example.com",
			"Reciprocal: No", "Reachable",
			"Every Partner can act independently. Only add someone you trust.",
			fmt.Sprintf("/partners/partnerships/%d/end", viewer.own),
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s Partner's Roster missing %q:\n%s", viewer.name, want, body)
			}
		}
		if strings.Contains(body, "/partners/partnerships/"+strconv.FormatInt(viewer.others, 10)) {
			t.Fatalf("%s Partner was offered a control over another Partnership:\n%s", viewer.name, body)
		}
	}

	// The Protectee's own Roster names both Partners and offers Cooling-off
	// on each independently.
	protecteeBody := get(t, env, "https://lite.nightloch.app/", protecteeSession, protecteeCSRF).Body.String()
	for _, want := range []string{
		"Partner lock is active", "first@example.com", "second@example.com",
		fmt.Sprintf("/partners/partnerships/%d/request-end", firstPartnership),
		fmt.Sprintf("/partners/partnerships/%d/request-end", secondPartnership),
		"Every Partner can act independently. Only add someone you trust.",
	} {
		if !strings.Contains(protecteeBody, want) {
			t.Fatalf("Protectee Roster missing %q:\n%s", want, protecteeBody)
		}
	}
	if !strings.Contains(protecteeBody, "Add a Partner") {
		t.Fatalf("Add a Partner did not survive the lifted gate:\n%s", protecteeBody)
	}

	// Both Partners hold the same independent authority over the Protectee.
	protecteeID := userID(t, env, "protectee@example.com")
	for _, partnerEmail := range []string{"first@example.com", "second@example.com"} {
		allowed, err := env.partnerships.CanLoosen(ctx, userID(t, env, partnerEmail), protecteeID)
		if err != nil || !allowed {
			t.Fatalf("%s could not act independently: %v, %v", partnerEmail, allowed, err)
		}
	}
	if allowed, err := env.partnerships.CanLoosen(ctx, protecteeID, protecteeID); err != nil || allowed {
		t.Fatalf("the Protectee kept their own loosen with two Partners: %v, %v", allowed, err)
	}
	// Log sharing stays per Partnership: only the first Partner was granted it.
	if allowed, err := env.partnerships.CanViewLogs(ctx, userID(t, env, "first@example.com"), protecteeID); err != nil || !allowed {
		t.Fatalf("the Log-sharing Partner lost domain-log access: %v, %v", allowed, err)
	}
	if allowed, err := env.partnerships.CanViewLogs(ctx, userID(t, env, "second@example.com"), protecteeID); err != nil || allowed {
		t.Fatalf("a Partner without Log sharing reached the domain log: %v, %v", allowed, err)
	}
}
