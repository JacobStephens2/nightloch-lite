package webapp

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sendInvitation drives Add a Partner as the signed-in Protectee and returns
// the emailed review link and the invitation's id.
func sendInvitation(t *testing.T, env *testEnv, session, csrf *http.Cookie, inviteeEmail string, logSharing bool) (reviewURL string, invitationID int64) {
	t.Helper()
	form := url.Values{
		"invitee_email": []string{inviteeEmail},
		"csrf_token":    []string{csrf.Value},
	}
	if logSharing {
		form.Set("log_sharing", "on")
	}
	if recorder := post(t, env, "https://lite.nightloch.app/partners/invite", form, session, csrf); recorder.Code != http.StatusSeeOther {
		t.Fatalf("POST /partners/invite = %d", recorder.Code)
	}
	if _, err := env.partnerships.DispatchDue(context.Background(), env.sender, 10); err != nil {
		t.Fatal(err)
	}
	// Current Partners receive their own alert about the same invitation, so
	// the review link is found by recipient rather than by arrival order.
	for _, envelope := range env.sender.Envelopes {
		if envelope.To == inviteeEmail {
			if link := reviewLinkPattern.FindString(envelope.Text); link != "" {
				reviewURL = link
			}
		}
	}
	if reviewURL == "" {
		t.Fatalf("no invitation with a review link reached %s", inviteeEmail)
	}
	if err := env.db.QueryRow(
		"SELECT id FROM partner_invitations WHERE invitee_email = ? ORDER BY id DESC LIMIT 1",
		inviteeEmail).Scan(&invitationID); err != nil {
		t.Fatal(err)
	}
	return reviewURL, invitationID
}

func invitationStateInDB(t *testing.T, env *testEnv, id int64) string {
	t.Helper()
	var state string
	if err := env.db.QueryRow("SELECT state FROM partner_invitations WHERE id = ?", id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func receivedPath(id int64) string {
	return "https://lite.nightloch.app/partners/received-invitations/" + strconv.FormatInt(id, 10)
}

func mustContain(t *testing.T, body string, wanted ...string) {
	t.Helper()
	for _, want := range wanted {
		if !strings.Contains(body, want) {
			t.Fatalf("response missing %q:\n%s", want, body)
		}
	}
}

func TestInviteeFindsAndDeclinesAnInvitationWithoutTheBearerLink(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)

	protecteeSession, protecteeCSRF := signIn(t, env, "protectee@example.com")
	reviewURL, invitationID := sendInvitation(t, env, protecteeSession, protecteeCSRF, "invitee@example.com", true)
	inviteeSession, inviteeCSRF := signIn(t, env, "invitee@example.com")

	// The invitation is discoverable from the invitee's own dashboard.
	dashboard := get(t, env, "https://lite.nightloch.app/", inviteeSession, inviteeCSRF)
	mustContain(t, dashboard.Body.String(),
		"Partner invitations",
		"These Partner invitations were sent to invitee@example.com.",
		"protectee@example.com</strong> invited you to be their Partner.",
		"/partners/received-invitations/"+strconv.FormatInt(invitationID, 10),
	)

	// It carries the same disclosures and the same two controls as the link.
	review := get(t, env, receivedPath(invitationID), inviteeSession, inviteeCSRF)
	if review.Code != http.StatusOK {
		t.Fatalf("GET received review = %d", review.Code)
	}
	mustContain(t, review.Body.String(),
		"Review Partner invitation",
		"Viewing this page does not accept the invitation.",
		"Every Partner can act independently. Only add someone you trust.",
		"On — you will receive domain-log access after acceptance.",
		"Accept Partnership", "Decline invitation",
		receivedPath(invitationID)[len("https://lite.nightloch.app"):]+"/decline",
	)

	// Declining takes one consequence-specific confirmation first (S3-46).
	confirm := get(t, env, receivedPath(invitationID)+"/decline", inviteeSession, inviteeCSRF)
	if confirm.Code != http.StatusOK {
		t.Fatalf("GET decline confirmation = %d", confirm.Code)
	}
	mustContain(t, confirm.Body.String(),
		"Decline this Partner invitation?",
		"creates no Partnership and grants no Partner powers",
		"Nightloch will tell protectee@example.com that you declined.",
		"This cannot be undone.",
		"Keep reading the invitation",
	)
	if invitationStateInDB(t, env, invitationID) != "pending" {
		t.Fatal("the confirmation screen resolved the invitation")
	}

	decline := post(t, env, receivedPath(invitationID)+"/decline",
		url.Values{"csrf_token": []string{inviteeCSRF.Value}}, inviteeSession, inviteeCSRF)
	if decline.Code != http.StatusOK {
		t.Fatalf("POST decline = %d", decline.Code)
	}
	mustContain(t, decline.Body.String(), "You declined this invitation",
		"No Partnership was created and no Partner powers were granted.")
	if state := invitationStateInDB(t, env, invitationID); state != "declined" {
		t.Fatalf("invitation state = %q", state)
	}
	var partnerships int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM partnerships").Scan(&partnerships); err != nil {
		t.Fatal(err)
	}
	if partnerships != 0 {
		t.Fatalf("declining created %d Partnerships", partnerships)
	}

	// The invitee's list empties, and the Protectee learns why.
	after := get(t, env, "https://lite.nightloch.app/", inviteeSession, inviteeCSRF)
	if strings.Contains(after.Body.String(), "invited you to be their Partner") {
		t.Fatalf("the declined invitation is still listed:\n%s", after.Body.String())
	}
	if _, err := env.partnerships.DispatchDue(context.Background(), env.sender, 10); err != nil {
		t.Fatal(err)
	}
	last := env.sender.Envelopes[len(env.sender.Envelopes)-1]
	if last.To != "protectee@example.com" ||
		last.Subject != "invitee@example.com declined your Nightloch Partner invitation" {
		t.Fatalf("decline alert = %q to %q", last.Subject, last.To)
	}

	// The bearer link now reports the precise outcome (S3-29).
	outcome := get(t, env, reviewURL)
	if outcome.Code != http.StatusOK {
		t.Fatalf("GET declined review = %d", outcome.Code)
	}
	mustContain(t, outcome.Body.String(), "This invitation was declined",
		"No Partnership was created and no Partner powers were granted.")
	if strings.Contains(outcome.Body.String(), "invitee@example.com") {
		t.Fatalf("the outcome screen unmasked the invitee:\n%s", outcome.Body.String())
	}
}

func TestProtecteeCancelsAnInvitationAndTheLinkReportsIt(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)

	protecteeSession, protecteeCSRF := signIn(t, env, "protectee@example.com")
	reviewURL, invitationID := sendInvitation(t, env, protecteeSession, protecteeCSRF, "typo@example.com", false)

	// The Protectee's own controls say plainly that correcting an invitation
	// means cancelling it and sending a new one.
	dashboard := get(t, env, "https://lite.nightloch.app/", protecteeSession, protecteeCSRF)
	mustContain(t, dashboard.Body.String(),
		"Invitations you sent",
		"The invited address and the proposed Log sharing cannot be edited.",
		"cancel this invitation and send a new one",
		"/partners/sent-invitations/"+strconv.FormatInt(invitationID, 10)+"/cancel",
	)
	if strings.Contains(dashboard.Body.String(), "Decline invitation") {
		t.Fatalf("the Protectee was offered the invitee's control:\n%s", dashboard.Body.String())
	}

	confirm := get(t, env, "https://lite.nightloch.app/partners/sent-invitations/"+strconv.FormatInt(invitationID, 10)+"/cancel",
		protecteeSession, protecteeCSRF)
	if confirm.Code != http.StatusOK {
		t.Fatalf("GET cancel confirmation = %d", confirm.Code)
	}
	mustContain(t, confirm.Body.String(),
		"Cancel this Partner invitation?",
		"typo@example.com has not answered yet.",
		"the invited address and the proposed Log sharing cannot be edited",
		"Keep the invitation",
	)
	if invitationStateInDB(t, env, invitationID) != "pending" {
		t.Fatal("the confirmation screen canceled the invitation")
	}

	cancel := post(t, env, "https://lite.nightloch.app/partners/sent-invitations/"+strconv.FormatInt(invitationID, 10)+"/cancel",
		url.Values{"csrf_token": []string{protecteeCSRF.Value}}, protecteeSession, protecteeCSRF)
	if cancel.Code != http.StatusOK {
		t.Fatalf("POST cancel = %d", cancel.Code)
	}
	mustContain(t, cancel.Body.String(), "You canceled this invitation",
		"To invite a different address, or to offer different Log sharing, send a new invitation.")
	if state := invitationStateInDB(t, env, invitationID); state != "canceled" {
		t.Fatalf("invitation state = %q", state)
	}
	if _, err := env.partnerships.DispatchDue(context.Background(), env.sender, 10); err != nil {
		t.Fatal(err)
	}
	last := env.sender.Envelopes[len(env.sender.Envelopes)-1]
	if last.To != "typo@example.com" ||
		last.Subject != "protectee@example.com canceled your Nightloch Partner invitation" {
		t.Fatalf("cancellation alert = %q to %q", last.Subject, last.To)
	}

	outcome := get(t, env, reviewURL)
	mustContain(t, outcome.Body.String(), "This invitation was canceled",
		"No Partnership was created and no Partner powers were granted.")

	// Cancelling frees Add a Partner again, which is the whole correction path.
	freed := get(t, env, "https://lite.nightloch.app/", protecteeSession, protecteeCSRF)
	mustContain(t, freed.Body.String(), `href="/partners/invite"`)
}

func TestExpiredInvitationReportsExpiryAndAnswersNothing(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)

	protecteeSession, protecteeCSRF := signIn(t, env, "protectee@example.com")
	reviewURL, invitationID := sendInvitation(t, env, protecteeSession, protecteeCSRF, "invitee@example.com", false)
	inviteeSession, inviteeCSRF := signIn(t, env, "invitee@example.com")

	// Age the invitation past its seven-day window, then run the durable sweep.
	lapsed := time.Now().Add(-time.Hour).Unix()
	if _, err := env.db.Exec(
		"UPDATE partner_invitations SET created_at = ?, expires_at = ? WHERE id = ?",
		lapsed-1, lapsed, invitationID); err != nil {
		t.Fatal(err)
	}
	expired, err := env.partnerships.ExpireDueInvitations(context.Background(), 10)
	if err != nil || expired != 1 {
		t.Fatalf("ExpireDueInvitations = %d, %v", expired, err)
	}
	if state := invitationStateInDB(t, env, invitationID); state != "expired" {
		t.Fatalf("invitation state = %q", state)
	}

	// Expiry is Activity only: no message follows it (S3-20).
	before := len(env.sender.Envelopes)
	if _, err := env.partnerships.DispatchDue(context.Background(), env.sender, 10); err != nil {
		t.Fatal(err)
	}
	if len(env.sender.Envelopes) != before {
		t.Fatalf("expiry sent %d messages", len(env.sender.Envelopes)-before)
	}
	var entries int
	if err := env.db.QueryRow(
		"SELECT COUNT(*) FROM activity_entries WHERE invitation_id = ? AND action = ?",
		invitationID, "partner_invitation_expired").Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 1 {
		t.Fatalf("expiry appended %d Activity entries", entries)
	}

	outcome := get(t, env, reviewURL, inviteeSession, inviteeCSRF)
	mustContain(t, outcome.Body.String(), "This invitation expired",
		"Invitations are valid for seven days and cannot be extended.")
	if strings.Contains(outcome.Body.String(), "Accept Partnership") {
		t.Fatalf("an expired invitation still offered acceptance:\n%s", outcome.Body.String())
	}

	accept := post(t, env, reviewURL+"/accept",
		url.Values{"csrf_token": []string{inviteeCSRF.Value}}, inviteeSession, inviteeCSRF)
	if accept.Code != http.StatusConflict {
		t.Fatalf("accepting an expired invitation = %d", accept.Code)
	}
	var partnerships int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM partnerships").Scan(&partnerships); err != nil {
		t.Fatal(err)
	}
	if partnerships != 0 {
		t.Fatalf("an expired invitation created %d Partnerships", partnerships)
	}
}

func TestUnknownAndMalformedReferencesRevealOnlyUnavailability(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	session, csrf := signIn(t, env, "invitee@example.com")

	const bearer = "https://lite.nightloch.app/partners/invitations/"
	const received = "https://lite.nightloch.app/partners/received-invitations/"
	for _, target := range []string{
		bearer + "not-base64-!!",
		bearer + strings.Repeat("A", 43),
		bearer + "short",
		received + "4242",
		received + "abc",
		received + "0",
	} {
		recorder := get(t, env, target, session, csrf)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", target, recorder.Code)
		}
		mustContain(t, recorder.Body.String(), "That invitation is unavailable")
		for _, leak := range []string{"expired", "declined", "canceled", "accepted"} {
			if strings.Contains(recorder.Body.String(), leak) {
				t.Fatalf("GET %s disclosed %q:\n%s", target, leak, recorder.Body.String())
			}
		}
	}
}

func TestWrongSessionMaySeeAnInvitationButNeverAnswerIt(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)

	protecteeSession, protecteeCSRF := signIn(t, env, "protectee@example.com")
	reviewURL, invitationID := sendInvitation(t, env, protecteeSession, protecteeCSRF, "invitee@example.com", false)
	strangerSession, strangerCSRF := signIn(t, env, "stranger@example.com")

	review := get(t, env, reviewURL, strangerSession, strangerCSRF)
	mustContain(t, review.Body.String(),
		"You are signed in as stranger@example.com, but this invitation was sent to")
	if strings.Contains(review.Body.String(), "Decline invitation") {
		t.Fatalf("a wrong session was offered the invitee's controls:\n%s", review.Body.String())
	}

	form := url.Values{"csrf_token": []string{strangerCSRF.Value}}
	for _, action := range []string{"/accept", "/decline"} {
		recorder := post(t, env, reviewURL+action, form, strangerSession, strangerCSRF)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("POST %s as a stranger = %d, want 403", action, recorder.Code)
		}
		// The id path does not even confirm the invitation exists.
		byID := post(t, env, receivedPath(invitationID)+action, form, strangerSession, strangerCSRF)
		if byID.Code != http.StatusNotFound {
			t.Fatalf("POST received%s as a stranger = %d, want 404", action, byID.Code)
		}
	}
	// Cancellation belongs to the Protectee alone, and a refusal is silent.
	cancel := post(t, env, "https://lite.nightloch.app/partners/sent-invitations/"+strconv.FormatInt(invitationID, 10)+"/cancel",
		form, strangerSession, strangerCSRF)
	if cancel.Code != http.StatusSeeOther {
		t.Fatalf("POST cancel as a stranger = %d", cancel.Code)
	}
	if state := invitationStateInDB(t, env, invitationID); state != "pending" {
		t.Fatalf("a stranger moved the invitation to %q", state)
	}
	if get(t, env, receivedPath(invitationID), strangerSession, strangerCSRF).Code != http.StatusNotFound {
		t.Fatal("a stranger read an invitation by id")
	}
}

func TestOnlyTheFirstAnswerCommitsAndTheRestSeeTheResult(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)

	protecteeSession, protecteeCSRF := signIn(t, env, "protectee@example.com")
	reviewURL, invitationID := sendInvitation(t, env, protecteeSession, protecteeCSRF, "invitee@example.com", false)
	inviteeSession, inviteeCSRF := signIn(t, env, "invitee@example.com")
	inviteeForm := url.Values{"csrf_token": []string{inviteeCSRF.Value}}

	if accept := post(t, env, reviewURL+"/accept", inviteeForm, inviteeSession, inviteeCSRF); accept.Code != http.StatusSeeOther {
		t.Fatalf("POST accept = %d", accept.Code)
	}

	// Every later answer loses: it mutates nothing and lands on the outcome
	// that won, which is the only state its actor is authorized to see.
	for _, action := range []string{"/accept", "/decline"} {
		replay := post(t, env, reviewURL+action, inviteeForm, inviteeSession, inviteeCSRF)
		if replay.Code != http.StatusConflict {
			t.Fatalf("replayed %s = %d, want 409", action, replay.Code)
		}
		mustContain(t, replay.Body.String(), "This invitation was already accepted")
	}
	lateCancel := post(t, env, "https://lite.nightloch.app/partners/sent-invitations/"+strconv.FormatInt(invitationID, 10)+"/cancel",
		url.Values{"csrf_token": []string{protecteeCSRF.Value}}, protecteeSession, protecteeCSRF)
	if lateCancel.Code != http.StatusSeeOther {
		t.Fatalf("late cancel = %d", lateCancel.Code)
	}

	var state string
	var partnerships, entries int
	if err := env.db.QueryRow(`SELECT
		(SELECT state FROM partner_invitations WHERE id = ?1),
		(SELECT COUNT(*) FROM partnerships),
		(SELECT COUNT(*) FROM activity_entries WHERE invitation_id = ?1)`, invitationID).
		Scan(&state, &partnerships, &entries); err != nil {
		t.Fatal(err)
	}
	if state != "accepted" || partnerships != 1 || entries != 2 {
		t.Fatalf("state %q with %d Partnerships and %d Activity entries", state, partnerships, entries)
	}
}

func TestNoInvitationMutationIsReachableByGETOrWithoutCSRF(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)

	protecteeSession, protecteeCSRF := signIn(t, env, "protectee@example.com")
	reviewURL, invitationID := sendInvitation(t, env, protecteeSession, protecteeCSRF, "invitee@example.com", false)
	inviteeSession, inviteeCSRF := signIn(t, env, "invitee@example.com")
	cancelURL := "https://lite.nightloch.app/partners/sent-invitations/" + strconv.FormatInt(invitationID, 10) + "/cancel"

	// Accept has no GET at all; decline and cancel answer a GET with their
	// confirmation screen, which is a pure read like every other GET (S3-28).
	for _, target := range []string{reviewURL + "/accept", receivedPath(invitationID) + "/accept"} {
		if recorder := get(t, env, target, inviteeSession, inviteeCSRF); recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET %s = %d, want 405", target, recorder.Code)
		}
	}
	for _, confirmation := range []struct {
		target  string
		session *http.Cookie
		csrf    *http.Cookie
	}{
		{reviewURL + "/decline", inviteeSession, inviteeCSRF},
		{receivedPath(invitationID) + "/decline", inviteeSession, inviteeCSRF},
		{cancelURL, protecteeSession, protecteeCSRF},
	} {
		recorder := get(t, env, confirmation.target, confirmation.session, confirmation.csrf)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", confirmation.target, recorder.Code)
		}
		mustContain(t, recorder.Body.String(), `method="post"`)
	}

	// Every mutation still refuses a POST without the matching CSRF token.
	for _, target := range []string{
		reviewURL + "/accept", reviewURL + "/decline",
		receivedPath(invitationID) + "/accept", receivedPath(invitationID) + "/decline",
		cancelURL,
	} {
		if recorder := post(t, env, target, url.Values{}, inviteeSession, inviteeCSRF); recorder.Code != http.StatusForbidden {
			t.Fatalf("POST %s without CSRF = %d, want 403", target, recorder.Code)
		}
	}

	// Reading the bearer link itself is still a pure read.
	if recorder := get(t, env, reviewURL); recorder.Code != http.StatusOK {
		t.Fatalf("GET review = %d", recorder.Code)
	}
	if state := invitationStateInDB(t, env, invitationID); state != "pending" {
		t.Fatalf("a GET moved the invitation to %q", state)
	}
	var partnerships int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM partnerships").Scan(&partnerships); err != nil {
		t.Fatal(err)
	}
	if partnerships != 0 {
		t.Fatalf("a GET created %d Partnerships", partnerships)
	}
}
