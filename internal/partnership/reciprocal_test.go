package partnership

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// reciprocalPair drives two Users into Reciprocal Partnerships: two live
// Partnerships in opposite directions between the same pair. The forward
// direction shares Logs and the reverse one does not, so every test below can
// tell the two apart by more than their ids.
func reciprocalPair(t *testing.T, service *Service, alice, bob int64) {
	t.Helper()
	partnerUp(t, service, alice, bob, "bob@example.com", true)
	partnerUp(t, service, bob, alice, "alice@example.com", false)
}

// TestReverseInvitationDisclosesTheReciprocalEffectToInviteeAndPartners is
// AC 1 and AC 2 on the invitation side: a Protectee may invite the very
// Partner who already protects them — or, read from the other side, a Partner
// may invite their own Protectee — and both the invitee and every current
// Partner are told the reciprocal effect without being told the two
// Partnerships merge.
func TestReverseInvitationDisclosesTheReciprocalEffectToInviteeAndPartners(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	alice := createUser(t, db, "alice@example.com")
	bob := createUser(t, db, "bob@example.com")
	watcher := createUser(t, db, "watcher@example.com")
	// Bob protects Alice, and Bob also already has a Partner of his own, so
	// the reverse-direction invitation has a current Partner to alert.
	partnerUp(t, service, alice, bob, "bob@example.com", false)
	partnerUp(t, service, bob, watcher, "watcher@example.com", false)

	since := lastJobID(t, service)
	if _, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: bob, InviteeEmail: "alice@example.com", LogSharing: true,
	}); err != nil {
		t.Fatalf("reverse-direction invitation refused: %v", err)
	}

	invitee := queuedBodies(t, service, MessagePartnerInvitation, since)["alice@example.com"]
	for _, want := range []string{
		"bob@example.com already protects you as your Partner.",
		"second, independent Partnership in the opposite direction",
		"do not merge",
	} {
		if !strings.Contains(invitee, want) {
			t.Fatalf("the invitee's reciprocal disclosure is missing %q:\n%s", want, invitee)
		}
	}
	partner := queuedBodies(t, service, MessagePartnerInvitationSent, since)["watcher@example.com"]
	if !strings.Contains(partner, "Reciprocal Partnerships if accepted: Yes") {
		t.Fatalf("the current Partner was not told the reciprocal effect:\n%s", partner)
	}
}

// TestForwardInvitationStatesNoReciprocalEffect keeps the disclosure honest in
// the ordinary direction: an invitee who protects nobody is told nothing about
// a reverse Partnership that would not exist.
func TestForwardInvitationStatesNoReciprocalEffect(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	createUser(t, db, "invitee@example.com")

	since := lastJobID(t, service)
	invite(t, service, protectee, "invitee@example.com", false)
	invitee := queuedBodies(t, service, MessagePartnerInvitation, since)["invitee@example.com"]
	if strings.Contains(invitee, "already protects you") {
		t.Fatalf("a non-reciprocal invitation claimed a reverse Partnership:\n%s", invitee)
	}
}

// TestReviewDisclosesReciprocalOnlyToTheInvitedUser keeps the reverse-direction
// relationship behind the same masking every other identity is: a bearer link
// alone never reveals that the inviting Protectee already protects the
// invitee.
func TestReviewDisclosesReciprocalOnlyToTheInvitedUser(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	alice := createUser(t, db, "alice@example.com")
	bob := createUser(t, db, "bob@example.com")
	stranger := createUser(t, db, "stranger@example.com")
	partnerUp(t, service, alice, bob, "bob@example.com", false)
	invitation := invite(t, service, bob, "alice@example.com", false)

	invited, err := service.ReviewInvitation(ctx, ByToken(invitation.RawToken), alice)
	if err != nil {
		t.Fatal(err)
	}
	if !invited.Reciprocal {
		t.Fatal("the invited User was not told accepting creates a Reciprocal Partnership")
	}
	for _, viewer := range []int64{0, stranger} {
		review, err := service.ReviewInvitation(ctx, ByToken(invitation.RawToken), viewer)
		if err != nil {
			t.Fatal(err)
		}
		if review.Reciprocal {
			t.Fatalf("viewer %d learned the reverse-direction Partnership from a bearer link", viewer)
		}
	}
}

// TestReciprocalAcceptanceDisclosesBothDirectionsWithoutMergingPowers is AC 2
// on the acceptance side.
func TestReciprocalAcceptanceDisclosesBothDirectionsWithoutMergingPowers(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	alice := createUser(t, db, "alice@example.com")
	bob := createUser(t, db, "bob@example.com")
	partnerUp(t, service, alice, bob, "bob@example.com", false)

	since := lastJobID(t, service)
	partnerUp(t, service, bob, alice, "alice@example.com", false)

	protectee := queuedBodies(t, service, MessagePartnershipAcceptedExisting, since)["bob@example.com"]
	if !strings.Contains(protectee, "Reciprocal Partnerships: Yes") {
		t.Fatalf("the Protectee was not told the Partnership is reciprocal:\n%s", protectee)
	}
	newPartner := queuedBodies(t, service, MessagePartnershipAcceptedPartner, since)["alice@example.com"]
	for _, want := range []string{
		"Reciprocal Partnerships: Yes.",
		"bob@example.com already protects you",
		"changing one does not change the other",
	} {
		if !strings.Contains(newPartner, want) {
			t.Fatalf("the new Partner's reciprocal disclosure is missing %q:\n%s", want, newPartner)
		}
	}
}

// TestReciprocalDirectionsKeepIndependentLogSharingAndAuthorization is AC 3
// and AC 8 at the authorization boundary: neither direction lends the other
// any power, and Log sharing is answered per direction.
func TestReciprocalDirectionsKeepIndependentLogSharingAndAuthorization(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	alice := createUser(t, db, "alice@example.com")
	bob := createUser(t, db, "bob@example.com")
	reciprocalPair(t, service, alice, bob)

	// Both Users are locked as Protectees and both may act as Partners, which
	// is two independent answers rather than one merged authority.
	for _, direction := range []struct {
		name                 string
		actor, protectee     int64
		wantLoosen, wantLogs bool
	}{
		{"Alice over her own Policy", alice, alice, false, true},
		{"Bob over his own Policy", bob, bob, false, true},
		{"Bob as Alice's Partner", bob, alice, true, true},
		{"Alice as Bob's Partner", alice, bob, true, false},
	} {
		loosen, err := service.CanLoosen(ctx, direction.actor, direction.protectee)
		if err != nil {
			t.Fatal(err)
		}
		if loosen != direction.wantLoosen {
			t.Fatalf("CanLoosen(%s) = %v, want %v", direction.name, loosen, direction.wantLoosen)
		}
		logs, err := service.CanViewLogs(ctx, direction.actor, direction.protectee)
		if err != nil {
			t.Fatal(err)
		}
		if logs != direction.wantLogs {
			t.Fatalf("CanViewLogs(%s) = %v, want %v", direction.name, logs, direction.wantLogs)
		}
	}

	// Each direction is one Partnership, marked reciprocal in both areas of
	// each User's dashboard.
	view, err := service.ProtecteeView(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if !view.PartnerLockActive || len(view.Partners) != 1 || !view.Partners[0].Reciprocal {
		t.Fatalf("Alice's own protection did not mark the reciprocal Roster row: %+v", view)
	}
	protecting, err := service.ProtectingView(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(protecting) != 1 || !protecting[0].Reciprocal {
		t.Fatalf("People you protect did not mark the reciprocal direction: %+v", protecting)
	}
	if protecting[0].PartnershipID == view.Partners[0].PartnershipID {
		t.Fatal("the two directions resolved to one Partnership")
	}
	if protecting[0].LogSharing == view.Partners[0].LogSharing {
		t.Fatal("the two directions share one Log-sharing choice")
	}
}

// TestEndingOneReciprocalDirectionLeavesTheOtherUntouched is AC 4 and AC 8 for
// an immediate ending: the departing Partner keeps their own Protectee-side
// Partnership, and the surviving direction's powers and receipts do not move.
func TestEndingOneReciprocalDirectionLeavesTheOtherUntouched(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	alice := createUser(t, db, "alice@example.com")
	bob := createUser(t, db, "bob@example.com")
	reciprocalPair(t, service, alice, bob)

	alicesRoster, err := service.ProtecteeView(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	bobProtectsAlice := alicesRoster.Partners[0].PartnershipID
	if _, err := service.EndPartnership(ctx, EndCommand{
		PartnershipID: bobProtectsAlice, PartnerID: bob, Confirmation: "END",
	}); err != nil {
		t.Fatal(err)
	}

	// Alice is unpartnered and may loosen again; Bob is still locked by her.
	after, err := service.ProtecteeView(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if after.PartnerLockActive || len(after.Partners) != 0 {
		t.Fatalf("ending Bob's direction left Alice partnered: %+v", after)
	}
	bobsView, err := service.ProtecteeView(ctx, bob)
	if err != nil {
		t.Fatal(err)
	}
	if !bobsView.PartnerLockActive || len(bobsView.Partners) != 1 {
		t.Fatalf("ending one direction mutated the reverse direction: %+v", bobsView)
	}
	if bobsView.Partners[0].Reciprocal {
		t.Fatal("Bob's surviving Roster row still claims a reciprocal Partnership")
	}
	loosen, err := service.CanLoosen(ctx, alice, alice)
	if err != nil {
		t.Fatal(err)
	}
	if !loosen {
		t.Fatal("Alice may not loosen her own Policy after her only Partner left")
	}
	stillPartner, err := service.CanLoosen(ctx, alice, bob)
	if err != nil {
		t.Fatal(err)
	}
	if !stillPartner {
		t.Fatal("Alice lost her Partner powers over Bob when Bob ended his own direction")
	}
	// Bob keeps a receipt for the direction he ended and nothing for the one
	// he is still the Protectee of.
	receipts, err := service.EndedPartnerships(ctx, bob)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || receipts[0].PartnershipID != bobProtectsAlice {
		t.Fatalf("receipts = %+v, want only the direction Bob ended", receipts)
	}
}

// TestCoolingOffOnOneReciprocalDirectionDoesNotScheduleTheOther is AC 3 and
// AC 4 for the 72-hour ending: one direction runs a deadline while the other
// stays unscheduled, and completing it leaves the reverse direction live.
func TestCoolingOffOnOneReciprocalDirectionDoesNotScheduleTheOther(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	alice := createUser(t, db, "alice@example.com")
	bob := createUser(t, db, "bob@example.com")
	reciprocalPair(t, service, alice, bob)

	alicesRoster, err := service.ProtecteeView(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	bobProtectsAlice := alicesRoster.Partners[0].PartnershipID
	if _, err := service.StartCoolingOff(ctx, CoolingOffCommand{
		PartnershipID: bobProtectsAlice, ProtecteeID: alice,
	}); err != nil {
		t.Fatal(err)
	}

	// Alice's direction counts down; Bob's own protection is unscheduled.
	scheduled, err := service.ProtecteeView(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if !scheduled.Partners[0].CoolingOffRunning() {
		t.Fatal("the requested direction is not counting down")
	}
	bobsView, err := service.ProtecteeView(ctx, bob)
	if err != nil {
		t.Fatal(err)
	}
	if bobsView.Partners[0].CoolingOffRunning() {
		t.Fatal("Alice's request scheduled the reverse direction as well")
	}
	// Bob cannot cancel or end the direction Alice scheduled from his own
	// Protectee side, and Alice cannot request an ending on Bob's direction.
	if _, err := service.CancelCoolingOff(ctx, CoolingOffCommand{
		PartnershipID: bobProtectsAlice, ProtecteeID: bob,
	}); !errors.Is(err, ErrPartnershipUnavailable) {
		t.Fatalf("Bob canceled the other direction's Cooling-off: %v", err)
	}
	if _, err := service.StartCoolingOff(ctx, CoolingOffCommand{
		PartnershipID: bobsView.Partners[0].PartnershipID, ProtecteeID: alice,
	}); !errors.Is(err, ErrPartnershipUnavailable) {
		t.Fatalf("Alice scheduled Bob's own Partnership: %v", err)
	}

	at(service, testClock.Add(72*time.Hour))
	completed, err := service.CompleteDueCoolingOff(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if completed != 1 {
		t.Fatalf("CompleteDueCoolingOff settled %d Partnerships, want exactly the scheduled direction", completed)
	}
	survivor, err := service.ProtecteeView(ctx, bob)
	if err != nil {
		t.Fatal(err)
	}
	if !survivor.PartnerLockActive || len(survivor.Partners) != 1 {
		t.Fatalf("the completed Cooling-off ended the reverse direction too: %+v", survivor)
	}
}

// activityFor lists one Protectee's chronology as `action:partnership_id`
// pairs, oldest first, so a test can assert which direction each entry was
// attributed to rather than only how many there are.
func activityFor(t *testing.T, service *Service, protecteeID int64) []string {
	t.Helper()
	rows, err := service.db.Query(`
		SELECT action, COALESCE(partnership_id, 0) FROM activity_entries
		WHERE protectee_id = ? ORDER BY id`, protecteeID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var entries []string
	for rows.Next() {
		var action string
		var partnershipID int64
		if err := rows.Scan(&action, &partnershipID); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, fmt.Sprintf("%s:%d", action, partnershipID))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return entries
}

// TestActivityAttributesEachReciprocalDirectionToItsOwnProtectee is AC 8 on the
// Activity side: every entry lands in the chronology of the Protectee whose
// Partnership it describes, and names that Partnership, so the two directions
// never share a record even though the same two Users appear in both.
func TestActivityAttributesEachReciprocalDirectionToItsOwnProtectee(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	alice := createUser(t, db, "alice@example.com")
	bob := createUser(t, db, "bob@example.com")
	reciprocalPair(t, service, alice, bob)

	alicesRoster, err := service.ProtecteeView(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	bobProtectsAlice := alicesRoster.Partners[0].PartnershipID
	bobsRoster, err := service.ProtecteeView(ctx, bob)
	if err != nil {
		t.Fatal(err)
	}
	aliceProtectsBob := bobsRoster.Partners[0].PartnershipID

	// Alice schedules her own ending and Bob ends it now, so both directions
	// have seen every kind of entry this slice appends.
	if _, err := service.StartCoolingOff(ctx, CoolingOffCommand{
		PartnershipID: bobProtectsAlice, ProtecteeID: alice,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.EndPartnership(ctx, EndCommand{
		PartnershipID: bobProtectsAlice, PartnerID: bob, Confirmation: "END",
	}); err != nil {
		t.Fatal(err)
	}

	wantAlice := []string{
		ActionInvitationSent + ":0",
		fmt.Sprintf("%s:%d", ActionInvitationAccepted, bobProtectsAlice),
		fmt.Sprintf("%s:%d", ActionCoolingOffStarted, bobProtectsAlice),
		fmt.Sprintf("%s:%d", ActionPartnershipEndedByPartner, bobProtectsAlice),
	}
	if got := activityFor(t, service, alice); !slices.Equal(got, wantAlice) {
		t.Fatalf("Alice's Activity = %v, want %v", got, wantAlice)
	}
	// Bob's own chronology records only his own direction. Nothing Alice did
	// to the Partnership protecting her reached it.
	wantBob := []string{
		ActionInvitationSent + ":0",
		fmt.Sprintf("%s:%d", ActionInvitationAccepted, aliceProtectsBob),
	}
	if got := activityFor(t, service, bob); !slices.Equal(got, wantBob) {
		t.Fatalf("Bob's Activity = %v, want %v", got, wantBob)
	}
}
