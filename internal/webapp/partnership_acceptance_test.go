package webapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var reviewLinkPattern = regexp.MustCompile(`https://lite\.nightloch\.app/partners/invitations/[A-Za-z0-9_-]+`)

// get issues an authenticated-or-anonymous GET with the supplied cookies.
func get(t *testing.T, env *testEnv, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	for _, cookie := range cookies {
		if cookie != nil {
			request.AddCookie(cookie)
		}
	}
	recorder := httptest.NewRecorder()
	env.app.ServeHTTP(recorder, request)
	return recorder
}

// post issues a form POST with the supplied cookies.
func post(t *testing.T, env *testEnv, target string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range cookies {
		if cookie != nil {
			request.AddCookie(cookie)
		}
	}
	recorder := httptest.NewRecorder()
	env.app.ServeHTTP(recorder, request)
	return recorder
}

// signInReturning drives the magic-link flow while carrying extra cookies, so
// a test can prove where an invitee lands after authenticating.
func signInReturning(t *testing.T, env *testEnv, email string, carried ...*http.Cookie) (session, csrf *http.Cookie, landing string) {
	t.Helper()
	getRecorder := get(t, env, "https://lite.nightloch.app/sign-in", carried...)
	csrf = cookieNamed(t, getRecorder.Result().Cookies(), csrfCookieName)

	form := url.Values{
		"email":                 []string{email},
		"csrf_token":            []string{csrf.Value},
		"cf-turnstile-response": []string{"human"},
	}
	post(t, env, "https://lite.nightloch.app/sign-in", form, append(carried, csrf)...)

	last := env.sender.Envelopes[len(env.sender.Envelopes)-1]
	link := strings.TrimPrefix(last.Text, "Sign in to Nightloch (this link expires in 15 minutes and can be used once):\n\n")
	consume := get(t, env, link, carried...)
	return cookieNamed(t, consume.Result().Cookies(), sessionCookieName), csrf,
		consume.Header().Get("Location")
}

func TestTwoUsersReviewSignInAndAcceptTheFirstPartnership(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	ctx := context.Background()

	protecteeSession, protecteeCSRF := signIn(t, env, "protectee@example.com")
	invite := url.Values{
		"invitee_email": []string{"invitee@example.com"},
		"log_sharing":   []string{"on"},
		"csrf_token":    []string{protecteeCSRF.Value},
	}
	if recorder := post(t, env, "https://lite.nightloch.app/partners/invite", invite,
		protecteeSession, protecteeCSRF); recorder.Code != http.StatusSeeOther {
		t.Fatalf("POST /partners/invite = %d", recorder.Code)
	}
	if _, err := env.partnerships.DispatchDue(ctx, env.sender, 10); err != nil {
		t.Fatal(err)
	}
	invitation := env.sender.Envelopes[len(env.sender.Envelopes)-1]
	reviewURL := reviewLinkPattern.FindString(invitation.Text)
	if reviewURL == "" {
		t.Fatalf("no review link in the invitation:\n%s", invitation.Text)
	}

	anonymous := get(t, env, reviewURL)
	anonymousBody := anonymous.Body.String()
	if anonymous.Code != http.StatusOK {
		t.Fatalf("anonymous review = %d", anonymous.Code)
	}
	for _, want := range []string{
		"Review Partner invitation", "Viewing this page does not accept the invitation.",
		"0 other Partners currently protect protectee@example.com",
		"On — you will receive domain-log access after acceptance.",
		"Every Partner can act independently. Only add someone you trust.",
		"takes 72 hours", "Sign in as i•••e@example.com to respond.",
		"can turn Log sharing off at any time; Nightloch will tell you when they do.",
	} {
		if !strings.Contains(anonymousBody, want) {
			t.Fatalf("review disclosure missing %q:\n%s", want, anonymousBody)
		}
	}
	if strings.Contains(anonymousBody, "invitee@example.com") {
		t.Fatalf("review screen unmasked the invitee:\n%s", anonymousBody)
	}
	if strings.Contains(anonymousBody, "Accept Partnership") {
		t.Fatalf("anonymous reader was offered acceptance:\n%s", anonymousBody)
	}
	var partnerships int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM partnerships").Scan(&partnerships); err != nil {
		t.Fatal(err)
	}
	if partnerships != 0 {
		t.Fatalf("reading the bearer link created %d Partnerships", partnerships)
	}

	strangerSession, strangerCSRF := signIn(t, env, "stranger@example.com")
	stranger := get(t, env, reviewURL, strangerSession, strangerCSRF)
	strangerBody := stranger.Body.String()
	if !strings.Contains(strangerBody,
		"You are signed in as stranger@example.com, but this invitation was sent to i•••e@example.com. Sign out and continue with the invited address.") {
		t.Fatalf("wrong-session guidance missing:\n%s", strangerBody)
	}
	if strings.Contains(strangerBody, "Accept Partnership") {
		t.Fatalf("a wrong signed-in User was offered acceptance:\n%s", strangerBody)
	}
	strangerAccept := post(t, env, reviewURL+"/accept",
		url.Values{"csrf_token": []string{strangerCSRF.Value}}, strangerSession, strangerCSRF)
	if strangerAccept.Code != http.StatusForbidden || !strings.Contains(strangerAccept.Body.String(), "Sign out and continue") {
		t.Fatalf("stranger acceptance = %d:\n%s", strangerAccept.Code, strangerAccept.Body.String())
	}
	// A refused mutation must not plant a return destination in the browser
	// of a User who was never invited.
	for _, cookie := range strangerAccept.Result().Cookies() {
		if cookie.Name == invitationReturnCookie && cookie.Value != "" {
			t.Fatalf("a refused acceptance armed the sign-in return: %+v", cookie)
		}
	}

	// The anonymous read above set the return destination; authenticating as
	// the matching invitee must land back on the review screen.
	returnCookie := cookieNamed(t, anonymous.Result().Cookies(), invitationReturnCookie)
	inviteeSession, inviteeCSRF, landing := signInReturning(t, env, "invitee@example.com", returnCookie)
	if landing != strings.TrimPrefix(reviewURL, "https://lite.nightloch.app") {
		t.Fatalf("sign-in landed on %q, want the review screen", landing)
	}

	matching := get(t, env, reviewURL, inviteeSession, inviteeCSRF)
	matchingBody := matching.Body.String()
	for _, want := range []string{"invitee@example.com", "Accept Partnership"} {
		if !strings.Contains(matchingBody, want) {
			t.Fatalf("matching review missing %q:\n%s", want, matchingBody)
		}
	}

	if recorder := post(t, env, reviewURL+"/accept", url.Values{}, inviteeSession); recorder.Code != http.StatusForbidden {
		t.Fatalf("acceptance without CSRF = %d", recorder.Code)
	}
	accept := post(t, env, reviewURL+"/accept",
		url.Values{"csrf_token": []string{inviteeCSRF.Value}}, inviteeSession, inviteeCSRF)
	if accept.Code != http.StatusSeeOther || accept.Header().Get("Location") != "/" {
		t.Fatalf("acceptance = %d location=%q body=%q",
			accept.Code, accept.Header().Get("Location"), accept.Body.String())
	}

	var live, activity, invitationState int
	var state string
	if err := env.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM partnerships WHERE ended_at IS NULL),
		(SELECT COUNT(*) FROM activity_entries WHERE action = ?),
		(SELECT COUNT(*) FROM mail_delivery_jobs WHERE state = 'queued'),
		(SELECT state FROM partner_invitations)`, "partner_invitation_accepted").
		Scan(&live, &activity, &invitationState, &state); err != nil {
		t.Fatal(err)
	}
	if live != 1 || activity != 1 || invitationState != 2 || state != "accepted" {
		t.Fatalf("acceptance state: partnerships=%d activity=%d queued=%d invitation=%q",
			live, activity, invitationState, state)
	}

	protecteeBody := get(t, env, "https://lite.nightloch.app/", protecteeSession, protecteeCSRF).Body.String()
	for _, want := range []string{
		"Partner lock is active", "Your Partners can loosen your Policy; you cannot.",
		"Your Partners", "invitee@example.com", "Log sharing: On",
	} {
		if !strings.Contains(protecteeBody, want) {
			t.Fatalf("Protectee zero-to-one state missing %q:\n%s", want, protecteeBody)
		}
	}
	if strings.Contains(protecteeBody, "Partner invitations") {
		t.Fatalf("an accepted invitation still shows as pending:\n%s", protecteeBody)
	}

	partnerBody := get(t, env, "https://lite.nightloch.app/", inviteeSession, inviteeCSRF).Body.String()
	for _, want := range []string{
		"People you protect", "protectee@example.com",
		"On — you can view their DNS domain log.", "Their Roster",
		"invitee@example.com — Partner since", "Reciprocal: No",
	} {
		if !strings.Contains(partnerBody, want) {
			t.Fatalf("Partner dashboard missing %q:\n%s", want, partnerBody)
		}
	}
	strangerBody = get(t, env, "https://lite.nightloch.app/", strangerSession, strangerCSRF).Body.String()
	if strings.Contains(strangerBody, "People you protect") || strings.Contains(strangerBody, "protectee@example.com") {
		t.Fatalf("an unrelated User saw the Partnership:\n%s", strangerBody)
	}

	protecteeID := userID(t, env, "protectee@example.com")
	partnerID := userID(t, env, "invitee@example.com")
	strangerID := userID(t, env, "stranger@example.com")
	for _, check := range []struct {
		name             string
		actor, protectee int64
		want             bool
	}{
		{"partnered Protectee denied closed", protecteeID, protecteeID, false},
		{"active Partner permitted", partnerID, protecteeID, true},
		{"stranger denied", strangerID, protecteeID, false},
	} {
		got, err := env.partnerships.CanLoosen(ctx, check.actor, check.protectee)
		if err != nil || got != check.want {
			t.Fatalf("%s: CanLoosen = %v, %v", check.name, got, err)
		}
	}
	logs, err := env.partnerships.CanViewLogs(ctx, partnerID, protecteeID)
	if err != nil || !logs {
		t.Fatalf("CanViewLogs for the accepted Log-sharing choice = %v, %v", logs, err)
	}
	denied, err := env.partnerships.CanViewLogs(ctx, strangerID, protecteeID)
	if err != nil || denied {
		t.Fatalf("CanViewLogs for a stranger = %v, %v", denied, err)
	}

	before := len(env.sender.Envelopes)
	if sent, err := env.partnerships.DispatchDue(ctx, env.sender, 10); err != nil || sent != 2 {
		t.Fatalf("DispatchDue after acceptance = %d, %v", sent, err)
	}
	acceptance := map[string]string{}
	for _, envelope := range env.sender.Envelopes[before:] {
		acceptance[envelope.To] = envelope.Subject + "\n" + envelope.Text
		if envelope.ReplyTo != "alerts@nightloch.app" {
			t.Fatalf("acceptance reply-to = %q", envelope.ReplyTo)
		}
	}
	if len(acceptance) != 2 {
		t.Fatalf("acceptance messages were not separate per recipient: %v", acceptance)
	}
	if !strings.Contains(acceptance["protectee@example.com"], "The Partner lock is now active.") ||
		!strings.Contains(acceptance["protectee@example.com"], "[Nightloch] invitee@example.com now protects protectee@example.com") {
		t.Fatalf("Protectee acceptance message = %q", acceptance["protectee@example.com"])
	}
	if !strings.Contains(acceptance["invitee@example.com"], "You now protect protectee@example.com with Nightloch") ||
		!strings.Contains(acceptance["invitee@example.com"], "has 0 other active Partner(s)") {
		t.Fatalf("Partner acceptance message = %q", acceptance["invitee@example.com"])
	}
	// The full Roster stays behind authentication; email only counts it.
	if strings.Contains(acceptance["invitee@example.com"], "Their Roster") {
		t.Fatalf("acceptance email carried the Roster: %q", acceptance["invitee@example.com"])
	}

	replay := post(t, env, reviewURL+"/accept",
		url.Values{"csrf_token": []string{inviteeCSRF.Value}}, inviteeSession, inviteeCSRF)
	if replay.Code != http.StatusConflict {
		t.Fatalf("replayed acceptance = %d", replay.Code)
	}
	if err := env.db.QueryRow("SELECT COUNT(*) FROM partnerships").Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatalf("replayed acceptance created %d Partnerships", live)
	}
}

func TestUnknownInvitationTokenRevealsOnlyThatItIsUnavailable(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	for _, token := range []string{"not-a-token", strings.Repeat("A", 43)} {
		recorder := get(t, env, "https://lite.nightloch.app/partners/invitations/"+token)
		if recorder.Code != http.StatusNotFound ||
			!strings.Contains(recorder.Body.String(), "That invitation is unavailable") {
			t.Fatalf("token %q = %d:\n%s", token, recorder.Code, recorder.Body.String())
		}
	}
}

func userID(t *testing.T, env *testEnv, email string) int64 {
	t.Helper()
	var id int64
	if err := env.db.QueryRow("SELECT id FROM users WHERE email = ?", email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
