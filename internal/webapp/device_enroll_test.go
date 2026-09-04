package webapp

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const enrollPath = "https://lite.nightloch.app/devices/enroll"

// The screen's recovery detail says exactly which setting takes a phone off
// the internet and how to undo it. That is written for a Protectee who is
// about to enroll, not for anyone who finds the address, so an anonymous
// visitor is sent to sign in and reads none of it.
func TestDeviceEnrollScreenRequiresASession(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)

	anonymous := get(t, env, enrollPath)
	if anonymous.Code != http.StatusSeeOther || anonymous.Header().Get("Location") != "/sign-in" {
		t.Fatalf("anonymous GET /devices/enroll = %d location=%q", anonymous.Code, anonymous.Header().Get("Location"))
	}
	if body := anonymous.Body.String(); strings.Contains(body, "Always-on VPN") || strings.Contains(body, "blackhole") {
		t.Fatalf("anonymous response disclosed recovery detail:\n%s", body)
	}
}

// enrollBody is the enroll screen as a signed-in Protectee reads it, which is
// the only way any of this copy is reachable.
func enrollBody(t *testing.T) string {
	t.Helper()
	env := newTestEnv(t)
	session, csrf := signIn(t, env, "protectee@example.com")
	recorder := get(t, env, enrollPath, session, csrf)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /devices/enroll = %d", recorder.Code)
	}
	return recorder.Body.String()
}

// The blackhole is the one thing the product must not let a Protectee meet
// unprepared: the Device stops reaching the internet rather than falling open,
// and the screen says so before anyone enrolls.
func TestDeviceEnrollScreenStatesTheBlackholeAndItsRecovery(t *testing.T) {
	t.Parallel()
	body := enrollBody(t)
	for _, want := range []string{
		"stops reaching the internet",
		"deliberate",
		"not a bug",
		"Nightloch does not fall open",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("enroll screen missing %q:\n%s", want, body)
		}
	}
	// Recovery is the other half of stating the failure: a screen that names
	// the blackhole without saying how to get out of it is worse than silent.
	for _, platform := range enrollPlatforms {
		if !strings.Contains(body, platform.Name) {
			t.Fatalf("enroll screen missing platform %q:\n%s", platform.Name, body)
		}
		for _, step := range platform.Recovery {
			if !strings.Contains(body, html.EscapeString(step)) {
				t.Fatalf("enroll screen missing %s recovery step %q:\n%s", platform.Name, step, body)
			}
		}
		for _, trap := range platform.Traps {
			for _, sentence := range []string{trap.Setting, trap.Consequence, trap.Recovery} {
				if !strings.Contains(body, html.EscapeString(sentence)) {
					t.Fatalf("enroll screen missing %s trap copy %q:\n%s", platform.Name, sentence, body)
				}
			}
		}
	}
}

// Every supported platform carries its own recovery steps and at least one
// named trap. A platform added to the table without them would ship a screen
// that names a failure it cannot tell the reader how to leave.
func TestEverySupportedPlatformCarriesRecoveryAndANamedTrap(t *testing.T) {
	t.Parallel()
	if len(enrollPlatforms) == 0 {
		t.Fatal("no supported platforms")
	}
	for _, platform := range enrollPlatforms {
		if platform.Name == "" || len(platform.Recovery) == 0 || len(platform.Traps) == 0 {
			t.Fatalf("platform %+v is missing a name, recovery steps, or a trap", platform)
		}
		for _, trap := range platform.Traps {
			if trap.Setting == "" || trap.Consequence == "" || trap.Recovery == "" {
				t.Fatalf("%s trap %+v is missing a setting, consequence, or recovery", platform.Name, trap)
			}
		}
	}
}

// The two traps the specification calls out by name are on the screen as the
// settings a Protectee would actually look for, not as a general warning.
func TestDeviceEnrollScreenNamesTheAndroidAndMacOSTraps(t *testing.T) {
	t.Parallel()
	body := enrollBody(t)
	for _, want := range []string{
		"Always-on VPN",
		"Block connections without VPN",
		"On-Demand",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("enroll screen missing trap %q:\n%s", want, body)
		}
	}
}

// The screen is honest in the direction that costs the product something too:
// the tunnel off means unprotected, and a determined bypass on hardware the
// Protectee owns is not fully preventable.
func TestDeviceEnrollScreenStatesTheLimitsOfProtection(t *testing.T) {
	t.Parallel()
	body := enrollBody(t)
	for _, want := range []string{
		"you are not protected",
		"encrypted DNS",
		"not fully preventable",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("enroll screen missing honesty copy %q:\n%s", want, body)
		}
	}
}

// The settled wording, pinned literally. The table-driven test above proves
// the screen renders whatever the platform table holds; this one proves what
// the table holds, so Slice 5 adds its action to copy that cannot be quietly
// weakened on the way (#39).
func TestDeviceEnrollScreenShipsTheSettledRecoveryWording(t *testing.T) {
	t.Parallel()
	body := enrollBody(t)
	for _, want := range []string{
		"Open the WireGuard app and turn the Nightloch tunnel off. The internet comes back as soon as the tunnel stops.",
		"the recovery is in Android&#39;s own settings rather than in the WireGuard app",
		"Android brings the tunnel straight back up every time you turn it off in the WireGuard app",
		"Turn Always-on VPN off under Settings, Network &amp; internet, VPN, and the gear beside WireGuard.",
		"Android drops every packet that is not going through the tunnel",
		"Turn Block connections without VPN off on that same VPN settings screen, then turn Always-on VPN off, then turn the tunnel off.",
		"Set Private DNS to Off or Automatic under Settings, Network &amp; internet, Private DNS.",
		"Open the WireGuard app, select the Nightloch tunnel, and click Deactivate.",
		"macOS reconnects the tunnel within seconds of every Deactivate",
		"Turn On-Demand off first: in the WireGuard app select the Nightloch tunnel, click Edit, clear the On-Demand setting, save, and then click Deactivate.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("enroll screen no longer ships the settled wording %q:\n%s", want, body)
		}
	}
}

// Recovery cannot claim an enforcement rule Nightloch has not built. Port 853
// is rejected from inside the tunnel by a later Slice 4 ticket, so the Private
// DNS trap says what will happen rather than what already does.
func TestDeviceEnrollScreenClaimsNoEnforcementThatIsNotBuiltYet(t *testing.T) {
	t.Parallel()
	body := enrollBody(t)
	if strings.Contains(body, "Nightloch rejects encrypted DNS") {
		t.Fatalf("enroll screen states an unbuilt :853 reject as fact:\n%s", body)
	}
	if !strings.Contains(body, "will not be carried out of the tunnel") {
		t.Fatalf("enroll screen dropped the Private DNS consequence:\n%s", body)
	}
}

// When a user is not on the enrollment allowlist, the screen explains that
// enrollment is invite-only during beta and offers no form, and POST returns 403.
func TestDeviceEnrollScreenGatingForNonAllowlistedUser(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	// Allowlist only a different user
	env.setAllowlist("other@example.com")
	session, csrf := signIn(t, env, "protectee@example.com")

	recorder := get(t, env, enrollPath, session, csrf)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /devices/enroll = %d", recorder.Code)
	}
	body := recorder.Body.String()
	wantNotice := "Device enrollment is invite-only during beta. Your account is not currently allowlisted to enroll Devices."
	if !strings.Contains(body, wantNotice) {
		t.Fatalf("enroll screen missing beta notice %q:\n%s", wantNotice, body)
	}
	if strings.Contains(body, "<form") || strings.Contains(strings.ToLower(body), "device_name") {
		t.Fatalf("non-allowlisted screen unexpectedly rendered a form:\n%s", body)
	}

	// POST /devices/enroll must be refused with 403 Forbidden
	form := url.Values{
		"csrf_token":  []string{csrf.Value},
		"device_name": []string{"Phone"},
	}
	post := httptest.NewRequest(http.MethodPost, enrollPath, strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(session)
	post.AddCookie(csrf)
	postRec := httptest.NewRecorder()
	env.app.ServeHTTP(postRec, post)
	if postRec.Code != http.StatusForbidden {
		t.Fatalf("POST /devices/enroll for non-allowlisted = %d, want 403", postRec.Code)
	}
}

// An empty or unset allowlist closes enrollment to all users.
func TestDeviceEnrollScreenGatingWithEmptyAllowlist(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	// Default allowlist is empty
	session, csrf := signIn(t, env, "protectee@example.com")

	recorder := get(t, env, enrollPath, session, csrf)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /devices/enroll = %d", recorder.Code)
	}
	body := recorder.Body.String()
	wantNotice := "Device enrollment is invite-only during beta. Your account is not currently allowlisted to enroll Devices."
	if !strings.Contains(body, wantNotice) {
		t.Fatalf("enroll screen missing beta notice %q:\n%s", wantNotice, body)
	}
	if strings.Contains(body, "<form") {
		t.Fatalf("empty allowlist screen offered a form:\n%s", body)
	}

	form := url.Values{
		"csrf_token":  []string{csrf.Value},
		"device_name": []string{"Phone"},
	}
	post := httptest.NewRequest(http.MethodPost, enrollPath, strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(session)
	post.AddCookie(csrf)
	postRec := httptest.NewRecorder()
	env.app.ServeHTTP(postRec, post)
	if postRec.Code != http.StatusForbidden {
		t.Fatalf("POST /devices/enroll with empty allowlist = %d, want 403", postRec.Code)
	}
}

// An allowlisted user sees the enrollment form with CSRF token and Device Name input.
func TestDeviceEnrollScreenForAllowlistedUser(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.setAllowlist("protectee@example.com")
	session, csrf := signIn(t, env, "protectee@example.com")

	recorder := get(t, env, enrollPath, session, csrf)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /devices/enroll = %d", recorder.Code)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "invite-only during beta") {
		t.Fatalf("allowlisted user saw invite-only notice:\n%s", body)
	}
	if !strings.Contains(body, `<form method="post" action="/devices/enroll"`) {
		t.Fatalf("allowlisted user missing enroll form:\n%s", body)
	}
	if !strings.Contains(body, `name="device_name"`) {
		t.Fatalf("allowlisted form missing device_name input:\n%s", body)
	}
	if !strings.Contains(body, `name="csrf_token"`) {
		t.Fatalf("allowlisted form missing csrf_token input:\n%s", body)
	}
}

// POST /devices/enroll requires CSRF verification.
func TestDeviceEnrollRequiresCSRF(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.setAllowlist("protectee@example.com")
	session, _ := signIn(t, env, "protectee@example.com")

	post := httptest.NewRequest(http.MethodPost, enrollPath, strings.NewReader("device_name=Phone"))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(session)
	rec := httptest.NewRecorder()
	env.app.ServeHTTP(rec, post)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /devices/enroll without CSRF = %d, want 403", rec.Code)
	}
}

// Validation errors (blank, >64 chars, non-graphic) re-render the form with error.
func TestDeviceEnrollValidationErrors(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.setAllowlist("protectee@example.com")
	session, csrf := signIn(t, env, "protectee@example.com")

	for _, badName := range []string{"", "   ", strings.Repeat("a", 65), "device\nname", "bad\x00char"} {
		form := url.Values{
			"csrf_token":  []string{csrf.Value},
			"device_name": []string{badName},
		}
		post := httptest.NewRequest(http.MethodPost, enrollPath, strings.NewReader(form.Encode()))
		post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		post.AddCookie(session)
		post.AddCookie(csrf)
		rec := httptest.NewRecorder()
		env.app.ServeHTTP(rec, post)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("POST /devices/enroll with name %q = %d, want 422", badName, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `class="error"`) {
			t.Fatalf("missing error element for name %q:\n%s", badName, body)
		}
	}
}

// Enrolling a 6th Device displays a user-facing refusal naming the 5-device quota.
func TestDeviceEnrollEnforcesFiveDeviceQuota(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.setAllowlist("protectee@example.com")
	session, csrf := signIn(t, env, "protectee@example.com")

	// Enroll 5 devices successfully
	for i := 1; i <= 5; i++ {
		form := url.Values{
			"csrf_token":  []string{csrf.Value},
			"device_name": []string{strings.Repeat("D", i)},
		}
		post := httptest.NewRequest(http.MethodPost, enrollPath, strings.NewReader(form.Encode()))
		post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		post.AddCookie(session)
		post.AddCookie(csrf)
		rec := httptest.NewRecorder()
		env.app.ServeHTTP(rec, post)
		if rec.Code != http.StatusOK {
			t.Fatalf("enroll device %d = %d", i, rec.Code)
		}
	}

	// 6th attempt must be refused with 422 and name the 5-device quota
	form := url.Values{
		"csrf_token":  []string{csrf.Value},
		"device_name": []string{"Sixth Device"},
	}
	post := httptest.NewRequest(http.MethodPost, enrollPath, strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(session)
	post.AddCookie(csrf)
	rec := httptest.NewRecorder()
	env.app.ServeHTTP(rec, post)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("6th device = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "5") {
		t.Fatalf("6th device refusal does not name the 5-device quota:\n%s", body)
	}
}

// Duplicate device names for active devices are refused.
func TestDeviceEnrollDuplicateNameRefusal(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.setAllowlist("protectee@example.com")
	session, csrf := signIn(t, env, "protectee@example.com")

	form := url.Values{
		"csrf_token":  []string{csrf.Value},
		"device_name": []string{"Phone"},
	}
	post := httptest.NewRequest(http.MethodPost, enrollPath, strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(session)
	post.AddCookie(csrf)
	rec := httptest.NewRecorder()
	env.app.ServeHTTP(rec, post)
	if rec.Code != http.StatusOK {
		t.Fatalf("first enroll Phone = %d", rec.Code)
	}

	// Duplicate enroll
	post2 := httptest.NewRequest(http.MethodPost, enrollPath, strings.NewReader(form.Encode()))
	post2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post2.AddCookie(session)
	post2.AddCookie(csrf)
	rec2 := httptest.NewRecorder()
	env.app.ServeHTTP(rec2, post2)
	if rec2.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate enroll Phone = %d, want 422", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "already have an active Device") {
		t.Fatalf("duplicate refusal missing copy:\n%s", rec2.Body.String())
	}
}

// Successful enrollment renders the one-time ephemeral view: IP, name, QR code,
// copyable config block, browser download link, and the warning. Private key is never
// stored in SQLite.
func TestDeviceEnrollSuccessFlow(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.setAllowlist("protectee@example.com")
	session, csrf := signIn(t, env, "protectee@example.com")

	form := url.Values{
		"csrf_token":  []string{csrf.Value},
		"device_name": []string{"Pixel 9"},
	}
	post := httptest.NewRequest(http.MethodPost, enrollPath, strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(session)
	post.AddCookie(csrf)
	rec := httptest.NewRecorder()
	env.app.ServeHTTP(rec, post)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /devices/enroll = %d, error: %s", rec.Code, rec.Body.String())
	}
	// Assert CSP allows data: for QR code image
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "img-src 'self' data:") {
		t.Fatalf("CSP header does not permit data: image sources: %s", csp)
	}

	body := rec.Body.String()

	// Title / heading
	if !strings.Contains(body, "Device enrolled") {
		t.Fatalf("response missing 'Device enrolled':\n%s", body)
	}
	// Name and allocated IP
	if !strings.Contains(body, "Pixel 9") {
		t.Fatalf("response missing device name 'Pixel 9':\n%s", body)
	}
	if !strings.Contains(body, "10.66.0.2") {
		t.Fatalf("response missing allocated IP '10.66.0.2':\n%s", body)
	}
	// Prominent warning
	wantWarning := "This configuration and private key are displayed once and cannot be viewed again. Download or scan it now."
	if !strings.Contains(body, wantWarning) {
		t.Fatalf("response missing one-time warning %q:\n%s", wantWarning, body)
	}
	// Inline scannable SVG QR code data URI
	unescapedBody := html.UnescapeString(body)
	if !strings.Contains(unescapedBody, `src="data:image/svg+xml;base64,`) {
		t.Fatalf("response missing SVG QR code base64 data URI img:\n%s", body)
	}
	// Copyable configuration block
	if !strings.Contains(body, `<pre class="conf-block"><code>`) {
		t.Fatalf("response missing <pre class=\"conf-block\"><code>:\n%s", body)
	}
	if !strings.Contains(body, "[Interface]") || !strings.Contains(body, "[Peer]") {
		t.Fatalf("response missing WireGuard configuration sections:\n%s", body)
	}
	// Browser-side .conf download link
	if !strings.Contains(unescapedBody, `href="data:text/plain;charset=utf-8;base64,`) {
		t.Fatalf("response missing data:text/plain;charset=utf-8;base64 download link:\n%s", body)
	}
	if !strings.Contains(body, `download="Pixel 9.conf"`) && !strings.Contains(body, `download="Pixel_9.conf"`) && !strings.Contains(body, `download=`) {
		t.Fatalf("response missing download attribute:\n%s", body)
	}

	// Verify private key is NEVER in SQLite
	// Extract the PrivateKey from the rendered body
	lines := strings.Split(body, "\n")
	var privateKeyLine string
	for _, l := range lines {
		if strings.Contains(l, "PrivateKey = ") {
			privateKeyLine = strings.TrimSpace(l)
			break
		}
	}
	if privateKeyLine == "" {
		t.Fatal("rendered configuration missing PrivateKey line")
	}
	privKey := strings.TrimPrefix(privateKeyLine, "PrivateKey = ")
	if len(privKey) < 30 {
		t.Fatalf("suspiciously short private key: %q", privKey)
	}

	// Assert private key is nowhere in SQLite
	var count int
	err := env.db.QueryRow("SELECT COUNT(*) FROM devices WHERE name = 'Pixel 9' AND address = '10.66.0.2'").Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("device record count = %d, err = %v", count, err)
	}
	// Check all string columns across DB tables
	rows, err := env.db.Query("SELECT name, public_key, address FROM devices")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, pubKey, addr string
		if err := rows.Scan(&name, &pubKey, &addr); err != nil {
			t.Fatal(err)
		}
		if name == privKey || pubKey == privKey || addr == privKey {
			t.Fatalf("private key was persisted into devices table!")
		}
	}
}

// Enrolling a Device appends a transactional Activity entry and alerts active Partners.
func TestDeviceEnrollAppendsActivityAndAlertsPartners(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.setAllowlist("protectee@example.com")
	session, csrf := signIn(t, env, "protectee@example.com")

	var protecteeID int64
	if err := env.db.QueryRow("SELECT id FROM users WHERE email = 'protectee@example.com'").Scan(&protecteeID); err != nil {
		t.Fatal(err)
	}
	partnerRes, err := env.db.Exec("INSERT INTO users(email, created_at) VALUES ('partner@example.com', unixepoch())")
	if err != nil {
		t.Fatal(err)
	}
	partnerID, _ := partnerRes.LastInsertId()
	_, err = env.db.Exec("INSERT INTO partnerships(protectee_id, partner_id, edge_type, log_sharing, accepted_at) VALUES (?, ?, 'peer', 0, unixepoch())", protecteeID, partnerID)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"csrf_token":  []string{csrf.Value},
		"device_name": []string{"Work Laptop"},
	}
	post := httptest.NewRequest(http.MethodPost, enrollPath, strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(session)
	post.AddCookie(csrf)
	rec := httptest.NewRecorder()
	env.app.ServeHTTP(rec, post)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /devices/enroll = %d", rec.Code)
	}

	// Verify Activity entry exists
	var activityCount int
	var action, detailsJSON string
	err = env.db.QueryRow("SELECT COUNT(*), action, details FROM activity_entries WHERE protectee_id = ? AND action = 'device_enrolled'", protecteeID).Scan(&activityCount, &action, &detailsJSON)
	if err != nil {
		t.Fatalf("query activity_entries: %v", err)
	}
	if activityCount != 1 {
		t.Fatalf("activityCount = %d, want 1", activityCount)
	}
	if !strings.Contains(detailsJSON, "Work Laptop") {
		t.Fatalf("activity details missing device name:\n%s", detailsJSON)
	}

	// Verify Action alert queued for partner
	var jobCount int
	var recipient string
	err = env.db.QueryRow("SELECT COUNT(*), recipient_email FROM mail_delivery_jobs WHERE recipient_email = 'partner@example.com'").Scan(&jobCount, &recipient)
	if err != nil {
		t.Fatalf("query mail_delivery_jobs: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("delivery job count = %d, want 1", jobCount)
	}
}

// Anonymous POST /devices/enroll redirects to sign-in when CSRF is valid,
// and rejects with 403 Forbidden without CSRF.
func TestDeviceEnrollPostAnonymousRedirectsToSignIn(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	_, csrf := signIn(t, env, "irrelevant@example.com")

	// Anonymous with valid CSRF is redirected to /sign-in
	form := url.Values{
		"csrf_token":  []string{csrf.Value},
		"device_name": []string{"Phone"},
	}
	postReq := httptest.NewRequest(http.MethodPost, enrollPath, strings.NewReader(form.Encode()))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.AddCookie(csrf)
	rec := httptest.NewRecorder()
	env.app.ServeHTTP(rec, postReq)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/sign-in" {
		t.Fatalf("anonymous POST /devices/enroll = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}

	// Anonymous without CSRF is rejected with 403 Forbidden
	bareReq := httptest.NewRequest(http.MethodPost, enrollPath, strings.NewReader("device_name=Phone"))
	bareReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	bareRec := httptest.NewRecorder()
	env.app.ServeHTTP(bareRec, bareReq)
	if bareRec.Code != http.StatusForbidden {
		t.Fatalf("cookieless anonymous POST /devices/enroll = %d, want 403", bareRec.Code)
	}
}

// The dashboard already tells a Protectee that enrollment is unavailable; that
// sentence is where they go to read what enrolling will mean.
func TestDashboardLinksToTheDeviceEnrollScreen(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	session, csrf := signIn(t, env, "protectee@example.com")

	body := get(t, env, "https://lite.nightloch.app/", session, csrf).Body.String()
	if !strings.Contains(body, `href="/devices/enroll"`) {
		t.Fatalf("dashboard does not link to the enroll screen:\n%s", body)
	}
}


