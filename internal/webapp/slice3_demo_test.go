package webapp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JacobStephens2/nightloch-lite/internal/partnership"
)

// TestSliceThreeRunsTheWholePartnershipPerimeterEndToEnd is the Slice 3
// promotion gate. One Protectee walks the complete perimeter over real HTTP —
// invitation, acceptance, a multi-Partner Roster, concurrent Cooling-off, a
// reachability warning and its recovery, an ended Partnership's receipt, and
// Activity browsing — while an unrelated signed-in User is refused at every
// door the same way an unauthenticated visitor is.
func TestSliceThreeRunsTheWholePartnershipPerimeterEndToEnd(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	ctx := context.Background()

	// 1. Zero state. A Protectee with no Partners may loosen their own Policy.
	protectee, protecteeCSRF := signIn(t, env, "protectee@example.com")
	protecteeID := protecteeIDOf(t, env)
	dashboard := body(t, get(t, env, "https://lite.nightloch.app/", protectee, protecteeCSRF))
	mustContain(t, dashboard, "Partner lock is inactive", "Add a Partner")
	assertLoosen(t, env, protecteeID, protecteeID, true)

	// 2. The first invitation and its acceptance. The bearer link arrives by
	// mail, the invitee signs in, and the Partner lock closes behind them.
	firstReview, _ := sendInvitation(t, env, protectee, protecteeCSRF, "alice@example.com", true)
	alice, aliceCSRF := signIn(t, env, "alice@example.com")
	review := body(t, get(t, env, firstReview, alice, aliceCSRF))
	mustContain(t, review, "protectee@example.com", "Log sharing")
	if recorder := post(t, env, firstReview+"/accept",
		url.Values{"csrf_token": {aliceCSRF.Value}}, alice, aliceCSRF); recorder.Code != http.StatusSeeOther {
		t.Fatalf("acceptance = %d", recorder.Code)
	}
	aliceID := userID(t, env, "alice@example.com")
	assertLoosen(t, env, protecteeID, protecteeID, false)
	assertLoosen(t, env, aliceID, protecteeID, true)

	// 3. A second Partner joins, and both Partners read the same full Roster.
	secondReview, _ := sendInvitation(t, env, protectee, protecteeCSRF, "bob@example.com", false)
	bob, bobCSRF := signIn(t, env, "bob@example.com")
	if recorder := post(t, env, secondReview+"/accept",
		url.Values{"csrf_token": {bobCSRF.Value}}, bob, bobCSRF); recorder.Code != http.StatusSeeOther {
		t.Fatalf("second acceptance = %d", recorder.Code)
	}
	dashboard = body(t, get(t, env, "https://lite.nightloch.app/", protectee, protecteeCSRF))
	mustContain(t, dashboard, "Partner lock is active", "alice@example.com", "bob@example.com")
	for _, partner := range []struct {
		name           string
		session, token *http.Cookie
	}{{"alice", alice, aliceCSRF}, {"bob", bob, bobCSRF}} {
		view := body(t, get(t, env, "https://lite.nightloch.app/", partner.session, partner.token))
		mustContain(t, view, "People you protect", "alice@example.com", "bob@example.com")
		if _, err := env.partnerships.ProtectingView(ctx, userID(t, env, partner.name+"@example.com")); err != nil {
			t.Fatalf("%s People you protect: %v", partner.name, err)
		}
	}

	// 4. Reachability. Bob's mailbox rejects an alert; the warning appears for
	// everyone entitled to the Roster and changes nobody's powers. A later
	// delivery clears it without erasing the failure from Activity.
	if _, err := env.partnerships.DispatchDue(ctx, env.sender, 50); err != nil {
		t.Fatal(err)
	}
	bounceAt := time.Now().UTC().Add(-time.Minute)
	deliverEvent(t, env, "evt-bounce", "bob@example.com", "email.bounced", "Permanent", bounceAt)
	for _, viewer := range []struct {
		name           string
		session, token *http.Cookie
	}{{"the Protectee", protectee, protecteeCSRF}, {"their other Partner", alice, aliceCSRF}} {
		page := body(t, get(t, env, "https://lite.nightloch.app/", viewer.session, viewer.token))
		if !strings.Contains(page, partnership.ReachabilityUnreachable.Label()) {
			t.Fatalf("%s never saw the reachability warning:\n%s", viewer.name, page)
		}
	}
	bobID := userID(t, env, "bob@example.com")
	assertLoosen(t, env, bobID, protecteeID, true)
	deliverEvent(t, env, "evt-delivered", "bob@example.com", "email.delivered", "", bounceAt.Add(time.Minute))
	dashboard = body(t, get(t, env, "https://lite.nightloch.app/", protectee, protecteeCSRF))
	if strings.Contains(dashboard, partnership.ReachabilityUnreachable.Label()) {
		t.Fatalf("the warning outlived its recovery:\n%s", dashboard)
	}
	activity := body(t, get(t, env, "https://lite.nightloch.app/activity", protectee, protecteeCSRF))
	mustContain(t, activity, "Nightloch could not reach", "Nightloch reached")

	// 5. Concurrent Cooling-off. Both Partnerships are scheduled to end, each
	// keeping its own deadline, and every power survives until it arrives.
	alicePartnership := partnershipIDFor(t, env, "protectee@example.com", "alice@example.com")
	bobPartnership := partnershipIDFor(t, env, "protectee@example.com", "bob@example.com")
	for _, partnershipID := range []int64{alicePartnership, bobPartnership} {
		request, _, _ := coolingOffPaths(partnershipID)
		if recorder := post(t, env, request, url.Values{"csrf_token": {protecteeCSRF.Value}},
			protectee, protecteeCSRF); recorder.Code != http.StatusOK {
			t.Fatalf("request-end %d = %d", partnershipID, recorder.Code)
		}
	}
	dashboard = body(t, get(t, env, "https://lite.nightloch.app/", protectee, protecteeCSRF))
	if strings.Count(dashboard, "Scheduled to end") < 2 {
		t.Fatalf("two concurrent countdowns did not both render:\n%s", dashboard)
	}
	assertLoosen(t, env, protecteeID, protecteeID, false)
	assertLoosen(t, env, aliceID, protecteeID, true)

	// 6. One deadline arrives. Nightloch's own runner ends that Partnership,
	// and Alice keeps a receipt and nothing else.
	dueNow(t, env, alicePartnership)
	if ended, err := env.partnerships.CompleteDueCoolingOff(ctx, 10); err != nil || ended != 1 {
		t.Fatalf("CompleteDueCoolingOff = %d, %v", ended, err)
	}
	receiptPath := fmt.Sprintf("https://lite.nightloch.app/partners/ended-partnerships/%d", alicePartnership)
	receipt := body(t, get(t, env, receiptPath, alice, aliceCSRF))
	mustContain(t, receipt, "read-only receipt", "protectee@example.com")
	assertLoosen(t, env, aliceID, protecteeID, false)
	// A former Partner's window is closed: they read no Activity at all.
	if recorder := get(t, env, activityPath(protecteeID), alice, aliceCSRF); recorder.Code != http.StatusNotFound {
		t.Fatalf("a former Partner read Activity = %d", recorder.Code)
	}
	// The Protectee is still locked, because Bob's Partnership stands.
	assertLoosen(t, env, protecteeID, protecteeID, false)

	// 7. Activity browsing. The Protectee reads their whole chronology with
	// filters and paging; Bob reads the same Protectee from his own window and
	// sees nothing from before he accepted.
	filtered := body(t, get(t, env,
		"https://lite.nightloch.app/activity?category="+string(partnership.CategoryDelivery),
		protectee, protecteeCSRF))
	mustContain(t, filtered, "Nightloch could not reach")
	if strings.Contains(filtered, "invited alice@example.com") {
		t.Fatalf("a delivery filter returned lifecycle prose:\n%s", filtered)
	}
	bobsView := body(t, get(t, env, activityPath(protecteeID), bob, bobCSRF))
	if strings.Contains(bobsView, "invited alice@example.com") {
		t.Fatalf("Bob read an event from before his own window:\n%s", bobsView)
	}
	mustContain(t, bobsView, "protectee@example.com")

	// 8. Cross-User isolation. An unrelated signed-in User infers nothing from
	// any id, and neither does an unauthenticated visitor.
	stranger, strangerCSRF := signIn(t, env, "stranger@example.com")
	for _, path := range []string{
		activityPath(protecteeID),
		receiptPath,
		fmt.Sprintf("https://lite.nightloch.app/partners/partnerships/%d/end", bobPartnership),
		fmt.Sprintf("https://lite.nightloch.app/partners/partnerships/%d/request-end", bobPartnership),
	} {
		if recorder := get(t, env, path, stranger, strangerCSRF); recorder.Code != http.StatusNotFound {
			t.Fatalf("an unrelated User reached %s = %d", path, recorder.Code)
		}
		anonymous := get(t, env, path)
		if anonymous.Code != http.StatusSeeOther || anonymous.Header().Get("Location") != "/sign-in" {
			t.Fatalf("an unauthenticated visitor reached %s = %d", path, anonymous.Code)
		}
	}
	assertLoosen(t, env, userID(t, env, "stranger@example.com"), protecteeID, false)

	// 9. Bob ends his own Partnership, and the zero state is restored: the
	// Protectee may loosen their own Policy again, and both former Partners
	// keep only their receipts.
	if recorder := post(t, env,
		fmt.Sprintf("https://lite.nightloch.app/partners/partnerships/%d/end", bobPartnership),
		url.Values{"csrf_token": {bobCSRF.Value}, "confirmation": {"END"}},
		bob, bobCSRF); recorder.Code != http.StatusOK {
		t.Fatalf("Bob's ending = %d", recorder.Code)
	}
	assertLoosen(t, env, protecteeID, protecteeID, true)
	dashboard = body(t, get(t, env, "https://lite.nightloch.app/", protectee, protecteeCSRF))
	mustContain(t, dashboard, "Partner lock is inactive")

	// Everything Nightloch queued along the way was delivered, once each.
	if _, err := env.partnerships.DispatchDue(ctx, env.sender, 100); err != nil {
		t.Fatal(err)
	}
	// Nothing is still waiting: every job reached a terminal state, whether
	// the provider took it or reported it undeliverable.
	var pending int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM mail_delivery_jobs WHERE state IN (?, ?)",
		partnership.DeliveryQueued, partnership.DeliveryDelayed).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("%d messages were never transmitted", pending)
	}
}

func activityPath(protecteeID int64) string {
	return "https://lite.nightloch.app/activity/" + strconv.FormatInt(protecteeID, 10)
}

// assertLoosen states the Partner-lock answer this moment of the demo must
// give, which is the one product rule every other screen exists to serve.
func assertLoosen(t *testing.T, env *testEnv, actorID, protecteeID int64, want bool) {
	t.Helper()
	allowed, err := env.partnerships.CanLoosen(context.Background(), actorID, protecteeID)
	if err != nil || allowed != want {
		t.Fatalf("CanLoosen(%d, %d) = %v, %v; want %v", actorID, protecteeID, allowed, err, want)
	}
}

func body(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("page = %d:\n%s", recorder.Code, recorder.Body.String())
	}
	return recorder.Body.String()
}
