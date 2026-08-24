package webapp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// coolingOffPaths are the three Partnership controls the two parties hold once
// a Partnership is live.
func coolingOffPaths(partnershipID int64) (request, cancel, end string) {
	base := fmt.Sprintf("https://lite.nightloch.app/partners/partnerships/%d", partnershipID)
	return base + "/request-end", base + "/cancel-end", base + "/end"
}

// dueNow pulls a Partnership's Cooling-off deadline into the past so the
// durable runner has real work, the way three elapsed days would.
func dueNow(t *testing.T, env *testEnv, partnershipID int64) time.Time {
	t.Helper()
	deadline := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	if _, err := env.db.Exec(`UPDATE partnerships
		SET cooling_off_requested_at = ?, cooling_off_ends_at = ? WHERE id = ?`,
		deadline.Add(-72*time.Hour).Unix(), deadline.Unix(), partnershipID); err != nil {
		t.Fatal(err)
	}
	return deadline
}

func TestProtecteeRunsCoolingOffToItsDeadlineEndToEnd(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	ctx := context.Background()
	protecteeSession, protecteeCSRF, partnerSession, partnerCSRF, partnershipID := livePartnership(t, env)
	requestURL, cancelURL, _ := coolingOffPaths(partnershipID)

	// Only the Protectee is offered — or may reach — the request.
	dashboard := get(t, env, "https://lite.nightloch.app/", protecteeSession, protecteeCSRF).Body.String()
	mustContain(t, dashboard, "Request to end this Partnership")
	strangerSession, strangerCSRF := signIn(t, env, "stranger@example.com")
	for _, refused := range []struct {
		name          string
		session, csrf *http.Cookie
	}{
		{"the Partner", partnerSession, partnerCSRF},
		{"another User", strangerSession, strangerCSRF},
	} {
		if code := get(t, env, requestURL, refused.session, refused.csrf).Code; code != http.StatusNotFound {
			t.Fatalf("%s reached the request confirmation: %d", refused.name, code)
		}
		recorder := post(t, env, requestURL,
			url.Values{"csrf_token": []string{refused.csrf.Value}}, refused.session, refused.csrf)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s requested somebody else's ending: %d", refused.name, recorder.Code)
		}
	}
	// Signing out is enough to lose both controls entirely.
	if code := get(t, env, requestURL).Code; code != http.StatusSeeOther {
		t.Fatalf("an anonymous request confirmation = %d", code)
	}

	// The confirmation names the exact deadline and the final-Partner
	// consequence before anything is committed (S3-43).
	confirm := get(t, env, requestURL, protecteeSession, protecteeCSRF)
	if confirm.Code != http.StatusOK {
		t.Fatalf("request confirmation = %d", confirm.Code)
	}
	mustContain(t, confirm.Body.String(),
		"Request to end this Partnership?", "72 hours from now, and that deadline does not move",
		"the Partnership stays active", "keeps every Partner power",
		"is your final active Partner", "Partner lock becomes inactive",
		"loosen your own Policy")

	receipt := post(t, env, requestURL,
		url.Values{"csrf_token": []string{protecteeCSRF.Value}}, protecteeSession, protecteeCSRF)
	if receipt.Code != http.StatusOK {
		t.Fatalf("ending request = %d", receipt.Code)
	}
	mustContain(t, receipt.Body.String(), "Ending requested", "remaining",
		"is your final active Partner")

	// Nothing about the Partnership moved. Both parties still see it as
	// active, both see the same deadline, and the lock is untouched.
	var deadline int64
	if err := env.db.QueryRow(
		"SELECT cooling_off_ends_at FROM partnerships WHERE id = ? AND ended_at IS NULL",
		partnershipID).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	protecteeID, partnerID := userID(t, env, "protectee@example.com"), userID(t, env, "partner@example.com")
	if allowed, err := env.partnerships.CanLoosen(ctx, partnerID, protecteeID); err != nil || !allowed {
		t.Fatalf("the Partner lost their powers during Cooling-off: %v, %v", allowed, err)
	}
	if allowed, err := env.partnerships.CanLoosen(ctx, protecteeID, protecteeID); err != nil || allowed {
		t.Fatalf("the Protectee loosened during Cooling-off: %v, %v", allowed, err)
	}
	stamp := time.Unix(deadline, 0).UTC().Format("2006-01-02 15:04") + " UTC"
	for _, viewer := range []struct {
		name          string
		session, csrf *http.Cookie
		wants         []string
	}{
		// Every entitled Roster viewer reads the same deadline, the same
		// countdown, and the same final-Partnership consequence (S3-07, S3-43).
		{"the Protectee", protecteeSession, protecteeCSRF,
			[]string{"Partner lock is active.", "Scheduled to end", stamp, "remaining",
				"At that deadline your Partner lock becomes inactive",
				"loosen your own Policy", "Cancel the ending request"}},
		{"the affected Partner", partnerSession, partnerCSRF,
			[]string{"requested to end this Partnership", stamp, "remaining",
				"you keep every Partner power",
				"Partner lock becomes inactive", "loosen their own Policy", "End now"}},
	} {
		body := get(t, env, "https://lite.nightloch.app/", viewer.session, viewer.csrf).Body.String()
		mustContain(t, body, viewer.wants...)
		if viewer.name == "the Protectee" && strings.Contains(body, "Request to end this Partnership</a>") {
			t.Fatalf("a second request was still offered:\n%s", body)
		}
	}

	// A second request loses to the first and leaves its deadline alone.
	repeat := post(t, env, requestURL,
		url.Values{"csrf_token": []string{protecteeCSRF.Value}}, protecteeSession, protecteeCSRF)
	if repeat.Code != http.StatusConflict {
		t.Fatalf("a second ending request = %d", repeat.Code)
	}
	// The losing request must not repeat the fresh-request copy, nor offer a
	// control that would only lose again.
	mustContain(t, repeat.Body.String(), "already scheduled to end",
		"That deadline does not move, and requesting again would not shorten it.")
	for _, false_ := range []string{"72 hours from now", ">Request to end this Partnership</button>"} {
		if strings.Contains(repeat.Body.String(), false_) {
			t.Fatalf("the losing request still showed %q:\n%s", false_, repeat.Body.String())
		}
	}
	var unchanged int64
	if err := env.db.QueryRow("SELECT cooling_off_ends_at FROM partnerships WHERE id = ?",
		partnershipID).Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if unchanged != deadline {
		t.Fatalf("a second request moved the deadline from %d to %d", deadline, unchanged)
	}

	// Only the affected Partner reads the start alert, and it names the
	// deadline and the End now they may choose (S3-20).
	if _, err := env.partnerships.DispatchDue(ctx, env.sender, 10); err != nil {
		t.Fatal(err)
	}
	started := 0
	for _, envelope := range env.sender.Envelopes {
		if !strings.Contains(envelope.Subject, "requested to end your Partnership") {
			continue
		}
		started++
		if envelope.To != "partner@example.com" {
			t.Fatalf("the Cooling-off start alert went to %s", envelope.To)
		}
		mustContain(t, envelope.Text, "It is scheduled to end on "+stamp,
			"you retain all Partner powers", "End now", requestURL[:len(requestURL)-12]+"/end")
	}
	if started != 1 {
		t.Fatalf("%d Cooling-off start alerts, want 1", started)
	}

	// The deadline arrives while nothing is watching. A later process picks
	// the work up from SQLite and ends the Partnership at that exact instant.
	endedAt := dueNow(t, env, partnershipID)
	ended, err := env.partnerships.CompleteDueCoolingOff(ctx, 10)
	if err != nil || ended != 1 {
		t.Fatalf("CompleteDueCoolingOff = %d, %v", ended, err)
	}
	var storedEnd int64
	var reason string
	if err := env.db.QueryRow("SELECT ended_at, ended_reason FROM partnerships WHERE id = ?",
		partnershipID).Scan(&storedEnd, &reason); err != nil {
		t.Fatal(err)
	}
	if storedEnd != endedAt.Unix() || reason != "cooling_off_completed" {
		t.Fatalf("completed at %d (%q), want the unchanged deadline %d", storedEnd, reason, endedAt.Unix())
	}

	// The zero-Partner consequence lands exactly now, and not before.
	if allowed, err := env.partnerships.CanLoosen(ctx, protecteeID, protecteeID); err != nil || !allowed {
		t.Fatalf("the Protectee still cannot loosen after Cooling-off: %v, %v", allowed, err)
	}
	if allowed, err := env.partnerships.CanLoosen(ctx, partnerID, protecteeID); err != nil || allowed {
		t.Fatalf("the former Partner kept their powers: %v, %v", allowed, err)
	}
	protecteeDashboard := get(t, env, "https://lite.nightloch.app/", protecteeSession, protecteeCSRF).Body.String()
	mustContain(t, protecteeDashboard, "Partner lock is inactive.", "Add a Partner")
	partnerDashboard := get(t, env, "https://lite.nightloch.app/", partnerSession, partnerCSRF).Body.String()
	if strings.Contains(partnerDashboard, "People you protect") {
		t.Fatalf("the former Partner kept their Roster access:\n%s", partnerDashboard)
	}
	mustContain(t, partnerDashboard, "Partnerships you ended")

	// Neither party may act on it afterwards, and the Protectee's canceling
	// control is gone with the Partnership.
	if code := get(t, env, cancelURL, protecteeSession, protecteeCSRF).Code; code != http.StatusNotFound {
		t.Fatalf("cancelling an ended Partnership = %d", code)
	}

	// The receipt names Nightloch's completion as the cause (S3-18).
	receiptBody := get(t, env, fmt.Sprintf("https://lite.nightloch.app/partners/ended-partnerships/%d",
		partnershipID), partnerSession, partnerCSRF).Body.String()
	mustContain(t, receiptBody, "Nightloch ended the Partnership after the Cooling-off")

	if _, err := env.partnerships.DispatchDue(ctx, env.sender, 10); err != nil {
		t.Fatal(err)
	}
	completions := map[string]bool{}
	for _, envelope := range env.sender.Envelopes {
		if envelope.Subject != "[Nightloch] A Partnership for protectee@example.com ended after Cooling-off" {
			continue
		}
		completions[envelope.To] = true
		mustContain(t, envelope.Text,
			"Nightloch ended the Partnership after the Cooling-off requested by",
			"protectee@example.com now has 0 active Partner(s).",
			"The Partner lock is now inactive.")
	}
	if len(completions) != 2 || !completions["protectee@example.com"] || !completions["partner@example.com"] {
		t.Fatalf("completion recipients = %v", completions)
	}
}

func TestProtecteeCancelsTheirEndingRequestAndThePartnershipCarriesOn(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	ctx := context.Background()
	protecteeSession, protecteeCSRF, partnerSession, partnerCSRF, partnershipID := livePartnership(t, env)
	requestURL, cancelURL, _ := coolingOffPaths(partnershipID)

	if code := post(t, env, requestURL,
		url.Values{"csrf_token": []string{protecteeCSRF.Value}}, protecteeSession, protecteeCSRF).Code; code != http.StatusOK {
		t.Fatalf("ending request = %d", code)
	}

	// Nobody but the Protectee may cancel — not even the Partner the request
	// is about.
	for _, refused := range []struct {
		name          string
		session, csrf *http.Cookie
	}{{"the Partner", partnerSession, partnerCSRF}} {
		if code := post(t, env, cancelURL,
			url.Values{"csrf_token": []string{refused.csrf.Value}}, refused.session, refused.csrf).Code; code != http.StatusNotFound {
			t.Fatalf("%s canceled the request: %d", refused.name, code)
		}
	}

	mustContain(t, get(t, env, cancelURL, protecteeSession, protecteeCSRF).Body.String(),
		"Cancel this ending request?", "the Partnership simply stays active")
	canceled := post(t, env, cancelURL,
		url.Values{"csrf_token": []string{protecteeCSRF.Value}}, protecteeSession, protecteeCSRF)
	if canceled.Code != http.StatusOK {
		t.Fatalf("cancellation = %d", canceled.Code)
	}
	mustContain(t, canceled.Body.String(), "Ending request canceled", "No ending is scheduled")

	var live, scheduled int
	if err := env.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM partnerships WHERE ended_at IS NULL),
		(SELECT COUNT(*) FROM partnerships WHERE cooling_off_ends_at IS NOT NULL)`).
		Scan(&live, &scheduled); err != nil {
		t.Fatal(err)
	}
	if live != 1 || scheduled != 0 {
		t.Fatalf("after cancellation live=%d scheduled=%d", live, scheduled)
	}
	if allowed, err := env.partnerships.CanLoosen(ctx,
		userID(t, env, "partner@example.com"), userID(t, env, "protectee@example.com")); err != nil || !allowed {
		t.Fatalf("cancellation disturbed the Partner's powers: %v, %v", allowed, err)
	}

	// A second cancellation has nothing left to cancel and changes nothing.
	repeat := post(t, env, cancelURL,
		url.Values{"csrf_token": []string{protecteeCSRF.Value}}, protecteeSession, protecteeCSRF)
	if repeat.Code != http.StatusConflict {
		t.Fatalf("a second cancellation = %d", repeat.Code)
	}
	mustContain(t, repeat.Body.String(), "no scheduled ending to cancel",
		"there is nothing to cancel")
	if strings.Contains(repeat.Body.String(), ">Cancel the ending request</button>") {
		t.Fatalf("the losing cancellation still offered the control:\n%s", repeat.Body.String())
	}

	// The Roster is back to offering the request, and the Partner's row no
	// longer claims an ending is coming.
	mustContain(t, get(t, env, "https://lite.nightloch.app/", protecteeSession, protecteeCSRF).Body.String(),
		"Request to end this Partnership")
	partnerDashboard := get(t, env, "https://lite.nightloch.app/", partnerSession, partnerCSRF).Body.String()
	if strings.Contains(partnerDashboard, "requested to end this Partnership") {
		t.Fatalf("the Partner still sees a canceled request:\n%s", partnerDashboard)
	}

	if _, err := env.partnerships.DispatchDue(ctx, env.sender, 10); err != nil {
		t.Fatal(err)
	}
	cancellations := 0
	for _, envelope := range env.sender.Envelopes {
		if !strings.Contains(envelope.Subject, "canceled a Partnership ending request") {
			continue
		}
		cancellations++
		if envelope.To != "partner@example.com" {
			t.Fatalf("the cancellation alert went to %s", envelope.To)
		}
		mustContain(t, envelope.Text, "The Partnership remains active. No end is scheduled")
	}
	if cancellations != 1 {
		t.Fatalf("%d cancellation alerts, want 1", cancellations)
	}
}

func TestAffectedPartnerEndsNowWithTheSameTypedConfirmation(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	ctx := context.Background()
	protecteeSession, protecteeCSRF, partnerSession, partnerCSRF, partnershipID := livePartnership(t, env)
	requestURL, cancelURL, endURL := coolingOffPaths(partnershipID)

	if code := post(t, env, requestURL,
		url.Values{"csrf_token": []string{protecteeCSRF.Value}}, protecteeSession, protecteeCSRF).Code; code != http.StatusOK {
		t.Fatalf("ending request = %d", code)
	}

	// The Partner's own screen reframes as End now and still demands END.
	confirm := get(t, env, endURL, partnerSession, partnerCSRF)
	mustContain(t, confirm.Body.String(), "requested to end this Partnership",
		"You are not required to agree", "waives the remaining time", "Type END to confirm")
	misspelled := post(t, env, endURL, url.Values{
		"confirmation": []string{"now"}, "csrf_token": []string{partnerCSRF.Value},
	}, partnerSession, partnerCSRF)
	if misspelled.Code != http.StatusUnprocessableEntity {
		t.Fatalf("End now without the typed word = %d", misspelled.Code)
	}
	var live int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM partnerships WHERE ended_at IS NULL").Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatal("a misspelled confirmation ended the Partnership")
	}

	ended := post(t, env, endURL, url.Values{
		"confirmation": []string{"END"}, "csrf_token": []string{partnerCSRF.Value},
	}, partnerSession, partnerCSRF)
	if ended.Code != http.StatusOK {
		t.Fatalf("End now = %d", ended.Code)
	}
	mustContain(t, ended.Body.String(), "You agreed to end your Partnership")

	var reason string
	if err := env.db.QueryRow("SELECT ended_reason FROM partnerships WHERE id = ?",
		partnershipID).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "partner_agreed_now" {
		t.Fatalf("End now stored reason %q", reason)
	}
	// It is the immediate ending: powers, Roster, and lock all move now, and
	// the Protectee's cancellation has nothing left to act on.
	protecteeID, partnerID := userID(t, env, "protectee@example.com"), userID(t, env, "partner@example.com")
	if allowed, err := env.partnerships.CanLoosen(ctx, partnerID, protecteeID); err != nil || allowed {
		t.Fatalf("the former Partner kept their powers: %v, %v", allowed, err)
	}
	if allowed, err := env.partnerships.CanLoosen(ctx, protecteeID, protecteeID); err != nil || !allowed {
		t.Fatalf("the Protectee cannot loosen after End now: %v, %v", allowed, err)
	}
	if code := post(t, env, cancelURL,
		url.Values{"csrf_token": []string{protecteeCSRF.Value}}, protecteeSession, protecteeCSRF).Code; code != http.StatusNotFound {
		t.Fatalf("cancelling after End now = %d", code)
	}
	// The runner that would have completed the Cooling-off finds nothing.
	if completed, err := env.partnerships.CompleteDueCoolingOff(ctx, 10); err != nil || completed != 0 {
		t.Fatalf("the runner ended an already-ended Partnership: %d, %v", completed, err)
	}

	if _, err := env.partnerships.DispatchDue(ctx, env.sender, 10); err != nil {
		t.Fatal(err)
	}
	agreed := map[string]bool{}
	for _, envelope := range env.sender.Envelopes {
		if !strings.Contains(envelope.Subject, "agreed to end their Partnership") {
			continue
		}
		agreed[envelope.To] = true
		mustContain(t, envelope.Text, "agreed to end the Partnership immediately after")
	}
	if len(agreed) != 2 || !agreed["protectee@example.com"] || !agreed["partner@example.com"] {
		t.Fatalf("End now recipients = %v", agreed)
	}
}

func TestNoCoolingOffMutationIsReachableByGETOrWithoutCSRF(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protecteeSession, protecteeCSRF, _, _, partnershipID := livePartnership(t, env)
	requestURL, cancelURL, _ := coolingOffPaths(partnershipID)

	// A GET renders a confirmation and never mutates (S3-28).
	for _, target := range []string{requestURL, cancelURL} {
		if code := get(t, env, target, protecteeSession, protecteeCSRF).Code; code != http.StatusOK {
			t.Fatalf("GET %s = %d", target, code)
		}
	}
	var scheduled int
	if err := env.db.QueryRow(
		"SELECT COUNT(*) FROM partnerships WHERE cooling_off_ends_at IS NOT NULL").Scan(&scheduled); err != nil {
		t.Fatal(err)
	}
	if scheduled != 0 {
		t.Fatal("a GET scheduled an ending")
	}

	// A POST without the matching CSRF token is refused before any handler.
	forged := post(t, env, requestURL, url.Values{"csrf_token": []string{"forged"}},
		protecteeSession, protecteeCSRF)
	if forged.Code != http.StatusForbidden {
		t.Fatalf("a forged CSRF token = %d", forged.Code)
	}
	if err := env.db.QueryRow(
		"SELECT COUNT(*) FROM partnerships WHERE cooling_off_ends_at IS NOT NULL").Scan(&scheduled); err != nil {
		t.Fatal(err)
	}
	if scheduled != 0 {
		t.Fatal("a forged CSRF token scheduled an ending")
	}
}
