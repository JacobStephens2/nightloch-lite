package webapp

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// submitInvitation drives Add a Partner and hands back the response itself,
// because these tests are about the invitations the form refuses as much as
// the ones it accepts.
func submitInvitation(t *testing.T, env *testEnv, session, csrf *http.Cookie, inviteeEmail string) *httptest.ResponseRecorder {
	t.Helper()
	return post(t, env, "https://lite.nightloch.app/partners/invite", url.Values{
		"invitee_email": []string{inviteeEmail},
		"csrf_token":    []string{csrf.Value},
	}, session, csrf)
}

func dashboard(t *testing.T, env *testEnv, session, csrf *http.Cookie) string {
	t.Helper()
	get := httptest.NewRequest(http.MethodGet, "https://lite.nightloch.app/", nil)
	get.AddCookie(session)
	get.AddCookie(csrf)
	recorder := httptest.NewRecorder()
	env.app.ServeHTTP(recorder, get)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET / = %d", recorder.Code)
	}
	return recorder.Body.String()
}

// backdateSends moves every recorded send a day into the past, which is how
// these tests reach a rolling window and a resend interval that the real clock
// would take a day to reach.
func backdateSends(t *testing.T, env *testEnv) {
	t.Helper()
	for _, statement := range []string{
		"UPDATE partner_invitations SET last_sent_at = last_sent_at - 86400",
		"UPDATE partner_invitation_sends SET sent_at = sent_at - 86400",
	} {
		if _, err := env.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSixthPendingInvitationIsRefusedOnTheForm(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	session, csrf := signIn(t, env, "protectee@example.com")

	for index := range 5 {
		recorder := submitInvitation(t, env, session, csrf, "invitee"+string(rune('a'+index))+"@example.com")
		if recorder.Code != http.StatusSeeOther {
			t.Fatalf("invitation %d = %d", index+1, recorder.Code)
		}
	}

	refused := submitInvitation(t, env, session, csrf, "sixth@example.com")
	if refused.Code != http.StatusUnprocessableEntity {
		t.Fatalf("sixth invitation = %d, want 422", refused.Code)
	}
	body := refused.Body.String()
	if !strings.Contains(body, "cancel one before sending another") {
		t.Fatalf("refusal does not name the remedy:\n%s", body)
	}
	// The refusal is shown on the Protectee's own form and never lists who
	// they already invited.
	for index := range 5 {
		if address := "invitee" + string(rune('a'+index)) + "@example.com"; strings.Contains(body, address) {
			t.Fatalf("refusal enumerates %q:\n%s", address, body)
		}
	}

	var pending int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM partner_invitations WHERE state = 'pending'").
		Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 5 {
		t.Fatalf("pending invitations = %d, want 5", pending)
	}
}

func TestProtecteeResendsOnePendingInvitationPerDay(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	session, csrf := signIn(t, env, "protectee@example.com")
	if recorder := submitInvitation(t, env, session, csrf, "invitee@example.com"); recorder.Code != http.StatusSeeOther {
		t.Fatalf("POST /partners/invite = %d", recorder.Code)
	}
	var invitationID int64
	var expiresAt int64
	if err := env.db.QueryRow("SELECT id, expires_at FROM partner_invitations").
		Scan(&invitationID, &expiresAt); err != nil {
		t.Fatal(err)
	}
	action := "https://lite.nightloch.app/partners/sent-invitations/" +
		strconv.FormatInt(invitationID, 10) + "/resend"

	// A just-sent invitation offers no resend control at all; it says when one
	// becomes available instead.
	body := dashboard(t, env, session, csrf)
	if strings.Contains(body, "Resend invitation") {
		t.Fatalf("a just-sent invitation offered a resend:\n%s", body)
	}
	if !strings.Contains(body, "once every 24 hours") {
		t.Fatalf("dashboard does not say when a resend becomes available:\n%s", body)
	}

	backdateSends(t, env)
	body = dashboard(t, env, session, csrf)
	if !strings.Contains(body, "Resend invitation") {
		t.Fatalf("a day-old invitation offered no resend:\n%s", body)
	}

	withoutCSRF := httptest.NewRequest(http.MethodPost, action, nil)
	withoutCSRF.AddCookie(session)
	withoutCSRFRecorder := httptest.NewRecorder()
	env.app.ServeHTTP(withoutCSRFRecorder, withoutCSRF)
	if withoutCSRFRecorder.Code != http.StatusForbidden {
		t.Fatalf("resend without CSRF = %d", withoutCSRFRecorder.Code)
	}

	queuedBefore := queuedJobs(t, env)
	resend := post(t, env, action, url.Values{"csrf_token": []string{csrf.Value}}, session, csrf)
	if resend.Code != http.StatusSeeOther || resend.Header().Get("Location") != "/" {
		t.Fatalf("resend = %d location=%q body=%q", resend.Code,
			resend.Header().Get("Location"), resend.Body.String())
	}
	if got := queuedJobs(t, env) - queuedBefore; got != 1 {
		t.Fatalf("resend queued %d delivery jobs, want exactly 1", got)
	}

	var recipient, kind string
	if err := env.db.QueryRow(`SELECT recipient_email, message_kind FROM mail_delivery_jobs
		ORDER BY id DESC LIMIT 1`).Scan(&recipient, &kind); err != nil {
		t.Fatal(err)
	}
	if recipient != "invitee@example.com" || kind != "partner_invitation_resent" {
		t.Fatalf("resend job = %q/%q, want the invitee's own reminder", recipient, kind)
	}

	var storedExpiry int64
	if err := env.db.QueryRow("SELECT expires_at FROM partner_invitations WHERE id = ?", invitationID).
		Scan(&storedExpiry); err != nil {
		t.Fatal(err)
	}
	if storedExpiry != expiresAt {
		t.Fatalf("resend moved the expiry to %d, want the original %d", storedExpiry, expiresAt)
	}

	body = dashboard(t, env, session, csrf)
	if !strings.Contains(body, "Resending does not extend the expiry above.") {
		t.Fatalf("dashboard does not report the resend:\n%s", body)
	}

	// A second resend inside the same 24 hours is refused on the dashboard and
	// queues nothing.
	queuedBefore = queuedJobs(t, env)
	refused := post(t, env, action, url.Values{"csrf_token": []string{csrf.Value}}, session, csrf)
	if refused.Code != http.StatusUnprocessableEntity {
		t.Fatalf("immediate second resend = %d, want 422", refused.Code)
	}
	if !strings.Contains(refused.Body.String(), "once every 24 hours") {
		t.Fatalf("refusal does not name the interval:\n%s", refused.Body.String())
	}
	if got := queuedJobs(t, env); got != queuedBefore {
		t.Fatalf("a refused resend queued mail: %d, want %d", got, queuedBefore)
	}
}

func TestResendIsRefusedForAnyoneButTheInvitingProtectee(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	session, csrf := signIn(t, env, "protectee@example.com")
	if recorder := submitInvitation(t, env, session, csrf, "invitee@example.com"); recorder.Code != http.StatusSeeOther {
		t.Fatalf("POST /partners/invite = %d", recorder.Code)
	}
	var invitationID int64
	if err := env.db.QueryRow("SELECT id FROM partner_invitations").Scan(&invitationID); err != nil {
		t.Fatal(err)
	}
	backdateSends(t, env)

	strangerSession, strangerCSRF := signIn(t, env, "stranger@example.com")
	queuedBefore := queuedJobs(t, env)
	action := "https://lite.nightloch.app/partners/sent-invitations/" +
		strconv.FormatInt(invitationID, 10) + "/resend"
	recorder := post(t, env, action, url.Values{"csrf_token": []string{strangerCSRF.Value}}, strangerSession, strangerCSRF)
	// The stranger is sent to their own dashboard, which never confirms that
	// somebody else's invitation exists.
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("stranger resend = %d, want 303", recorder.Code)
	}
	if got := queuedJobs(t, env); got != queuedBefore {
		t.Fatalf("stranger resend queued mail: %d, want %d", got, queuedBefore)
	}
}

func queuedJobs(t *testing.T, env *testEnv) int {
	t.Helper()
	var jobs int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM mail_delivery_jobs").Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	return jobs
}
