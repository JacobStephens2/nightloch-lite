package webapp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// acceptInvitation drives one invitee through the emailed review link, which
// is the only path that creates a Partnership.
func acceptInvitation(t *testing.T, env *testEnv, reviewURL string, session, csrf *http.Cookie) {
	t.Helper()
	if recorder := post(t, env, reviewURL+"/accept",
		url.Values{"csrf_token": []string{csrf.Value}}, session, csrf); recorder.Code != http.StatusSeeOther {
		t.Fatalf("acceptance = %d:\n%s", recorder.Code, recorder.Body.String())
	}
}

// TestReciprocalPartnershipsStayIndependentAcrossBothDirections is the #10
// gate: two Users protect each other through two opposite-direction
// Partnerships, and every screen, recipient, control, and authorization
// decision keeps the directions apart — while both are active, while one is
// counting down, and after one has ended.
func TestReciprocalPartnershipsStayIndependentAcrossBothDirections(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)

	// Alice is protected by Bob first, with Log sharing on.
	aliceSession, aliceCSRF := signIn(t, env, "alice@example.com")
	bobReview, _ := sendInvitation(t, env, aliceSession, aliceCSRF, "bob@example.com", true)
	bobSession, bobCSRF := signIn(t, env, "bob@example.com")
	acceptInvitation(t, env, bobReview, bobSession, bobCSRF)

	// Bob now invites his own Protectee to become his Partner. The reverse
	// direction is permitted, and it names the reciprocal effect to the
	// invitee alone.
	if _, err := env.partnerships.DispatchDue(context.Background(), env.sender, 20); err != nil {
		t.Fatal(err)
	}
	beforeInvite := len(env.sender.Envelopes)
	aliceReview, aliceInvitationID := sendInvitation(t, env, bobSession, bobCSRF, "alice@example.com", false)
	invitationMail := deliveredTo(t, env, beforeInvite)
	if len(invitationMail) != 1 {
		t.Fatalf("the reverse invitation reached %v, want only the invitee", invitationMail)
	}
	for _, want := range []string{
		"bob@example.com already protects you as your Partner.",
		"second, independent Partnership in the opposite direction",
		"The two Partnerships do not merge",
	} {
		if !strings.Contains(invitationMail["alice@example.com"], want) {
			t.Fatalf("reverse invitation missing %q:\n%s", want, invitationMail["alice@example.com"])
		}
	}

	// The invited User sees the reciprocal consequence on the review screen
	// and in her own Partner invitations list; a stranger holding the same
	// bearer link sees neither.
	reviewBody := get(t, env, aliceReview, aliceSession, aliceCSRF).Body.String()
	if !strings.Contains(reviewBody, "Reciprocal Partnerships") ||
		!strings.Contains(reviewBody, "ending one does not end the other") {
		t.Fatalf("the review screen withheld the reciprocal disclosure from the invitee:\n%s", reviewBody)
	}
	strangerSession, strangerCSRF := signIn(t, env, "stranger@example.com")
	strangerBody := get(t, env, aliceReview, strangerSession, strangerCSRF).Body.String()
	if strings.Contains(strangerBody, "Reciprocal Partnerships") {
		t.Fatalf("a bearer link disclosed the reverse-direction Partnership to a stranger:\n%s", strangerBody)
	}
	inviteeDashboard := get(t, env, "https://lite.nightloch.app/", aliceSession, aliceCSRF).Body.String()
	if !strings.Contains(inviteeDashboard, "Reciprocal Partnerships if you accept: Yes.") {
		t.Fatalf("the invitee's own invitation list did not mark the reciprocal effect:\n%s", inviteeDashboard)
	}
	// Bob's own pending-invitation row marks the same effect from his side.
	if !strings.Contains(get(t, env, "https://lite.nightloch.app/", bobSession, bobCSRF).Body.String(),
		"Reciprocal Partnership if accepted: Yes") {
		t.Fatal("the inviting Protectee's pending invitation did not mark the reciprocal effect")
	}

	beforeAccept := len(env.sender.Envelopes)
	acceptInvitation(t, env, aliceReview, aliceSession, aliceCSRF)
	if state := invitationStateInDB(t, env, aliceInvitationID); state != "accepted" {
		t.Fatalf("reverse invitation state = %q", state)
	}
	acceptanceMail := deliveredTo(t, env, beforeAccept)
	if !strings.Contains(acceptanceMail["bob@example.com"], "Reciprocal Partnerships: Yes") {
		t.Fatalf("the Protectee was not told the acceptance is reciprocal:\n%s", acceptanceMail["bob@example.com"])
	}
	for _, want := range []string{
		"Reciprocal Partnerships: Yes.",
		"bob@example.com already protects you",
		"changing one does not change the other",
	} {
		if !strings.Contains(acceptanceMail["alice@example.com"], want) {
			t.Fatalf("the new Partner's acceptance is missing %q:\n%s", want, acceptanceMail["alice@example.com"])
		}
	}

	bobProtectsAlice := partnershipIDFor(t, env, "alice@example.com", "bob@example.com")
	aliceProtectsBob := partnershipIDFor(t, env, "bob@example.com", "alice@example.com")
	if bobProtectsAlice == aliceProtectsBob {
		t.Fatal("the two directions resolved to one Partnership")
	}

	// Both directions active: each dashboard separates its own protection
	// from the people it protects, marks both rows reciprocal, and offers
	// each control only on the side that holds it.
	for _, viewer := range []struct {
		name             string
		session, csrf    *http.Cookie
		self, other      string
		protectedBy      int64
		partnerOver      int64
		ownLogSharing    string
		otherLogSharing  string
		requestEndTarget int64
	}{
		{"alice", aliceSession, aliceCSRF, "alice@example.com", "bob@example.com",
			bobProtectsAlice, aliceProtectsBob,
			"Log sharing: On", "Off — you cannot view their DNS domain log.", bobProtectsAlice},
		{"bob", bobSession, bobCSRF, "bob@example.com", "alice@example.com",
			aliceProtectsBob, bobProtectsAlice,
			"Log sharing: Off", "On — you can view their DNS domain log.", aliceProtectsBob},
	} {
		body := get(t, env, "https://lite.nightloch.app/", viewer.session, viewer.csrf).Body.String()
		areas := []struct {
			name string
			text string
			want []string
		}{
			{"Your protection", ownProtection(body), []string{
				"Partner lock is active.",
				"Reciprocal: Yes — you are also their Partner",
				viewer.ownLogSharing,
				// Only the Protectee side offers the 72-hour Cooling-off.
				fmt.Sprintf("/partners/partnerships/%d/request-end", viewer.protectedBy),
			}},
			{"People you protect", peopleYouProtect(t, body), []string{
				"This area is separate from Your protection above.",
				"Reciprocal: Yes — " + viewer.other + " is also one of your own Partners",
				viewer.otherLogSharing,
				// Only the Partner side offers the immediate ending.
				fmt.Sprintf("/partners/partnerships/%d/end", viewer.partnerOver),
			}},
		}
		for _, area := range areas {
			for _, want := range area.want {
				if !strings.Contains(area.text, want) {
					t.Fatalf("%s's %s area missing %q:\n%s", viewer.name, area.name, want, body)
				}
			}
		}
		// No control ever crosses the directions.
		for _, forbidden := range []string{
			fmt.Sprintf("/partners/partnerships/%d/request-end", viewer.partnerOver),
			fmt.Sprintf("/partners/partnerships/%d/end", viewer.protectedBy),
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s was offered %q across the two directions:\n%s", viewer.name, forbidden, body)
			}
		}
	}

	// Authorization does not leak across the directions even when the route
	// is requested directly.
	for _, attempt := range []struct {
		name    string
		path    string
		session *http.Cookie
		csrf    *http.Cookie
	}{
		{"Alice ending the Partnership she is the Protectee of",
			endPath(bobProtectsAlice), aliceSession, aliceCSRF},
		{"Bob requesting Cooling-off on the Partnership he is the Partner of",
			requestEndPath(bobProtectsAlice), bobSession, bobCSRF},
		{"Bob ending the Partnership he is the Protectee of",
			endPath(aliceProtectsBob), bobSession, bobCSRF},
		{"Alice requesting Cooling-off on the Partnership she is the Partner of",
			requestEndPath(aliceProtectsBob), aliceSession, aliceCSRF},
	} {
		if recorder := get(t, env, attempt.path, attempt.session, attempt.csrf); recorder.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want the same unavailable answer a stranger gets", attempt.name, recorder.Code)
		}
	}

	// One direction Cooling-off: Alice schedules the ending of the
	// Partnership that protects her. Bob's own protection is untouched.
	beforeCoolingOff := len(env.sender.Envelopes)
	if recorder := post(t, env, requestEndPath(bobProtectsAlice),
		url.Values{"csrf_token": []string{aliceCSRF.Value}}, aliceSession, aliceCSRF); recorder.Code != http.StatusOK {
		t.Fatalf("request-end = %d:\n%s", recorder.Code, recorder.Body.String())
	}
	coolingOffMail := deliveredTo(t, env, beforeCoolingOff)
	if len(coolingOffMail) != 1 || !strings.Contains(coolingOffMail["bob@example.com"],
		"[Nightloch] alice@example.com requested to end your Partnership") {
		t.Fatalf("Cooling-off reached %v, want only the affected Partner", coolingOffMail)
	}
	aliceBody := get(t, env, "https://lite.nightloch.app/", aliceSession, aliceCSRF).Body.String()
	if !strings.Contains(ownProtection(aliceBody), "Scheduled to end") {
		t.Fatalf("the scheduled direction is not counting down on the Protectee's own Roster:\n%s", aliceBody)
	}
	if strings.Contains(peopleYouProtect(t, aliceBody), "Scheduled to end") {
		t.Fatalf("Alice's request appeared against the direction she is the Partner of:\n%s", aliceBody)
	}
	bobBody := get(t, env, "https://lite.nightloch.app/", bobSession, bobCSRF).Body.String()
	// Bob reads the countdown as the affected Partner, under People you
	// protect, and his own protection is untouched by it.
	if !strings.Contains(peopleYouProtect(t, bobBody), "alice@example.com requested to end this Partnership") {
		t.Fatalf("the affected Partner was not shown the countdown:\n%s", bobBody)
	}
	if strings.Contains(ownProtection(bobBody), "Scheduled to end") {
		t.Fatalf("Alice's request scheduled Bob's own Partnership too:\n%s", bobBody)
	}
	if !strings.Contains(ownProtection(bobBody), fmt.Sprintf("/partners/partnerships/%d/request-end", aliceProtectsBob)) {
		t.Fatalf("Bob's own unscheduled protection lost its Cooling-off control:\n%s", bobBody)
	}

	// One direction ended: Bob agrees to end now. Alice's Partner lock
	// releases; Bob stays locked by the Partnership Alice still holds.
	if recorder := post(t, env, endPath(bobProtectsAlice),
		url.Values{"csrf_token": []string{bobCSRF.Value}, "confirmation": []string{"END"}},
		bobSession, bobCSRF); recorder.Code != http.StatusOK {
		t.Fatalf("End now = %d:\n%s", recorder.Code, recorder.Body.String())
	}

	aliceAfter := get(t, env, "https://lite.nightloch.app/", aliceSession, aliceCSRF).Body.String()
	if !strings.Contains(aliceAfter, "Partner lock is inactive.") {
		t.Fatalf("Alice stayed locked after her only Partner left:\n%s", aliceAfter)
	}
	for _, want := range []string{
		"People you protect", "bob@example.com",
		"Reciprocal: No",
		fmt.Sprintf("/partners/partnerships/%d/end", aliceProtectsBob),
	} {
		if !strings.Contains(aliceAfter, want) {
			t.Fatalf("ending Bob's direction disturbed Alice's own Partnership over him, missing %q:\n%s", want, aliceAfter)
		}
	}
	bobAfter := get(t, env, "https://lite.nightloch.app/", bobSession, bobCSRF).Body.String()
	for _, want := range []string{
		"Partner lock is active.",
		"alice@example.com",
		"Reciprocal: No",
		"Partnerships you ended",
		fmt.Sprintf("/partners/ended-partnerships/%d", bobProtectsAlice),
	} {
		if !strings.Contains(bobAfter, want) {
			t.Fatalf("Bob's surviving direction is wrong, missing %q:\n%s", want, bobAfter)
		}
	}
	if strings.Contains(bobAfter, "People you protect") {
		t.Fatalf("Bob still protects someone after ending his only Partnership:\n%s", bobAfter)
	}
	// The receipt Bob keeps says nothing about the direction he is still the
	// Protectee of.
	receipt := get(t, env, "https://lite.nightloch.app/partners/ended-partnerships/"+
		strconv.FormatInt(bobProtectsAlice, 10), bobSession, bobCSRF).Body.String()
	if !strings.Contains(receipt, "alice@example.com") || strings.Contains(receipt, "People you protect") {
		t.Fatalf("the receipt is not scoped to the ended direction:\n%s", receipt)
	}
}

// ownProtection and peopleYouProtect split a mixed-role dashboard into its two
// areas, so an assertion about one User's own protection cannot be satisfied by
// text that belongs to somebody they protect (S3-44).
func ownProtection(body string) string {
	own, _, _ := strings.Cut(body, "<h2>People you protect</h2>")
	return own
}

func peopleYouProtect(t *testing.T, body string) string {
	t.Helper()
	_, protecting, found := strings.Cut(body, "<h2>People you protect</h2>")
	if !found {
		t.Fatalf("the dashboard has no People you protect area:\n%s", body)
	}
	return protecting
}

func endPath(partnershipID int64) string {
	return "https://lite.nightloch.app/partners/partnerships/" +
		strconv.FormatInt(partnershipID, 10) + "/end"
}

func requestEndPath(partnershipID int64) string {
	return "https://lite.nightloch.app/partners/partnerships/" +
		strconv.FormatInt(partnershipID, 10) + "/request-end"
}
