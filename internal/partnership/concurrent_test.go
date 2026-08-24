package partnership

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// roster is one Protectee with two accepted Partnerships, which is the state
// every concurrent-Cooling-off scenario starts from. Both Partners are named
// after the order they joined, because Roster order follows acceptance.
type rosterFixture struct {
	protectee     int64
	firstID       int64
	secondID      int64
	firstPartner  int64
	secondPartner int64
}

func twoPartnerships(t *testing.T, service *Service, db *sql.DB) rosterFixture {
	t.Helper()
	fixture := rosterFixture{protectee: createUser(t, db, "protectee@example.com")}
	fixture.firstPartner = createUser(t, db, "first@example.com")
	fixture.secondPartner = createUser(t, db, "second@example.com")
	fixture.firstID = acceptFor(t, service,
		invite(t, service, fixture.protectee, "first@example.com", false), fixture.firstPartner).PartnershipID
	fixture.secondID = acceptFor(t, service,
		invite(t, service, fixture.protectee, "second@example.com", false), fixture.secondPartner).PartnershipID
	return fixture
}

// startEnding requests the Protectee's own Cooling-off, failing on refusal.
func startEnding(t *testing.T, service *Service, partnershipID, protecteeID int64) CoolingOff {
	t.Helper()
	request, err := service.StartCoolingOff(context.Background(), CoolingOffCommand{
		PartnershipID: partnershipID, ProtecteeID: protecteeID,
	})
	if err != nil {
		t.Fatalf("StartCoolingOff: %v", err)
	}
	return request
}

// rosterRow is the Roster row for one Partnership as the Protectee reads it.
func rosterRow(t *testing.T, service *Service, protecteeID, partnershipID int64) RosterEntry {
	t.Helper()
	view, err := service.ProtecteeView(context.Background(), protecteeID)
	if err != nil {
		t.Fatalf("ProtecteeView: %v", err)
	}
	for _, entry := range view.Partners {
		if entry.PartnershipID == partnershipID {
			return entry
		}
	}
	t.Fatalf("Partnership %d is not on the Roster: %+v", partnershipID, view.Partners)
	return RosterEntry{}
}

func TestConcurrentCoolingOffKeepsEachDeadlineAndEveryPartnerPower(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	fixture := twoPartnerships(t, service, db)

	firstRequest := startEnding(t, service, fixture.firstID, fixture.protectee)
	// The second request arrives a day later and gets its own unchanged
	// 72 hours; neither deadline moves because of the other.
	nextDay := testClock.Add(24 * time.Hour)
	at(service, nextDay)
	secondRequest := startEnding(t, service, fixture.secondID, fixture.protectee)

	if !firstRequest.EndsAt.Equal(testClock.Add(coolingOffDuration)) {
		t.Fatalf("first deadline = %v", firstRequest.EndsAt)
	}
	if !secondRequest.EndsAt.Equal(nextDay.Add(coolingOffDuration)) {
		t.Fatalf("second deadline = %v", secondRequest.EndsAt)
	}

	// Both Partnerships stay active and keep every power until their own end.
	view, err := service.ProtecteeView(ctx, fixture.protectee)
	if err != nil || !view.PartnerLockActive || len(view.Partners) != 2 {
		t.Fatalf("Protectee view = %+v, %v", view, err)
	}
	for _, partner := range []int64{fixture.firstPartner, fixture.secondPartner} {
		if allowed, err := service.CanLoosen(ctx, partner, fixture.protectee); err != nil || !allowed {
			t.Fatalf("Partner %d lost their powers during Cooling-off: %v, %v", partner, allowed, err)
		}
	}
	if allowed, err := service.CanLoosen(ctx, fixture.protectee, fixture.protectee); err != nil || allowed {
		t.Fatalf("Protectee loosened while two Partnerships were still active: %v, %v", allowed, err)
	}

	// The earlier deadline leaves the later Partnership behind it, so it does
	// not deactivate the Partner lock; the later one does.
	earlier := rosterRow(t, service, fixture.protectee, fixture.firstID)
	if !earlier.CoolingOffEndsAt.Equal(firstRequest.EndsAt) ||
		earlier.PartnersAfterDeadline != 1 || earlier.EndsPartnerLock() {
		t.Fatalf("earlier Roster row = %+v", earlier)
	}
	later := rosterRow(t, service, fixture.protectee, fixture.secondID)
	if !later.CoolingOffEndsAt.Equal(secondRequest.EndsAt) ||
		later.PartnersAfterDeadline != 0 || !later.EndsPartnerLock() {
		t.Fatalf("later Roster row = %+v", later)
	}
}

func TestConcurrentCoolingOffAlertsOnlyTheRecipientsEachRequestEntitles(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	fixture := twoPartnerships(t, service, db)

	since := lastJobID(t, service)
	startEnding(t, service, fixture.firstID, fixture.protectee)
	started := queuedBodies(t, service, MessageCoolingOffStarted, since)
	// The affected Partner and the other current Partner each read their own
	// body; the Protectee acted and reads an in-app receipt instead (S3-20).
	if len(started) != 2 || started["protectee@example.com"] != "" {
		t.Fatalf("first Cooling-off recipients = %v", keysOf(started))
	}
	if !strings.Contains(started["first@example.com"], "requested to end your Partnership") {
		t.Fatalf("affected Partner read the wrong body:\n%s", started["first@example.com"])
	}
	for _, want := range []string{
		"requested to end their Partnership with\nfirst@example.com",
		"you cannot cancel it or\nend another Partner’s Partnership",
	} {
		if !strings.Contains(started["second@example.com"], want) {
			t.Fatalf("observer alert missing %q:\n%s", want, started["second@example.com"])
		}
	}

	// The second request is a separate signal about a separate Partnership,
	// so it names the second Partner and reverses the two roles.
	since = lastJobID(t, service)
	startEnding(t, service, fixture.secondID, fixture.protectee)
	started = queuedBodies(t, service, MessageCoolingOffStarted, since)
	if len(started) != 2 || started["protectee@example.com"] != "" {
		t.Fatalf("second Cooling-off recipients = %v", keysOf(started))
	}
	if !strings.Contains(started["second@example.com"], "requested to end your Partnership") {
		t.Fatalf("second affected Partner read the wrong body:\n%s", started["second@example.com"])
	}
	if !strings.Contains(started["first@example.com"],
		"requested to end their Partnership with\nsecond@example.com") {
		t.Fatalf("second observer alert = %s", started["first@example.com"])
	}
}

func TestConcurrentCoolingOffExitsInOneHorizonRatherThanInSeries(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	fixture := twoPartnerships(t, service, db)

	startEnding(t, service, fixture.firstID, fixture.protectee)
	startEnding(t, service, fixture.secondID, fixture.protectee)
	deadline := testClock.Add(coolingOffDuration)

	// A moment before the shared deadline both Partnerships are still active.
	at(service, deadline.Add(-time.Second))
	if ended, err := service.CompleteDueCoolingOff(ctx, 10); err != nil || ended != 0 {
		t.Fatalf("CompleteDueCoolingOff before the deadline = %d, %v", ended, err)
	}
	if allowed, err := service.CanLoosen(ctx, fixture.protectee, fixture.protectee); err != nil || allowed {
		t.Fatalf("Protectee loosened before the deadline: %v, %v", allowed, err)
	}

	// One 72-hour horizon settles the whole Roster, not one period per Partner.
	at(service, deadline)
	ended, err := service.CompleteDueCoolingOff(ctx, 10)
	if err != nil || ended != 2 {
		t.Fatalf("CompleteDueCoolingOff = %d, %v", ended, err)
	}
	view, err := service.ProtecteeView(ctx, fixture.protectee)
	if err != nil || view.PartnerLockActive || len(view.Partners) != 0 {
		t.Fatalf("Protectee view after the horizon = %+v, %v", view, err)
	}
	if allowed, err := service.CanLoosen(ctx, fixture.protectee, fixture.protectee); err != nil || !allowed {
		t.Fatalf("Partner lock survived the last deadline: %v, %v", allowed, err)
	}
	// Both endings are Nightloch's own completions, stored at their own
	// deadlines, and a repeated sweep settles nothing twice.
	var completions int
	if err := db.QueryRow("SELECT COUNT(*) FROM partnerships WHERE ended_reason = ? AND ended_at = ?",
		EndReasonCoolingOffCompleted, deadline.Unix()).Scan(&completions); err != nil {
		t.Fatal(err)
	}
	if completions != 2 {
		t.Fatalf("%d Partnerships ended at the shared deadline, want 2", completions)
	}
	if again, err := service.CompleteDueCoolingOff(ctx, 10); err != nil || again != 0 {
		t.Fatalf("a repeated sweep settled %d Partnerships, %v", again, err)
	}
}

func TestEndingAnotherPartnershipMakesARunningCoolingOffFinalWithoutMovingIt(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	fixture := twoPartnerships(t, service, db)

	request := startEnding(t, service, fixture.secondID, fixture.protectee)
	// The row is not final yet: the first Partnership outlives that deadline.
	if row := rosterRow(t, service, fixture.protectee, fixture.secondID); row.EndsPartnerLock() {
		t.Fatalf("Roster row was final before the other Partnership ended: %+v", row)
	}

	endedAt := testClock.Add(time.Hour)
	at(service, endedAt)
	since := lastJobID(t, service)
	if _, err := service.EndPartnership(ctx, EndCommand{
		PartnershipID: fixture.firstID, PartnerID: fixture.firstPartner, Confirmation: "END",
	}); err != nil {
		t.Fatalf("EndPartnership: %v", err)
	}

	// The countdown is untouched: same deadline, no restart, no extension.
	after := rosterRow(t, service, fixture.protectee, fixture.secondID)
	if !after.CoolingOffEndsAt.Equal(request.EndsAt) || !after.EndsPartnerLock() ||
		after.PartnersAfterDeadline != 0 {
		t.Fatalf("final Roster row = %+v, want the unchanged deadline %v", after, request.EndsAt)
	}

	// Nightloch appends its own Activity entry for the transition and points
	// it at the deadline that will deactivate the lock.
	var actor sql.NullInt64
	var occurredAt, effectiveAt int64
	var partnershipID int64
	if err := db.QueryRow(`SELECT actor_user_id, occurred_at, effective_at, partnership_id
		FROM activity_entries WHERE action = ?`, ActionCoolingOffBecameFinal).
		Scan(&actor, &occurredAt, &effectiveAt, &partnershipID); err != nil {
		t.Fatalf("final-Partnership Activity: %v", err)
	}
	if actor.Valid || occurredAt != endedAt.Unix() ||
		effectiveAt != request.EndsAt.Unix() || partnershipID != fixture.secondID {
		t.Fatalf("final-Partnership Activity actor=%v occurred=%d effective=%d partnership=%d",
			actor, occurredAt, effectiveAt, partnershipID)
	}

	// The Protectee and every current Partner read the approved alert, each in
	// their own envelope; the Partner who just left is not a current Partner.
	alerts := queuedBodies(t, service, MessageCoolingOffBecameFinal, since)
	if len(alerts) != 2 || alerts["first@example.com"] != "" {
		t.Fatalf("final-Partnership alert recipients = %v", keysOf(alerts))
	}
	for _, recipient := range []string{"protectee@example.com", "second@example.com"} {
		for _, want := range []string{
			"[Nightloch] A scheduled Partnership end will deactivate protectee@example.com’s Partner lock",
			"first@example.com’s Partnership with protectee@example.com ended on\n2026-08-23 16:04 UTC.",
			"second@example.com is now the final active Partner",
			"remains scheduled to end on 2026-08-26 15:04 UTC. Its deadline has not changed.",
			"the Partner\nlock will become inactive",
			"second@example.com retains all Partner powers and the\nPartner lock remains active",
			"alerts@nightloch.app",
		} {
			if !strings.Contains(alerts[recipient], want) {
				t.Fatalf("final-Partnership alert to %s missing %q:\n%s", recipient, want, alerts[recipient])
			}
		}
	}
}

func TestAcceptanceBeforeAFinalDeadlineSaysThePartnerLockSurvivesIt(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	fixture := twoPartnerships(t, service, db)
	third := createUser(t, db, "third@example.com")

	request := startEnding(t, service, fixture.secondID, fixture.protectee)
	if _, err := service.EndPartnership(context.Background(), EndCommand{
		PartnershipID: fixture.firstID, PartnerID: fixture.firstPartner, Confirmation: "END",
	}); err != nil {
		t.Fatalf("EndPartnership: %v", err)
	}

	pending := invite(t, service, fixture.protectee, "third@example.com", false)
	since := lastJobID(t, service)
	acceptFor(t, service, pending, third)

	surviving := "The Partner lock will remain active after second@example.com’s\n" +
		"Partnership is scheduled to end on " + utcStamp(request.EndsAt) + "."
	alerts := queuedBodies(t, service, MessagePartnershipAcceptedExisting, since)
	if len(alerts) != 2 {
		t.Fatalf("acceptance alert recipients = %v", keysOf(alerts))
	}
	for recipient, body := range alerts {
		if !strings.Contains(body, surviving) {
			t.Fatalf("acceptance alert to %s did not say the lock survives:\n%s", recipient, body)
		}
	}

	// The scheduled ending is no longer final, and its deadline still stands.
	row := rosterRow(t, service, fixture.protectee, fixture.secondID)
	if !row.CoolingOffEndsAt.Equal(request.EndsAt) || row.EndsPartnerLock() ||
		row.PartnersAfterDeadline != 1 {
		t.Fatalf("Roster row after the later acceptance = %+v", row)
	}
}

func TestAnOrdinaryAcceptanceSaysNothingAboutASurvivingPartnerLock(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	fixture := twoPartnerships(t, service, db)
	third := createUser(t, db, "third@example.com")

	// Two other Partnerships remain, so no single scheduled ending is the one
	// the Partner lock hangs on and the extra sentence would be noise.
	startEnding(t, service, fixture.secondID, fixture.protectee)
	pending := invite(t, service, fixture.protectee, "third@example.com", false)
	since := lastJobID(t, service)
	acceptFor(t, service, pending, third)

	for recipient, body := range queuedBodies(t, service, MessagePartnershipAcceptedExisting, since) {
		if strings.Contains(body, "will remain active after") {
			t.Fatalf("acceptance alert to %s named a scheduled end it need not:\n%s", recipient, body)
		}
	}
}

func TestCancelingOneCoolingOffLeavesTheOtherCountdownRunning(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	fixture := twoPartnerships(t, service, db)

	startEnding(t, service, fixture.firstID, fixture.protectee)
	second := startEnding(t, service, fixture.secondID, fixture.protectee)

	since := lastJobID(t, service)
	if _, err := service.CancelCoolingOff(ctx, CoolingOffCommand{
		PartnershipID: fixture.firstID, ProtecteeID: fixture.protectee,
	}); err != nil {
		t.Fatalf("CancelCoolingOff: %v", err)
	}

	canceled := queuedBodies(t, service, MessageCoolingOffCanceled, since)
	if len(canceled) != 2 || canceled["protectee@example.com"] != "" {
		t.Fatalf("cancellation recipients = %v", keysOf(canceled))
	}
	if !strings.Contains(canceled["first@example.com"], "with\nfirst@example.com") {
		t.Fatalf("cancellation named the wrong Partnership:\n%s", canceled["first@example.com"])
	}

	kept := rosterRow(t, service, fixture.protectee, fixture.firstID)
	if kept.CoolingOffRunning() {
		t.Fatalf("the canceled Partnership is still scheduled to end: %+v", kept)
	}
	running := rosterRow(t, service, fixture.protectee, fixture.secondID)
	if !running.CoolingOffEndsAt.Equal(second.EndsAt) || running.EndsPartnerLock() {
		t.Fatalf("the other countdown changed: %+v", running)
	}

	// Only the still-scheduled Partnership completes at the shared deadline.
	at(service, second.EndsAt)
	ended, err := service.CompleteDueCoolingOff(ctx, 10)
	if err != nil || ended != 1 {
		t.Fatalf("CompleteDueCoolingOff = %d, %v", ended, err)
	}
	view, err := service.ProtecteeView(ctx, fixture.protectee)
	if err != nil || !view.PartnerLockActive || len(view.Partners) != 1 {
		t.Fatalf("Protectee view after one completion = %+v, %v", view, err)
	}
}

func TestSimultaneousDeadlinesSettleOnceWithoutAPrematureFinalAlert(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	fixture := twoPartnerships(t, service, db)

	startEnding(t, service, fixture.firstID, fixture.protectee)
	startEnding(t, service, fixture.secondID, fixture.protectee)
	deadline := testClock.Add(coolingOffDuration)

	at(service, deadline)
	since := lastJobID(t, service)
	if ended, err := service.CompleteDueCoolingOff(ctx, 10); err != nil || ended != 2 {
		t.Fatalf("CompleteDueCoolingOff = %d, %v", ended, err)
	}

	// Neither ending announces the other as a future final Partnership: the
	// deadline they would point at has already arrived.
	if alerts := queuedBodies(t, service, MessageCoolingOffBecameFinal, since); len(alerts) != 0 {
		t.Fatalf("simultaneous deadlines announced a final Partnership: %v", keysOf(alerts))
	}
	// Each Partnership ends exactly once, so every recipient reads each ending
	// exactly once. queuedBodies fails the test on a duplicate recipient.
	for _, partnershipID := range []int64{fixture.firstID, fixture.secondID} {
		var endings int
		if err := db.QueryRow(`SELECT COUNT(*) FROM activity_entries
			WHERE partnership_id = ? AND action = ?`,
			partnershipID, ActionPartnershipEndedAfterCoolingOff).Scan(&endings); err != nil {
			t.Fatal(err)
		}
		if endings != 1 {
			t.Fatalf("Partnership %d appended %d completion entries", partnershipID, endings)
		}
	}
	var jobs, distinct int
	if err := db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT idempotency_key)
		FROM mail_delivery_jobs WHERE id > ?`, since).Scan(&jobs, &distinct); err != nil {
		t.Fatal(err)
	}
	if jobs != distinct {
		t.Fatalf("%d delivery jobs carry only %d distinct idempotency keys", jobs, distinct)
	}
}

func TestPartnerEndingNowDuringConcurrentCoolingOffTouchesOnlyItsOwnPartnership(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	fixture := twoPartnerships(t, service, db)

	startEnding(t, service, fixture.firstID, fixture.protectee)
	second := startEnding(t, service, fixture.secondID, fixture.protectee)

	at(service, testClock.Add(time.Hour))
	if _, err := service.EndPartnership(ctx, EndCommand{
		PartnershipID: fixture.firstID, PartnerID: fixture.firstPartner, Confirmation: "END",
	}); err != nil {
		t.Fatalf("EndPartnership: %v", err)
	}

	var reason string
	if err := db.QueryRow("SELECT ended_reason FROM partnerships WHERE id = ?",
		fixture.firstID).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != EndReasonPartnerAgreedNow {
		t.Fatalf("End now stored reason %q", reason)
	}
	// The surviving countdown is untouched and is now the final one.
	row := rosterRow(t, service, fixture.protectee, fixture.secondID)
	if !row.CoolingOffEndsAt.Equal(second.EndsAt) || !row.EndsPartnerLock() {
		t.Fatalf("surviving countdown = %+v, want the unchanged %v", row, second.EndsAt)
	}
	if allowed, err := service.CanLoosen(ctx, fixture.protectee, fixture.protectee); err != nil || allowed {
		t.Fatalf("Protectee loosened while one Partnership was still active: %v, %v", allowed, err)
	}
}

// keysOf lists a recipient map's addresses for a failure message.
func keysOf(bodies map[string]string) []string {
	addresses := make([]string, 0, len(bodies))
	for address := range bodies {
		addresses = append(addresses, address)
	}
	return addresses
}
