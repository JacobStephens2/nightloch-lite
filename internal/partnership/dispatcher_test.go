package partnership

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	mailmessage "github.com/JacobStephens2/nightloch-lite/internal/mail"
)

func TestDispatcherDrainsWorkQueuedBeforeItStarted(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	protectee := createUser(t, db, "protectee@example.com")
	if _, err := service.InvitePartner(context.Background(), InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "invitee@example.com",
	}); err != nil {
		t.Fatal(err)
	}

	sender := &mailmessage.RecordingSender{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dispatcher := &Dispatcher{
		Service:  service,
		Sender:   sender,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Interval: time.Millisecond,
	}
	done := make(chan struct{})
	go func() {
		dispatcher.Run(ctx)
		close(done)
	}()

	deadline := time.After(5 * time.Second)
	for {
		var state string
		if err := db.QueryRow("SELECT state FROM mail_delivery_jobs").Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "sent" {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("delivery job never left state %q", state)
		case <-time.After(time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Dispatcher did not stop when its context was cancelled")
	}
}

// A process that dies mid-send leaves a claimed job behind. The retry must
// reuse the same idempotency key, which is what stops the provider treating it
// as a second message to the same recipient (AC1, ADR 0002).
func TestInterruptedSendRetriesUnderTheSameIdempotencyKey(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()
	protectee := createUser(t, db, "protectee@example.com")
	if _, err := service.InvitePartner(ctx, InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "invitee@example.com"}); err != nil {
		t.Fatal(err)
	}

	// A send the process never saw finish looks exactly like a failed one:
	// claimed, attempted, and due again after its backoff.
	interrupted := &mailmessage.RecordingSender{SendError: errors.New("process died mid-send")}
	if _, err := service.DispatchDue(ctx, interrupted, 10); err == nil {
		t.Fatal("DispatchDue hid the interrupted send")
	}

	restarted := NewService(db, service.baseURL)
	restarted.now = func() time.Time { return testClock.Add(time.Hour) }
	sender := &mailmessage.RecordingSender{}
	if sent, err := restarted.DispatchDue(ctx, sender, 10); err != nil || sent != 1 {
		t.Fatalf("retry after restart = %d, %v", sent, err)
	}
	if len(sender.Envelopes) != 1 {
		t.Fatalf("retry sent %d envelopes", len(sender.Envelopes))
	}
	if sender.Envelopes[0].IdempotencyKey != interrupted.Envelopes[0].IdempotencyKey {
		t.Fatalf("the retry changed idempotency key from %q to %q",
			interrupted.Envelopes[0].IdempotencyKey, sender.Envelopes[0].IdempotencyKey)
	}
	// One recipient, one job, one row — the retry queued nothing extra.
	var jobs int
	if err := db.QueryRow("SELECT COUNT(*) FROM mail_delivery_jobs").Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("delivery jobs after a restarted retry = %d, want 1", jobs)
	}
}
