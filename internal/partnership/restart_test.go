package partnership

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	mailmessage "github.com/JacobStephens2/nightloch-lite/internal/mail"
)

// crashingSender accepts sends until the process it stands for dies, after
// which every send fails the way an interrupted one does: the job stays
// claimed and becomes due again. It is safe for concurrent use because a
// restart test may run two dispatchers over the same database.
type crashingSender struct {
	mu sync.Mutex
	// process distinguishes the provider message ids this process's sends
	// receive, because a real provider never reuses an id across restarts.
	process string
	// crashAfter is how many envelopes this process transmits before dying.
	// Zero means it never dies.
	crashAfter int
	envelopes  []mailmessage.Envelope
}

func (s *crashingSender) Send(_ context.Context, envelope mailmessage.Envelope) (mailmessage.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashAfter > 0 && len(s.envelopes) >= s.crashAfter {
		return mailmessage.Receipt{}, errors.New("process died mid-send")
	}
	s.envelopes = append(s.envelopes, envelope)
	return mailmessage.Receipt{ProviderMessageID: s.process + mailmessage.RecordedMessageID(len(s.envelopes))}, nil
}

func (s *crashingSender) delivered() []mailmessage.Envelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mailmessage.Envelope(nil), s.envelopes...)
}

// drain runs the module's due work until nothing is left to send, which is
// what a restarted process's dispatcher loop does on its first few ticks.
func drain(t *testing.T, service *Service, sender mailmessage.Sender) {
	t.Helper()
	for range 10 {
		if _, err := service.ExpireDueInvitations(context.Background(), 20); err != nil {
			t.Fatalf("ExpireDueInvitations: %v", err)
		}
		if _, err := service.CompleteDueCoolingOff(context.Background(), 20); err != nil {
			t.Fatalf("CompleteDueCoolingOff: %v", err)
		}
		sent, err := service.DispatchDue(context.Background(), sender, 20)
		if err != nil {
			t.Fatalf("DispatchDue: %v", err)
		}
		if sent == 0 {
			return
		}
	}
	t.Fatal("due work never drained")
}

func TestDueTimersAndQueuedMailResumeAfterRestartExactlyOnce(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, _, partnership := firstPartnership(t, service, db, false)
	lapsing := invite(t, service, protectee, "invitee@example.com", false)
	startEnding(t, service, partnership, protectee)

	// The process dies with work queued and both timers still in the future.
	dying := &crashingSender{process: "before-", crashAfter: 1}
	if _, err := service.DispatchDue(context.Background(), dying, 20); err == nil {
		t.Fatal("the crashing process reported a clean sweep")
	}

	// Nothing runs for a week. Both the invitation's deadline and the
	// Cooling-off deadline pass while Nightloch is down.
	restarted := NewService(db, service.baseURL)
	restartAt := testClock.Add(invitationTTL + 24*time.Hour)
	restarted.now = func() time.Time { return restartAt }
	sender := &crashingSender{process: "after-"}
	drain(t, restarted, sender)

	// Each lifecycle change landed once, not once per missed tick.
	expired := countRows(t, db, "SELECT COUNT(*) FROM activity_entries WHERE action = ?", ActionInvitationExpired)
	ended := countRows(t, db, "SELECT COUNT(*) FROM activity_entries WHERE action = ?", ActionPartnershipEndedAfterCoolingOff)
	if expired != 1 || ended != 1 {
		t.Fatalf("after the restart: %d expiries and %d completed deadlines, want 1 and 1", expired, ended)
	}
	if state := invitationState(t, restarted, lapsing.ID); state != string(InvitationExpired) {
		t.Fatalf("the lapsed invitation resumed as %q", state)
	}
	if live := countRows(t, db, "SELECT COUNT(*) FROM partnerships WHERE ended_at IS NULL"); live != 0 {
		t.Fatalf("%d Partnerships outlived their own deadline", live)
	}

	// Every queued job was transmitted, and no message reached the provider
	// twice: the interrupted send returns under the key it already used.
	if queued := countRows(t, db, "SELECT COUNT(*) FROM mail_delivery_jobs WHERE state <> 'sent'"); queued != 0 {
		t.Fatalf("%d delivery jobs never resumed", queued)
	}
	keys := map[string]int{}
	for _, envelope := range append(dying.delivered(), sender.delivered()...) {
		keys[envelope.IdempotencyKey]++
	}
	for key, count := range keys {
		if count != 1 {
			t.Fatalf("idempotency key %q was transmitted %d times", key, count)
		}
	}
	jobs := countRows(t, db, "SELECT COUNT(*) FROM mail_delivery_jobs")
	if len(keys) != jobs {
		t.Fatalf("%d jobs produced %d transmitted messages", jobs, len(keys))
	}
}

func TestTwoDispatchersOverOneDatabaseSendEachMessageOnce(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	for index := range 4 {
		invite(t, service, protectee, inviteAddress(index), false)
	}

	// A restart that overlaps its predecessor — or a second process started by
	// mistake — must not double-send: the claim, not the read, is what owns a
	// job.
	other := NewService(db, service.baseURL)
	other.now = service.now
	sender := &crashingSender{}
	results := raceAll(
		func() error {
			_, err := service.DispatchDue(context.Background(), sender, 20)
			return err
		},
		func() error {
			_, err := other.DispatchDue(context.Background(), sender, 20)
			return err
		},
	)
	for index, err := range results {
		if err != nil {
			t.Fatalf("dispatcher %d = %v", index, err)
		}
	}

	delivered := sender.delivered()
	if len(delivered) != 4 {
		t.Fatalf("two dispatchers transmitted %d messages, want 4", len(delivered))
	}
	seen := map[string]bool{}
	for _, envelope := range delivered {
		if seen[envelope.IdempotencyKey] {
			t.Fatalf("%s was transmitted twice", envelope.To)
		}
		seen[envelope.IdempotencyKey] = true
	}
	if queued := countRows(t, db, "SELECT COUNT(*) FROM mail_delivery_jobs WHERE state <> 'sent'"); queued != 0 {
		t.Fatalf("%d jobs were left behind by two dispatchers", queued)
	}
}

func TestARestartedRunnerCompletesADeadlineItNeverScheduled(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee, partner, partnership := firstPartnership(t, service, db, false)
	startEnding(t, service, partnership, protectee)

	// The process that took the request never sees the deadline arrive.
	restarted := NewService(db, service.baseURL)
	restarted.now = func() time.Time { return testClock.Add(coolingOffDuration) }
	if ended, err := restarted.CompleteDueCoolingOff(context.Background(), 10); err != nil || ended != 1 {
		t.Fatalf("restarted runner completed %d deadlines: %v", ended, err)
	}
	// A second sweep, and a third process after it, find nothing to do.
	for _, sweeper := range []*Service{restarted, NewService(db, service.baseURL)} {
		sweeper.now = restarted.now
		if ended, err := sweeper.CompleteDueCoolingOff(context.Background(), 10); err != nil || ended != 0 {
			t.Fatalf("a repeat sweep ended %d Partnerships: %v", ended, err)
		}
	}

	// The Partner keeps one receipt and was told once, by Nightloch, that the
	// deadline it carried out was the Protectee's own request.
	sender := &crashingSender{}
	drain(t, restarted, sender)
	told := 0
	for _, envelope := range sender.delivered() {
		if envelope.To == "partner@example.com" && strings.Contains(envelope.Subject, "ended") {
			told++
		}
	}
	if told != 1 {
		t.Fatalf("the Partner was told %d times that the Partnership ended", told)
	}
	receipt, err := restarted.EndedPartnershipReceipt(context.Background(), partnership, partner)
	if err != nil || !receipt.EndedAfterCoolingOff() {
		t.Fatalf("receipt after the restarted deadline = %+v, %v", receipt, err)
	}
}
