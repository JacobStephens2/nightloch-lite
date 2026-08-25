package webapp

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/JacobStephens2/nightloch-lite/internal/partnership"
)

// fillActivity records count entries directly against one Protectee, which is
// how a test builds a chronology long enough to page without spending the
// invitation limits that bound how fast a real Protectee can produce one.
// Every third entry shares a second, so paging is exercised over ties.
func fillActivity(t *testing.T, env *testEnv, protecteeID int64, count int) {
	t.Helper()
	for index := range count {
		if _, err := env.db.Exec(`
			INSERT INTO activity_entries(protectee_id, action, actor_email, target_email,
				occurred_at, details)
			VALUES (?, ?, ?, ?, ?, ?)`,
			protecteeID, partnership.ActionInvitationSent, "protectee@example.com",
			fmt.Sprintf("invitee%d@example.com", index),
			1750000000+int64(index/3), `{"log_sharing":false}`); err != nil {
			t.Fatal(err)
		}
	}
}

// olderLink is the address of the next page as the rendered page offers it.
func olderLink(t *testing.T, body string) string {
	t.Helper()
	marker := `<a class="button-link" href="`
	start := strings.Index(body, marker)
	if start < 0 {
		return ""
	}
	rest := body[start+len(marker):]
	href := rest[:strings.Index(rest, `"`)]
	if !strings.Contains(rest[:strings.Index(rest, "</a>")], "Older entries") {
		return ""
	}
	return strings.ReplaceAll(href, "&amp;", "&")
}

// entryIDs is every entry the rendered page listed, in the order it listed
// them. The list item's own id is what a page anchors on, so it is also the
// most direct way to ask what a viewer actually read.
func entryIDs(body string) []string {
	var ids []string
	for _, part := range strings.Split(body, `<li id="entry-`)[1:] {
		ids = append(ids, part[:strings.Index(part, `"`)])
	}
	return ids
}

// TestActivityPagesThroughTheHTTPSeam walks a chronology longer than one page
// the way a viewer does, following the page's own older-entries link, and
// proves no entry is read twice or skipped across the boundaries.
func TestActivityPagesThroughTheHTTPSeam(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	session, csrf := signIn(t, env, "protectee@example.com")
	protecteeID := userID(t, env, "protectee@example.com")
	const entries = 2*partnership.ActivityPageSize + 4
	fillActivity(t, env, protecteeID, entries)

	seen := map[string]bool{}
	target := "https://lite.nightloch.app/activity"
	pages := 0
	for target != "" {
		recorder := get(t, env, target, session, csrf)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", target, recorder.Code)
		}
		body := recorder.Body.String()
		ids := entryIDs(body)
		pages++
		older := olderLink(t, body)
		if older != "" && len(ids) != partnership.ActivityPageSize {
			t.Fatalf("page %d held %d entries with more to follow", pages, len(ids))
		}
		for _, id := range ids {
			if seen[id] {
				t.Fatalf("entry %s was read twice", id)
			}
			seen[id] = true
		}
		if older == "" {
			break
		}
		target = "https://lite.nightloch.app" + older
		if pages > 10 {
			t.Fatal("paging did not terminate")
		}
	}
	if len(seen) != entries || pages != 3 {
		t.Fatalf("paging read %d entries over %d pages, want %d over 3", len(seen), pages, entries)
	}
}

// TestActivityFiltersThroughTheHTTPSeam applies each filter the screen offers
// and then all of them together, and proves the two things a filter must never
// do: reach outside the viewer's window, or answer a filter Nightloch cannot
// honour with an empty page.
func TestActivityFiltersThroughTheHTTPSeam(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protecteeSession, protecteeCSRF, partnerSession, partnerCSRF, _ := livePartnership(t, env)
	protecteeID := userID(t, env, "protectee@example.com")
	partnerActivity := fmt.Sprintf("https://lite.nightloch.app/activity/%d", protecteeID)

	// Nightloch's own work is an actor the filter offers alongside the Users,
	// because a viewer looking for a timer or a delivery outcome is looking
	// for something no User did (S3-18).
	if _, err := env.db.Exec(`
		INSERT INTO activity_entries(protectee_id, action, target_email, occurred_at, details)
		VALUES (?, ?, ?, ?, ?)`,
		protecteeID, partnership.ActionDeliveryDelayed, "partner@example.com", 1750000000,
		`{"message_kind":"partner_invitation","reachability":"delayed"}`); err != nil {
		t.Fatal(err)
	}

	own := get(t, env, "https://lite.nightloch.app/activity", protecteeSession, protecteeCSRF).Body.String()
	// The filter controls are offered, and free-text search and export are not
	// part of Activity in v1 (S3-48).
	for _, want := range []string{
		`<select id="category"`, `<select id="actor"`,
		`<input id="from" name="from" type="date"`, `<input id="to" name="to" type="date"`,
		`<option value="invitations">Invitations</option>`,
		`<option value="protectee@example.com"`, `<option value="Nightloch"`,
	} {
		if !strings.Contains(own, want) {
			t.Fatalf("the filter controls are missing %q:\n%s", want, own)
		}
	}
	for _, forbidden := range []string{`type="search"`, `name="q"`, "Export", "Download"} {
		if strings.Contains(own, forbidden) {
			t.Fatalf("Activity offers %q, which v1 does not have:\n%s", forbidden, own)
		}
	}

	// One category, which keeps the invitation and drops the acceptance.
	invitations := get(t, env, "https://lite.nightloch.app/activity?category=invitations",
		protecteeSession, protecteeCSRF).Body.String()
	if !strings.Contains(invitations, "invited partner@example.com as a Partner.") ||
		strings.Contains(invitations, "became their Partner.") {
		t.Fatalf("the invitations category did not narrow the page:\n%s", invitations)
	}
	// One actor, which keeps only what that Partner did.
	byPartner := get(t, env, "https://lite.nightloch.app/activity?actor=partner@example.com",
		protecteeSession, protecteeCSRF).Body.String()
	if !strings.Contains(byPartner, "became their Partner.") ||
		strings.Contains(byPartner, "invited partner@example.com as a Partner.") {
		t.Fatalf("the actor filter did not narrow the page:\n%s", byPartner)
	}
	// A date range in the future matches nothing, and says so as a filter
	// result rather than as an empty chronology.
	empty := get(t, env, "https://lite.nightloch.app/activity?from=2099-01-01",
		protecteeSession, protecteeCSRF).Body.String()
	if !strings.Contains(empty, "Nothing in this chronology matches those filters.") {
		t.Fatalf("an empty filter result reads as an empty history:\n%s", empty)
	}
	// Combined filters narrow together and keep working.
	combined := get(t, env,
		"https://lite.nightloch.app/activity?category=partnerships&actor=partner@example.com&from=2020-01-01",
		protecteeSession, protecteeCSRF).Body.String()
	if !strings.Contains(combined, "became their Partner.") {
		t.Fatalf("the combined filter dropped an entry it should keep:\n%s", combined)
	}

	// A filter Nightloch cannot honour falls back to the unfiltered page and
	// says so, rather than claiming nothing happened.
	refused := get(t, env, "https://lite.nightloch.app/activity?category=everything-ever&to=not-a-date",
		protecteeSession, protecteeCSRF)
	if refused.Code != http.StatusOK ||
		!strings.Contains(refused.Body.String(), "could not apply that filter") ||
		!strings.Contains(refused.Body.String(), "became their Partner.") {
		t.Fatalf("an unavailable filter = %d:\n%s", refused.Code, refused.Body.String())
	}

	// A Partner may filter, and no filter widens their window: a range
	// reaching back before their acceptance still cannot show them the
	// invitation that created it.
	windowed := get(t, env, partnerActivity+"?from=2000-01-01&category=invitations",
		partnerSession, partnerCSRF)
	if windowed.Code != http.StatusOK {
		t.Fatalf("the Partner's filtered window = %d", windowed.Code)
	}
	if strings.Contains(windowed.Body.String(), "invited partner@example.com as a Partner.") {
		t.Fatalf("a filter reached before the Partner's window:\n%s", windowed.Body.String())
	}
	// Their own filter form and its links stay on the chronology they are
	// reading rather than sending them to their own.
	if !strings.Contains(windowed.Body.String(), fmt.Sprintf(`action="/activity/%d"`, protecteeID)) {
		t.Fatalf("the Partner's filter form does not point at this chronology:\n%s", windowed.Body.String())
	}
	// The actor choices a Partner is offered come from their window alone.
	if strings.Contains(windowed.Body.String(), `<option value="later@example.com"`) {
		t.Fatalf("the Partner was offered an actor from outside their window:\n%s", windowed.Body.String())
	}
}

// TestActivityRendersLabelledTimesAndTypedDetails covers what an entry shows
// once it is on the page: a labelled instant a browser can localize, typed
// expandable details, delivery state kept apart from lifecycle prose, and no
// trace of the stored payload any of it came from.
func TestActivityRendersLabelledTimesAndTypedDetails(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protecteeSession, protecteeCSRF, _, _, _ := livePartnership(t, env)
	protecteeID := userID(t, env, "protectee@example.com")
	// A delivery outcome, which is a reachability signal rather than a
	// lifecycle change and reads as one.
	if _, err := env.db.Exec(`
		INSERT INTO activity_entries(protectee_id, action, target_email, occurred_at, details)
		VALUES (?, ?, ?, ?, ?)`,
		protecteeID, partnership.ActionDeliveryFailed, "partner@example.com", 1750000000,
		`{"message_kind":"partner_invitation","reachability":"unreachable","failed":true}`); err != nil {
		t.Fatal(err)
	}

	body := get(t, env, "https://lite.nightloch.app/activity", protecteeSession, protecteeCSRF).Body.String()

	// Every instant is rendered as an explicit UTC stamp the browser script
	// relabels with the viewer's own timezone beside it (S3-27).
	if !strings.Contains(body, "<time datetime=") || !strings.Contains(body, " UTC</time>") {
		t.Fatalf("Activity rendered an unlabelled time:\n%s", body)
	}
	if !strings.Contains(body, `<script src="/assets/local-time.js" defer>`) {
		t.Fatalf("Activity does not load the local-time labelling:\n%s", body)
	}
	script := get(t, env, "https://lite.nightloch.app/assets/local-time.js").Body.String()
	if !strings.Contains(script, "timeZoneName") {
		t.Fatalf("the local-time script does not name the timezone:\n%s", script)
	}

	// Details are typed rows, and the delivery entry states its reachability
	// separately from the lifecycle prose around it.
	for _, want := range []string{
		"<details><summary>Details</summary>",
		"Log sharing: On",
		"Reachability: Nightloch cannot reach this Partner",
		"Message: Partner invitation",
		`<span class="muted">Delivery</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("Activity details are missing %q:\n%s", want, body)
		}
	}
	// Nothing on the page carries the shape of a stored payload (S3-49).
	for _, raw := range []string{"log_sharing", "message_kind", "partner_count", "{&#34;", `{"`} {
		if strings.Contains(body, raw) {
			t.Fatalf("Activity rendered stored data %q:\n%s", raw, body)
		}
	}
}

// TestTheTimerLinkReachesTheRequestOnItsOwnPage follows the link a completed
// timer offers, from a chronology long enough that the request and the
// completion cannot share a page. A bare fragment would resolve to nothing
// here, which is the whole point of the test (S3-18).
func TestTheTimerLinkReachesTheRequestOnItsOwnPage(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	session, csrf := signIn(t, env, "protectee@example.com")
	protecteeID := userID(t, env, "protectee@example.com")

	const requestedAt = 1750000000
	result, err := env.db.Exec(`
		INSERT INTO activity_entries(protectee_id, action, actor_email, target_email,
			occurred_at, effective_at, details)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		protecteeID, partnership.ActionCoolingOffStarted, "protectee@example.com",
		"partner@example.com", requestedAt, requestedAt+72*3600,
		`{"log_sharing":false,"partner_count":0,"final_partnership":true}`)
	if err != nil {
		t.Fatal(err)
	}
	requestID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	// More than a page of chronology between the request and the deadline.
	fillActivity(t, env, protecteeID, partnership.ActivityPageSize+5)
	if _, err := env.db.Exec(`
		INSERT INTO activity_entries(protectee_id, action, target_email, occurred_at, details)
		VALUES (?, ?, ?, ?, ?)`,
		protecteeID, partnership.ActionPartnershipEndedAfterCoolingOff, "partner@example.com",
		requestedAt+72*3600, fmt.Sprintf(
			`{"ended_reason":"cooling_off_completed","requested_by":"protectee@example.com","requested_at":%d,"requested_activity_id":%d,"partner_count":0,"partner_lock_active":false}`,
			requestedAt, requestID)); err != nil {
		t.Fatal(err)
	}

	first := get(t, env, "https://lite.nightloch.app/activity", session, csrf).Body.String()
	if !strings.Contains(first, "Nightloch ended protectee@example.com’s Partnership") {
		t.Fatalf("the completion is not on the newest page:\n%s", first)
	}
	if contains := entryIDs(first); slicesContain(contains, fmt.Sprint(requestID)) {
		t.Fatal("the request shares the newest page with the completion, so this proves nothing")
	}

	marker := `<a href="/activity?after=`
	start := strings.Index(first, marker)
	if start < 0 {
		t.Fatalf("the completion offers no link to the request it carried out:\n%s", first)
	}
	rest := first[start+len(`<a href="`):]
	href := strings.ReplaceAll(rest[:strings.Index(rest, `"`)], "&amp;", "&")
	if !strings.HasSuffix(href, fmt.Sprintf("#entry-%d", requestID)) {
		t.Fatalf("the link does not anchor on the request: %q", href)
	}

	// A browser keeps the fragment to itself and sends only the path and
	// query, so the test asks for exactly what the server would receive.
	path, _, _ := strings.Cut(href, "#")
	landing := get(t, env, "https://lite.nightloch.app"+path, session, csrf)
	if landing.Code != http.StatusOK {
		t.Fatalf("following the link = %d", landing.Code)
	}
	ids := entryIDs(landing.Body.String())
	if len(ids) == 0 || ids[0] != fmt.Sprint(requestID) {
		t.Fatalf("the link opened on %v, want the request %d at the top", ids, requestID)
	}
	if !strings.Contains(landing.Body.String(), "requested to end their Partnership with partner@example.com after Cooling-off.") {
		t.Fatalf("the request is not on the page its own link opened:\n%s", landing.Body.String())
	}
	// A viewer several pages deep is given a way back rather than left to
	// browser history.
	if !strings.Contains(landing.Body.String(), "Back to the newest entries") {
		t.Fatalf("a later page offers no way back to the newest:\n%s", landing.Body.String())
	}
}

func slicesContain(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
