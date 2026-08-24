package mail

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"testing"
	"time"
)

const testWebhookSecret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"

func signedHeader(t *testing.T, secret, id string, at time.Time, body []byte) http.Header {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(secret[len("whsec_"):])
	if err != nil {
		t.Fatal(err)
	}
	stamp := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + stamp + "."))
	mac.Write(body)
	header := http.Header{}
	header.Set("svix-id", id)
	header.Set("svix-timestamp", stamp)
	header.Set("svix-signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return header
}

func TestWebhookVerifierAcceptsAProviderSignature(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	body := []byte(`{"type":"email.delivered"}`)
	verifier := WebhookVerifier{Secret: testWebhookSecret}

	id, err := verifier.Verify(signedHeader(t, testWebhookSecret, "msg_1", now, body), body, now.Add(time.Second))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id != "msg_1" {
		t.Fatalf("event id = %q, want msg_1", id)
	}
}

// A verifier that accepted extra signatures, a rewritten body, a stale
// timestamp, or a missing header would let anyone mark a Partner unreachable.
func TestWebhookVerifierRejectsUnauthenticatedCallbacks(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	body := []byte(`{"type":"email.delivered"}`)
	verifier := WebhookVerifier{Secret: testWebhookSecret}

	valid := signedHeader(t, testWebhookSecret, "msg_1", now, body)

	tampered := signedHeader(t, testWebhookSecret, "msg_1", now, body)
	tampered.Set("svix-id", "msg_2")

	wrongSecret := signedHeader(t, "whsec_"+base64.StdEncoding.EncodeToString([]byte("another-signing-key")), "msg_1", now, body)

	missing := signedHeader(t, testWebhookSecret, "msg_1", now, body)
	missing.Del("svix-signature")

	unversioned := signedHeader(t, testWebhookSecret, "msg_1", now, body)
	unversioned.Set("svix-signature", "v0,"+unversioned.Get("svix-signature")[len("v1,"):])

	for name, attempt := range map[string]struct {
		header http.Header
		body   []byte
		at     time.Time
	}{
		"rewritten body":    {valid, []byte(`{"type":"email.bounced"}`), now},
		"replayed id":       {tampered, body, now},
		"wrong secret":      {wrongSecret, body, now},
		"missing signature": {missing, body, now},
		"unknown version":   {unversioned, body, now},
		"timestamp in past": {valid, body, now.Add(time.Hour)},
		"timestamp ahead":   {valid, body, now.Add(-time.Hour)},
	} {
		if _, err := verifier.Verify(attempt.header, attempt.body, attempt.at); err == nil {
			t.Errorf("%s: Verify accepted an unauthenticated callback", name)
		}
	}
}

// Svix rotates by sending several signatures at once; one match is enough.
func TestWebhookVerifierAcceptsOneOfSeveralSignatures(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	body := []byte(`{"type":"email.delivered"}`)
	header := signedHeader(t, testWebhookSecret, "msg_1", now, body)
	header.Set("svix-signature", "v1,ZGVjb3k= "+header.Get("svix-signature"))

	if _, err := (WebhookVerifier{Secret: testWebhookSecret}).Verify(header, body, now); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestWebhookVerifierRefusesToRunWithoutASecret(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	body := []byte(`{}`)
	if _, err := (WebhookVerifier{}).Verify(signedHeader(t, testWebhookSecret, "msg_1", now, body), body, now); err == nil {
		t.Fatal("Verify accepted a callback with no signing secret configured")
	}
}
