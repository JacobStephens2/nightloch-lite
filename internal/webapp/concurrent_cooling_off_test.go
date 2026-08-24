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

// partnerJoins drives one invitation through to an accepted Partnership and
// returns the new Partner's credentials, which is how a concurrent-Cooling-off
// scenario builds a Roster of more than one.
func partnerJoins(t *testing.T, env *testEnv, protectee, protecteeCSRF *http.Cookie,
	partnerEmail string) (session, csrf *http.Cookie, partnershipID int64) {

	t.Helper()
	reviewURL, _ := sendInvitation(t, env, protectee, protecteeCSRF, partnerEmail, false)
	session, csrf = signIn(t, env, partnerEmail)
	if recorder := post(t, env, reviewURL+"/accept",
		url.Values{"csrf_token": []string{csrf.Value}}, session, csrf); recorder.Code != http.StatusSeeOther {
		t.Fatalf("%s acceptance = %d", partnerEmail, recorder.Code)
	}
	if _, err := env.partnerships.DispatchDue(context.Background(), env.sender, 20); err != nil {
		t.Fatal(err)
	}
	return session, csrf, partnershipIDFor(t, env, "protectee@example.com", partnerEmail)
}

// requestEnding submits the Protectee's own ending request for one Partnership.
func requestEnding(t *testing.T, env *testEnv, session, csrf *http.Cookie, partnershipID int64) string {
	t.Helper()
	request, _, _ := coolingOffPaths(partnershipID)
	recorder := post(t, env, request, url.Values{"csrf_token": []string{csrf.Value}}, session, csrf)
	if recorder.Code != http.StatusOK {
		t.Fatalf("ending request for Partnership %d = %d", partnershipID, recorder.Code)
	}
	return recorder.Body.String()
}

// TestTwoCountdownsRunConcurrentlyAndSettleInOneHorizon is the #11 gate: a
// Protectee schedules both of their Partnerships to end, reads the correct
// Partner-lock consequence on each row, and exits the whole relationship in one
// 72-hour horizon rather than two in series.
func TestTwoCountdownsRunConcurrentlyAndSettleInOneHorizon(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	ctx := context.Background()

	protectee, protecteeCSRF := signIn(t, env, "protectee@example.com")
	firstSession, firstCSRF, firstID := partnerJoins(t, env, protectee, protecteeCSRF, "first@example.com")
	_, _, secondID := partnerJoins(t, env, protectee, protecteeCSRF, "second@example.com")

	// Neither request is final on its own while the other Partnership has no
	// scheduled ending of its own to end alongside it.
	receipt := requestEnding(t, env, protectee, protecteeCSRF, firstID)
	mustContain(t, receipt, "Ending requested",
		"1 other active Partner(s) outlast that deadline")
	if strings.Contains(receipt, "is your final active Partner") {
		t.Fatalf("the first request claimed to be final:\n%s", receipt)
	}

	// Stagger the two countdowns the way an hour between requests would, so
	// the dashboard has two different horizons to state.
	shiftDeadline(t, env, firstID, -time.Hour)

	// The second request is the later deadline, and nothing survives it, so
	// this one is the request that deactivates the Partner lock.
	receipt = requestEnding(t, env, protectee, protecteeCSRF, secondID)
	mustContain(t, receipt, "is your final active Partner",
		"Partner lock becomes inactive", "loosen your own Policy")

	// Both Partnerships are still active with every power, and the dashboard
	// states each row's own consequence rather than one blanket answer.
	dashboard := get(t, env, "https://lite.nightloch.app/", protectee, protecteeCSRF).Body.String()
	mustContain(t, dashboard, "Partner lock is active.",
		"Your Partner lock stays active afterwards, because 1 other Partner(s) outlast that deadline",
		"At that deadline your Partner lock becomes inactive")
	for _, partnerEmail := range []string{"first@example.com", "second@example.com"} {
		if !strings.Contains(dashboard, partnerEmail) {
			t.Fatalf("%s left the Roster during Cooling-off:\n%s", partnerEmail, dashboard)
		}
	}
	if allowed, err := env.partnerships.CanLoosen(ctx, protecteeIDOf(t, env), protecteeIDOf(t, env)); err != nil || allowed {
		t.Fatalf("Protectee loosened while both Partnerships were active: %v, %v", allowed, err)
	}
	// The Partner still holds every power, including their own End now.
	_, _, endURL := coolingOffPaths(firstID)
	if code := get(t, env, endURL, firstSession, firstCSRF).Code; code != http.StatusOK {
		t.Fatalf("the affected Partner lost End now during Cooling-off: %d", code)
	}

	// One horizon settles the whole Roster.
	deadline := dueNow(t, env, firstID)
	if _, err := env.db.Exec(`UPDATE partnerships
		SET cooling_off_requested_at = ?, cooling_off_ends_at = ? WHERE id = ?`,
		deadline.Add(-72*time.Hour).Unix(), deadline.Unix(), secondID); err != nil {
		t.Fatal(err)
	}
	ended, err := env.partnerships.CompleteDueCoolingOff(ctx, 10)
	if err != nil || ended != 2 {
		t.Fatalf("CompleteDueCoolingOff = %d, %v", ended, err)
	}
	dashboard = get(t, env, "https://lite.nightloch.app/", protectee, protecteeCSRF).Body.String()
	mustContain(t, dashboard, "Partner lock is inactive.", "you can still loosen your own Policy")
	if allowed, err := env.partnerships.CanLoosen(ctx, protecteeIDOf(t, env), protecteeIDOf(t, env)); err != nil || !allowed {
		t.Fatalf("Partner lock survived the shared deadline: %v, %v", allowed, err)
	}
}

// TestARosterChangeMakesARunningCountdownFinalAndAnAcceptanceUndoesIt walks the
// dynamic half of #11: the same unchanged deadline becomes final when another
// Partnership ends, and stops being final when a new Partner accepts.
func TestARosterChangeMakesARunningCountdownFinalAndAnAcceptanceUndoesIt(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	ctx := context.Background()

	protectee, protecteeCSRF := signIn(t, env, "protectee@example.com")
	firstSession, firstCSRF, firstID := partnerJoins(t, env, protectee, protecteeCSRF, "first@example.com")
	_, _, secondID := partnerJoins(t, env, protectee, protecteeCSRF, "second@example.com")
	requestEnding(t, env, protectee, protecteeCSRF, secondID)

	var scheduled int64
	if err := env.db.QueryRow("SELECT cooling_off_ends_at FROM partnerships WHERE id = ?",
		secondID).Scan(&scheduled); err != nil {
		t.Fatal(err)
	}

	// The first Partner ends their own Partnership, which leaves the running
	// countdown as the only thing holding the Partner lock up.
	if _, err := env.partnerships.DispatchDue(ctx, env.sender, 20); err != nil {
		t.Fatal(err)
	}
	beforeEnding := len(env.sender.Envelopes)
	_, _, endURL := coolingOffPaths(firstID)
	if recorder := post(t, env, endURL, url.Values{
		"csrf_token": []string{firstCSRF.Value}, "confirmation": []string{"END"},
	}, firstSession, firstCSRF); recorder.Code != http.StatusOK {
		t.Fatalf("the first Partner's ending = %d", recorder.Code)
	}

	// The countdown is untouched, and the Protectee and the final Partner are
	// told what it now means. The Partner who left is not.
	var after int64
	if err := env.db.QueryRow("SELECT cooling_off_ends_at FROM partnerships WHERE id = ?",
		secondID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != scheduled {
		t.Fatalf("the deadline moved from %d to %d", scheduled, after)
	}
	alerts := deliveredTo(t, env, beforeEnding)
	subject := "[Nightloch] A scheduled Partnership end will deactivate protectee@example.com’s Partner lock"
	for _, recipient := range []string{"protectee@example.com", "second@example.com"} {
		mustContain(t, alerts[recipient], subject,
			"second@example.com is now the final active Partner",
			"Its deadline has not changed.",
			"the Partner\nlock will become inactive")
	}
	if strings.Contains(alerts["first@example.com"], subject) {
		t.Fatalf("the departed Partner was told about a Roster they cannot see:\n%s",
			alerts["first@example.com"])
	}
	dashboard := get(t, env, "https://lite.nightloch.app/", protectee, protecteeCSRF).Body.String()
	mustContain(t, dashboard, "At that deadline your Partner lock becomes inactive")

	// A third Partner accepts before the deadline. The deadline still stands,
	// but it is no longer the one that deactivates the lock, and the
	// acceptance alert says so to everyone who was told it was final.
	beforeAcceptance := len(env.sender.Envelopes)
	partnerJoins(t, env, protectee, protecteeCSRF, "third@example.com")
	surviving := fmt.Sprintf("The Partner lock will remain active after second@example.com’s\n"+
		"Partnership is scheduled to end on %s.",
		time.Unix(scheduled, 0).UTC().Format("2006-01-02 15:04")+" UTC")
	acceptance := deliveredTo(t, env, beforeAcceptance)
	for _, recipient := range []string{"protectee@example.com", "second@example.com"} {
		mustContain(t, acceptance[recipient], surviving)
	}
	dashboard = get(t, env, "https://lite.nightloch.app/", protectee, protecteeCSRF).Body.String()
	mustContain(t, dashboard,
		"Your Partner lock stays active afterwards, because 1 other Partner(s) outlast that deadline")
	if strings.Contains(dashboard, "At that deadline your Partner lock becomes inactive") {
		t.Fatalf("the countdown still claims to be final after a later acceptance:\n%s", dashboard)
	}
}

// shiftDeadline moves one Partnership's whole Cooling-off window, which is how
// a test stages the staggered requests that real elapsed time would produce.
func shiftDeadline(t *testing.T, env *testEnv, partnershipID int64, by time.Duration) {
	t.Helper()
	if _, err := env.db.Exec(`UPDATE partnerships
		SET cooling_off_requested_at = cooling_off_requested_at + ?,
		    cooling_off_ends_at = cooling_off_ends_at + ?
		WHERE id = ?`, int64(by.Seconds()), int64(by.Seconds()), partnershipID); err != nil {
		t.Fatal(err)
	}
}

// protecteeIDOf is the signed-in Protectee's User id, which the authorization
// reads take directly rather than through HTTP.
func protecteeIDOf(t *testing.T, env *testEnv) int64 {
	t.Helper()
	var id int64
	if err := env.db.QueryRow("SELECT id FROM users WHERE email = ?",
		"protectee@example.com").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
