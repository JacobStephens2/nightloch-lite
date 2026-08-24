package webapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// signIn drives the real magic-link flow so the test holds the same cookies a
// browser would.
func signIn(t *testing.T, env *testEnv, email string) (session, csrf *http.Cookie) {
	t.Helper()
	get := httptest.NewRequest(http.MethodGet, "https://lite.nightloch.app/sign-in", nil)
	get.RemoteAddr = "127.0.0.1:32100"
	getRecorder := httptest.NewRecorder()
	env.app.ServeHTTP(getRecorder, get)
	csrf = cookieNamed(t, getRecorder.Result().Cookies(), csrfCookieName)

	form := url.Values{
		"email":                 []string{email},
		"csrf_token":            []string{csrf.Value},
		"cf-turnstile-response": []string{"human"},
	}
	post := httptest.NewRequest(http.MethodPost, "https://lite.nightloch.app/sign-in", strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.RemoteAddr = "127.0.0.1:32100"
	post.AddCookie(csrf)
	env.app.ServeHTTP(httptest.NewRecorder(), post)

	last := env.sender.Envelopes[len(env.sender.Envelopes)-1]
	link := strings.TrimPrefix(last.Text, "Sign in to Nightloch (this link expires in 15 minutes and can be used once):\n\n")
	consumeRecorder := httptest.NewRecorder()
	env.app.ServeHTTP(consumeRecorder, httptest.NewRequest(http.MethodGet, link, nil))
	return cookieNamed(t, consumeRecorder.Result().Cookies(), sessionCookieName), csrf
}

func TestProtecteeSendsFirstPartnerInvitationEndToEnd(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	session, csrf := signIn(t, env, "protectee@example.com")

	dashboard := httptest.NewRequest(http.MethodGet, "https://lite.nightloch.app/", nil)
	dashboard.AddCookie(session)
	dashboard.AddCookie(csrf)
	dashboardRecorder := httptest.NewRecorder()
	env.app.ServeHTTP(dashboardRecorder, dashboard)
	body := dashboardRecorder.Body.String()
	if dashboardRecorder.Code != http.StatusOK {
		t.Fatalf("GET / = %d", dashboardRecorder.Code)
	}
	for _, want := range []string{"Your protection", "Partner lock is inactive", "Add a Partner", "/partners/invite"} {
		if !strings.Contains(body, want) {
			t.Fatalf("zero state missing %q:\n%s", want, body)
		}
	}
	if len(env.sender.Envelopes) != 1 {
		t.Fatalf("zero state sent unsolicited mail: %+v", env.sender.Envelopes)
	}

	form := httptest.NewRequest(http.MethodGet, "https://lite.nightloch.app/partners/invite", nil)
	form.AddCookie(session)
	form.AddCookie(csrf)
	formRecorder := httptest.NewRecorder()
	env.app.ServeHTTP(formRecorder, form)
	formBody := formRecorder.Body.String()
	if formRecorder.Code != http.StatusOK {
		t.Fatalf("GET /partners/invite = %d", formRecorder.Code)
	}
	for _, want := range []string{
		"Share my DNS domain log with this Partner. I can turn this off immediately; Nightloch will tell them.",
		"Every Partner can act independently. Only add someone you trust.",
	} {
		if !strings.Contains(formBody, want) {
			t.Fatalf("invitation form missing %q:\n%s", want, formBody)
		}
	}
	if strings.Contains(formBody, "checked") {
		t.Fatalf("Log sharing did not default off:\n%s", formBody)
	}

	withoutCSRF := httptest.NewRequest(http.MethodPost, "https://lite.nightloch.app/partners/invite",
		strings.NewReader("invitee_email=invitee%40example.com"))
	withoutCSRF.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	withoutCSRF.AddCookie(session)
	withoutCSRFRecorder := httptest.NewRecorder()
	env.app.ServeHTTP(withoutCSRFRecorder, withoutCSRF)
	if withoutCSRFRecorder.Code != http.StatusForbidden {
		t.Fatalf("POST /partners/invite without CSRF = %d", withoutCSRFRecorder.Code)
	}

	invite := url.Values{
		"invitee_email": []string{"invitee@example.com"},
		"log_sharing":   []string{"on"},
		"csrf_token":    []string{csrf.Value},
	}
	post := httptest.NewRequest(http.MethodPost, "https://lite.nightloch.app/partners/invite", strings.NewReader(invite.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(session)
	post.AddCookie(csrf)
	postRecorder := httptest.NewRecorder()
	env.app.ServeHTTP(postRecorder, post)
	if postRecorder.Code != http.StatusSeeOther || postRecorder.Header().Get("Location") != "/" {
		t.Fatalf("POST /partners/invite = %d location=%q body=%q",
			postRecorder.Code, postRecorder.Header().Get("Location"), postRecorder.Body.String())
	}

	var invitations, queued int
	if err := env.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM partner_invitations WHERE state = 'pending'),
		(SELECT COUNT(*) FROM mail_delivery_jobs WHERE state = 'queued')`).Scan(&invitations, &queued); err != nil {
		t.Fatal(err)
	}
	if invitations != 1 || queued != 1 {
		t.Fatalf("durable invitation=%d queued delivery=%d, want 1/1", invitations, queued)
	}
	if len(env.sender.Envelopes) != 1 {
		t.Fatal("the request thread transmitted mail instead of queuing it")
	}

	after := httptest.NewRequest(http.MethodGet, "https://lite.nightloch.app/", nil)
	after.AddCookie(session)
	after.AddCookie(csrf)
	afterRecorder := httptest.NewRecorder()
	env.app.ServeHTTP(afterRecorder, after)
	afterBody := afterRecorder.Body.String()
	for _, want := range []string{
		"invitee@example.com", "Partner lock is inactive", "acceptance is required",
		// A pending invitation caps nothing, so Add a Partner stays available
		// alongside it.
		"Add a Partner", "Delivery: Queued",
	} {
		if !strings.Contains(afterBody, want) {
			t.Fatalf("pending state missing %q:\n%s", want, afterBody)
		}
	}

	dispatched, err := env.partnerships.DispatchDue(context.Background(), env.sender, 10)
	if err != nil || dispatched != 1 {
		t.Fatalf("DispatchDue = %d, %v", dispatched, err)
	}
	envelope := env.sender.Envelopes[len(env.sender.Envelopes)-1]
	if envelope.To != "invitee@example.com" || envelope.ReplyTo != "protectee@example.com" {
		t.Fatalf("invitation routing = %+v", envelope)
	}
	if envelope.Subject != "protectee@example.com invited you to be their Nightloch Partner" {
		t.Fatalf("invitation subject = %q", envelope.Subject)
	}
	for _, want := range []string{
		"protectee@example.com invited you to become their Partner in Nightloch.",
		"Log sharing: On — you will receive domain-log access after acceptance.",
		"https://lite.nightloch.app/partners/invitations/",
		"Replies to this email go directly to protectee@example.com.",
	} {
		if !strings.Contains(envelope.Text, want) {
			t.Fatalf("canonical invitation missing %q:\n%s", want, envelope.Text)
		}
	}
	if !strings.Contains(envelope.HTML, "<p>") || strings.Contains(envelope.HTML, "<img") {
		t.Fatalf("invitation HTML part = %q", envelope.HTML)
	}
}

func TestPartnerInvitationRejectsSelfInvitationWithoutQueuingMail(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	session, csrf := signIn(t, env, "protectee@example.com")

	invite := url.Values{
		"invitee_email": []string{"Protectee@Example.com"},
		"csrf_token":    []string{csrf.Value},
	}
	post := httptest.NewRequest(http.MethodPost, "https://lite.nightloch.app/partners/invite", strings.NewReader(invite.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(session)
	post.AddCookie(csrf)
	recorder := httptest.NewRecorder()
	env.app.ServeHTTP(recorder, post)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("self invitation = %d", recorder.Code)
	}
	var jobs int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM mail_delivery_jobs").Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatalf("rejected invitation queued %d delivery jobs", jobs)
	}
}

func TestPartnerInvitationRequiresAuthentication(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	get := httptest.NewRequest(http.MethodGet, "https://lite.nightloch.app/partners/invite", nil)
	recorder := httptest.NewRecorder()
	env.app.ServeHTTP(recorder, get)
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/sign-in" {
		t.Fatalf("unauthenticated GET /partners/invite = %d location=%q", recorder.Code, recorder.Header().Get("Location"))
	}
}
