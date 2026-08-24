package webapp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// livePartnership drives the whole HTTP invitation flow to a live Partnership
// and returns both parties' credentials plus its id.
func livePartnership(t *testing.T, env *testEnv) (protectee, protecteeCSRF, partner, partnerCSRF *http.Cookie, partnershipID int64) {
	t.Helper()
	protectee, protecteeCSRF = signIn(t, env, "protectee@example.com")
	invite := url.Values{
		"invitee_email": []string{"partner@example.com"},
		"log_sharing":   []string{"on"},
		"csrf_token":    []string{protecteeCSRF.Value},
	}
	if recorder := post(t, env, "https://lite.nightloch.app/partners/invite", invite,
		protectee, protecteeCSRF); recorder.Code != http.StatusSeeOther {
		t.Fatalf("POST /partners/invite = %d", recorder.Code)
	}
	if _, err := env.partnerships.DispatchDue(context.Background(), env.sender, 10); err != nil {
		t.Fatal(err)
	}
	reviewURL := reviewLinkPattern.FindString(env.sender.Envelopes[len(env.sender.Envelopes)-1].Text)
	if reviewURL == "" {
		t.Fatal("no review link in the invitation")
	}
	partner, partnerCSRF = signIn(t, env, "partner@example.com")
	accept := post(t, env, reviewURL+"/accept",
		url.Values{"csrf_token": []string{partnerCSRF.Value}}, partner, partnerCSRF)
	if accept.Code != http.StatusSeeOther {
		t.Fatalf("acceptance = %d", accept.Code)
	}
	// Drain the acceptance mail so an ending test counts only its own.
	if _, err := env.partnerships.DispatchDue(context.Background(), env.sender, 10); err != nil {
		t.Fatal(err)
	}
	if err := env.db.QueryRow("SELECT id FROM partnerships WHERE ended_at IS NULL").Scan(&partnershipID); err != nil {
		t.Fatal(err)
	}
	return protectee, protecteeCSRF, partner, partnerCSRF, partnershipID
}

func TestPartnerEndsTheirPartnershipImmediatelyWithATypedConfirmation(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	ctx := context.Background()
	protecteeSession, protecteeCSRF, partnerSession, partnerCSRF, partnershipID := livePartnership(t, env)
	endURL := fmt.Sprintf("https://lite.nightloch.app/partners/partnerships/%d/end", partnershipID)
	receiptURL := fmt.Sprintf("https://lite.nightloch.app/partners/ended-partnerships/%d", partnershipID)

	// The Partner sees the control; nobody else does.
	partnerDashboard := get(t, env, "https://lite.nightloch.app/", partnerSession, partnerCSRF).Body.String()
	if !strings.Contains(partnerDashboard, "End this Partnership") {
		t.Fatalf("Partner dashboard has no ending control:\n%s", partnerDashboard)
	}
	protecteeDashboard := get(t, env, "https://lite.nightloch.app/", protecteeSession, protecteeCSRF).Body.String()
	if strings.Contains(protecteeDashboard, "End this Partnership") {
		t.Fatalf("the Protectee was offered the Partner's ending control:\n%s", protecteeDashboard)
	}

	strangerSession, strangerCSRF := signIn(t, env, "stranger@example.com")
	for _, refused := range []struct {
		name          string
		session, csrf *http.Cookie
	}{
		{"the Protectee", protecteeSession, protecteeCSRF},
		{"another User", strangerSession, strangerCSRF},
	} {
		if recorder := get(t, env, endURL, refused.session, refused.csrf); recorder.Code != http.StatusNotFound {
			t.Fatalf("%s reached the ending confirmation: %d", refused.name, recorder.Code)
		}
		recorder := post(t, env, endURL, url.Values{
			"confirmation": []string{"END"}, "csrf_token": []string{refused.csrf.Value},
		}, refused.session, refused.csrf)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s ended someone else's Partnership: %d", refused.name, recorder.Code)
		}
	}
	if allowed, err := env.partnerships.CanLoosen(ctx,
		userID(t, env, "partner@example.com"), userID(t, env, "protectee@example.com")); err != nil || !allowed {
		t.Fatalf("a refused ending removed Partner powers: %v, %v", allowed, err)
	}

	confirm := get(t, env, endURL, partnerSession, partnerCSRF)
	confirmBody := confirm.Body.String()
	if confirm.Code != http.StatusOK {
		t.Fatalf("ending confirmation = %d", confirm.Code)
	}
	for _, want := range []string{
		"End this Partnership now?", "protectee@example.com",
		"takes effect immediately and cannot be undone",
		"will have 0 active Partners", "loosen their own Policy",
		"read-only receipt", "Type END to confirm",
	} {
		if !strings.Contains(confirmBody, want) {
			t.Fatalf("ending confirmation missing %q:\n%s", want, confirmBody)
		}
	}

	// The typed confirmation is load-bearing: without it nothing changes.
	misspelled := post(t, env, endURL, url.Values{
		"confirmation": []string{"end"}, "csrf_token": []string{partnerCSRF.Value},
	}, partnerSession, partnerCSRF)
	if misspelled.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(misspelled.Body.String(), "type END to end this Partnership") {
		t.Fatalf("misspelled confirmation = %d:\n%s", misspelled.Code, misspelled.Body.String())
	}
	var live int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM partnerships WHERE ended_at IS NULL").Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatalf("a misspelled confirmation ended %d Partnerships", 1-live)
	}
	if recorder := post(t, env, endURL, url.Values{"confirmation": []string{"END"}},
		partnerSession); recorder.Code != http.StatusForbidden {
		t.Fatalf("ending without CSRF = %d", recorder.Code)
	}

	ended := post(t, env, endURL, url.Values{
		"confirmation": []string{"END"}, "csrf_token": []string{partnerCSRF.Value},
	}, partnerSession, partnerCSRF)
	endedBody := ended.Body.String()
	if ended.Code != http.StatusOK {
		t.Fatalf("ending = %d:\n%s", ended.Code, endedBody)
	}
	for _, want := range []string{
		"You ended your Partnership with protectee@example.com",
		"You can no longer change their Policy",
		"read-only receipt",
	} {
		if !strings.Contains(endedBody, want) {
			t.Fatalf("ending receipt missing %q:\n%s", want, endedBody)
		}
	}

	// Powers, Roster, and Activity access go at the ending instant.
	partnerID, protecteeID := userID(t, env, "partner@example.com"), userID(t, env, "protectee@example.com")
	for _, check := range []struct {
		name string
		got  func() (bool, error)
		want bool
	}{
		{"former Partner may not loosen", func() (bool, error) { return env.partnerships.CanLoosen(ctx, partnerID, protecteeID) }, false},
		{"Protectee loosens again", func() (bool, error) { return env.partnerships.CanLoosen(ctx, protecteeID, protecteeID) }, true},
		{"former Partner loses log access", func() (bool, error) { return env.partnerships.CanViewLogs(ctx, partnerID, protecteeID) }, false},
	} {
		got, err := check.got()
		if err != nil || got != check.want {
			t.Fatalf("%s = %v, %v; want %v", check.name, got, err, check.want)
		}
	}

	afterEnding := get(t, env, "https://lite.nightloch.app/", partnerSession, partnerCSRF).Body.String()
	if strings.Contains(afterEnding, "People you protect") || strings.Contains(afterEnding, "Their Roster") {
		t.Fatalf("a former Partner still sees the Roster:\n%s", afterEnding)
	}
	if !strings.Contains(afterEnding, "Partnerships you ended") ||
		!strings.Contains(afterEnding, fmt.Sprintf("/partners/ended-partnerships/%d", partnershipID)) {
		t.Fatalf("a former Partner lost their receipt:\n%s", afterEnding)
	}

	protecteeAfter := get(t, env, "https://lite.nightloch.app/", protecteeSession, protecteeCSRF).Body.String()
	for _, want := range []string{
		"Partner lock is inactive", "you can still loosen your own Policy", "Add a Partner",
	} {
		if !strings.Contains(protecteeAfter, want) {
			t.Fatalf("Protectee zero-Partner state missing %q:\n%s", want, protecteeAfter)
		}
	}
	if strings.Contains(protecteeAfter, "Your Partners") {
		t.Fatalf("the ended Partner still appears on the Roster:\n%s", protecteeAfter)
	}

	// The receipt is read-only, authorized to the former Partner alone.
	receipt := get(t, env, receiptURL, partnerSession, partnerCSRF)
	receiptBody := receipt.Body.String()
	if receipt.Code != http.StatusOK {
		t.Fatalf("receipt = %d", receipt.Code)
	}
	for _, want := range []string{
		"Ended Partnership", "protectee@example.com",
		"You ended the Partnership immediately.", "Log sharing",
	} {
		if !strings.Contains(receiptBody, want) {
			t.Fatalf("receipt missing %q:\n%s", want, receiptBody)
		}
	}
	if strings.Contains(receiptBody, "End this Partnership") {
		t.Fatalf("the receipt still offers a mutation:\n%s", receiptBody)
	}
	for _, viewer := range []struct {
		name          string
		session, csrf *http.Cookie
	}{
		{"the Protectee", protecteeSession, protecteeCSRF},
		{"another User", strangerSession, strangerCSRF},
	} {
		if recorder := get(t, env, receiptURL, viewer.session, viewer.csrf); recorder.Code != http.StatusNotFound {
			t.Fatalf("%s read the former Partner's receipt: %d", viewer.name, recorder.Code)
		}
	}

	// Canonical mail: one envelope per party, never a shared recipient list.
	before := len(env.sender.Envelopes)
	if sent, err := env.partnerships.DispatchDue(ctx, env.sender, 10); err != nil || sent != 2 {
		t.Fatalf("DispatchDue after the ending = %d, %v", sent, err)
	}
	endings := map[string]string{}
	for _, envelope := range env.sender.Envelopes[before:] {
		if envelope.Subject != "[Nightloch] partner@example.com ended their Partnership with protectee@example.com" {
			t.Fatalf("ending subject = %q", envelope.Subject)
		}
		if envelope.ReplyTo != "alerts@nightloch.app" {
			t.Fatalf("ending reply-to = %q", envelope.ReplyTo)
		}
		endings[envelope.To] = envelope.Text
	}
	if len(endings) != 2 || endings["protectee@example.com"] == "" || endings["partner@example.com"] == "" {
		t.Fatalf("ending recipients = %v", endings)
	}
	for recipient, text := range endings {
		for _, want := range []string{
			"partner@example.com ended the Partnership immediately.",
			"protectee@example.com now has 0 active Partner(s).",
			"The Partner lock is now inactive. protectee@example.com can loosen their own Policy.",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s ending message missing %q:\n%s", recipient, want, text)
			}
		}
	}
	// The former Partner's copy links the receipt they keep, not the Roster
	// they just lost — and that link is the route this app actually serves.
	if !strings.Contains(endings["partner@example.com"],
		"You keep a read-only receipt for this ended Partnership:\n"+receiptURL) {
		t.Fatalf("former Partner ending message = %q", endings["partner@example.com"])
	}
	if strings.Contains(endings["partner@example.com"], "Review the Roster and Activity") {
		t.Fatalf("the former Partner was emailed a Roster link:\n%s", endings["partner@example.com"])
	}
	if !strings.Contains(endings["protectee@example.com"], "Review the Roster and Activity") {
		t.Fatalf("Protectee ending message lost its Roster link:\n%s", endings["protectee@example.com"])
	}
	if followed := get(t, env, receiptURL, partnerSession, partnerCSRF); followed.Code != http.StatusOK {
		t.Fatalf("the emailed receipt link answered %d", followed.Code)
	}

	// Activity records the ending exactly once, and a replay adds nothing.
	replay := post(t, env, endURL, url.Values{
		"confirmation": []string{"END"}, "csrf_token": []string{partnerCSRF.Value},
	}, partnerSession, partnerCSRF)
	if replay.Code != http.StatusConflict ||
		!strings.Contains(replay.Body.String(), "You ended your Partnership with protectee@example.com") {
		t.Fatalf("replayed ending = %d:\n%s", replay.Code, replay.Body.String())
	}
	var activity, jobs int
	if err := env.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM activity_entries WHERE action = 'partnership_ended_by_partner'),
		(SELECT COUNT(*) FROM mail_delivery_jobs WHERE message_kind = 'partnership_ended')`).
		Scan(&activity, &jobs); err != nil {
		t.Fatal(err)
	}
	if activity != 1 || jobs != 2 {
		t.Fatalf("replayed ending duplicated work: activity=%d jobs=%d", activity, jobs)
	}
}
