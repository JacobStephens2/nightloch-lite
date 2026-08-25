package webapp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestActivityWindowsThroughTheHTTPSeam walks the whole viewer matrix over one
// Protectee's chronology: the Protectee reads all of it, their Partner reads
// only from their acceptance, and nobody else reads any of it.
func TestActivityWindowsThroughTheHTTPSeam(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protecteeSession, protecteeCSRF, partnerSession, partnerCSRF, _ := livePartnership(t, env)
	protecteeID := userID(t, env, "protectee@example.com")
	partnerActivityURL := fmt.Sprintf("https://lite.nightloch.app/activity/%d", protecteeID)

	// A later invitation is inside the Partner's window; the invitation that
	// created their own Partnership is not.
	invite := url.Values{
		"invitee_email": []string{"later@example.com"},
		"csrf_token":    []string{protecteeCSRF.Value},
	}
	if recorder := post(t, env, "https://lite.nightloch.app/partners/invite", invite,
		protecteeSession, protecteeCSRF); recorder.Code != http.StatusSeeOther {
		t.Fatalf("POST /partners/invite = %d", recorder.Code)
	}

	own := get(t, env, "https://lite.nightloch.app/activity", protecteeSession, protecteeCSRF)
	if own.Code != http.StatusOK {
		t.Fatalf("GET /activity as the Protectee = %d", own.Code)
	}
	for _, want := range []string{
		"Your Activity",
		"protectee@example.com invited partner@example.com as a Partner.",
		"partner@example.com accepted protectee@example.com’s Partner invitation",
		"protectee@example.com invited later@example.com as a Partner.",
	} {
		if !strings.Contains(own.Body.String(), want) {
			t.Fatalf("the Protectee's own Activity is missing %q:\n%s", want, own.Body.String())
		}
	}

	windowed := get(t, env, partnerActivityURL, partnerSession, partnerCSRF)
	if windowed.Code != http.StatusOK {
		t.Fatalf("GET %s as the Partner = %d", partnerActivityURL, windowed.Code)
	}
	body := windowed.Body.String()
	if !strings.Contains(body, "Activity for protectee@example.com") ||
		!strings.Contains(body, "protectee@example.com invited later@example.com as a Partner.") {
		t.Fatalf("the Partner's window is missing their authorized entries:\n%s", body)
	}
	if strings.Contains(body, "invited partner@example.com as a Partner.") {
		t.Fatalf("the Partner read the invitation from before their acceptance:\n%s", body)
	}

	// An unrelated User and an invitee who has not accepted read nothing, and
	// neither learns that this Protectee exists.
	strangerSession, strangerCSRF := signIn(t, env, "stranger@example.com")
	inviteeSession, inviteeCSRF := signIn(t, env, "later@example.com")
	for _, refused := range []struct {
		name          string
		session, csrf *http.Cookie
	}{
		{"an unrelated signed-in User", strangerSession, strangerCSRF},
		{"a pending invitee", inviteeSession, inviteeCSRF},
	} {
		recorder := get(t, env, partnerActivityURL, refused.session, refused.csrf)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s reached the Activity: %d", refused.name, recorder.Code)
		}
		if strings.Contains(recorder.Body.String(), "protectee@example.com") {
			t.Fatalf("the refusal disclosed the Protectee to %s:\n%s", refused.name, recorder.Body.String())
		}
	}

	// An unauthenticated visitor is sent to sign in and reads nothing.
	anonymous := get(t, env, partnerActivityURL)
	if anonymous.Code != http.StatusSeeOther || anonymous.Header().Get("Location") != "/sign-in" {
		t.Fatalf("unauthenticated GET = %d location=%q", anonymous.Code, anonymous.Header().Get("Location"))
	}
	if strings.Contains(anonymous.Body.String(), "protectee@example.com") {
		t.Fatalf("the unauthenticated response disclosed the Protectee:\n%s", anonymous.Body.String())
	}
}

// TestEndedPartnershipLeavesTheReceiptAndNoActivity proves the two halves of
// the same instant: ongoing Activity and Roster access disappear the moment
// the Partnership ends, and the receipt the former Partner keeps does not.
func TestEndedPartnershipLeavesTheReceiptAndNoActivity(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protecteeSession, protecteeCSRF, partnerSession, partnerCSRF, partnershipID := livePartnership(t, env)
	protecteeID := userID(t, env, "protectee@example.com")
	activityURL := fmt.Sprintf("https://lite.nightloch.app/activity/%d", protecteeID)

	if recorder := get(t, env, activityURL, partnerSession, partnerCSRF); recorder.Code != http.StatusOK {
		t.Fatalf("the active Partner could not read Activity: %d", recorder.Code)
	}
	end := post(t, env, fmt.Sprintf("https://lite.nightloch.app/partners/partnerships/%d/end", partnershipID),
		url.Values{"confirmation": []string{"END"}, "csrf_token": []string{partnerCSRF.Value}},
		partnerSession, partnerCSRF)
	if end.Code != http.StatusOK {
		t.Fatalf("ending the Partnership = %d", end.Code)
	}

	gone := get(t, env, activityURL, partnerSession, partnerCSRF)
	if gone.Code != http.StatusNotFound {
		t.Fatalf("the former Partner still reached Activity: %d", gone.Code)
	}
	receipt := get(t, env, fmt.Sprintf("https://lite.nightloch.app/partners/ended-partnerships/%d", partnershipID),
		partnerSession, partnerCSRF)
	if receipt.Code != http.StatusOK || !strings.Contains(receipt.Body.String(), "protectee@example.com") {
		t.Fatalf("the retained receipt = %d:\n%s", receipt.Code, receipt.Body.String())
	}
	dashboard := get(t, env, "https://lite.nightloch.app/", partnerSession, partnerCSRF).Body.String()
	if strings.Contains(dashboard, "People you protect") {
		t.Fatalf("the former Partner kept Roster access:\n%s", dashboard)
	}

	// The Protectee's own chronology still holds the whole relationship.
	own := get(t, env, "https://lite.nightloch.app/activity", protecteeSession, protecteeCSRF).Body.String()
	if !strings.Contains(own, "partner@example.com ended their Partnership with protectee@example.com immediately.") {
		t.Fatalf("the Protectee's Activity lost the ending:\n%s", own)
	}
}

// TestReturningPartnerReadsANewWindowAndNotTheGap re-partners the same two
// Users after an ending and proves the second window starts fresh.
func TestReturningPartnerReadsANewWindowAndNotTheGap(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	ctx := context.Background()
	protecteeSession, protecteeCSRF, partnerSession, partnerCSRF, partnershipID := livePartnership(t, env)
	protecteeID := userID(t, env, "protectee@example.com")
	activityURL := fmt.Sprintf("https://lite.nightloch.app/activity/%d", protecteeID)

	if end := post(t, env, fmt.Sprintf("https://lite.nightloch.app/partners/partnerships/%d/end", partnershipID),
		url.Values{"confirmation": []string{"END"}, "csrf_token": []string{partnerCSRF.Value}},
		partnerSession, partnerCSRF); end.Code != http.StatusOK {
		t.Fatalf("ending the Partnership = %d", end.Code)
	}

	// The gap: an invitation sent while the Partner held no Partnership.
	gap := url.Values{
		"invitee_email": []string{"gap@example.com"},
		"csrf_token":    []string{protecteeCSRF.Value},
	}
	if recorder := post(t, env, "https://lite.nightloch.app/partners/invite", gap,
		protecteeSession, protecteeCSRF); recorder.Code != http.StatusSeeOther {
		t.Fatalf("gap invitation = %d", recorder.Code)
	}

	// The same two Users partner again, which is a new durable record.
	again := url.Values{
		"invitee_email": []string{"partner@example.com"},
		"csrf_token":    []string{protecteeCSRF.Value},
	}
	if recorder := post(t, env, "https://lite.nightloch.app/partners/invite", again,
		protecteeSession, protecteeCSRF); recorder.Code != http.StatusSeeOther {
		t.Fatalf("second invitation = %d", recorder.Code)
	}
	if _, err := env.partnerships.DispatchDue(ctx, env.sender, 20); err != nil {
		t.Fatal(err)
	}
	reviewURL := reviewLinkPattern.FindString(env.sender.Envelopes[len(env.sender.Envelopes)-1].Text)
	if accept := post(t, env, reviewURL+"/accept",
		url.Values{"csrf_token": []string{partnerCSRF.Value}},
		partnerSession, partnerCSRF); accept.Code != http.StatusSeeOther {
		t.Fatalf("second acceptance = %d", accept.Code)
	}

	var partnerships int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM partnerships").Scan(&partnerships); err != nil {
		t.Fatal(err)
	}
	if partnerships != 2 {
		t.Fatalf("re-partnering produced %d Partnership records, want 2", partnerships)
	}

	body := get(t, env, activityURL, partnerSession, partnerCSRF).Body.String()
	if !strings.Contains(body, "partner@example.com accepted protectee@example.com’s Partner invitation") {
		t.Fatalf("the returning Partner cannot see their new window:\n%s", body)
	}
	for _, hidden := range []string{"gap@example.com", "ended their Partnership"} {
		if strings.Contains(body, hidden) {
			t.Fatalf("the returning Partner read %q from the gap:\n%s", hidden, body)
		}
	}
	// They still keep the receipt for the Partnership that ended.
	receipt := get(t, env, fmt.Sprintf("https://lite.nightloch.app/partners/ended-partnerships/%d", partnershipID),
		partnerSession, partnerCSRF)
	if receipt.Code != http.StatusOK {
		t.Fatalf("the earlier receipt = %d", receipt.Code)
	}
}

// TestDirectIdentifiersDiscloseNothingToUnauthorizedViewers walks the four
// things a direct id could otherwise be used to probe for — Roster, Activity,
// delivery state, and the existence of a receipt — as every viewer who holds
// none of them.
func TestDirectIdentifiersDiscloseNothingToUnauthorizedViewers(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protecteeSession, protecteeCSRF, partnerSession, partnerCSRF, partnershipID := livePartnership(t, env)
	protecteeID := userID(t, env, "protectee@example.com")

	// A receipt exists only once a Partnership has ended, and it belongs to
	// the former Partner alone.
	if end := post(t, env, fmt.Sprintf("https://lite.nightloch.app/partners/partnerships/%d/end", partnershipID),
		url.Values{"confirmation": []string{"END"}, "csrf_token": []string{partnerCSRF.Value}},
		partnerSession, partnerCSRF); end.Code != http.StatusOK {
		t.Fatalf("ending the Partnership = %d", end.Code)
	}

	strangerSession, strangerCSRF := signIn(t, env, "stranger@example.com")
	probes := []struct {
		what string
		url  string
	}{
		{"Activity", fmt.Sprintf("https://lite.nightloch.app/activity/%d", protecteeID)},
		{"the receipt", fmt.Sprintf("https://lite.nightloch.app/partners/ended-partnerships/%d", partnershipID)},
		{"the ending control", fmt.Sprintf("https://lite.nightloch.app/partners/partnerships/%d/end", partnershipID)},
		{"the Cooling-off control", fmt.Sprintf("https://lite.nightloch.app/partners/partnerships/%d/request-end", partnershipID)},
	}
	for _, viewer := range []struct {
		name          string
		session, csrf *http.Cookie
		// ownActivity marks the viewer whose chronology this is, because they
		// are entitled to that one probe and to none of the others.
		ownActivity bool
	}{
		{name: "an unrelated signed-in User", session: strangerSession, csrf: strangerCSRF},
		// The Protectee is a party to the Partnership and still holds no
		// receipt for it; the receipt belongs to the Partner who left.
		{name: "the Protectee", session: protecteeSession, csrf: protecteeCSRF, ownActivity: true},
	} {
		for _, probe := range probes {
			if viewer.ownActivity && probe.what == "Activity" {
				continue
			}
			recorder := get(t, env, probe.url, viewer.session, viewer.csrf)
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("%s reached %s: %d", viewer.name, probe.what, recorder.Code)
			}
			if strings.Contains(recorder.Body.String(), "partner@example.com") {
				t.Fatalf("the refusal disclosed the Partner to %s at %s:\n%s",
					viewer.name, probe.what, recorder.Body.String())
			}
		}
	}

	// Delivery state is a Roster and pending-invitation fact, so it never
	// appears on an unrelated User's own dashboard.
	dashboard := get(t, env, "https://lite.nightloch.app/", strangerSession, strangerCSRF).Body.String()
	for _, hidden := range []string{"protectee@example.com", "partner@example.com", "Delivery:"} {
		if strings.Contains(dashboard, hidden) {
			t.Fatalf("an unrelated User's dashboard disclosed %q:\n%s", hidden, dashboard)
		}
	}
}
