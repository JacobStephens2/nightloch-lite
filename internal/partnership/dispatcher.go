package partnership

import (
	"context"
	"log/slog"
	"time"

	mailmessage "github.com/JacobStephens2/nightloch-lite/internal/mail"
)

const (
	defaultDispatchInterval = 15 * time.Second
	defaultDispatchBatch    = 20
)

// Dispatcher drains durable delivery jobs from the single-process control
// plane. It owns no relationship rules; it only asks the Partnership module
// for due work. Because the queue lives in SQLite, a restart resumes whatever
// the previous process had not yet sent.
type Dispatcher struct {
	Service  *Service
	Sender   mailmessage.Sender
	Logger   *slog.Logger
	Interval time.Duration
	Batch    int
}

// Run dispatches until ctx is cancelled. A delivery failure is logged and
// retried later; it never stops the loop and never touches Partnership state.
func (d *Dispatcher) Run(ctx context.Context) {
	interval := d.Interval
	if interval <= 0 {
		interval = defaultDispatchInterval
	}
	batch := d.Batch
	if batch <= 0 {
		batch = defaultDispatchBatch
	}
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if sent, err := d.Service.DispatchDue(ctx, d.Sender, batch); err != nil {
			logger.Error("partnership mail delivery failed", "sent", sent, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
