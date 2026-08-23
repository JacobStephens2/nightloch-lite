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

// Dispatcher runs the Partnership module's due background work in the
// single-process control plane: the invitation-expiry sweep and the durable
// mail queue. It owns no relationship rules; it only asks the module what is
// due. Both kinds of work live in SQLite, so a restart resumes whatever the
// previous process had not finished.
type Dispatcher struct {
	Service  *Service
	Sender   mailmessage.Sender
	Logger   *slog.Logger
	Interval time.Duration
	Batch    int
}

// Run works until ctx is cancelled. A failure in either sweep is logged and
// retried on the next tick; it never stops the loop and never touches
// Partnership state the module did not change itself.
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
		// Expiry runs first so an invitation that lapsed while the process was
		// down stops being answerable before anything else is attempted.
		if expired, err := d.Service.ExpireDueInvitations(ctx, batch); err != nil {
			logger.Error("partner invitation expiry failed", "expired", expired, "error", err)
		}
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
