package webapp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// deleteUserByEmail runs the Partnership module's teardown hook for one User,
// which is what the later account-deletion slice will call on its deadline.
// These tests exercise only the retained views it leaves behind; there is no
// account-deletion screen to drive.
func deleteUserByEmail(t *testing.T, env *testEnv, email string) {
	t.Helper()
	var id int64
	if err := env.db.QueryRow("SELECT id FROM users WHERE email = ?", email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := env.partnerships.DeleteUser(context.Background(), id); err != nil {
		t.Fatalf("DeleteUser(%s): %v", email, err)
	}
}

func TestReceiptForADeletedProtecteeShowsOnlyDeletedUserTheCauseAndTheInstant(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	_, _, partnerSession, partnerCSRF, partnershipID := livePartnership(t, env)

	deleteUserByEmail(t, env, "protectee@example.com")

	receiptURL := fmt.Sprintf("https://lite.nightloch.app/partners/ended-partnerships/%d", partnershipID)
	receipt := get(t, env, receiptURL, partnerSession, partnerCSRF)
	if receipt.Code != http.StatusOK {
		t.Fatalf("GET receipt = %d", receipt.Code)
	}
	body := receipt.Body.String()
	if strings.Contains(body, "protectee@example.com") {
		t.Fatalf("the receipt still names the deleted verified email:\n%s", body)
	}
	if !strings.Contains(body, "Deleted User") {
		t.Fatalf("the receipt does not identify the former Protectee as Deleted User:\n%s", body)
	}
	if !strings.Contains(body, "deleted their Nightloch User") {
		t.Fatalf("the receipt does not state deletion as the ending cause:\n%s", body)
	}
	if strings.Contains(body, "Partner since") || strings.Contains(body, "Log sharing while active") {
		t.Fatalf("the receipt retained more than the cause and the instant:\n%s", body)
	}
	// A delivered email is external history and the receipt says so rather
	// than implying Nightloch could take one back.
	if !strings.Contains(body, "cannot be recalled or pseudonymized") {
		t.Fatalf("the receipt does not treat delivered email as external history:\n%s", body)
	}

	// The dashboard offers the same receipt and no more.
	dashboard := get(t, env, "https://lite.nightloch.app/", partnerSession, partnerCSRF).Body.String()
	if strings.Contains(dashboard, "protectee@example.com") {
		t.Fatalf("the dashboard still names the deleted User:\n%s", dashboard)
	}
	if !strings.Contains(dashboard, "Deleted User") {
		t.Fatalf("the dashboard dropped the retained receipt:\n%s", dashboard)
	}
	// Nothing else about them is reachable: not their Activity, and not the
	// Roster the Partner used to read.
	var protecteeID int64
	if err := env.db.QueryRow("SELECT protectee_id FROM partnerships WHERE id = ?", partnershipID).
		Scan(&protecteeID); err != nil {
		t.Fatal(err)
	}
	activity := get(t, env, fmt.Sprintf("https://lite.nightloch.app/activity/%d", protecteeID),
		partnerSession, partnerCSRF)
	if activity.Code != http.StatusNotFound {
		t.Fatalf("GET the deleted Protectee's Activity = %d", activity.Code)
	}
}

func TestRetainedActivityRendersADeletedActorAsDeletedUser(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protecteeSession, protecteeCSRF, _, _, _ := livePartnership(t, env)

	// The Partner deletes their own User, which ends the Partnership and
	// pseudonymizes everything they did on the Protectee's chronology.
	deleteUserByEmail(t, env, "partner@example.com")

	activity := get(t, env, "https://lite.nightloch.app/activity", protecteeSession, protecteeCSRF)
	if activity.Code != http.StatusOK {
		t.Fatalf("GET /activity = %d", activity.Code)
	}
	body := activity.Body.String()
	if strings.Contains(body, "partner@example.com") {
		t.Fatalf("retained Activity still names the deleted User:\n%s", body)
	}
	if !strings.Contains(body, "Deleted User") {
		t.Fatalf("retained Activity dropped the deleted actor instead of pseudonymizing them:\n%s", body)
	}
	// The authorized actions themselves survive, with their own instants.
	for _, kept := range []string{"invited", "accepted", "deleted their Nightloch User"} {
		if !strings.Contains(body, kept) {
			t.Fatalf("retained Activity lost %q:\n%s", kept, body)
		}
	}
	// The Protectee is unpartnered again, so their own dashboard says so
	// without naming who left.
	dashboard := get(t, env, "https://lite.nightloch.app/", protecteeSession, protecteeCSRF).Body.String()
	if strings.Contains(dashboard, "partner@example.com") {
		t.Fatalf("the Roster still names the deleted Partner:\n%s", dashboard)
	}
}

func TestADeletedUserHoldsNoSessionAndNoInvitation(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protecteeSession, protecteeCSRF, _, _, _ := livePartnership(t, env)

	deleteUserByEmail(t, env, "protectee@example.com")

	// Their own signed-in session stops being one, so a deleted User cannot
	// keep reading Nightloch through a cookie issued before the deletion.
	home := get(t, env, "https://lite.nightloch.app/", protecteeSession, protecteeCSRF)
	if home.Code != http.StatusSeeOther {
		t.Fatalf("GET / as a deleted User = %d, want a redirect to sign-in", home.Code)
	}
	activity := get(t, env, "https://lite.nightloch.app/activity", protecteeSession, protecteeCSRF)
	if activity.Code != http.StatusSeeOther {
		t.Fatalf("GET /activity as a deleted User = %d", activity.Code)
	}
}
