package partnership

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	mailmessage "github.com/JacobStephens2/nightloch-lite/internal/mail"
)

// at moves the deterministic test clock, so a Cooling-off test states the
// instant it is acting at instead of sleeping for three days.
func at(service *Service, instant time.Time) { service.now = func() time.Time { return instant } }

func TestStartCoolingOffRetainsThePartnershipAndEveryPartnerPower(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, partnership := firstPartnership(t, service, db, true)

	// The preview names the same deadline the request will commit, and the
	// final-Partnership consequence the Protectee must read first (S3-43).
	preview, err := service.CoolingOffPreview(context.Background(), partnership, protectee)
	if err != nil {
		t.Fatalf("CoolingOffPreview: %v", err)
	}
	if want := testClock.Add(coolingOffDuration); !preview.EndsAt.Equal(want) {
		t.Fatalf("preview deadline = %v, want %v", preview.EndsAt, want)
	}
	if preview.PartnerEmail != "partner@example.com" || !preview.FinalPartnership() {
		t.Fatalf("preview = %+v", preview)
	}

	request, err := service.StartCoolingOff(context.Background(), CoolingOffCommand{
		PartnershipID: partnership, ProtecteeID: protectee,
	})
	if err != nil {
		t.Fatalf("StartCoolingOff: %v", err)
	}
	if !request.RequestedAt.Equal(testClock) || !request.EndsAt.Equal(testClock.Add(coolingOffDuration)) {
		t.Fatalf("request = %+v", request)
	}

	// The Partnership stays live, so every power and the Partner lock are
	// exactly as they were before the request.
	var live, requestedAt, endsAt int64
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(cooling_off_requested_at), 0),
		COALESCE(MAX(cooling_off_ends_at), 0) FROM partnerships WHERE ended_at IS NULL`).
		Scan(&live, &requestedAt, &endsAt); err != nil {
		t.Fatal(err)
	}
	if live != 1 || requestedAt != testClock.Unix() || endsAt != testClock.Add(coolingOffDuration).Unix() {
		t.Fatalf("live=%d requestedAt=%d endsAt=%d", live, requestedAt, endsAt)
	}
	if allowed, err := service.CanLoosen(context.Background(), partner, protectee); err != nil || !allowed {
		t.Fatalf("Partner lost their powers during Cooling-off: %v, %v", allowed, err)
	}
	if allowed, err := service.CanLoosen(context.Background(), protectee, protectee); err != nil || allowed {
		t.Fatalf("Protectee loosened during Cooling-off: %v, %v", allowed, err)
	}
	view, err := service.ProtecteeView(context.Background(), protectee)
	if err != nil || !view.PartnerLockActive || len(view.Partners) != 1 {
		t.Fatalf("Protectee view during Cooling-off = %+v, %v", view, err)
	}
	// Both the Protectee and every entitled Roster viewer read the exact
	// deadline (S3-07).
	if !view.Partners[0].CoolingOffEndsAt.Equal(request.EndsAt) {
		t.Fatalf("Roster deadline = %v", view.Partners[0].CoolingOffEndsAt)
	}
	protecting, err := service.ProtectingView(context.Background(), partner)
	if err != nil || len(protecting) != 1 || !protecting[0].CoolingOffEndsAt.Equal(request.EndsAt) {
		t.Fatalf("Partner view during Cooling-off = %+v, %v", protecting, err)
	}

	var activity int
	if err := db.QueryRow("SELECT COUNT(*) FROM activity_entries WHERE action = ?",
		ActionCoolingOffStarted).Scan(&activity); err != nil {
		t.Fatal(err)
	}
	if activity != 1 {
		t.Fatalf("Cooling-off start appended %d Activity entries", activity)
	}

	sender := &mailmessage.RecordingSender{}
	if _, err := service.DispatchDue(context.Background(), sender, 20); err != nil {
		t.Fatal(err)
	}
	// The affected Partner is the only recipient today; the Protectee
	// initiated it and gets an in-app receipt instead (S3-20).
	started := 0
	for _, envelope := range sender.Envelopes {
		if !strings.Contains(envelope.Subject, "requested to end your Partnership") {
			continue
		}
		started++
		if envelope.To != "partner@example.com" {
			t.Fatalf("Cooling-off start went to %s", envelope.To)
		}
		for _, want := range []string{
			"It is scheduled to end on 2026-08-26 15:04 UTC.",
			"the Partnership remains active and you retain all Partner powers",
			"may cancel the request", "End now",
		} {
			if !strings.Contains(envelope.Text, want) {
				t.Fatalf("Cooling-off start missing %q:\n%s", want, envelope.Text)
			}
		}
	}
	if started != 1 {
		t.Fatalf("%d Cooling-off start messages, want 1", started)
	}
}

func TestOnlyTheProtecteeStartsOrCancelsCoolingOffOnTheirOwnPartnership(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, partnership := firstPartnership(t, service, db, false)
	stranger := createUser(t, db, "stranger@example.com")

	for _, refusal := range []struct {
		name   string
		actor  int64
		target int64
		want   error
	}{
		{"the Partner", partner, partnership, ErrPartnershipUnavailable},
		{"another User", stranger, partnership, ErrPartnershipUnavailable},
		{"an unknown Partnership", protectee, partnership + 99, ErrPartnershipUnavailable},
	} {
		if _, err := service.CoolingOffPreview(context.Background(), refusal.target, refusal.actor); !errors.Is(err, refusal.want) {
			t.Fatalf("%s previewed = %v, want %v", refusal.name, err, refusal.want)
		}
		if _, err := service.StartCoolingOff(context.Background(), CoolingOffCommand{
			PartnershipID: refusal.target, ProtecteeID: refusal.actor,
		}); !errors.Is(err, refusal.want) {
			t.Fatalf("%s started Cooling-off = %v, want %v", refusal.name, err, refusal.want)
		}
		if _, err := service.CancelCoolingOff(context.Background(), CoolingOffCommand{
			PartnershipID: refusal.target, ProtecteeID: refusal.actor,
		}); !errors.Is(err, refusal.want) {
			t.Fatalf("%s canceled Cooling-off = %v, want %v", refusal.name, err, refusal.want)
		}
	}

	// Nothing may cancel before something is running, and nothing may start
	// twice.
	if _, err := service.CancelCoolingOff(context.Background(), CoolingOffCommand{
		PartnershipID: partnership, ProtecteeID: protectee,
	}); !errors.Is(err, ErrCoolingOffNotRunning) {
		t.Fatalf("cancel with nothing scheduled = %v", err)
	}
	if _, err := service.StartCoolingOff(context.Background(), CoolingOffCommand{
		PartnershipID: partnership, ProtecteeID: protectee,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartCoolingOff(context.Background(), CoolingOffCommand{
		PartnershipID: partnership, ProtecteeID: protectee,
	}); !errors.Is(err, ErrCoolingOffRunning) {
		t.Fatalf("second start = %v, want ErrCoolingOffRunning", err)
	}

	var jobs int
	if err := db.QueryRow("SELECT COUNT(*) FROM mail_delivery_jobs WHERE message_kind = ?",
		MessageCoolingOffStarted).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("%d Cooling-off start jobs queued, want 1", jobs)
	}
}

func TestCancelingCoolingOffKeepsThePartnershipAndTellsTheRetainedPartner(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, partnership := firstPartnership(t, service, db, false)

	if _, err := service.StartCoolingOff(context.Background(), CoolingOffCommand{
		PartnershipID: partnership, ProtecteeID: protectee,
	}); err != nil {
		t.Fatal(err)
	}
	canceledAt := testClock.Add(time.Hour)
	at(service, canceledAt)

	cancellation, err := service.CancelCoolingOff(context.Background(), CoolingOffCommand{
		PartnershipID: partnership, ProtecteeID: protectee,
	})
	if err != nil {
		t.Fatalf("CancelCoolingOff: %v", err)
	}
	if cancellation.PartnerEmail != "partner@example.com" || !cancellation.CanceledAt.Equal(canceledAt) {
		t.Fatalf("cancellation = %+v", cancellation)
	}

	var live, scheduled int
	if err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM partnerships WHERE ended_at IS NULL),
		(SELECT COUNT(*) FROM partnerships WHERE cooling_off_ends_at IS NOT NULL)`).
		Scan(&live, &scheduled); err != nil {
		t.Fatal(err)
	}
	if live != 1 || scheduled != 0 {
		t.Fatalf("after cancellation live=%d scheduled=%d", live, scheduled)
	}
	if allowed, err := service.CanLoosen(context.Background(), partner, protectee); err != nil || !allowed {
		t.Fatalf("cancellation removed Partner powers: %v, %v", allowed, err)
	}

	// The completed-timer runner must find nothing to do, even long past the
	// deadline the canceled request had named.
	at(service, testClock.Add(coolingOffDuration+time.Hour))
	if ended, err := service.CompleteDueCoolingOff(context.Background(), 10); err != nil || ended != 0 {
		t.Fatalf("canceled Cooling-off still completed: %d, %v", ended, err)
	}

	sender := &mailmessage.RecordingSender{}
	if _, err := service.DispatchDue(context.Background(), sender, 20); err != nil {
		t.Fatal(err)
	}
	canceled := 0
	for _, envelope := range sender.Envelopes {
		if !strings.Contains(envelope.Subject, "canceled a Partnership ending request") {
			continue
		}
		canceled++
		if envelope.To != "partner@example.com" {
			t.Fatalf("cancellation went to %s", envelope.To)
		}
		for _, want := range []string{
			"The Partnership remains active. No end is scheduled",
			"retains the same Partner powers",
		} {
			if !strings.Contains(envelope.Text, want) {
				t.Fatalf("cancellation missing %q:\n%s", want, envelope.Text)
			}
		}
	}
	if canceled != 1 {
		t.Fatalf("%d cancellation messages, want 1", canceled)
	}

	// Cancelling is not terminal: the Protectee may request again, and the
	// new deadline runs from the new request.
	restarted, err := service.StartCoolingOff(context.Background(), CoolingOffCommand{
		PartnershipID: partnership, ProtecteeID: protectee,
	})
	if err != nil {
		t.Fatalf("second StartCoolingOff: %v", err)
	}
	if want := testClock.Add(coolingOffDuration + time.Hour).Add(coolingOffDuration); !restarted.EndsAt.Equal(want) {
		t.Fatalf("restarted deadline = %v, want %v", restarted.EndsAt, want)
	}
}

func TestCoolingOffCompletesOnItsUnchangedDeadlineAsNightlochsOwnAct(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, partnership := firstPartnership(t, service, db, true)

	request, err := service.StartCoolingOff(context.Background(), CoolingOffCommand{
		PartnershipID: partnership, ProtecteeID: protectee,
	})
	if err != nil {
		t.Fatal(err)
	}

	// One second before the deadline nothing is due, and every power holds.
	at(service, request.EndsAt.Add(-time.Second))
	if ended, err := service.CompleteDueCoolingOff(context.Background(), 10); err != nil || ended != 0 {
		t.Fatalf("Cooling-off completed early: %d, %v", ended, err)
	}
	if allowed, err := service.CanLoosen(context.Background(), protectee, protectee); err != nil || allowed {
		t.Fatalf("Protectee loosened before the deadline: %v, %v", allowed, err)
	}

	// The runner may start late — a restarted process picks the work up from
	// SQLite — and the Partnership still ends at its own unchanged deadline.
	at(service, request.EndsAt.Add(9*time.Hour))
	restarted := NewService(db, service.baseURL)
	restarted.now = service.now
	ended, err := restarted.CompleteDueCoolingOff(context.Background(), 10)
	if err != nil || ended != 1 {
		t.Fatalf("CompleteDueCoolingOff = %d, %v", ended, err)
	}
	if again, err := restarted.CompleteDueCoolingOff(context.Background(), 10); err != nil || again != 0 {
		t.Fatalf("a second sweep ended %d more Partnerships: %v", again, err)
	}

	var endedAt int64
	var reason string
	var actor any
	if err := db.QueryRow(`SELECT partnership.ended_at, partnership.ended_reason,
		(SELECT actor_user_id FROM activity_entries WHERE action = ?)
		FROM partnerships partnership`, ActionPartnershipEndedAfterCoolingOff).
		Scan(&endedAt, &reason, &actor); err != nil {
		t.Fatal(err)
	}
	if endedAt != request.EndsAt.Unix() || reason != EndReasonCoolingOffCompleted {
		t.Fatalf("ended at %d reason %q, want %d", endedAt, reason, request.EndsAt.Unix())
	}
	// Timer completion is Nightloch's act, never the Protectee's (S3-18).
	if actor != nil {
		t.Fatalf("timer completion was attributed to User %v", actor)
	}
	// It still links back to the Protectee's original request.
	var startedID, linkedID int64
	if err := db.QueryRow(`SELECT
		(SELECT id FROM activity_entries WHERE action = ?),
		(SELECT CAST(json_extract(details, '$.requested_activity_id') AS INTEGER)
		   FROM activity_entries WHERE action = ?)`,
		ActionCoolingOffStarted, ActionPartnershipEndedAfterCoolingOff).Scan(&startedID, &linkedID); err != nil {
		t.Fatal(err)
	}
	if startedID == 0 || linkedID != startedID {
		t.Fatalf("completion linked to Activity %d, want the request %d", linkedID, startedID)
	}

	// The exact zero-Partner consequence lands only now.
	if allowed, err := service.CanLoosen(context.Background(), protectee, protectee); err != nil || !allowed {
		t.Fatalf("Protectee cannot loosen after the final Partnership ended: %v, %v", allowed, err)
	}
	if allowed, err := service.CanLoosen(context.Background(), partner, protectee); err != nil || allowed {
		t.Fatalf("former Partner kept their powers: %v, %v", allowed, err)
	}
	view, err := service.ProtecteeView(context.Background(), protectee)
	if err != nil || view.PartnerLockActive || len(view.Partners) != 0 {
		t.Fatalf("Protectee view after completion = %+v, %v", view, err)
	}

	sender := &mailmessage.RecordingSender{}
	if _, err := restarted.DispatchDue(context.Background(), sender, 20); err != nil {
		t.Fatal(err)
	}
	recipients := map[string]bool{}
	for _, envelope := range sender.Envelopes {
		if envelope.Subject != "[Nightloch] A Partnership for protectee@example.com ended after Cooling-off" {
			continue
		}
		recipients[envelope.To] = true
		for _, want := range []string{
			"Nightloch ended the Partnership after the Cooling-off requested by",
			"The Partnership ended on 2026-08-26 15:04 UTC.",
			"protectee@example.com now has 0 active Partner(s).",
			"The Partner lock is now inactive. protectee@example.com can loosen their own Policy.",
		} {
			if !strings.Contains(envelope.Text, want) {
				t.Fatalf("%s completion message missing %q:\n%s", envelope.To, want, envelope.Text)
			}
		}
	}
	if len(recipients) != 2 || !recipients["protectee@example.com"] || !recipients["partner@example.com"] {
		t.Fatalf("completion recipients = %v", recipients)
	}
	// The departing Partner keeps only their receipt, and it names the cause.
	receipt, err := service.EndedPartnershipReceipt(context.Background(), partnership, partner)
	if err != nil || !receipt.EndedAfterCoolingOff() || !receipt.EndedAt.Equal(request.EndsAt) {
		t.Fatalf("receipt = %+v, %v", receipt, err)
	}
}

func TestOnlyTheAffectedPartnerEndsNowAndItReusesTheImmediateEnding(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, partnership := firstPartnership(t, service, db, false)
	other := createUser(t, db, "other@example.com")
	addPartnership(t, db, protectee, other)

	if _, err := service.StartCoolingOff(context.Background(), CoolingOffCommand{
		PartnershipID: partnership, ProtecteeID: protectee,
	}); err != nil {
		t.Fatal(err)
	}

	// No other Partner may waive another Partner's Cooling-off (S3-08).
	if _, err := service.EndPartnership(context.Background(), EndCommand{
		PartnershipID: partnership, PartnerID: other, Confirmation: "END",
	}); !errors.Is(err, ErrPartnershipUnavailable) {
		t.Fatalf("another Partner ended now = %v", err)
	}
	// End now keeps the same typed friction as the immediate ending.
	if _, err := service.EndPartnership(context.Background(), EndCommand{
		PartnershipID: partnership, PartnerID: partner, Confirmation: "yes",
	}); !errors.Is(err, ErrEndConfirmationRequired) {
		t.Fatalf("End now without the typed word = %v", err)
	}

	endedAt := testClock.Add(2 * time.Hour)
	at(service, endedAt)
	preview, err := service.EndablePartnership(context.Background(), partnership, partner)
	if err != nil || !preview.CoolingOffRunning() {
		t.Fatalf("preview during Cooling-off = %+v, %v", preview, err)
	}
	ending, err := service.EndPartnership(context.Background(), EndCommand{
		PartnershipID: partnership, PartnerID: partner, Confirmation: "END",
	})
	if err != nil || !ending.EndedAt.Equal(endedAt) || ending.RemainingPartners != 1 {
		t.Fatalf("End now = %+v, %v", ending, err)
	}

	var reason string
	if err := db.QueryRow("SELECT ended_reason FROM partnerships WHERE id = ?", partnership).
		Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != EndReasonPartnerAgreedNow {
		t.Fatalf("End now stored reason %q", reason)
	}
	// It is audited as Partner-initiated termination, and the timer that
	// would have completed it finds nothing left.
	var activity int
	if err := db.QueryRow("SELECT COUNT(*) FROM activity_entries WHERE action = ?",
		ActionPartnershipEndedByPartner).Scan(&activity); err != nil {
		t.Fatal(err)
	}
	if activity != 1 {
		t.Fatalf("End now appended %d Partner-ending Activity entries", activity)
	}
	at(service, testClock.Add(coolingOffDuration+time.Hour))
	if completed, err := service.CompleteDueCoolingOff(context.Background(), 10); err != nil || completed != 0 {
		t.Fatalf("the timer ended an already-ended Partnership: %d, %v", completed, err)
	}

	sender := &mailmessage.RecordingSender{}
	if _, err := service.DispatchDue(context.Background(), sender, 20); err != nil {
		t.Fatal(err)
	}
	agreed := 0
	for _, envelope := range sender.Envelopes {
		if envelope.Subject != "[Nightloch] partner@example.com agreed to end their Partnership with protectee@example.com now" {
			continue
		}
		agreed++
		if !strings.Contains(envelope.Text,
			"partner@example.com agreed to end the Partnership immediately after") {
			t.Fatalf("End now message body:\n%s", envelope.Text)
		}
	}
	if agreed != 3 {
		t.Fatalf("%d End now messages, want both parties plus the remaining Partner", agreed)
	}
}

func TestConcurrentCoolingOffOutcomesCommitExactlyOnce(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, partnership := firstPartnership(t, service, db, false)
	if _, err := service.StartCoolingOff(context.Background(), CoolingOffCommand{
		PartnershipID: partnership, ProtecteeID: protectee,
	}); err != nil {
		t.Fatal(err)
	}
	at(service, testClock.Add(coolingOffDuration))

	// Cancel, End now, and the timer all race for the same Partnership.
	results := make(chan error, 4)
	start := make(chan struct{})
	attempt := func(run func() error) {
		go func() { <-start; results <- run() }()
	}
	attempt(func() error {
		_, err := service.CancelCoolingOff(context.Background(), CoolingOffCommand{
			PartnershipID: partnership, ProtecteeID: protectee,
		})
		return err
	})
	attempt(func() error {
		_, err := service.EndPartnership(context.Background(), EndCommand{
			PartnershipID: partnership, PartnerID: partner, Confirmation: "END",
		})
		return err
	})
	attempt(func() error {
		_, err := service.CompleteDueCoolingOff(context.Background(), 10)
		return err
	})
	attempt(func() error {
		_, err := service.CompleteDueCoolingOff(context.Background(), 10)
		return err
	})
	close(start)
	for range 4 {
		switch err := <-results; {
		case err == nil, errors.Is(err, ErrPartnershipUnavailable),
			errors.Is(err, ErrCoolingOffNotRunning):
		default:
			t.Fatalf("concurrent Cooling-off outcome = %v", err)
		}
	}

	// Exactly one outcome landed: either the cancellation, or one ending.
	var ended, endings, cancellations int
	if err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM partnerships WHERE ended_at IS NOT NULL),
		(SELECT COUNT(*) FROM activity_entries WHERE action IN (?, ?)),
		(SELECT COUNT(*) FROM activity_entries WHERE action = ?)`,
		ActionPartnershipEndedByPartner, ActionPartnershipEndedAfterCoolingOff,
		ActionCoolingOffCanceled).Scan(&ended, &endings, &cancellations); err != nil {
		t.Fatal(err)
	}
	if ended != endings || endings+cancellations != 1 {
		t.Fatalf("ended=%d endings=%d cancellations=%d, want exactly one outcome", ended, endings, cancellations)
	}
}
