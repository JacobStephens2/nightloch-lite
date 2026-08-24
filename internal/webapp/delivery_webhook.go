package webapp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/JacobStephens2/nightloch-lite/internal/partnership"
)

const (
	// deliveryWebhookPath is the provider callback endpoint. It is the one
	// route with no session and no CSRF token: its caller is Resend, which
	// authenticates by signing the request instead.
	deliveryWebhookPath = "/webhooks/resend"
	// maxWebhookBytes bounds an unauthenticated request body. The signature is
	// computed over the body, so the read has to happen before the check.
	maxWebhookBytes = 64 << 10
	// transientBounce is the only bounce Resend reports as still worth
	// retrying. Anything else — permanent, undetermined, or unstated — renders
	// the warning, because a visible warning that turns out to be premature
	// costs a Protectee nothing: it changes no power and no deadline (S3-58).
	transientBounce = "Transient"
)

// deliveryCallback is the part of a Resend event Nightloch acts on. Everything
// else in the payload, including the recipient list and the provider's own
// prose about a bounce, is deliberately unread: the message id already names
// exactly one recipient's job.
type deliveryCallback struct {
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Data      struct {
		EmailID string `json:"email_id"`
		Bounce  struct {
			Type string `json:"type"`
		} `json:"bounce"`
	} `json:"data"`
}

// deliveryWebhook applies one authenticated provider delivery event. It never
// reads a session and never answers with anything a caller could learn from:
// the same empty 204 covers an event that changed a Roster warning and one
// about a message this control plane never sent.
func (a *App) deliveryWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBytes))
	if err != nil {
		http.Error(w, "Callback rejected.", http.StatusBadRequest)
		return
	}
	eventID, err := a.deliveryVerifier.Verify(r.Header, body, a.now())
	if err != nil {
		// The signature is the whole authorization, so a failure says nothing
		// about whether the message, the recipient, or the event was real.
		a.logger.Warn("rejected an unauthenticated delivery callback")
		http.Error(w, "Callback rejected.", http.StatusUnauthorized)
		return
	}

	var callback deliveryCallback
	if err := json.Unmarshal(body, &callback); err != nil {
		http.Error(w, "Callback rejected.", http.StatusBadRequest)
		return
	}
	if callback.Data.EmailID == "" {
		// Nothing names a message, so nothing can be attributed. Acknowledge
		// it rather than making the provider retry what will never apply.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// A payload with no usable timestamp is stamped on arrival rather than at
	// the zero time, which would sort behind every event already applied and
	// silently do nothing.
	occurredAt := callback.CreatedAt
	if occurredAt.IsZero() {
		occurredAt = a.now()
	}

	err = a.partnerships.RecordDeliveryEvent(r.Context(), partnership.DeliveryEvent{
		ProviderEventID:   eventID,
		ProviderMessageID: callback.Data.EmailID,
		Type:              partnership.DeliveryEventType(callback.Type),
		Permanent:         !strings.EqualFold(callback.Data.Bounce.Type, transientBounce),
		OccurredAt:        occurredAt,
	})
	switch {
	case errors.Is(err, partnership.ErrUnknownDeliveryEvent):
		// Opens, clicks, and anything Resend adds later are not reachability.
		// Acknowledging stops the provider retrying an event Nightloch will
		// never act on.
		w.WriteHeader(http.StatusNoContent)
	case err != nil:
		// A storage failure must not be acknowledged: the provider's retry is
		// the only thing that will deliver this event again.
		a.logger.Error("delivery callback failed", "error", err)
		http.Error(w, "Callback failed.", http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
