package mail

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// webhookSecretPrefix is how Resend presents an endpoint's signing secret. The
// bytes that sign a callback are the base64 body after it.
const webhookSecretPrefix = "whsec_"

// webhookTolerance is how far a callback's own timestamp may sit from the
// receiving clock. It bounds replay of a captured, correctly signed callback.
const webhookTolerance = 5 * time.Minute

// signatureVersion is the only signature scheme Nightloch accepts. Unknown
// versions are ignored rather than trusted.
const signatureVersion = "v1"

// ErrUnauthenticatedWebhook rejects a provider callback Nightloch cannot
// attribute to its own configured endpoint secret. It carries no detail,
// because everything that distinguishes the failures is attacker-supplied.
var ErrUnauthenticatedWebhook = errors.New("unauthenticated provider callback")

// WebhookVerifier authenticates Resend's Svix-signed delivery callbacks. The
// signature is the only thing that authorizes one: the endpoint holds no
// session, so an unsigned request must change nothing.
type WebhookVerifier struct {
	// Secret is the endpoint signing secret in Resend's own `whsec_` form.
	Secret string
}

// Verify authenticates one callback and returns the provider's event id, which
// is the unit of delivery-event idempotency. now is the receiving clock.
func (v WebhookVerifier) Verify(header http.Header, body []byte, now time.Time) (string, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v.Secret, webhookSecretPrefix))
	if err != nil || len(key) == 0 {
		return "", ErrUnauthenticatedWebhook
	}
	eventID := header.Get("svix-id")
	stamp := header.Get("svix-timestamp")
	signatures := strings.Fields(header.Get("svix-signature"))
	if eventID == "" || stamp == "" || len(signatures) == 0 {
		return "", ErrUnauthenticatedWebhook
	}
	seconds, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return "", ErrUnauthenticatedWebhook
	}
	if drift := now.Sub(time.Unix(seconds, 0)); drift > webhookTolerance || drift < -webhookTolerance {
		return "", ErrUnauthenticatedWebhook
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(eventID + "." + stamp + "."))
	mac.Write(body)
	want := mac.Sum(nil)
	for _, candidate := range signatures {
		version, encoded, found := strings.Cut(candidate, ",")
		if !found || version != signatureVersion {
			continue
		}
		got, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			continue
		}
		if hmac.Equal(got, want) {
			return eventID, nil
		}
	}
	return "", ErrUnauthenticatedWebhook
}
