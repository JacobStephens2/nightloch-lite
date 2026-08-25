package webapp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// perimeterFixture is one Protectee with a current Partner, a second Partner,
// a pending invitation, and a running Cooling-off, so every state-changing
// route in the app has something real to act on.
type perimeterFixture struct {
	env *testEnv

	protectee, protecteeCSRF *http.Cookie
	partner, partnerCSRF     *http.Cookie
	stranger, strangerCSRF   *http.Cookie
	invitee, inviteeCSRF     *http.Cookie

	partnershipID int64
	invitationID  int64
}

func newPerimeterFixture(t *testing.T) perimeterFixture {
	t.Helper()
	env := newTestEnv(t)
	fixture := perimeterFixture{env: env}
	fixture.protectee, fixture.protecteeCSRF, fixture.partner, fixture.partnerCSRF,
		fixture.partnershipID = livePartnership(t, env)
	fixture.stranger, fixture.strangerCSRF = signIn(t, env, "stranger@example.com")

	_, fixture.invitationID = sendInvitation(t, env,
		fixture.protectee, fixture.protecteeCSRF, "invitee@example.com", false)
	fixture.invitee, fixture.inviteeCSRF = signIn(t, env, "invitee@example.com")
	if recorder := post(t, env,
		fmt.Sprintf("https://lite.nightloch.app/partners/partnerships/%d/request-end", fixture.partnershipID),
		url.Values{"csrf_token": []string{fixture.protecteeCSRF.Value}},
		fixture.protectee, fixture.protecteeCSRF); recorder.Code != http.StatusOK {
		// The Protectee acted, so they read their in-app receipt rather than
		// a redirect: they are never mailed about their own request (S3-20).
		t.Fatalf("request-end = %d", recorder.Code)
	}
	return fixture
}

// mutatingRoute is one state-changing action of the control plane: the POST
// that performs it, the GET that states its consequence first, and the form a
// legitimate actor would submit.
type mutatingRoute struct {
	name string
	path string
	// session and token are the one actor this control belongs to. Every
	// other actor is refused, which is what TestNoActorReachesAControl checks;
	// here the point is that even the right actor is refused without a
	// session, without a token, and without having read the consequence.
	session, token *http.Cookie
	// confirm is the GET that discloses the consequence before it happens. It
	// is empty only where the form itself is the confirmation screen.
	confirm string
	form    url.Values
}

func (f perimeterFixture) mutatingRoutes() []mutatingRoute {
	partnership := strconv.FormatInt(f.partnershipID, 10)
	invitation := strconv.FormatInt(f.invitationID, 10)
	const base = "https://lite.nightloch.app"
	return []mutatingRoute{
		{
			name: "sign out", path: base + "/sign-out",
			session: f.protectee, token: f.protecteeCSRF,
		},
		{
			name: "invite a Partner", path: base + "/partners/invite",
			confirm: base + "/partners/invite",
			form:    url.Values{"invitee_email": {"another@example.com"}},
			session: f.protectee, token: f.protecteeCSRF,
		},
		{
			name: "accept an invitation", path: base + "/partners/received-invitations/" + invitation + "/accept",
			confirm: base + "/partners/received-invitations/" + invitation,
			session: f.invitee, token: f.inviteeCSRF,
		},
		{
			name: "decline an invitation", path: base + "/partners/received-invitations/" + invitation + "/decline",
			confirm: base + "/partners/received-invitations/" + invitation + "/decline",
			session: f.invitee, token: f.inviteeCSRF,
		},
		{
			name: "withdraw an invitation", path: base + "/partners/sent-invitations/" + invitation + "/cancel",
			confirm: base + "/partners/sent-invitations/" + invitation + "/cancel",
			session: f.protectee, token: f.protecteeCSRF,
		},
		{
			name: "resend an invitation", path: base + "/partners/sent-invitations/" + invitation + "/resend",
			confirm: base + "/partners/sent-invitations/" + invitation + "/resend",
			session: f.protectee, token: f.protecteeCSRF,
		},
		{
			name: "end a Partnership", path: base + "/partners/partnerships/" + partnership + "/end",
			confirm: base + "/partners/partnerships/" + partnership + "/end",
			form:    url.Values{"confirmation": {"END"}},
			session: f.partner, token: f.partnerCSRF,
		},
		{
			name: "request an ending", path: base + "/partners/partnerships/" + partnership + "/request-end",
			confirm: base + "/partners/partnerships/" + partnership + "/request-end",
			session: f.protectee, token: f.protecteeCSRF,
		},
		{
			name: "cancel a requested ending", path: base + "/partners/partnerships/" + partnership + "/cancel-end",
			confirm: base + "/partners/partnerships/" + partnership + "/cancel-end",
			session: f.protectee, token: f.protecteeCSRF,
		},
	}
}

// stateFingerprint is every durable fact a request could change, so a test can
// prove a refused or read-only request changed nothing at all rather than
// merely returning the status it expected.
func stateFingerprint(t *testing.T, env *testEnv) string {
	t.Helper()
	var invitations, partnerships, live, activity, jobs, sessions, scheduled int
	if err := env.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM partner_invitations),
		(SELECT COUNT(*) FROM partnerships),
		(SELECT COUNT(*) FROM partnerships WHERE ended_at IS NULL),
		(SELECT COUNT(*) FROM activity_entries),
		(SELECT COUNT(*) FROM mail_delivery_jobs),
		(SELECT COUNT(*) FROM sessions),
		(SELECT COUNT(*) FROM partnerships WHERE cooling_off_ends_at IS NOT NULL)`).
		Scan(&invitations, &partnerships, &live, &activity, &jobs, &sessions, &scheduled); err != nil {
		t.Fatal(err)
	}
	states, err := env.db.Query("SELECT id, state FROM partner_invitations ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer states.Close()
	var detail []string
	for states.Next() {
		var id int64
		var state string
		if err := states.Scan(&id, &state); err != nil {
			t.Fatal(err)
		}
		detail = append(detail, fmt.Sprintf("%d=%s", id, state))
	}
	if err := states.Err(); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("invitations=%d partnerships=%d live=%d activity=%d jobs=%d sessions=%d scheduled=%d %s",
		invitations, partnerships, live, activity, jobs, sessions, scheduled, strings.Join(detail, ","))
}

func TestEveryStateChangingActionIsAuthenticatedCSRFProtectedAndConfirmed(t *testing.T) {
	t.Parallel()
	fixture := newPerimeterFixture(t)
	env := fixture.env

	for _, route := range fixture.mutatingRoutes() {
		before := stateFingerprint(t, env)

		// A GET of the POST path is never the action. Either the route
		// answers a confirmation screen, or the method is refused outright.
		readOnly := get(t, env, route.path, route.session, route.token)
		if readOnly.Code != http.StatusOK && readOnly.Code != http.StatusMethodNotAllowed &&
			readOnly.Code != http.StatusSeeOther {
			t.Errorf("GET %s = %d", route.name, readOnly.Code)
		}

		// Unauthenticated but otherwise well formed: the CSRF token matches
		// its cookie, and only the session is missing. The visitor is sent to
		// sign in rather than told what they nearly did.
		anonymous := post(t, env, route.path, withToken(route.form, route.token.Value), route.token)
		if anonymous.Code != http.StatusSeeOther || anonymous.Header().Get("Location") != "/sign-in" {
			t.Errorf("anonymous POST %s = %d location=%q",
				route.name, anonymous.Code, anonymous.Header().Get("Location"))
		}

		// And with no cookies at all.
		bare := post(t, env, route.path, withToken(route.form, "irrelevant"))
		if bare.Code != http.StatusForbidden {
			t.Errorf("cookieless POST %s = %d", route.name, bare.Code)
		}

		// Authenticated but without a matching CSRF token: refused before the
		// handler ever runs.
		forged := post(t, env, route.path, withToken(route.form, forgedToken), route.session)
		if forged.Code != http.StatusForbidden {
			t.Errorf("forged-CSRF POST %s = %d", route.name, forged.Code)
		}
		missing := post(t, env, route.path, route.form, route.session, route.token)
		if missing.Code != http.StatusForbidden {
			t.Errorf("POST %s without a token = %d", route.name, missing.Code)
		}

		if after := stateFingerprint(t, env); after != before {
			t.Fatalf("%s changed state without an authorized submission:\n%s\n%s", route.name, before, after)
		}

		// The consequence screen exists and is a GET, which is what makes the
		// POST a confirmed action rather than a surprise.
		if route.confirm == "" {
			continue
		}
		if recorder := get(t, env, route.confirm, route.session, route.token); recorder.Code != http.StatusOK &&
			recorder.Code != http.StatusSeeOther {
			t.Errorf("consequence screen for %s = %d", route.name, recorder.Code)
		}
	}
}

const forgedToken = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAY"

func withToken(form url.Values, token string) url.Values {
	filled := url.Values{}
	for key, values := range form {
		filled[key] = values
	}
	filled.Set("csrf_token", token)
	return filled
}

var mailLinkPattern = regexp.MustCompile(`https://lite\.nightloch\.app/\S+`)

func TestNoLinkNightlochEmailsChangesAnythingWhenFollowed(t *testing.T) {
	t.Parallel()
	fixture := newPerimeterFixture(t)
	env := fixture.env
	if _, err := env.partnerships.DispatchDue(context.Background(), env.sender, 50); err != nil {
		t.Fatal(err)
	}

	// Every link Nightloch has ever put in an email, including the bearer
	// links that authorize an invitee. Following one is reading, never acting:
	// a mail client that prefetches must not accept, decline, cancel, or end
	// anything (S3-25).
	links := map[string]bool{}
	for _, envelope := range env.sender.Envelopes {
		for _, link := range mailLinkPattern.FindAllString(envelope.Text, -1) {
			links[strings.TrimRight(link, ".,")] = true
		}
	}
	ordered := make([]string, 0, len(links))
	for link := range links {
		ordered = append(ordered, link)
	}
	sort.Strings(ordered)
	if len(ordered) < 4 {
		t.Fatalf("the scenario produced only %d links to follow: %v", len(ordered), ordered)
	}

	before := stateFingerprint(t, env)
	for _, link := range ordered {
		// Followed by nobody, by the Protectee, by their Partner, by an
		// unrelated signed-in User, and by the invitee the bearer link names.
		for _, follower := range []struct {
			name           string
			session, token *http.Cookie
		}{
			{name: "an unauthenticated visitor"},
			{"the Protectee", fixture.protectee, fixture.protecteeCSRF},
			{"their Partner", fixture.partner, fixture.partnerCSRF},
			{"an unrelated User", fixture.stranger, fixture.strangerCSRF},
		} {
			recorder := get(t, env, link, follower.session, follower.token)
			if recorder.Code >= http.StatusInternalServerError {
				t.Errorf("%s following %s = %d", follower.name, link, recorder.Code)
			}
			if after := stateFingerprint(t, env); after != before {
				t.Fatalf("%s following %s changed state:\n%s\n%s", follower.name, link, before, after)
			}
		}
	}
}

func TestNoActorReachesAControlThatBelongsToSomebodyElse(t *testing.T) {
	t.Parallel()
	fixture := newPerimeterFixture(t)
	env := fixture.env
	partnership := strconv.FormatInt(fixture.partnershipID, 10)
	invitation := strconv.FormatInt(fixture.invitationID, 10)
	const base = "https://lite.nightloch.app"

	// Each control names the one actor it belongs to; every other signed-in
	// User must be refused, and must change nothing by asking.
	for _, attempt := range []struct {
		name string
		path string
		form url.Values
		// want is the refusal this attempt must receive. Every control an
		// actor does not hold answers the same unavailable page as an id that
		// names nobody, so nothing can be inferred from the difference.
		want           int
		session, token *http.Cookie
	}{
		{
			name:    "a Partner requesting their Protectee's own Cooling-off",
			path:    base + "/partners/partnerships/" + partnership + "/request-end",
			want:    http.StatusNotFound,
			session: fixture.partner, token: fixture.partnerCSRF,
		},
		{
			name:    "a Protectee cancelling their own Cooling-off from the Partner's control",
			path:    base + "/partners/partnerships/" + partnership + "/end",
			form:    url.Values{"confirmation": {"END"}},
			want:    http.StatusNotFound,
			session: fixture.protectee, token: fixture.protecteeCSRF,
		},
		{
			name:    "an unrelated User ending somebody's Partnership",
			path:    base + "/partners/partnerships/" + partnership + "/end",
			form:    url.Values{"confirmation": {"END"}},
			want:    http.StatusNotFound,
			session: fixture.stranger, token: fixture.strangerCSRF,
		},
		{
			// A withdrawal nobody may make sends the actor back to their own
			// dashboard, which discloses nothing about the invitation.
			name:    "an unrelated User withdrawing somebody's invitation",
			path:    base + "/partners/sent-invitations/" + invitation + "/cancel",
			want:    http.StatusSeeOther,
			session: fixture.stranger, token: fixture.strangerCSRF,
		},
		{
			name:    "a Partner accepting an invitation addressed to somebody else",
			path:    base + "/partners/received-invitations/" + invitation + "/accept",
			want:    http.StatusNotFound,
			session: fixture.partner, token: fixture.partnerCSRF,
		},
		{
			// Activity has no mutation at all, so there is nothing here for
			// any actor to reach.
			name:    "an unrelated User posting to a Protectee's Activity",
			path:    base + "/activity/" + strconv.FormatInt(protecteeIDOf(t, env), 10),
			want:    http.StatusMethodNotAllowed,
			session: fixture.stranger, token: fixture.strangerCSRF,
		},
	} {
		before := stateFingerprint(t, env)
		recorder := post(t, env, attempt.path, withToken(attempt.form, attempt.token.Value),
			attempt.session, attempt.token)
		if recorder.Code != attempt.want {
			t.Errorf("%s = %d, want %d", attempt.name, recorder.Code, attempt.want)
		}
		if after := stateFingerprint(t, env); after != before {
			t.Fatalf("%s changed state:\n%s\n%s", attempt.name, before, after)
		}
	}
}
