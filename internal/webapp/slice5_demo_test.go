package webapp

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/JacobStephens2/nightloch-lite/internal/enforcement"
	"github.com/JacobStephens2/nightloch-lite/internal/partnership"
	"github.com/JacobStephens2/nightloch-lite/internal/policy"
)

// TestSliceFiveRunsTheWholeDeviceLifecycleEndToEnd is the Slice 5 promotion
// gate. It asserts the complete multi-tenant lifecycle over real HTTP:
// gated admission, QR and profile configuration generation, WireGuard peer and
// AGH client registration, notification dispatch to active Partners, quota
// enforcement, allowlist resilience, authorized Device removal with alerts,
// quarantine address retention for 8 days, and atomic account-deletion teardown
// leaving zero orphan peers or resolver clients.
func TestSliceFiveRunsTheWholeDeviceLifecycleEndToEnd(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	ctx := context.Background()

	// -------------------------------------------------------------------------
	// 1. Setup & Gated Admission (Adversarial Check)
	// -------------------------------------------------------------------------
	// Allowlist only protectee@example.com
	env.setAllowlist("protectee@example.com")

	// Unauthenticated visitor is redirected to sign-in on GET
	anonGet := get(t, env, enrollPath)
	if anonGet.Code != http.StatusSeeOther || anonGet.Header().Get("Location") != "/sign-in" {
		t.Fatalf("anonymous GET /devices/enroll = %d, want redirect to /sign-in", anonGet.Code)
	}
	// Without CSRF token, POST is rejected with 403 Forbidden
	anonPostNoCSRF := post(t, env, enrollPath, url.Values{"device_name": {"HackerPhone"}})
	if anonPostNoCSRF.Code != http.StatusForbidden {
		t.Fatalf("anonymous POST /devices/enroll without CSRF = %d, want 403 Forbidden", anonPostNoCSRF.Code)
	}
	// With valid CSRF cookie and token but without session, POST redirects to /sign-in
	anonCSRFCookie := cookieNamed(t, get(t, env, "https://lite.nightloch.app/sign-in").Result().Cookies(), csrfCookieName)
	anonPostWithCSRF := post(t, env, enrollPath, url.Values{
		"csrf_token":  {anonCSRFCookie.Value},
		"device_name": {"HackerPhone"},
	}, anonCSRFCookie)
	if anonPostWithCSRF.Code != http.StatusSeeOther || anonPostWithCSRF.Header().Get("Location") != "/sign-in" {
		t.Fatalf("anonymous POST /devices/enroll with CSRF = %d, want redirect to /sign-in", anonPostWithCSRF.Code)
	}

	// Non-allowlisted user (Mallory) signs in
	mallorySession, malloryCSRF := signIn(t, env, "mallory@example.com")

	// Mallory sees informational beta notice on GET
	malloryGet := get(t, env, enrollPath, mallorySession, malloryCSRF)
	if malloryGet.Code != http.StatusOK {
		t.Fatalf("mallory GET /devices/enroll = %d, want 200", malloryGet.Code)
	}
	mustContain(t, malloryGet.Body.String(), "Device enrollment is invite-only during beta")

	// Mallory is forbidden on POST (403)
	malloryPost := post(t, env, enrollPath, url.Values{
		"csrf_token":  {malloryCSRF.Value},
		"device_name": {"MalloryDevice"},
	}, mallorySession, malloryCSRF)
	if malloryPost.Code != http.StatusForbidden {
		t.Fatalf("mallory POST /devices/enroll = %d, want 403 Forbidden", malloryPost.Code)
	}

	// Assert no peers or clients were created by unauthorized attempts
	peers, err := env.tunnel.Peers(ctx)
	if err != nil || len(peers) != 0 {
		t.Fatalf("tunnel has %d peers after unauthorized enrollment, want 0", len(peers))
	}
	if count := env.resolver.ClientCount(); count != 0 {
		t.Fatalf("resolver has %d clients after unauthorized enrollment, want 0", count)
	}

	// -------------------------------------------------------------------------
	// 2. Allowlisted Protectee Enrollment & Notifications
	// -------------------------------------------------------------------------
	protecteeSession, protecteeCSRF := signIn(t, env, "protectee@example.com")
	protecteeID := userID(t, env, "protectee@example.com")

	// Form an active Partnership with Alice first, so Alice receives the Action alert
	aliceReview, _ := sendInvitation(t, env, protecteeSession, protecteeCSRF, "alice@example.com", true)
	aliceSession, aliceCSRF := signIn(t, env, "alice@example.com")
	if recorder := post(t, env, aliceReview+"/accept", url.Values{"csrf_token": {aliceCSRF.Value}}, aliceSession, aliceCSRF); recorder.Code != http.StatusSeeOther {
		t.Fatalf("Alice acceptance = %d", recorder.Code)
	}

	// Protectee visits /devices/enroll and sees enrollment form
	enrollPage := body(t, get(t, env, enrollPath, protecteeSession, protecteeCSRF))
	mustContain(t, enrollPage, "Enroll a Device", "Device name", "Android", "macOS")

	// Protectee enrolls first device: "Pixel 8"
	enrollResp := post(t, env, enrollPath, url.Values{
		"csrf_token":  {protecteeCSRF.Value},
		"device_name": {"Pixel 8"},
	}, protecteeSession, protecteeCSRF)
	if enrollResp.Code != http.StatusOK {
		t.Fatalf("POST /devices/enroll = %d:\n%s", enrollResp.Code, enrollResp.Body.String())
	}
	successBody := enrollResp.Body.String()
	unescapedSuccess := html.UnescapeString(successBody)
	mustContain(t, successBody, "Device enrolled", "Pixel 8", "qr-image", "download=\"Pixel 8.conf\"")
	mustContain(t, unescapedSuccess, `src="data:image/svg+xml;base64,`, `href="data:text/plain;charset=utf-8;base64,`)
	mustContain(t, unescapedSuccess, "[Interface]", "PrivateKey =", "Address = 10.66.0.2/32", "DNS = 10.66.0.1", "[Peer]", "AllowedIPs = 0.0.0.0/0, ::/0")

	// Read dev1 from DB
	var dev1ID int64
	var dev1Pub string
	if err := env.db.QueryRow("SELECT id, public_key FROM devices WHERE protectee_id = ? AND name = 'Pixel 8'", protecteeID).Scan(&dev1ID, &dev1Pub); err != nil {
		t.Fatalf("query dev1: %v", err)
	}

	// Verify WireGuard peer was added
	if !env.tunnel.HasPeer(dev1Pub) {
		t.Fatalf("dev1 peer %s not found in tunnel", dev1Pub)
	}
	// Verify AGH client was registered
	client1Name := enforcement.DeviceClientName(dev1ID)
	if !env.resolver.HasClient(client1Name) {
		t.Fatalf("dev1 client %s not found in resolver", client1Name)
	}

	// Dispatch mail and verify Partner Alice received device_enrolled Action alert
	if _, err := env.partnerships.DispatchDue(ctx, env.sender, 50); err != nil {
		t.Fatal(err)
	}
	var enrolledAlertRecipient, enrolledAlertSubject string
	if err := env.db.QueryRow(`
		SELECT recipient_email, subject FROM mail_delivery_jobs
		WHERE message_kind = ? AND state = 'sent'
		ORDER BY id DESC LIMIT 1`, partnership.MessageDeviceEnrolled).
		Scan(&enrolledAlertRecipient, &enrolledAlertSubject); err != nil {
		t.Fatalf("query device_enrolled mail job: %v", err)
	}
	if enrolledAlertRecipient != "alice@example.com" {
		t.Fatalf("device_enrolled recipient = %q, want alice@example.com", enrolledAlertRecipient)
	}
	mustContain(t, enrolledAlertSubject, "Pixel 8", "enrolled")

	// Dashboard check: Protectee sees "Pixel 8", Partner Alice sees "Pixel 8"
	protecteeDashboard := body(t, get(t, env, "https://lite.nightloch.app/", protecteeSession, protecteeCSRF))
	mustContain(t, protecteeDashboard, "Your Devices", "Pixel 8", "10.66.0.2")

	aliceDashboard := body(t, get(t, env, "https://lite.nightloch.app/", aliceSession, aliceCSRF))
	mustContain(t, aliceDashboard, "Pixel 8", "10.66.0.2")

	// -------------------------------------------------------------------------
	// 3. Quota Enforcement (At Most 5 Live Devices)
	// -------------------------------------------------------------------------
	for _, devName := range []string{"Laptop", "Work iPad", "Home PC", "Backup Phone"} {
		res := post(t, env, enrollPath, url.Values{
			"csrf_token":  {protecteeCSRF.Value},
			"device_name": {devName},
		}, protecteeSession, protecteeCSRF)
		if res.Code != http.StatusOK {
			t.Fatalf("enroll %s: code = %d:\n%s", devName, res.Code, res.Body.String())
		}
	}

	// Now protectee holds 5 live devices. Attempting 6th ("Watch") must be rejected.
	sixthResp := post(t, env, enrollPath, url.Values{
		"csrf_token":  {protecteeCSRF.Value},
		"device_name": {"Watch"},
	}, protecteeSession, protecteeCSRF)
	if sixthResp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("enroll 6th device code = %d, want 422 Unprocessable Entity", sixthResp.Code)
	}
	mustContain(t, sixthResp.Body.String(), "limit of 5 live Devices", "Remove an existing Device")

	// Assert exactly 5 peers and 5 clients exist
	peers5, err := env.tunnel.Peers(ctx)
	if err != nil || len(peers5) != 5 {
		t.Fatalf("tunnel has %d peers, want exactly 5", len(peers5))
	}
	if count := env.resolver.ClientCount(); count != 5 {
		t.Fatalf("resolver has %d clients, want exactly 5", count)
	}

	// -------------------------------------------------------------------------
	// 4. Allowlist Removal Resilience (Adversarial Check)
	// -------------------------------------------------------------------------
	// Remove protectee from allowlist (empty allowlist)
	env.setAllowlist()

	// Existing devices continue to be active and operational
	peersAfterRemoval, _ := env.tunnel.Peers(ctx)
	if len(peersAfterRemoval) != 5 || env.resolver.ClientCount() != 5 {
		t.Fatalf("allowlist removal disrupted existing devices! tunnel=%d, resolver=%d",
			len(peersAfterRemoval), env.resolver.ClientCount())
	}

	// New enrollment is blocked
	blockedPost := post(t, env, enrollPath, url.Values{
		"csrf_token":  {protecteeCSRF.Value},
		"device_name": {"BlockedPhone"},
	}, protecteeSession, protecteeCSRF)
	if blockedPost.Code != http.StatusForbidden {
		t.Fatalf("post-allowlist-removal POST /devices/enroll = %d, want 403 Forbidden", blockedPost.Code)
	}

	// Restore protectee to allowlist
	env.setAllowlist("protectee@example.com")

	// -------------------------------------------------------------------------
	// 5. Device Removal Flow & Adversarial Authorization
	// -------------------------------------------------------------------------
	dev1RemoveURL := fmt.Sprintf("https://lite.nightloch.app/devices/%d/remove", dev1ID)

	// Anonymous cannot access remove confirmation or submit removal
	anonRemGet := get(t, env, dev1RemoveURL)
	if anonRemGet.Code != http.StatusSeeOther || anonRemGet.Header().Get("Location") != "/sign-in" {
		t.Fatalf("anonymous GET remove = %d, want redirect", anonRemGet.Code)
	}
	anonRemPostNoCSRF := post(t, env, dev1RemoveURL, url.Values{"csrf_token": {"fake"}})
	if anonRemPostNoCSRF.Code != http.StatusForbidden {
		t.Fatalf("anonymous POST remove without CSRF = %d, want 403 Forbidden", anonRemPostNoCSRF.Code)
	}
	anonRemPostWithCSRF := post(t, env, dev1RemoveURL, url.Values{
		"csrf_token": {anonCSRFCookie.Value},
	}, anonCSRFCookie)
	if anonRemPostWithCSRF.Code != http.StatusSeeOther || anonRemPostWithCSRF.Header().Get("Location") != "/sign-in" {
		t.Fatalf("anonymous POST remove with CSRF = %d, want redirect to /sign-in", anonRemPostWithCSRF.Code)
	}

	// Stranger (unrelated user) cannot access remove confirmation or post removal
	strangerSession, strangerCSRF := signIn(t, env, "stranger@example.com")
	strangerGet := get(t, env, dev1RemoveURL, strangerSession, strangerCSRF)
	if strangerGet.Code != http.StatusForbidden {
		t.Fatalf("stranger GET remove = %d, want 403 Forbidden", strangerGet.Code)
	}
	strangerPost := post(t, env, dev1RemoveURL, url.Values{"csrf_token": {strangerCSRF.Value}}, strangerSession, strangerCSRF)
	if strangerPost.Code != http.StatusForbidden {
		t.Fatalf("stranger POST remove = %d, want 403 Forbidden", strangerPost.Code)
	}

	// Mallory (unpartnered) cannot remove
	malloryRem := post(t, env, dev1RemoveURL, url.Values{"csrf_token": {malloryCSRF.Value}}, mallorySession, malloryCSRF)
	if malloryRem.Code != http.StatusForbidden {
		t.Fatalf("mallory POST remove = %d, want 403 Forbidden", malloryRem.Code)
	}

	// Non-existent ID returns 404
	fakeRemURL := "https://lite.nightloch.app/devices/999999/remove"
	if get(t, env, fakeRemURL, protecteeSession, protecteeCSRF).Code != http.StatusNotFound {
		t.Fatalf("GET non-existent device remove = %d, want 404", get(t, env, fakeRemURL, protecteeSession, protecteeCSRF).Code)
	}

	// Add Bob as a second active Partner to test multi-partner alert matrix
	bobReview, _ := sendInvitation(t, env, protecteeSession, protecteeCSRF, "bob@example.com", false)
	bobSession, bobCSRF := signIn(t, env, "bob@example.com")
	if recorder := post(t, env, bobReview+"/accept", url.Values{"csrf_token": {bobCSRF.Value}}, bobSession, bobCSRF); recorder.Code != http.StatusSeeOther {
		t.Fatalf("Bob acceptance = %d", recorder.Code)
	}

	// Partner Alice removes Protectee's "Pixel 8"
	aliceRemConfirm := body(t, get(t, env, dev1RemoveURL, aliceSession, aliceCSRF))
	mustContain(t, aliceRemConfirm, "Remove Pixel 8?", "Immediate disconnect", "Quarantine", "Remove Device")

	aliceRemAction := post(t, env, dev1RemoveURL, url.Values{"csrf_token": {aliceCSRF.Value}}, aliceSession, aliceCSRF)
	if aliceRemAction.Code != http.StatusSeeOther {
		t.Fatalf("Alice POST remove = %d, want 303 Redirect", aliceRemAction.Code)
	}
	if loc := aliceRemAction.Header().Get("Location"); loc != "/?removed=Pixel+8" {
		t.Fatalf("Alice POST remove redirect location = %q, want /?removed=Pixel+8", loc)
	}

	// Verification: Peer is withdrawn from WireGuard, AGH client is deleted
	if env.tunnel.HasPeer(dev1Pub) {
		t.Fatalf("peer %s was not withdrawn from tunnel upon removal", dev1Pub)
	}
	if env.resolver.HasClient(client1Name) {
		t.Fatalf("client %s was not deleted from resolver upon removal", client1Name)
	}

	// Dispatch alerts
	if _, err := env.partnerships.DispatchDue(ctx, env.sender, 50); err != nil {
		t.Fatal(err)
	}

	// Verify removal alerts: Protectee and Bob received alerts; Alice (actor) did not
	rows, err := env.db.Query(`
		SELECT recipient_email FROM mail_delivery_jobs
		WHERE message_kind = ? AND state = 'sent'`, partnership.MessageDeviceRemoved)
	if err != nil {
		t.Fatalf("query device_removed jobs: %v", err)
	}
	defer rows.Close()
	var removedRecipients []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			t.Fatal(err)
		}
		removedRecipients = append(removedRecipients, r)
	}
	if len(removedRecipients) != 2 {
		t.Fatalf("device_removed recipient count = %d, want 2 (protectee and bob): %v", len(removedRecipients), removedRecipients)
	}
	mustContain(t, strings.Join(removedRecipients, ","), "protectee@example.com", "bob@example.com")
	if strings.Contains(strings.Join(removedRecipients, ","), "alice@example.com") {
		t.Fatal("actor Alice was sent her own device_removed alert")
	}

	// Activity entry check
	actPage := body(t, get(t, env, "https://lite.nightloch.app/activity", protecteeSession, protecteeCSRF))
	mustContain(t, actPage, "Pixel 8", "removed Device")

	// -------------------------------------------------------------------------
	// 6. Quarantine Assertion (8-day hold)
	// -------------------------------------------------------------------------
	// 10.66.0.2 is in quarantine.
	membership, err := env.enforcement.Devices(ctx)
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	var foundQuarantined bool
	for _, q := range membership.Quarantined {
		if q.Address.String() == "10.66.0.2" {
			foundQuarantined = true
			if q.ReusableAt.Before(time.Now().UTC().Add(7 * 24 * time.Hour)) {
				t.Fatalf("quarantine ReusableAt %v is less than 7 days from now", q.ReusableAt)
			}
		}
	}
	if !foundQuarantined {
		t.Fatal("removed address 10.66.0.2 was not found in Quarantined list")
	}

	// Enroll a new device "Pixel 8a" now that protectee has 4 live devices.
	// Since 10.66.0.2 is quarantined, it MUST receive 10.66.0.7, NOT 10.66.0.2.
	enroll8a := post(t, env, enrollPath, url.Values{
		"csrf_token":  {protecteeCSRF.Value},
		"device_name": {"Pixel 8a"},
	}, protecteeSession, protecteeCSRF)
	if enroll8a.Code != http.StatusOK {
		t.Fatalf("enroll Pixel 8a: %d", enroll8a.Code)
	}
	var addr8a string
	if err := env.db.QueryRow("SELECT address FROM devices WHERE protectee_id = ? AND name = 'Pixel 8a'", protecteeID).Scan(&addr8a); err != nil {
		t.Fatalf("query Pixel 8a address: %v", err)
	}
	if addr8a == "10.66.0.2" {
		t.Fatalf("quarantined address 10.66.0.2 was prematurely reused!")
	}
	if addr8a != "10.66.0.7" {
		t.Fatalf("Pixel 8a address = %s, want 10.66.0.7", addr8a)
	}

	// -------------------------------------------------------------------------
	// 7. Account Deletion Teardown Hook & Multi-Device Dismantling
	// -------------------------------------------------------------------------
	// Protectee currently holds 5 live devices:
	// "Laptop", "Work iPad", "Home PC", "Backup Phone", "Pixel 8a".
	peersPreDel, _ := env.tunnel.Peers(ctx)
	if len(peersPreDel) != 5 {
		t.Fatalf("tunnel has %d peers before deletion, want 5", len(peersPreDel))
	}
	if count := env.resolver.ClientCount(); count != 5 {
		t.Fatalf("resolver has %d clients before deletion, want 5", count)
	}

	// Bob also mints a device for himself to verify other users' devices survive protectee's deletion
	env.setAllowlist("protectee@example.com", "bob@example.com")
	bobDev := enrollTestDevice(t, env, userID(t, env, "bob@example.com"), "BobPhone")
	peersWithBob, _ := env.tunnel.Peers(ctx)
	if len(peersWithBob) != 6 {
		t.Fatalf("tunnel has %d peers including Bob's, want 6", len(peersWithBob))
	}
	if count := env.resolver.ClientCount(); count != 6 {
		t.Fatalf("resolver has %d clients including Bob's, want 6", count)
	}

	// Trigger Account Deletion for protectee
	delClock := time.Now().UTC().Add(2 * time.Hour)
	env.partnerships.SetDeviceTeardown(env.tunnel, env.resolver)
	deletion, err := env.partnerships.DeleteUser(ctx, protecteeID)
	if err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if deletion.DismantledDevices != 5 {
		t.Fatalf("deletion.DismantledDevices = %d, want 5", deletion.DismantledDevices)
	}

	// Zero orphan peers or resolver clients for protectee!
	// Only Bob's single device remains in tunnel and resolver.
	peersPostDel, _ := env.tunnel.Peers(ctx)
	if len(peersPostDel) != 1 {
		t.Fatalf("tunnel has %d peers after teardown, want exactly 1 (Bob's)", len(peersPostDel))
	}
	if !env.tunnel.HasPeer(bobDev.Device.PublicKey) {
		t.Fatal("Bob's peer was mistakenly removed during Protectee deletion")
	}

	bobClientName := enforcement.DeviceClientName(bobDev.Device.ID)
	if env.resolver.ClientCount() != 1 {
		t.Fatalf("resolver has %d clients after teardown, want exactly 1 (Bob's)", env.resolver.ClientCount())
	}
	if !env.resolver.HasClient(bobClientName) {
		t.Fatal("Bob's resolver client was mistakenly removed during Protectee deletion")
	}

	// All 6 Protectee devices (Pixel 8 + 5 dismantled) have removed_at IS NOT NULL in SQLite
	var liveDevicesRemaining int
	if err := env.db.QueryRow("SELECT COUNT(*) FROM devices WHERE protectee_id = ? AND removed_at IS NULL", protecteeID).Scan(&liveDevicesRemaining); err != nil {
		t.Fatal(err)
	}
	if liveDevicesRemaining != 0 {
		t.Fatalf("%d live devices survived account deletion in database", liveDevicesRemaining)
	}

	// All addresses from the deleted user are quarantined
	postDelMembership, err := env.enforcement.Devices(ctx)
	if err != nil {
		t.Fatalf("Devices after deletion: %v", err)
	}
	if len(postDelMembership.Quarantined) != 6 {
		t.Fatalf("Quarantined count = %d, want 6 (all addresses held for 8 days)", len(postDelMembership.Quarantined))
	}

	// Protectee's user record was pseudonymized with deleted_at, not hard deleted
	var deletedEmail string
	var deletedAt sql.NullInt64
	if err := env.db.QueryRow("SELECT email, deleted_at FROM users WHERE id = ?", protecteeID).Scan(&deletedEmail, &deletedAt); err != nil {
		t.Fatalf("query deleted user: %v", err)
	}
	if !deletedAt.Valid {
		t.Fatal("users.deleted_at was not set")
	}
	if !strings.HasPrefix(deletedEmail, "deleted-user-") {
		t.Fatalf("users.email was not pseudonymized: %q", deletedEmail)
	}

	// Idempotent retry of DeleteUser returns AlreadyDeleted and does nothing
	repeatDeletion, err := env.partnerships.DeleteUser(ctx, protecteeID)
	if err != nil {
		t.Fatalf("repeat DeleteUser: %v", err)
	}
	if !repeatDeletion.AlreadyDeleted {
		t.Fatalf("repeat DeleteUser = %+v, want AlreadyDeleted", repeatDeletion)
	}
	peersRepeat, _ := env.tunnel.Peers(ctx)
	if len(peersRepeat) != 1 || env.resolver.ClientCount() != 1 {
		t.Fatalf("repeat DeleteUser disturbed remaining peers or clients")
	}

	// -------------------------------------------------------------------------
	// 10. Pure Policy Predicates Intact (Zero Regressions)
	// -------------------------------------------------------------------------
	// Demonstrate that core policy predicates remain deny-closed and functional:
	baseP := policy.Policy{
		ExtraBlocks: []policy.Target{{Domain: "blocked.com"}},
	}
	loosenedP := policy.Policy{}
	tightenedP := policy.Policy{
		ExtraBlocks: []policy.Target{{Domain: "blocked.com"}, {Domain: "extra.com"}},
	}

	// When partnered, loosening is denied
	if res := policy.SubjectMayApplyEdit(baseP, loosenedP, delClock, true); res != policy.EditDeniedLoosen {
		t.Fatalf("SubjectMayApplyEdit(partnered, loosen) = %v, want %v", res, policy.EditDeniedLoosen)
	}
	// When partnered, tightening is allowed
	if res := policy.SubjectMayApplyEdit(baseP, tightenedP, delClock, true); res != policy.EditAllowed {
		t.Fatalf("SubjectMayApplyEdit(partnered, tighten) = %v, want %v", res, policy.EditAllowed)
	}
	// When unpartnered, any structurally valid edit is allowed
	if res := policy.SubjectMayApplyEdit(baseP, loosenedP, delClock, false); res != policy.EditAllowed {
		t.Fatalf("SubjectMayApplyEdit(unpartnered, loosen) = %v, want %v", res, policy.EditAllowed)
	}

	// Check that Deleted User is denied closed by partnership authorizer CanLoosen
	canLoosenDeleted, err := env.partnerships.CanLoosen(ctx, protecteeID, protecteeID)
	if err != nil {
		t.Fatalf("CanLoosen for deleted user: %v", err)
	}
	if canLoosenDeleted {
		t.Fatal("CanLoosen for deleted user was true, want deny-closed (false)")
	}
}
