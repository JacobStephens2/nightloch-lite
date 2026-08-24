package partnership

import (
	"context"
	"database/sql"
	"testing"
	"time"

	mailmessage "github.com/JacobStephens2/nightloch-lite/internal/mail"
)

// deliveredMessageID is the provider message id of the one job addressed to
// recipient, which is what a delivery callback names.
func deliveredMessageID(t *testing.T, service *Service, recipient string) string {
	t.Helper()
	var id string
	if err := service.db.QueryRow(`SELECT COALESCE(provider_message_id, '') FROM mail_delivery_jobs
		WHERE recipient_email = ? ORDER BY id DESC LIMIT 1`, recipient).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatalf("no provider message id stored for %s", recipient)
	}
	return id
}

// activityActions is every Activity action recorded for protectee, oldest
// first. Activity is append-only, so a recovery must not erase the failure.
func activityActions(t *testing.T, service *Service, protectee int64) []string {
	t.Helper()
	rows, err := service.db.Query(
		"SELECT action FROM activity_entries WHERE protectee_id = ? ORDER BY id", protectee)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return actions
}

func contains(actions []string, want string) bool {
	for _, action := range actions {
		if action == want {
			return true
		}
	}
	return false
}

// partnerReachability is what protectee's own Roster says about their one
// Partner right now.
func partnerReachability(t *testing.T, service *Service, protectee int64) Reachability {
	t.Helper()
	view, err := service.ProtecteeView(context.Background(), protectee)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Partners) != 1 {
		t.Fatalf("Roster = %+v", view.Partners)
	}
	return view.Partners[0].Reachability
}

// dispatchedPartnership is one live Partnership whose queued alerts have all
// been handed to the provider, which is the state every delivery callback
// arrives into.
func dispatchedPartnership(t *testing.T) (*Service, int64, int64) {
	t.Helper()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")
	partnerUp(t, service, protectee, partner, "partner@example.com", false)
	if _, err := service.DispatchDue(context.Background(), &mailmessage.RecordingSender{}, 50); err != nil {
		t.Fatal(err)
	}
	return service, protectee, partner
}

func TestHardBounceRendersThePartnerUnreachableWithoutTouchingTheirPowers(t *testing.T) {
	t.Parallel()
	service, protectee, partner := dispatchedPartnership(t)
	ctx := context.Background()

	if got := partnerReachability(t, service, protectee); got != ReachabilityOK {
		t.Fatalf("an accepted send already reads as %q", got.Label())
	}
	if err := service.RecordDeliveryEvent(ctx, DeliveryEvent{
		ProviderEventID:   "evt_bounce",
		ProviderMessageID: deliveredMessageID(t, service, "partner@example.com"),
		Type:              EventBounced,
		Permanent:         true,
		OccurredAt:        testClock.Add(time.Minute),
	}); err != nil {
		t.Fatalf("RecordDeliveryEvent: %v", err)
	}

	if got := partnerReachability(t, service, protectee); got != ReachabilityUnreachable ||
		got.Label() != "Nightloch cannot reach this Partner" {
		t.Fatalf("hard bounce reads as %q", got.Label())
	}
	if !contains(activityActions(t, service, protectee), ActionDeliveryFailed) {
		t.Fatalf("the failure is missing from Activity: %v", activityActions(t, service, protectee))
	}
	// An unreachable Partner keeps every power and every deadline (S3-58).
	allowed, err := service.CanLoosen(ctx, partner, protectee)
	if err != nil || !allowed {
		t.Fatalf("an unreachable Partner lost CanLoosen: %v, %v", allowed, err)
	}
}

func TestComplaintRendersThePartnerUnreachable(t *testing.T) {
	t.Parallel()
	service, protectee, _ := dispatchedPartnership(t)

	if err := service.RecordDeliveryEvent(context.Background(), DeliveryEvent{
		ProviderEventID:   "evt_complaint",
		ProviderMessageID: deliveredMessageID(t, service, "partner@example.com"),
		Type:              EventComplained,
		OccurredAt:        testClock.Add(time.Minute),
	}); err != nil {
		t.Fatalf("RecordDeliveryEvent: %v", err)
	}
	if got := partnerReachability(t, service, protectee); got != ReachabilityUnreachable {
		t.Fatalf("complaint reads as %q", got.Label())
	}
}

func TestTransientBounceRendersDeliveryDelayed(t *testing.T) {
	t.Parallel()
	service, protectee, _ := dispatchedPartnership(t)

	if err := service.RecordDeliveryEvent(context.Background(), DeliveryEvent{
		ProviderEventID:   "evt_transient",
		ProviderMessageID: deliveredMessageID(t, service, "partner@example.com"),
		Type:              EventBounced,
		OccurredAt:        testClock.Add(time.Minute),
	}); err != nil {
		t.Fatalf("RecordDeliveryEvent: %v", err)
	}
	if got := partnerReachability(t, service, protectee); got != ReachabilityDelayed ||
		got.Label() != "Delivery delayed" {
		t.Fatalf("transient bounce reads as %q", got.Label())
	}
}

// A provider retries a callback it did not see acknowledged, so processing one
// twice must change nothing the first one did not already change.
func TestDuplicateDeliveryCallbackChangesNothingTwice(t *testing.T) {
	t.Parallel()
	service, protectee, _ := dispatchedPartnership(t)
	ctx := context.Background()
	event := DeliveryEvent{
		ProviderEventID:   "evt_bounce",
		ProviderMessageID: deliveredMessageID(t, service, "partner@example.com"),
		Type:              EventBounced,
		Permanent:         true,
		OccurredAt:        testClock.Add(time.Minute),
	}
	if err := service.RecordDeliveryEvent(ctx, event); err != nil {
		t.Fatalf("RecordDeliveryEvent: %v", err)
	}
	before := activityActions(t, service, protectee)

	if err := service.RecordDeliveryEvent(ctx, event); err != nil {
		t.Fatalf("redelivered callback: %v", err)
	}
	after := activityActions(t, service, protectee)
	if len(after) != len(before) {
		t.Fatalf("a redelivered callback appended Activity again: %v then %v", before, after)
	}
	if got := partnerReachability(t, service, protectee); got != ReachabilityUnreachable {
		t.Fatalf("reachability after a duplicate = %q", got.Label())
	}
}

// Providers do not promise ordering. A delivery stamped before a bounce must
// not clear the warning the bounce raised.
func TestStaleDeliveryEventDoesNotClearALaterBounce(t *testing.T) {
	t.Parallel()
	service, protectee, _ := dispatchedPartnership(t)
	ctx := context.Background()
	messageID := deliveredMessageID(t, service, "partner@example.com")

	if err := service.RecordDeliveryEvent(ctx, DeliveryEvent{
		ProviderEventID: "evt_bounce", ProviderMessageID: messageID,
		Type: EventBounced, Permanent: true, OccurredAt: testClock.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordDeliveryEvent(ctx, DeliveryEvent{
		ProviderEventID: "evt_delivered", ProviderMessageID: messageID,
		Type: EventDelivered, OccurredAt: testClock.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if got := partnerReachability(t, service, protectee); got != ReachabilityUnreachable {
		t.Fatalf("a stale delivery cleared the bounce: %q", got.Label())
	}
}

// A Partner who becomes reachable again stops carrying a warning, but the
// chronology keeps both the failure and the recovery.
func TestLaterDeliveryClearsTheWarningAndKeepsBothInActivity(t *testing.T) {
	t.Parallel()
	service, protectee, _ := dispatchedPartnership(t)
	ctx := context.Background()
	messageID := deliveredMessageID(t, service, "partner@example.com")

	if err := service.RecordDeliveryEvent(ctx, DeliveryEvent{
		ProviderEventID: "evt_delayed", ProviderMessageID: messageID,
		Type: EventDelayed, OccurredAt: testClock.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if got := partnerReachability(t, service, protectee); got != ReachabilityDelayed {
		t.Fatalf("delay reads as %q", got.Label())
	}
	if err := service.RecordDeliveryEvent(ctx, DeliveryEvent{
		ProviderEventID: "evt_delivered", ProviderMessageID: messageID,
		Type: EventDelivered, OccurredAt: testClock.Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if got := partnerReachability(t, service, protectee); got != ReachabilityOK {
		t.Fatalf("recovery left the warning up: %q", got.Label())
	}
	actions := activityActions(t, service, protectee)
	if !contains(actions, ActionDeliveryDelayed) || !contains(actions, ActionDeliveryRecovered) {
		t.Fatalf("Activity lost the failure or the recovery: %v", actions)
	}
}

// A sign-in link is sent through the same provider and has no Partnership
// state at all. Its callbacks must be accepted and change nothing.
func TestCallbackForAnUnknownMessageChangesNoPartnershipState(t *testing.T) {
	t.Parallel()
	service, protectee, _ := dispatchedPartnership(t)
	before := activityActions(t, service, protectee)

	if err := service.RecordDeliveryEvent(context.Background(), DeliveryEvent{
		ProviderEventID: "evt_unknown", ProviderMessageID: "a-message-nightloch-never-queued",
		Type: EventBounced, Permanent: true, OccurredAt: testClock.Add(time.Minute),
	}); err != nil {
		t.Fatalf("RecordDeliveryEvent refused an unrelated message: %v", err)
	}
	if got := partnerReachability(t, service, protectee); got != ReachabilityOK {
		t.Fatalf("an unrelated bounce changed reachability to %q", got.Label())
	}
	if len(activityActions(t, service, protectee)) != len(before) {
		t.Fatal("an unrelated bounce appended Activity")
	}
}

// A bounced invitation is the one delivery failure with a remedy: the
// invitation is immutable, so the Protectee must cancel and send a new one.
func TestBouncedInvitationReadsAsNotDelivered(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	if _, err := service.InvitePartner(ctx, InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "invitee@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DispatchDue(ctx, &mailmessage.RecordingSender{}, 10); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordDeliveryEvent(ctx, DeliveryEvent{
		ProviderEventID:   "evt_bounce",
		ProviderMessageID: deliveredMessageID(t, service, "invitee@example.com"),
		Type:              EventBounced, Permanent: true,
		OccurredAt: testClock.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	view, err := service.ProtecteeView(ctx, protectee)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.PendingInvitations) != 1 {
		t.Fatalf("pending invitations = %+v", view.PendingInvitations)
	}
	state := view.PendingInvitations[0].DeliveryState
	if state != DeliveryFailed || state.Label() != "Invitation not delivered" {
		t.Fatalf("bounced invitation reads as %q", state.Label())
	}
	if !state.Failed() {
		t.Fatal("a bounced invitation does not report itself as failed")
	}
	// The invitation itself is untouched: it is still pending and answerable
	// if the invitee reaches it another way.
	var invitationState string
	if err := db.QueryRow("SELECT state FROM partner_invitations").Scan(&invitationState); err != nil {
		t.Fatal(err)
	}
	if invitationState != "pending" {
		t.Fatalf("a delivery failure changed the invitation to %q", invitationState)
	}
}

// Queueing another message is not evidence that anything reached the Partner.
// Only an outcome clears the warning, so a Roster row cannot go quiet just
// because Nightloch has more to say to an address it already cannot reach.
func TestNewlyQueuedMessageDoesNotClearAnUnreachablePartner(t *testing.T) {
	t.Parallel()
	service, protectee, partner := dispatchedPartnership(t)
	ctx := context.Background()

	if err := service.RecordDeliveryEvent(ctx, DeliveryEvent{
		ProviderEventID:   "evt_bounce",
		ProviderMessageID: deliveredMessageID(t, service, "partner@example.com"),
		Type:              EventBounced, Permanent: true,
		OccurredAt: testClock.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if got := partnerReachability(t, service, protectee); got != ReachabilityUnreachable {
		t.Fatalf("hard bounce reads as %q", got.Label())
	}

	// Any later mutation queues that Partner another alert.
	if _, err := service.InvitePartner(ctx, InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "another@example.com"}); err != nil {
		t.Fatal(err)
	}
	if got := partnerReachability(t, service, protectee); got != ReachabilityUnreachable {
		t.Fatalf("a freshly queued alert cleared the warning: %q", got.Label())
	}
	_ = partner
}

// A delivery failure is a message that did not arrive, not a change of mind.
// It must leave the Partnership, its Cooling-off deadline, and the powers on
// both sides exactly as they were (S3-58).
func TestUnreachablePartnerKeepsTheirCoolingOffDeadlineAndPowers(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	partner := createUser(t, db, "partner@example.com")
	partnerUp(t, service, protectee, partner, "partner@example.com", false)
	view, err := service.ProtecteeView(ctx, protectee)
	if err != nil {
		t.Fatal(err)
	}
	partnership := view.Partners[0].PartnershipID
	if _, err := service.StartCoolingOff(ctx, CoolingOffCommand{
		PartnershipID: partnership, ProtecteeID: protectee}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DispatchDue(ctx, &mailmessage.RecordingSender{}, 50); err != nil {
		t.Fatal(err)
	}
	var deadlineBefore int64
	if err = db.QueryRow("SELECT cooling_off_ends_at FROM partnerships WHERE id = ?", partnership).
		Scan(&deadlineBefore); err != nil {
		t.Fatal(err)
	}

	if err := service.RecordDeliveryEvent(ctx, DeliveryEvent{
		ProviderEventID:   "evt_bounce",
		ProviderMessageID: deliveredMessageID(t, service, "partner@example.com"),
		Type:              EventBounced, Permanent: true,
		OccurredAt: testClock.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	var deadlineAfter int64
	var endedAt sql.NullInt64
	if err := db.QueryRow("SELECT cooling_off_ends_at, ended_at FROM partnerships WHERE id = ?", partnership).
		Scan(&deadlineAfter, &endedAt); err != nil {
		t.Fatal(err)
	}
	if deadlineAfter != deadlineBefore {
		t.Fatalf("a hard bounce moved the Cooling-off deadline from %d to %d", deadlineBefore, deadlineAfter)
	}
	if endedAt.Valid {
		t.Fatal("a hard bounce ended the Partnership")
	}
	allowed, err := service.CanLoosen(ctx, partner, protectee)
	if err != nil || !allowed {
		t.Fatalf("an unreachable Partner lost CanLoosen during Cooling-off: %v, %v", allowed, err)
	}
}

// A provider payload can name several recipients; Nightloch's own jobs never
// do. An event is attributed by message id alone, so a recipient list in the
// payload cannot reach a second Partner's Roster row.
func TestDeliveryEventAffectsExactlyOneRecipientsJob(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	first := createUser(t, db, "first@example.com")
	partnerUp(t, service, protectee, first, "first@example.com", false)
	second := createUser(t, db, "second@example.com")
	partnerUp(t, service, protectee, second, "second@example.com", false)
	if _, err := service.DispatchDue(ctx, &mailmessage.RecordingSender{}, 50); err != nil {
		t.Fatal(err)
	}

	if err := service.RecordDeliveryEvent(ctx, DeliveryEvent{
		ProviderEventID:   "evt_bounce",
		ProviderMessageID: deliveredMessageID(t, service, "first@example.com"),
		Type:              EventBounced, Permanent: true,
		OccurredAt: testClock.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	view, err := service.ProtecteeView(ctx, protectee)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Partners) != 2 {
		t.Fatalf("Roster = %+v", view.Partners)
	}
	for _, entry := range view.Partners {
		want := ReachabilityOK
		if entry.PartnerEmail == "first@example.com" {
			want = ReachabilityUnreachable
		}
		if entry.Reachability != want {
			t.Fatalf("%s reads as %q, want %q", entry.PartnerEmail, entry.Reachability, want)
		}
	}
	// Every job is still one recipient; nothing about this event combined two.
	var combined int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM mail_delivery_jobs WHERE recipient_email LIKE '%,%'").Scan(&combined); err != nil {
		t.Fatal(err)
	}
	if combined != 0 {
		t.Fatalf("%d delivery jobs hold a combined recipient list", combined)
	}
}

// Providers stamp events to the second, so a message accepted and delivered
// inside one second arrives as a tie. The delivery is the more conclusive
// outcome and must not be dropped behind its own send.
func TestSameSecondDeliveryStillLandsBehindItsOwnSend(t *testing.T) {
	t.Parallel()
	service, protectee, _ := dispatchedPartnership(t)
	ctx := context.Background()
	messageID := deliveredMessageID(t, service, "partner@example.com")
	at := testClock.Add(time.Minute)

	for id, kind := range map[string]DeliveryEventType{"evt_sent": EventSent, "evt_delivered": EventDelivered} {
		if err := service.RecordDeliveryEvent(ctx, DeliveryEvent{
			ProviderEventID: id, ProviderMessageID: messageID, Type: kind, OccurredAt: at,
		}); err != nil {
			t.Fatal(err)
		}
	}
	var state DeliveryState
	if err := service.db.QueryRow(
		"SELECT state FROM mail_delivery_jobs WHERE provider_message_id = ?", messageID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != DeliveryDelivered {
		t.Fatalf("same-second delivery left the job at %q", state)
	}
	if got := partnerReachability(t, service, protectee); got != ReachabilityOK {
		t.Fatalf("a delivered message reads as %q", got.Label())
	}
}

// Delivery events are durable, so a process that restarts between the callback
// and its retry neither loses the first one nor applies the retry twice.
func TestDeliveryEventSurvivesRestartAndStaysIdempotent(t *testing.T) {
	t.Parallel()
	service, protectee, _ := dispatchedPartnership(t)
	ctx := context.Background()
	event := DeliveryEvent{
		ProviderEventID:   "evt_bounce",
		ProviderMessageID: deliveredMessageID(t, service, "partner@example.com"),
		Type:              EventBounced, Permanent: true,
		OccurredAt: testClock.Add(time.Minute),
	}
	if err := service.RecordDeliveryEvent(ctx, event); err != nil {
		t.Fatal(err)
	}

	restarted := NewService(service.db, service.baseURL)
	restarted.now = func() time.Time { return testClock.Add(time.Hour) }
	if got := partnerReachability(t, restarted, protectee); got != ReachabilityUnreachable {
		t.Fatalf("a restart lost the bounce: %q", got.Label())
	}
	if err := restarted.RecordDeliveryEvent(ctx, event); err != nil {
		t.Fatalf("redelivered callback after restart: %v", err)
	}
	var entries int
	if err := service.db.QueryRow(
		"SELECT COUNT(*) FROM activity_entries WHERE action = ?", ActionDeliveryFailed).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 1 {
		t.Fatalf("Activity holds %d failure entries after a restarted retry", entries)
	}
}
