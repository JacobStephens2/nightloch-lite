package partnership

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// visibleActions is one viewer's authorized chronology reduced to its stable
// actions, newest first. Every window assertion in this file is stated in
// these, because a window is about which events reach a reader at all.
func visibleActions(t *testing.T, service *Service, protecteeID, viewerID int64) []string {
	t.Helper()
	view, err := service.Activity(context.Background(), protecteeID, viewerID)
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	actions := make([]string, 0, len(view.Entries))
	for _, entry := range view.Entries {
		actions = append(actions, entry.Action)
	}
	return actions
}

// refuseActivity asserts that this viewer reads nothing at all about this
// Protectee, and that the refusal is the single unavailable answer rather than
// anything that distinguishes "not yours" from "no such User".
func refuseActivity(t *testing.T, service *Service, protecteeID, viewerID int64) {
	t.Helper()
	view, err := service.Activity(context.Background(), protecteeID, viewerID)
	if !errors.Is(err, ErrActivityUnavailable) {
		t.Fatalf("Activity = %+v, %v; want ErrActivityUnavailable", view, err)
	}
	if len(view.Entries) != 0 || view.ProtecteeEmail != "" {
		t.Fatalf("a refused Activity read still disclosed %+v", view)
	}
}

// partnershipOf is the live Partnership one Partner holds over one Protectee,
// which is how a test names the relationship it is about to end.
func partnershipOf(t *testing.T, service *Service, protecteeID, partnerID int64) int64 {
	t.Helper()
	var id int64
	if err := service.db.QueryRow(
		"SELECT id FROM partnerships WHERE protectee_id = ? AND partner_id = ? AND ended_at IS NULL",
		protecteeID, partnerID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func sameActions(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func TestProtecteeReadsTheirCompleteActivityNewestFirst(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")

	first := invite(t, service, protectee, "partner@example.com", false)
	advance(time.Hour)
	acceptFor(t, service, first, partner)
	advance(time.Hour)
	second := invite(t, service, protectee, "other@example.com", false)
	advance(time.Hour)
	if _, err := service.CancelInvitation(context.Background(), second.ID, protectee); err != nil {
		t.Fatalf("CancelInvitation: %v", err)
	}

	want := []string{
		ActionInvitationCanceled, ActionInvitationSent,
		ActionInvitationAccepted, ActionInvitationSent,
	}
	if got := visibleActions(t, service, protectee, protectee); !sameActions(got, want) {
		t.Fatalf("Protectee Activity = %v, want %v", got, want)
	}

	view, err := service.Activity(context.Background(), protectee, protectee)
	if err != nil {
		t.Fatal(err)
	}
	if view.ProtecteeEmail != "protectee@example.com" || view.Windowed() {
		t.Fatalf("the Protectee's own view is windowed: %+v", view)
	}
	if summary := view.Entries[0].Summary(); !strings.Contains(summary, "protectee@example.com") ||
		!strings.Contains(summary, "other@example.com") {
		t.Fatalf("cancellation entry reads %q", summary)
	}
}

func TestPartnerActivityBeginsAtTheExactAcceptanceInstant(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")

	// The invitation and the acceptance share one second, which is the sharp
	// case: the window opens at the acceptance itself, so the invitation that
	// produced it stays outside even though both are stamped identically.
	invitation := invite(t, service, protectee, "partner@example.com", false)
	acceptance := acceptFor(t, service, invitation, partner)
	advance(time.Hour)
	// A later authorized action inside the window, and one the Partner is
	// entitled to observe because they are on the Roster it changes.
	invite(t, service, protectee, "later@example.com", false)

	want := []string{ActionInvitationSent, ActionInvitationAccepted}
	if got := visibleActions(t, service, protectee, partner); !sameActions(got, want) {
		t.Fatalf("Partner Activity = %v, want %v", got, want)
	}

	view, err := service.Activity(ctx, protectee, partner)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Windowed() || !view.VisibleFrom.Equal(acceptance.AcceptedAt) {
		t.Fatalf("window opens at %v, want the acceptance instant %v", view.VisibleFrom, acceptance.AcceptedAt)
	}
	// The oldest thing they read is their own acceptance, never the
	// invitation stamped in the same second that produced it.
	oldest := view.Entries[len(view.Entries)-1]
	if oldest.Action != ActionInvitationAccepted || oldest.OccurredAt.Before(acceptance.AcceptedAt) {
		t.Fatalf("the Partner read an entry from before acceptance: %+v", oldest)
	}
}

func TestPendingInviteeReadsNoActivityBeforeAccepting(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	invitee := createUser(t, db, "invitee@example.com")
	invite(t, service, protectee, "invitee@example.com", false)

	refuseActivity(t, service, protectee, invitee)
}

func TestEndingAPartnershipRemovesActivityAndRosterButKeepsTheReceipt(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")
	partnerUp(t, service, protectee, partner, "partner@example.com", false)

	advance(time.Hour)
	ending, err := service.EndPartnership(ctx, EndCommand{
		PartnershipID: partnershipOf(t, service, protectee, partner),
		PartnerID:     partner, Confirmation: "END",
	})
	if err != nil {
		t.Fatalf("EndPartnership: %v", err)
	}

	// The ending instant is the moment access stops, including the ending's
	// own entry, which the former Partner reads on their receipt instead.
	refuseActivity(t, service, protectee, partner)
	protecting, err := service.ProtectingView(ctx, partner)
	if err != nil || len(protecting) != 0 {
		t.Fatalf("People you protect = %v, %v", protecting, err)
	}
	receipt, err := service.EndedPartnershipReceipt(ctx, ending.PartnershipID, partner)
	if err != nil {
		t.Fatalf("EndedPartnershipReceipt: %v", err)
	}
	if !receipt.EndedAt.Equal(ending.EndedAt) || receipt.ProtecteeEmail != "protectee@example.com" {
		t.Fatalf("receipt = %+v", receipt)
	}
	// The Protectee's own chronology is unaffected by the other side losing it.
	if got := visibleActions(t, service, protectee, protectee); len(got) != 3 {
		t.Fatalf("Protectee Activity after the ending = %v", got)
	}
}

func TestLaterPartnershipOpensANewWindowAndHidesTheGap(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")
	partnerUp(t, service, protectee, partner, "partner@example.com", false)
	firstPartnership := partnershipOf(t, service, protectee, partner)

	advance(time.Hour)
	if _, err := service.EndPartnership(ctx, EndCommand{
		PartnershipID: firstPartnership, PartnerID: partner, Confirmation: "END",
	}); err != nil {
		t.Fatalf("EndPartnership: %v", err)
	}

	// The gap: an invitation the returning Partner was not part of and must
	// never learn about.
	advance(time.Hour)
	gap := invite(t, service, protectee, "gap@example.com", false)
	advance(time.Hour)
	if _, err := service.CancelInvitation(ctx, gap.ID, protectee); err != nil {
		t.Fatalf("CancelInvitation: %v", err)
	}

	advance(time.Hour)
	second := invite(t, service, protectee, "partner@example.com", false)
	advance(time.Hour)
	rejoined := acceptFor(t, service, second, partner)
	secondPartnership := partnershipOf(t, service, protectee, partner)
	if secondPartnership == firstPartnership {
		t.Fatal("re-partnering reused the ended Partnership record")
	}

	want := []string{ActionInvitationAccepted}
	if got := visibleActions(t, service, protectee, partner); !sameActions(got, want) {
		t.Fatalf("returning Partner Activity = %v, want %v", got, want)
	}
	view, err := service.Activity(ctx, protectee, partner)
	if err != nil {
		t.Fatal(err)
	}
	if !view.VisibleFrom.Equal(rejoined.AcceptedAt) {
		t.Fatalf("window opens at %v, want the new acceptance %v", view.VisibleFrom, rejoined.AcceptedAt)
	}
	for _, entry := range view.Entries {
		if strings.Contains(entry.Summary(), "gap@example.com") {
			t.Fatalf("the returning Partner read an event from the gap: %+v", entry)
		}
	}

	// The old Partnership survives as its own receipt with its own outcome.
	receipts, err := service.EndedPartnerships(ctx, partner)
	if err != nil || len(receipts) != 1 || receipts[0].PartnershipID != firstPartnership {
		t.Fatalf("retained receipts = %+v, %v", receipts, err)
	}
}

func TestReciprocalPartnershipsKeepSeparateWindowsInEachDirection(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	ctx := context.Background()
	ada := createUser(t, db, "ada@example.com")
	bo := createUser(t, db, "bo@example.com")

	// Bo becomes Ada's Partner first, so Ada's chronology already holds an
	// entry before the reverse direction exists.
	partnerUp(t, service, ada, bo, "bo@example.com", false)
	advance(time.Hour)
	// Bo now invites Ada, which Ada answers an hour later.
	reverse := invite(t, service, bo, "ada@example.com", false)
	advance(time.Hour)
	acceptedReverse := acceptFor(t, service, reverse, ada)

	// Bo reads Ada's chronology from Bo's acceptance of Ada's invitation.
	if got := visibleActions(t, service, ada, bo); !sameActions(got, []string{ActionInvitationAccepted}) {
		t.Fatalf("Bo reading Ada's Activity = %v", got)
	}
	// Ada reads Bo's chronology only from the reverse acceptance, so Bo's own
	// earlier invitation to Ada stays outside Ada's window.
	if got := visibleActions(t, service, bo, ada); !sameActions(got, []string{ActionInvitationAccepted}) {
		t.Fatalf("Ada reading Bo's Activity = %v", got)
	}
	adaOnBo, err := service.Activity(ctx, bo, ada)
	if err != nil {
		t.Fatal(err)
	}
	boOnAda, err := service.Activity(ctx, ada, bo)
	if err != nil {
		t.Fatal(err)
	}
	if !adaOnBo.VisibleFrom.Equal(acceptedReverse.AcceptedAt) {
		t.Fatalf("Ada's window on Bo opens at %v", adaOnBo.VisibleFrom)
	}
	if !adaOnBo.VisibleFrom.After(boOnAda.VisibleFrom) {
		t.Fatalf("the two directions shared one window: %v and %v", adaOnBo.VisibleFrom, boOnAda.VisibleFrom)
	}
}

func TestActivityIsDenyClosedForEveryUnauthorizedReference(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")
	stranger := createUser(t, db, "stranger@example.com")
	partnerUp(t, service, protectee, partner, "partner@example.com", false)

	for _, refused := range []struct {
		name                  string
		protecteeID, viewerID int64
	}{
		{"an unrelated signed-in User", protectee, stranger},
		{"an unauthenticated visitor", protectee, 0},
		{"a negative viewer id", protectee, -1},
		{"an unknown Protectee id", 9999, partner},
		{"a Protectee id of zero", 0, partner},
		{"a Partner reading the reverse direction they do not hold", partner, protectee},
		{"a stranger reading their own unknown chronology", 9999, 9999},
	} {
		t.Run(refused.name, func(t *testing.T) {
			refuseActivity(t, service, refused.protecteeID, refused.viewerID)
		})
	}
}

// TestPartnerAlertsLinkToTheProtecteeChronology proves the Action alerts a
// current Partner receives send them to the chronology the change belongs to,
// not to their own dashboard, and that the link is one they may actually open.
func TestPartnerAlertsLinkToTheProtecteeChronology(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")
	partnerUp(t, service, protectee, partner, "partner@example.com", false)

	since := lastJobID(t, service)
	second := invite(t, service, protectee, "second@example.com", false)
	want := fmt.Sprintf("https://lite.nightloch.app/activity/%d", protectee)
	alert := queuedBodies(t, service, MessagePartnerInvitationSent, since)["partner@example.com"]
	if !strings.Contains(alert, "Review Activity:\n"+want) {
		t.Fatalf("the invitation-sent alert does not link to the Protectee's Activity:\n%s", alert)
	}

	since = lastJobID(t, service)
	if _, err := service.CancelInvitation(ctx, second.ID, protectee); err != nil {
		t.Fatalf("CancelInvitation: %v", err)
	}
	canceled := queuedBodies(t, service, MessageInvitationCanceledPartner, since)["partner@example.com"]
	if !strings.Contains(canceled, "Review Activity:\n"+want) {
		t.Fatalf("the cancellation alert does not link to the Protectee's Activity:\n%s", canceled)
	}

	// The recipient of both alerts is authorized to open that link.
	if _, err := service.Activity(ctx, protectee, partner); err != nil {
		t.Fatalf("the alerted Partner cannot read the Activity they were sent to: %v", err)
	}
}

// TestActivityWindowHoldsThroughCoolingOffAndClosesOnCompletion pins the other
// boundary instant: a Partnership scheduled to end is still live, so its
// Partner keeps reading right up to the deadline, and Nightloch's own
// completion is where their access stops.
func TestActivityWindowHoldsThroughCoolingOffAndClosesOnCompletion(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")
	partnerUp(t, service, protectee, partner, "partner@example.com", false)
	partnership := partnershipOf(t, service, protectee, partner)

	advance(time.Hour)
	request, err := service.StartCoolingOff(ctx, CoolingOffCommand{
		PartnershipID: partnership, ProtecteeID: protectee,
	})
	if err != nil {
		t.Fatalf("StartCoolingOff: %v", err)
	}

	// A Partner whose Partnership is scheduled to end keeps every power,
	// including the reading of the request itself.
	during := visibleActions(t, service, protectee, partner)
	if !sameActions(during, []string{ActionCoolingOffStarted, ActionInvitationAccepted}) {
		t.Fatalf("Activity during Cooling-off = %v", during)
	}

	// One second before the deadline nothing is due and nothing has changed.
	at(service, request.EndsAt.Add(-time.Second))
	if completed, err := service.CompleteDueCoolingOff(ctx, 10); err != nil || completed != 0 {
		t.Fatalf("CompleteDueCoolingOff before the deadline = %d, %v", completed, err)
	}
	if got := visibleActions(t, service, protectee, partner); len(got) != 2 {
		t.Fatalf("Activity one second before the deadline = %v", got)
	}

	// At the deadline Nightloch completes the ending, and the entry it appends
	// is stamped on the deadline itself — the sharp case for a window whose
	// closing is the ending instant.
	at(service, request.EndsAt)
	if completed, err := service.CompleteDueCoolingOff(ctx, 10); err != nil || completed != 1 {
		t.Fatalf("CompleteDueCoolingOff on the deadline = %d, %v", completed, err)
	}
	refuseActivity(t, service, protectee, partner)

	// The Protectee still reads the completion, attributed to Nightloch and
	// naming the deadline they themselves requested.
	view, err := service.Activity(ctx, protectee, protectee)
	if err != nil {
		t.Fatal(err)
	}
	newest := view.Entries[0]
	if newest.Action != ActionPartnershipEndedAfterCoolingOff || newest.ActorEmail != "" {
		t.Fatalf("the completion is misattributed: %+v", newest)
	}
	if !newest.OccurredAt.Equal(request.EndsAt) {
		t.Fatalf("the completion is stamped %v, want the deadline %v", newest.OccurredAt, request.EndsAt)
	}
	if !strings.Contains(newest.Summary(), "Nightloch ended protectee@example.com’s Partnership") {
		t.Fatalf("completion reads %q", newest.Summary())
	}
}

// TestEndNowReadsApartFromAnIndependentEnding proves Activity keeps the
// distinction the store already makes: a Partner's own ending and the **End
// now** that waives a Protectee's Cooling-off share one action but are not the
// same event (S3-08).
func TestEndNowReadsApartFromAnIndependentEnding(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	independent := createUser(t, db, "independent@example.com")
	agreeing := createUser(t, db, "agreeing@example.com")
	partnerUp(t, service, protectee, independent, "independent@example.com", false)
	partnerUp(t, service, protectee, agreeing, "agreeing@example.com", false)

	advance(time.Hour)
	if _, err := service.EndPartnership(ctx, EndCommand{
		PartnershipID: partnershipOf(t, service, protectee, independent),
		PartnerID:     independent, Confirmation: "END",
	}); err != nil {
		t.Fatalf("independent ending: %v", err)
	}

	advance(time.Hour)
	agreed := partnershipOf(t, service, protectee, agreeing)
	if _, err := service.StartCoolingOff(ctx, CoolingOffCommand{
		PartnershipID: agreed, ProtecteeID: protectee,
	}); err != nil {
		t.Fatalf("StartCoolingOff: %v", err)
	}
	advance(time.Hour)
	if _, err := service.EndPartnership(ctx, EndCommand{
		PartnershipID: agreed, PartnerID: agreeing, Confirmation: "END",
	}); err != nil {
		t.Fatalf("End now: %v", err)
	}

	view, err := service.Activity(ctx, protectee, protectee)
	if err != nil {
		t.Fatal(err)
	}
	prose := map[string]string{}
	for _, entry := range view.Entries {
		if entry.Action == ActionPartnershipEndedByPartner {
			prose[entry.TargetEmail+"|"+entry.ActorEmail] = entry.Summary()
		}
	}
	independentProse := prose["protectee@example.com|independent@example.com"]
	agreedProse := prose["protectee@example.com|agreeing@example.com"]
	if !strings.Contains(independentProse, "ended their Partnership with protectee@example.com immediately.") {
		t.Fatalf("independent ending reads %q", independentProse)
	}
	if !strings.Contains(agreedProse, "waiving the rest of the Cooling-off") {
		t.Fatalf("End now reads %q", agreedProse)
	}
}

// TestEveryStableActionHasItsOwnProse walks the actions this package can
// append. The Summary fallback exists so no entry ever vanishes from a
// chronology that claims to be complete; nothing Nightloch actually records
// should reach it.
func TestEveryStableActionHasItsOwnProse(t *testing.T) {
	t.Parallel()
	entry := ActivityEntry{
		ProtecteeEmail: "protectee@example.com",
		ActorEmail:     "actor@example.com",
		TargetEmail:    "target@example.com",
	}
	fallback := ActivityEntry{ProtecteeEmail: entry.ProtecteeEmail, ActorEmail: entry.ActorEmail}
	for _, action := range []string{
		ActionInvitationSent, ActionInvitationResent, ActionInvitationAccepted,
		ActionInvitationDeclined, ActionInvitationCanceled, ActionInvitationExpired,
		ActionPartnershipEndedByPartner, ActionCoolingOffStarted, ActionCoolingOffCanceled,
		ActionPartnershipEndedAfterCoolingOff, ActionCoolingOffBecameFinal,
		ActionDeliveryDelayed, ActionDeliveryFailed, ActionDeliveryRecovered,
	} {
		entry.Action = action
		if entry.Summary() == fallback.Summary() {
			t.Fatalf("%s has no prose of its own", action)
		}
	}
}
