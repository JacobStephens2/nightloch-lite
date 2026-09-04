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

	"github.com/JacobStephens2/nightloch-lite/internal/enforcement"
	"github.com/JacobStephens2/nightloch-lite/internal/partnership"
)

func userIDByEmail(t *testing.T, db *sql.DB, email string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow("SELECT id FROM users WHERE email = ?", email).Scan(&id); err != nil {
		t.Fatalf("query user id for %s: %v", email, err)
	}
	return id
}

func enrollTestDevice(t *testing.T, env *testEnv, protecteeID int64, name string) enforcement.MintedDevice {
	t.Helper()
	minted, err := env.enforcement.EnrollDevice(context.Background(), protecteeID, name)
	if err != nil {
		t.Fatalf("enroll device %s: %v", name, err)
	}
	return minted
}

func TestDashboardYourDevicesForProtectee(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)

	// Case 1: Non-allowlisted user with 0 devices sees beta notice instead of "Enroll a Device" button
	protecteeSession, protecteeCSRF := signIn(t, env, "protectee@example.com")
	dashboard := get(t, env, "https://lite.nightloch.app/", protecteeSession, protecteeCSRF).Body.String()
	if !strings.Contains(dashboard, "Your Devices") {
		t.Fatalf("dashboard missing 'Your Devices':\n%s", dashboard)
	}
	if strings.Contains(dashboard, "Enroll a Device") {
		t.Fatalf("non-allowlisted dashboard should not show 'Enroll a Device' button:\n%s", dashboard)
	}
	if !strings.Contains(dashboard, "Device enrollment is invite-only during beta") {
		t.Fatalf("non-allowlisted dashboard missing beta notice:\n%s", dashboard)
	}

	// Case 2: Allowlisted user sees "Enroll a Device" button
	env.setAllowlist("protectee@example.com")
	dashboard = get(t, env, "https://lite.nightloch.app/", protecteeSession, protecteeCSRF).Body.String()
	if !strings.Contains(dashboard, "Enroll a Device") {
		t.Fatalf("allowlisted dashboard missing 'Enroll a Device' button:\n%s", dashboard)
	}

	// Case 3: Enrolled devices are rendered with Name, IP, enrolled UTC, and Remove link
	protecteeID := userIDByEmail(t, env.db, "protectee@example.com")
	dev1 := enrollTestDevice(t, env, protecteeID, "Pixel 8")
	dev2 := enrollTestDevice(t, env, protecteeID, "MacBook Pro")

	dashboard = get(t, env, "https://lite.nightloch.app/", protecteeSession, protecteeCSRF).Body.String()
	for _, want := range []string{
		"Pixel 8",
		dev1.Device.Address.String(),
		fmt.Sprintf("/devices/%d/remove", dev1.Device.ID),
		"MacBook Pro",
		dev2.Device.Address.String(),
		fmt.Sprintf("/devices/%d/remove", dev2.Device.ID),
		"Remove Device",
	} {
		if !strings.Contains(dashboard, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, dashboard)
		}
	}
}

func TestDashboardProtecteeDevicesForPartner(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	_, _, partnerSession, partnerCSRF, _ := livePartnership(t, env)
	protecteeID := userIDByEmail(t, env.db, "protectee@example.com")

	dev := enrollTestDevice(t, env, protecteeID, "Work iPad")

	// Partner views dashboard under "People you protect"
	partnerDashboard := get(t, env, "https://lite.nightloch.app/", partnerSession, partnerCSRF).Body.String()
	if !strings.Contains(partnerDashboard, "People you protect") {
		t.Fatalf("missing 'People you protect' in partner dashboard:\n%s", partnerDashboard)
	}
	for _, want := range []string{
		"Work iPad",
		dev.Device.Address.String(),
		fmt.Sprintf("/devices/%d/remove", dev.Device.ID),
		"Remove Device",
	} {
		if !strings.Contains(partnerDashboard, want) {
			t.Fatalf("partner dashboard missing Protectee device detail %q:\n%s", want, partnerDashboard)
		}
	}
}

func TestDeviceRemovalConfirmationScreen(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protecteeSession, protecteeCSRF, partnerSession, partnerCSRF, _ := livePartnership(t, env)
	protecteeID := userIDByEmail(t, env.db, "protectee@example.com")
	dev := enrollTestDevice(t, env, protecteeID, "Personal Phone")
	removeURL := fmt.Sprintf("https://lite.nightloch.app/devices/%d/remove", dev.Device.ID)

	// 1. Unauthenticated visitor redirected to sign-in
	anon := get(t, env, removeURL)
	if anon.Code != http.StatusSeeOther || anon.Header().Get("Location") != "/sign-in" {
		t.Fatalf("anonymous GET = %d, want redirect to /sign-in", anon.Code)
	}

	// 2. Unrelated signed-in user refused (403 Forbidden)
	strangerSession, strangerCSRF := signIn(t, env, "stranger@example.com")
	strangerRec := get(t, env, removeURL, strangerSession, strangerCSRF)
	if strangerRec.Code != http.StatusForbidden {
		t.Fatalf("stranger GET = %d, want 403 Forbidden", strangerRec.Code)
	}

	// 3. Unknown or non-existent device ID returns 404
	notFoundRec := get(t, env, "https://lite.nightloch.app/devices/99999/remove", protecteeSession, protecteeCSRF)
	if notFoundRec.Code != http.StatusNotFound {
		t.Fatalf("unknown device GET = %d, want 404", notFoundRec.Code)
	}

	// 4. Protectee owner confirmation screen
	protecteeRec := get(t, env, removeURL, protecteeSession, protecteeCSRF)
	if protecteeRec.Code != http.StatusOK {
		t.Fatalf("protectee GET = %d, want 200", protecteeRec.Code)
	}
	body := protecteeRec.Body.String()
	for _, want := range []string{
		"Personal Phone",
		dev.Device.Address.String(),
		"Block connections without VPN",
		"offline",
		"8 days",
		"quarantine",
		"Remove Device",
		"Cancel",
		fmt.Sprintf(`action="/devices/%d/remove"`, dev.Device.ID),
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("confirmation screen missing %q:\n%s", want, body)
		}
	}

	// 5. Active Partner confirmation screen
	partnerRec := get(t, env, removeURL, partnerSession, partnerCSRF)
	if partnerRec.Code != http.StatusOK {
		t.Fatalf("partner GET = %d, want 200", partnerRec.Code)
	}
	partnerBody := partnerRec.Body.String()
	if !strings.Contains(partnerBody, "Personal Phone") || !strings.Contains(partnerBody, "Remove Device") {
		t.Fatalf("partner confirmation screen missing device info:\n%s", partnerBody)
	}
}

func TestDeviceRemovalByProtectee(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protecteeSession, protecteeCSRF, _, _, _ := livePartnership(t, env)
	protecteeID := userIDByEmail(t, env.db, "protectee@example.com")
	dev := enrollTestDevice(t, env, protecteeID, "Old Phone")
	removeURL := fmt.Sprintf("https://lite.nightloch.app/devices/%d/remove", dev.Device.ID)

	clientName := enforcement.DeviceClientName(dev.Device.ID)
	if !env.resolver.HasClient(clientName) {
		t.Fatalf("expected resolver client %s to exist before removal", clientName)
	}

	// Clear sender envelopes from invitation flow
	env.sender.Envelopes = nil

	// Submit removal
	form := url.Values{"csrf_token": []string{protecteeCSRF.Value}}
	rec := post(t, env, removeURL, form, protecteeSession, protecteeCSRF)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST remove = %d, want 303 SeeOther", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/") {
		t.Fatalf("Location = %q, want redirect to dashboard", loc)
	}

	dash := get(t, env, "https://lite.nightloch.app"+loc, protecteeSession, protecteeCSRF)
	if !strings.Contains(html.UnescapeString(dash.Body.String()), `Device "Old Phone" was removed.`) {
		t.Fatalf("dashboard missing removal confirmation message:\n%s", dash.Body.String())
	}

	// Resolver client deleted
	if env.resolver.HasClient(clientName) {
		t.Fatalf("resolver still has client %s after removal", clientName)
	}

	// WireGuard peer withdrawn
	if len(env.tunnel.Calls) == 0 || env.tunnel.Calls[len(env.tunnel.Calls)-1] != "remove-peer "+dev.Device.PublicKey {
		t.Fatalf("tunnel calls = %v, want last call to be remove-peer", env.tunnel.Calls)
	}

	// DB marked removed_at
	var removedAt sql.NullInt64
	if err := env.db.QueryRow("SELECT removed_at FROM devices WHERE id = ?", dev.Device.ID).Scan(&removedAt); err != nil {
		t.Fatal(err)
	}
	if !removedAt.Valid {
		t.Fatal("device removed_at is null, want timestamp")
	}

	// Activity entry logged under CategoryDevices
	activity, err := env.partnerships.Activity(context.Background(), protecteeID, protecteeID, partnership.ActivityQuery{
		Category: partnership.CategoryDevices,
	})
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if len(activity.Entries) != 1 {
		t.Fatalf("got %d activity entries, want 1", len(activity.Entries))
	}
	if activity.Entries[0].Action != partnership.ActionDeviceRemoved {
		t.Errorf("action = %s, want %s", activity.Entries[0].Action, partnership.ActionDeviceRemoved)
	}

	// Action alert email queued to Partner
	dispatched, err := env.partnerships.DispatchDue(context.Background(), env.sender, 10)
	if err != nil {
		t.Fatalf("DispatchDue: %v", err)
	}
	if dispatched != 1 {
		t.Fatalf("dispatched = %d, want 1 mail job to active partner", dispatched)
	}
	if len(env.sender.Envelopes) != 1 || env.sender.Envelopes[0].To != "partner@example.com" {
		t.Fatalf("envelope = %+v, want sent to partner@example.com", env.sender.Envelopes)
	}
	if !strings.Contains(env.sender.Envelopes[0].Text, "Old Phone") {
		t.Errorf("alert email missing device name:\n%s", env.sender.Envelopes[0].Text)
	}
}

func TestDeviceRemovalByPartner(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	_, _, partnerSession, partnerCSRF, _ := livePartnership(t, env)
	protecteeID := userIDByEmail(t, env.db, "protectee@example.com")
	dev := enrollTestDevice(t, env, protecteeID, "Compromised Laptop")
	removeURL := fmt.Sprintf("https://lite.nightloch.app/devices/%d/remove", dev.Device.ID)

	env.sender.Envelopes = nil

	// Partner removes device
	form := url.Values{"csrf_token": []string{partnerCSRF.Value}}
	rec := post(t, env, removeURL, form, partnerSession, partnerCSRF)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("Partner POST remove = %d, want 303 SeeOther", rec.Code)
	}

	// Resolver client deleted
	clientName := enforcement.DeviceClientName(dev.Device.ID)
	if env.resolver.HasClient(clientName) {
		t.Fatalf("resolver still has client %s after removal", clientName)
	}

	// Action alert emails sent to Protectee
	dispatched, err := env.partnerships.DispatchDue(context.Background(), env.sender, 10)
	if err != nil {
		t.Fatalf("DispatchDue: %v", err)
	}
	if dispatched != 1 {
		t.Fatalf("dispatched = %d, want 1 mail job to Protectee", dispatched)
	}
	if len(env.sender.Envelopes) != 1 || env.sender.Envelopes[0].To != "protectee@example.com" {
		t.Fatalf("envelope = %+v, want sent to protectee@example.com", env.sender.Envelopes)
	}
	if !strings.Contains(env.sender.Envelopes[0].Text, "partner@example.com") {
		t.Errorf("alert email missing actor email:\n%s", env.sender.Envelopes[0].Text)
	}
	if !strings.Contains(env.sender.Envelopes[0].Text, "Compromised Laptop") {
		t.Errorf("alert email missing device name:\n%s", env.sender.Envelopes[0].Text)
	}
}

func TestDeviceRemovalRefusesUnauthorizedAndInvalidCSRF(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protecteeSession, protecteeCSRF, _, _, _ := livePartnership(t, env)
	protecteeID := userIDByEmail(t, env.db, "protectee@example.com")
	dev := enrollTestDevice(t, env, protecteeID, "Secure Tablet")
	removeURL := fmt.Sprintf("https://lite.nightloch.app/devices/%d/remove", dev.Device.ID)

	// 1. Unauthenticated post
	anonRec := post(t, env, removeURL, url.Values{"csrf_token": []string{"forged"}})
	if anonRec.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated forged POST = %d, want 403", anonRec.Code)
	}

	// 2. Unrelated signed-in user
	strangerSession, strangerCSRF := signIn(t, env, "stranger@example.com")
	strangerRec := post(t, env, removeURL, url.Values{"csrf_token": []string{strangerCSRF.Value}}, strangerSession, strangerCSRF)
	if strangerRec.Code != http.StatusForbidden {
		t.Fatalf("stranger POST = %d, want 403 Forbidden", strangerRec.Code)
	}

	// 3. Missing CSRF token
	missingCSRFRec := post(t, env, removeURL, url.Values{}, protecteeSession, protecteeCSRF)
	if missingCSRFRec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF POST = %d, want 403 Forbidden", missingCSRFRec.Code)
	}

	// 4. Valid removal
	validRec := post(t, env, removeURL, url.Values{"csrf_token": []string{protecteeCSRF.Value}}, protecteeSession, protecteeCSRF)
	if validRec.Code != http.StatusSeeOther {
		t.Fatalf("valid removal POST = %d, want 303", validRec.Code)
	}

	// 5. Repeated removal of already-removed device
	repeatRec := post(t, env, removeURL, url.Values{"csrf_token": []string{protecteeCSRF.Value}}, protecteeSession, protecteeCSRF)
	if repeatRec.Code != http.StatusNotFound {
		t.Fatalf("repeat removal POST = %d, want 404 Not Found", repeatRec.Code)
	}
}

func TestRemovedDeviceAddressQuarantinedCannotBeReEnrolled(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protecteeSession, protecteeCSRF := signIn(t, env, "protectee@example.com")
	protecteeID := userIDByEmail(t, env.db, "protectee@example.com")
	env.setAllowlist("protectee@example.com")

	dev1 := enrollTestDevice(t, env, protecteeID, "Device 1")
	firstAddress := dev1.Device.Address

	// Remove Device 1
	removeURL := fmt.Sprintf("https://lite.nightloch.app/devices/%d/remove", dev1.Device.ID)
	rec := post(t, env, removeURL, url.Values{"csrf_token": []string{protecteeCSRF.Value}}, protecteeSession, protecteeCSRF)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST remove = %d", rec.Code)
	}

	// Enrolling Device 2 must allocate the next available address, not the quarantined firstAddress
	dev2 := enrollTestDevice(t, env, protecteeID, "Device 2")
	if dev2.Device.Address == firstAddress {
		t.Fatalf("enrolled device 2 received quarantined address %s", firstAddress)
	}
}
