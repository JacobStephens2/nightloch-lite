package partnership

import (
	"context"
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
