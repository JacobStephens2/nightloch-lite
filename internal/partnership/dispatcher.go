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
// single-process control plane: the invitation-expiry sweep, the Cooling-off
// completion runner, and the durable mail queue. It owns no relationship
// rules; it only asks the module what is due. Every kind of work lives in
// SQLite, so a restart resumes whatever the previous process had not finished.
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
		// Cooling-off completes before mail goes out, so a deadline that
		// passed while nothing was running lands as an ending rather than
		// sitting behind a queue of older alerts.
		if ended, err := d.Service.CompleteDueCoolingOff(ctx, batch); err != nil {
			logger.Error("Cooling-off completion failed", "ended", ended, "error", err)
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
