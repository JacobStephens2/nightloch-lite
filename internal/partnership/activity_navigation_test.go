package partnership

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// record appends one entry directly, which is how these tests build a
// chronology long enough to page and precise enough to share timestamps. The
// read path is what is under test here; the write paths have their own tests.
func record(t *testing.T, service *Service, entry activityEntry) int64 {
	t.Helper()
	tx, err := service.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	id, err := appendActivity(context.Background(), tx, entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return id
}

// readAllPages walks every page a viewer can reach from the first, following
// NextPage exactly as a screen's "older" link does, and returns the entries in
// the order they were read along with the number of pages it took.
func readAllPages(t *testing.T, service *Service, protecteeID, viewerID int64, query ActivityQuery) ([]ActivityEntry, int) {
	t.Helper()
	var all []ActivityEntry
	pages := 0
	for {
		view, err := service.Activity(context.Background(), protecteeID, viewerID, query)
		if err != nil {
			t.Fatalf("Activity page %d: %v", pages+1, err)
		}
		pages++
		all = append(all, view.Entries...)
		if !view.More() {
			if len(view.Entries) > ActivityPageSize {
				t.Fatalf("a page held %d entries, want at most %d", len(view.Entries), ActivityPageSize)
			}
			return all, pages
		}
		if len(view.Entries) != ActivityPageSize {
			t.Fatalf("a page with more to follow held %d entries, want %d",
				len(view.Entries), ActivityPageSize)
		}
		query.After = view.NextPage
		if pages > 20 {
			t.Fatal("paging did not terminate")
		}
	}
}

// assertNewestFirstAndDistinct is the ordering contract every page boundary
// has to preserve: strictly descending by instant and, within one second, by
// insertion order, with no entry read twice and none skipped.
func assertNewestFirstAndDistinct(t *testing.T, entries []ActivityEntry) {
	t.Helper()
	seen := make(map[int64]bool, len(entries))
	for index, entry := range entries {
		if seen[entry.ID] {
			t.Fatalf("entry %d was read twice", entry.ID)
		}
		seen[entry.ID] = true
		if index == 0 {
			continue
		}
		previous := entries[index-1]
		if previous.OccurredAt.Before(entry.OccurredAt) ||
			(previous.OccurredAt.Equal(entry.OccurredAt) && previous.ID < entry.ID) {
			t.Fatalf("entry %d (%v) came before %d (%v), which is not newest first",
				previous.ID, previous.OccurredAt, entry.ID, entry.OccurredAt)
		}
	}
}

// fillActivity records count entries for one Protectee, packing every group of
// three into the same second so the chronology exercises ties rather than a
// convenient strictly increasing clock.
func fillActivity(t *testing.T, service *Service, protecteeID int64, count int) {
	t.Helper()
	for index := range count {
		record(t, service, activityEntry{
			ProtecteeID: protecteeID,
			Action:      ActionInvitationSent,
			ActorEmail:  "protectee@example.com",
			TargetEmail: "invitee@example.com",
			OccurredAt:  testClock.Add(time.Duration(index/3) * time.Second),
			Details:     map[string]any{"log_sharing": false},
		})
	}
}

func TestActivityPagesNewestFirstWithoutRepeatingOrSkipping(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	// Two full pages and a partial third, so both boundaries are crossed and
	// the last page is the one that says there is nothing after it.
	const entries = 2*ActivityPageSize + 7
	fillActivity(t, service, protectee, entries)

	read, pages := readAllPages(t, service, protectee, protectee, ActivityQuery{})
	if len(read) != entries {
		t.Fatalf("paging read %d entries, want %d", len(read), entries)
	}
	if pages != 3 {
		t.Fatalf("paging took %d pages, want 3", pages)
	}
	assertNewestFirstAndDistinct(t, read)
}

func TestPageBoundaryInsideOneSecondNeitherRepeatsNorSkips(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	// Every entry shares one instant, so the instant alone can settle nothing
	// and the boundary between pages falls inside the tie.
	const entries = ActivityPageSize + 5
	for range entries {
		record(t, service, activityEntry{
			ProtecteeID: protectee,
			Action:      ActionInvitationSent,
			ActorEmail:  "protectee@example.com",
			TargetEmail: "invitee@example.com",
			OccurredAt:  testClock,
		})
	}

	read, pages := readAllPages(t, service, protectee, protectee, ActivityQuery{})
	if len(read) != entries || pages != 2 {
		t.Fatalf("paging identical timestamps read %d entries over %d pages, want %d over 2",
			len(read), pages, entries)
	}
	assertNewestFirstAndDistinct(t, read)
}

// TestFiltersNarrowInsideTheWindowAndCombine walks the three filters alone and
// together, and then proves the one thing a filter must never do: reach past
// the viewer's own window (S3-48).
func TestFiltersNarrowInsideTheWindowAndCombine(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")
	before := testClock

	// One event before the Partner's window, then their acceptance, then a
	// spread of categories and actors inside it.
	first := invite(t, service, protectee, "partner@example.com", false)
	advance(24 * time.Hour)
	acceptFor(t, service, first, partner)
	acceptance := service.now()
	advance(24 * time.Hour)
	second := invite(t, service, protectee, "later@example.com", false)
	advance(24 * time.Hour)
	if _, err := service.CancelInvitation(ctx, second.ID, protectee); err != nil {
		t.Fatalf("CancelInvitation: %v", err)
	}
	advance(24 * time.Hour)
	if _, err := service.StartCoolingOff(ctx, CoolingOffCommand{
		PartnershipID: partnershipOf(t, service, protectee, partner), ProtecteeID: protectee,
	}); err != nil {
		t.Fatalf("StartCoolingOff: %v", err)
	}
	// Nightloch's own work, which is the actor filter's other side.
	nightloch := service.now()
	record(t, service, activityEntry{
		ProtecteeID: protectee,
		Action:      ActionDeliveryFailed,
		TargetEmail: "partner@example.com",
		OccurredAt:  nightloch,
		Details:     map[string]any{"message_kind": MessagePartnerInvitation, "reachability": "unreachable"},
	})

	categories := func(viewer int64, query ActivityQuery) []string {
		t.Helper()
		entries, _ := readAllPages(t, service, protectee, viewer, query)
		var actions []string
		for _, entry := range entries {
			actions = append(actions, entry.Action)
		}
		return actions
	}

	if got := categories(protectee, ActivityQuery{Category: CategoryInvitations}); !sameActions(got,
		[]string{ActionInvitationCanceled, ActionInvitationSent, ActionInvitationSent}) {
		t.Fatalf("the invitations category = %v", got)
	}
	if got := categories(protectee, ActivityQuery{Category: CategoryDelivery}); !sameActions(got,
		[]string{ActionDeliveryFailed}) {
		t.Fatalf("the delivery category = %v", got)
	}
	if got := categories(protectee, ActivityQuery{Actor: ActorNightloch}); !sameActions(got,
		[]string{ActionDeliveryFailed}) {
		t.Fatalf("the Nightloch actor filter = %v", got)
	}
	if got := categories(protectee, ActivityQuery{Actor: "partner@example.com"}); !sameActions(got,
		[]string{ActionInvitationAccepted}) {
		t.Fatalf("the Partner actor filter = %v", got)
	}
	// A date range that ends before the acceptance keeps the first invitation
	// and nothing after it.
	if got := categories(protectee, ActivityQuery{To: before.Add(time.Hour)}); !sameActions(got,
		[]string{ActionInvitationSent}) {
		t.Fatalf("the date range = %v", got)
	}
	// All three at once: the Protectee's own invitations, after the
	// acceptance, in the invitations category.
	combined := ActivityQuery{
		Category: CategoryInvitations,
		Actor:    "protectee@example.com",
		From:     acceptance,
		To:       nightloch,
	}
	if got := categories(protectee, combined); !sameActions(got,
		[]string{ActionInvitationCanceled, ActionInvitationSent}) {
		t.Fatalf("the combined filter = %v", got)
	}

	// The Partner may filter, and a range reaching back before their
	// acceptance still cannot show them the invitation that produced it.
	partnerRange := ActivityQuery{Category: CategoryInvitations, From: before.Add(-time.Hour)}
	if got := categories(partner, partnerRange); !sameActions(got,
		[]string{ActionInvitationCanceled, ActionInvitationSent}) {
		t.Fatalf("the Partner's filtered window = %v", got)
	}
	for _, entry := range mustPage(t, service, protectee, partner, partnerRange).Entries {
		if entry.TargetEmail == "partner@example.com" {
			t.Fatalf("a filter reached the invitation from before the Partner's window: %+v", entry)
		}
	}
}

// mustPage is one page for a viewer who is expected to be authorized.
func mustPage(t *testing.T, service *Service, protecteeID, viewerID int64, query ActivityQuery) ActivityView {
	t.Helper()
	view, err := service.Activity(context.Background(), protecteeID, viewerID, query)
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	return view
}

// TestUnavailableFiltersAreRefusedRatherThanAnsweredEmpty pins the deny-closed
// half of filtering: a filter Nightloch cannot honour is an error, never an
// empty page that would read as "nothing happened".
func TestUnavailableFiltersAreRefusedRatherThanAnsweredEmpty(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	fillActivity(t, service, protectee, 3)

	for name, query := range map[string]ActivityQuery{
		"an unknown category": {Category: ActivityCategory("everything-ever")},
		"an inverted range":   {From: testClock, To: testClock.Add(-time.Hour)},
	} {
		view, err := service.Activity(ctx, protectee, protectee, query)
		if !errors.Is(err, ErrActivityFilterInvalid) {
			t.Fatalf("%s returned %+v, %v; want ErrActivityFilterInvalid", name, view, err)
		}
		if len(view.Entries) != 0 {
			t.Fatalf("%s still returned entries", name)
		}
	}
	if _, err := ParseActivityCursor("not-a-cursor"); !errors.Is(err, ErrActivityFilterInvalid) {
		t.Fatalf("a malformed cursor parsed as %v", err)
	}
	if cursor, err := ParseActivityCursor(""); err != nil || !cursor.Zero() {
		t.Fatalf("an absent cursor = %+v, %v; want the first page", cursor, err)
	}
	round := ActivityCursor{OccurredAt: testClock, ID: 42}
	if back, err := ParseActivityCursor(round.String()); err != nil || back != round {
		t.Fatalf("a cursor did not survive a link: %+v, %v", back, err)
	}

	// An unauthorized viewer's bad filter is still answered as unavailable, so
	// a filter error cannot be used to learn that a Protectee exists.
	stranger := createUser(t, db, "stranger@example.com")
	_, err := service.Activity(ctx, protectee, stranger, ActivityQuery{Category: ActivityCategory("nope")})
	if !errors.Is(err, ErrActivityUnavailable) {
		t.Fatalf("an unauthorized viewer's invalid filter = %v", err)
	}
}

// TestActorChoicesComeFromTheWindowAlone proves the filter's own vocabulary
// obeys the window: a Partner is offered only actors their window already
// discloses, and the choices do not shift as they page.
func TestActorChoicesComeFromTheWindowAlone(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	protectee := createUser(t, db, "protectee@example.com")
	early := createUser(t, db, "early@example.com")
	late := createUser(t, db, "late@example.com")

	partnerUp(t, service, protectee, early, "early@example.com", false)
	advance(24 * time.Hour)
	partnerUp(t, service, protectee, late, "late@example.com", false)
	advance(time.Hour)
	record(t, service, activityEntry{
		ProtecteeID: protectee,
		Action:      ActionDeliveryFailed,
		TargetEmail: "late@example.com",
		OccurredAt:  service.now(),
		Details:     map[string]any{"message_kind": MessagePartnerInvitation, "reachability": "unreachable"},
	})

	own := mustPage(t, service, protectee, protectee, ActivityQuery{}).Actors
	if !sameActions(own, []string{"early@example.com", "late@example.com", "protectee@example.com", ActorNightloch}) {
		t.Fatalf("the Protectee's actor choices = %v", own)
	}
	// The early Partner accepted before the late one, so both acceptances are
	// inside their window; the late Partner's window opens at their own.
	lateChoices := mustPage(t, service, protectee, late, ActivityQuery{}).Actors
	if !sameActions(lateChoices, []string{"late@example.com", ActorNightloch}) {
		t.Fatalf("the late Partner's actor choices = %v", lateChoices)
	}
}

// TestDetailsAreTypedAndCarryNoStoredPayload walks every entry a full
// relationship produces and proves two things at once: each detail row is a
// labelled human-readable fact, and nothing anywhere in the view carries the
// shape of the stored JSON it came from (S3-49).
func TestDetailsAreTypedAndCarryNoStoredPayload(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")
	partnerUp(t, service, protectee, partner, "partner@example.com", true)
	advance(time.Hour)
	if _, err := service.StartCoolingOff(ctx, CoolingOffCommand{
		PartnershipID: partnershipOf(t, service, protectee, partner), ProtecteeID: protectee,
	}); err != nil {
		t.Fatalf("StartCoolingOff: %v", err)
	}
	advance(time.Hour)
	record(t, service, activityEntry{
		ProtecteeID: protectee,
		Action:      ActionDeliveryFailed,
		TargetEmail: "partner@example.com",
		OccurredAt:  service.now(),
		Details: map[string]any{
			"message_kind": MessageCoolingOffStarted,
			"reachability": string(ReachabilityUnreachable),
			"failed":       true,
		},
	})

	view := mustPage(t, service, protectee, protectee, ActivityQuery{})
	rowsByAction := map[string]map[string]string{}
	for _, entry := range view.Entries {
		rows := map[string]string{}
		for _, detail := range entry.Details() {
			if detail.Label == "" {
				t.Fatalf("%s has an unlabelled detail row", entry.Action)
			}
			if detail.Instant() == (detail.Text != "") {
				t.Fatalf("%s detail %q is neither exactly text nor exactly an instant",
					entry.Action, detail.Label)
			}
			value := detail.Text
			if detail.Instant() {
				value = detail.At.Format(time.RFC3339)
			}
			// A stored key or a fragment of JSON reaching a viewer is the
			// failure this whole seam exists to prevent.
			for _, raw := range []string{"{", "}", "\"", "_", "map["} {
				if strings.Contains(detail.Label+value, raw) {
					t.Fatalf("%s detail %q = %q looks like stored data", entry.Action, detail.Label, value)
				}
			}
			rows[detail.Label] = value
		}
		rowsByAction[entry.Action] = rows
	}

	if got := rowsByAction[ActionInvitationAccepted]["Log sharing"]; got != "On" {
		t.Fatalf("the acceptance does not state Log sharing: %v", rowsByAction[ActionInvitationAccepted])
	}
	if got := rowsByAction[ActionCoolingOffStarted]["Partner lock at the deadline"]; got != "Becomes inactive at the deadline" {
		t.Fatalf("the Cooling-off request does not state its consequence: %v", rowsByAction[ActionCoolingOffStarted])
	}
	if got := rowsByAction[ActionDeliveryFailed]["Reachability"]; got != "Nightloch cannot reach this Partner" {
		t.Fatalf("the delivery entry does not state reachability: %v", rowsByAction[ActionDeliveryFailed])
	}
	// The stored message kind is an internal identifier and is translated,
	// never rendered as stored.
	if got := rowsByAction[ActionDeliveryFailed]["Message"]; got != "Cooling-off started alert" {
		t.Fatalf("the message kind reads %q", got)
	}
	// The delivery entry is its own category, so a screen can show
	// reachability apart from the lifecycle prose around it.
	for _, entry := range view.Entries {
		if entry.Delivery() != (entry.Action == ActionDeliveryFailed) {
			t.Fatalf("%s reports Delivery() = %v", entry.Action, entry.Delivery())
		}
	}
}

// TestUnknownMessageKindRendersNothingRatherThanItself is the deny-closed edge
// of the same rule: a kind the label table does not know is omitted, not shown.
func TestUnknownMessageKindRendersNothingRatherThanItself(t *testing.T) {
	t.Parallel()
	entry := ActivityEntry{
		Action:  ActionDeliveryDelayed,
		details: activityDetails{MessageKind: "some_future_message", Reachability: "delayed"},
	}
	for _, detail := range entry.Details() {
		if strings.Contains(detail.Text, "some_future_message") {
			t.Fatalf("an unknown message kind reached a viewer: %+v", detail)
		}
	}
}

// TestTimerCompletionNamesNightlochAndLinksToTheRequest is S3-18 through the
// read path: Nightloch executed the ending, the Protectee asked for it, and
// the entry points back at where they did.
func TestTimerCompletionNamesNightlochAndLinksToTheRequest(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")
	partnerUp(t, service, protectee, partner, "partner@example.com", false)
	advance(time.Hour)
	request, err := service.StartCoolingOff(ctx, CoolingOffCommand{
		PartnershipID: partnershipOf(t, service, protectee, partner), ProtecteeID: protectee,
	})
	if err != nil {
		t.Fatalf("StartCoolingOff: %v", err)
	}
	at(service, request.EndsAt)
	if completed, err := service.CompleteDueCoolingOff(ctx, 10); err != nil || completed != 1 {
		t.Fatalf("CompleteDueCoolingOff = %d, %v", completed, err)
	}

	view := mustPage(t, service, protectee, protectee, ActivityQuery{})
	var completion, started ActivityEntry
	for _, entry := range view.Entries {
		switch entry.Action {
		case ActionPartnershipEndedAfterCoolingOff:
			completion = entry
		case ActionCoolingOffStarted:
			started = entry
		}
	}
	if completion.ActorEmail != "" || !strings.HasPrefix(completion.Summary(), "Nightloch ended") {
		t.Fatalf("the completion is attributed to %q: %q", completion.ActorEmail, completion.Summary())
	}
	if completion.RequestEntryID() != started.ID {
		t.Fatalf("the completion links to entry %d, want the request %d",
			completion.RequestEntryID(), started.ID)
	}
	rows := map[string]string{}
	for _, detail := range completion.Details() {
		rows[detail.Label] = detail.Text
	}
	if rows["Requested by"] != "protectee@example.com" {
		t.Fatalf("the completion does not name who requested it: %v", rows)
	}
	// The request itself states when its consequence lands, which is what the
	// completion later carried out.
	if !started.Effective() || !started.EffectiveAt.Equal(request.EndsAt) {
		t.Fatalf("the request's effective time = %v, want %v", started.EffectiveAt, request.EndsAt)
	}
}

// TestAPartnerReadsNoDetailFromBeforeTheirWindow is the audience half of
// S3-49: an entry inside a Partner's window must not become a way to read an
// event outside it, so the instants it carries obey the window too.
func TestAPartnerReadsNoDetailFromBeforeTheirWindow(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	leaving := createUser(t, db, "leaving@example.com")
	arriving := createUser(t, db, "arriving@example.com")
	partnerUp(t, service, protectee, leaving, "leaving@example.com", false)

	advance(time.Hour)
	request, err := service.StartCoolingOff(ctx, CoolingOffCommand{
		PartnershipID: partnershipOf(t, service, protectee, leaving), ProtecteeID: protectee,
	})
	if err != nil {
		t.Fatalf("StartCoolingOff: %v", err)
	}
	// A second Partner joins after the request and before the deadline, so
	// the completion is inside their window and the request is not.
	advance(time.Hour)
	partnerUp(t, service, protectee, arriving, "arriving@example.com", false)
	at(service, request.EndsAt)
	if completed, err := service.CompleteDueCoolingOff(ctx, 10); err != nil || completed != 1 {
		t.Fatalf("CompleteDueCoolingOff = %d, %v", completed, err)
	}

	completionOf := func(viewer int64) ActivityEntry {
		t.Helper()
		for _, entry := range mustPage(t, service, protectee, viewer, ActivityQuery{}).Entries {
			if entry.Action == ActionPartnershipEndedAfterCoolingOff {
				return entry
			}
		}
		t.Fatalf("viewer %d cannot read the completion", viewer)
		return ActivityEntry{}
	}
	labels := func(entry ActivityEntry) []string {
		var found []string
		for _, detail := range entry.Details() {
			found = append(found, detail.Label)
		}
		return found
	}

	windowed := completionOf(arriving)
	for _, label := range labels(windowed) {
		if label == "Requested on" {
			t.Fatalf("the arriving Partner read an instant from before their window: %v", labels(windowed))
		}
	}
	if windowed.RequestEntryID() != 0 {
		t.Fatalf("the arriving Partner was linked to entry %d from before their window",
			windowed.RequestEntryID())
	}
	// They still read the entry itself, and the requester it names in prose,
	// because the ending is theirs to know about.
	if !strings.Contains(windowed.Summary(), "Nightloch ended protectee@example.com’s Partnership") {
		t.Fatalf("the arriving Partner lost the completion prose: %q", windowed.Summary())
	}

	own := completionOf(protectee)
	var requested bool
	for _, label := range labels(own) {
		requested = requested || label == "Requested on"
	}
	if !requested || own.RequestEntryID() == 0 {
		t.Fatalf("the Protectee lost their own request detail: %v", labels(own))
	}
}

// TestCorrectionsAppendRatherThanMutate is S3-17 through the read path: a
// recovery clears the Roster warning without erasing the failure that is still
// part of the chronology.
func TestCorrectionsAppendRatherThanMutate(t *testing.T) {
	t.Parallel()
	service, protectee, _ := dispatchedPartnership(t)
	ctx := context.Background()
	messageID := deliveredMessageID(t, service, "partner@example.com")

	if err := service.RecordDeliveryEvent(ctx, DeliveryEvent{
		ProviderEventID: "evt_transient", ProviderMessageID: messageID,
		Type: EventBounced, OccurredAt: testClock.Add(time.Minute),
	}); err != nil {
		t.Fatalf("RecordDeliveryEvent: %v", err)
	}
	failure := mustPage(t, service, protectee, protectee, ActivityQuery{Category: CategoryDelivery}).Entries[0]

	if err := service.RecordDeliveryEvent(ctx, DeliveryEvent{
		ProviderEventID: "evt_delivered", ProviderMessageID: messageID,
		Type: EventDelivered, OccurredAt: testClock.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("RecordDeliveryEvent: %v", err)
	}

	after := mustPage(t, service, protectee, protectee, ActivityQuery{Category: CategoryDelivery}).Entries
	if len(after) != 2 {
		t.Fatalf("a correction produced %d delivery entries, want 2", len(after))
	}
	if after[0].Action != ActionDeliveryRecovered {
		t.Fatalf("the newest delivery entry is %s", after[0].Action)
	}
	corrected := after[1]
	if corrected.ID != failure.ID || corrected.Action != failure.Action ||
		!corrected.OccurredAt.Equal(failure.OccurredAt) || corrected.Summary() != failure.Summary() {
		t.Fatalf("the earlier failure was mutated: %+v became %+v", failure, corrected)
	}
	if got := partnerReachability(t, service, protectee); got != ReachabilityOK {
		t.Fatalf("the recovery did not clear the warning: %q", got.Label())
	}
}

// TestEveryEntryAttributesItsActorTargetAndTime walks one Protectee's whole
// relationship and holds every entry to the same shape: an active-voice
// sentence naming a verified email or Nightloch, the target it was about, and
// an exact instant, with the effective instant carried wherever a consequence
// lands later than the action (S3-30).
func TestEveryEntryAttributesItsActorTargetAndTime(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")
	declining := createUser(t, db, "declining@example.com")

	partnerUp(t, service, protectee, partner, "partner@example.com", false)
	advance(time.Hour)
	declined := invite(t, service, protectee, "declining@example.com", false)
	if _, err := service.DeclineInvitation(ctx, ByToken(declined.RawToken), declining); err != nil {
		t.Fatalf("DeclineInvitation: %v", err)
	}
	advance(time.Hour)
	request, err := service.StartCoolingOff(ctx, CoolingOffCommand{
		PartnershipID: partnershipOf(t, service, protectee, partner), ProtecteeID: protectee,
	})
	if err != nil {
		t.Fatalf("StartCoolingOff: %v", err)
	}
	at(service, request.EndsAt)
	if _, err := service.CompleteDueCoolingOff(ctx, 10); err != nil {
		t.Fatalf("CompleteDueCoolingOff: %v", err)
	}

	view := mustPage(t, service, protectee, protectee, ActivityQuery{})
	if len(view.Entries) < 5 {
		t.Fatalf("the chronology is too short to hold the whole relationship: %d", len(view.Entries))
	}
	for _, entry := range view.Entries {
		summary := entry.Summary()
		attribution := entry.ActorEmail
		if attribution == "" {
			attribution = "Nightloch"
		}
		if !strings.Contains(summary, attribution) {
			t.Fatalf("%s does not name who acted: %q", entry.Action, summary)
		}
		if entry.TargetEmail != "" && !strings.Contains(summary, entry.TargetEmail) &&
			!strings.Contains(summary, entry.ProtecteeEmail) {
			t.Fatalf("%s does not name what it was about: %q", entry.Action, summary)
		}
		if entry.OccurredAt.IsZero() {
			t.Fatalf("%s has no occurrence instant", entry.Action)
		}
		if strings.HasSuffix(summary, ",") || !strings.HasSuffix(summary, ".") {
			t.Fatalf("%s is not a finished sentence: %q", entry.Action, summary)
		}
		if entry.Action == ActionCoolingOffStarted && !entry.EffectiveAt.Equal(request.EndsAt) {
			t.Fatalf("the Cooling-off request does not carry its effective instant: %v", entry.EffectiveAt)
		}
	}
}

// TestEveryActionHasACategoryAndEveryCategoryHasActions holds the claim the
// filter vocabulary makes: no action falls outside the categories a viewer is
// offered, so a category filter can never silently drop an entry, and no
// category is offered that matches nothing.
func TestEveryActionHasACategoryAndEveryCategoryHasActions(t *testing.T) {
	t.Parallel()
	offered := map[ActivityCategory]bool{}
	for _, category := range ActivityCategories() {
		if len(actionsIn(category)) == 0 {
			t.Fatalf("%s is offered as a filter and covers no action", category)
		}
		offered[category] = true
	}
	for action, category := range categoryOfAction {
		if !offered[category] {
			t.Fatalf("%s is in category %q, which no viewer can filter by", action, category)
		}
	}
	// Every action this package appends is in the table, so a new one cannot
	// join Activity without being placed.
	for _, action := range []string{
		ActionInvitationSent, ActionInvitationResent, ActionInvitationAccepted,
		ActionInvitationDeclined, ActionInvitationCanceled, ActionInvitationExpired,
		ActionPartnershipEndedByPartner, ActionCoolingOffStarted, ActionCoolingOffCanceled,
		ActionPartnershipEndedAfterCoolingOff, ActionCoolingOffBecameFinal,
		ActionDeliveryDelayed, ActionDeliveryFailed, ActionDeliveryRecovered,
	} {
		if categoryOfAction[action] == AnyCategory {
			t.Fatalf("%s has no category", action)
		}
	}
}

// TestAnExactlyFullPageSaysThereIsNothingAfterIt is the boundary the
// look-ahead row exists for: a chronology that is an exact multiple of the
// page size must end, not offer an empty page after itself.
func TestAnExactlyFullPageSaysThereIsNothingAfterIt(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	fillActivity(t, service, protectee, ActivityPageSize)

	view := mustPage(t, service, protectee, protectee, ActivityQuery{})
	if len(view.Entries) != ActivityPageSize || view.More() {
		t.Fatalf("an exactly full page held %d entries and offered another: %v",
			len(view.Entries), view.NextPage)
	}

	// One more entry, and the same page now leads somewhere that holds it.
	fillActivity(t, service, protectee, 1)
	full := mustPage(t, service, protectee, protectee, ActivityQuery{})
	if !full.More() {
		t.Fatal("a chronology one longer than a page offered no second page")
	}
	second := mustPage(t, service, protectee, protectee, ActivityQuery{After: full.NextPage})
	if len(second.Entries) != 1 || second.More() {
		t.Fatalf("the second page held %d entries and offered another: %v",
			len(second.Entries), second.NextPage)
	}
}

// TestTheRequestPagePutsTheRequestAtTheTopOfItsPage proves the link a
// completed timer offers lands on the request rather than on whichever page
// the reader happened to be on: 72 hours of chronology separate them.
func TestTheRequestPagePutsTheRequestAtTheTopOfItsPage(t *testing.T) {
	t.Parallel()
	service, db, advance := newTimedService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")
	partnerUp(t, service, protectee, partner, "partner@example.com", false)
	advance(time.Hour)
	request, err := service.StartCoolingOff(ctx, CoolingOffCommand{
		PartnershipID: partnershipOf(t, service, protectee, partner), ProtecteeID: protectee,
	})
	if err != nil {
		t.Fatalf("StartCoolingOff: %v", err)
	}
	// More than a page of chronology between the request and its deadline, so
	// the two entries cannot share a page.
	for index := range ActivityPageSize + 5 {
		record(t, service, activityEntry{
			ProtecteeID: protectee,
			Action:      ActionInvitationSent,
			ActorEmail:  "protectee@example.com",
			TargetEmail: "invitee@example.com",
			OccurredAt:  request.RequestedAt.Add(time.Duration(index+1) * time.Minute),
		})
	}
	at(service, request.EndsAt)
	if _, err := service.CompleteDueCoolingOff(ctx, 10); err != nil {
		t.Fatalf("CompleteDueCoolingOff: %v", err)
	}

	first := mustPage(t, service, protectee, protectee, ActivityQuery{})
	completion := first.Entries[0]
	if completion.Action != ActionPartnershipEndedAfterCoolingOff {
		t.Fatalf("the newest entry is %s", completion.Action)
	}
	for _, entry := range first.Entries {
		if entry.ID == completion.RequestEntryID() {
			t.Fatal("the request shares a page with the completion, so this proves nothing")
		}
	}

	landing := mustPage(t, service, protectee, protectee, ActivityQuery{After: completion.RequestPage()})
	if len(landing.Entries) == 0 || landing.Entries[0].ID != completion.RequestEntryID() {
		t.Fatalf("the request page opens on %+v, want entry %d",
			landing.Entries, completion.RequestEntryID())
	}
	if landing.Entries[0].Action != ActionCoolingOffStarted {
		t.Fatalf("the request page opens on %s", landing.Entries[0].Action)
	}
}
