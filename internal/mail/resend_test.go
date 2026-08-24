package mail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestResendSenderTransmitsPreparedEnvelope(t *testing.T) {
	t.Parallel()

	envelope := Envelope{
		To:             "person@example.com",
		Subject:        "Prepared subject",
		Text:           "Prepared plain text",
		HTML:           "<p>Prepared HTML</p>",
		ReplyTo:        "alerts@nightloch.app",
		IdempotencyKey: "message/123/person",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != envelope.IdempotencyKey {
			t.Errorf("Idempotency-Key = %q", got)
		}

		var payload struct {
			From    string   `json:"from"`
			To      []string `json:"to"`
			Subject string   `json:"subject"`
			Text    string   `json:"text"`
			HTML    string   `json:"html"`
			ReplyTo string   `json:"reply_to"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		want := struct {
			From    string   `json:"from"`
			To      []string `json:"to"`
			Subject string   `json:"subject"`
			Text    string   `json:"text"`
			HTML    string   `json:"html"`
			ReplyTo string   `json:"reply_to"`
		}{
			From: "Nightloch <alerts@nightloch.app>", To: []string{envelope.To},
			Subject: envelope.Subject, Text: envelope.Text, HTML: envelope.HTML,
			ReplyTo: envelope.ReplyTo,
		}
		if !reflect.DeepEqual(payload, want) {
			t.Errorf("payload = %#v, want %#v", payload, want)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"4ef9a417-02e9-4d39-ad75-9611e0fcc33c"}`))
	}))
	defer server.Close()

	sender := &ResendSender{
		APIKey: "secret", From: "Nightloch <alerts@nightloch.app>",
		Client: server.Client(), Endpoint: server.URL,
	}
	receipt, err := sender.Send(context.Background(), envelope)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Without the provider's own message id, a later delivery or bounce
	// callback cannot be attributed to this recipient's message.
	if receipt.ProviderMessageID != "4ef9a417-02e9-4d39-ad75-9611e0fcc33c" {
		t.Fatalf("provider message id = %q", receipt.ProviderMessageID)
	}
}

func TestResendSenderRejectsNonSuccessWithoutExposingMessage(t *testing.T) {
	t.Parallel()

	const messageSecret = "bearer-link-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"` + messageSecret + `"}`))
	}))
	defer server.Close()

	sender := &ResendSender{
		APIKey: "secret", From: "Nightloch <alerts@nightloch.app>",
		Client: server.Client(), Endpoint: server.URL,
	}
	_, err := sender.Send(context.Background(), Envelope{
		To: "person@example.com", Subject: "Subject", Text: messageSecret,
		HTML: "<p>" + messageSecret + "</p>", ReplyTo: "alerts@nightloch.app",
		IdempotencyKey: "message/secret",
	})
	if err == nil {
		t.Fatal("Send succeeded for a non-success Resend response")
	}
	if strings.Contains(err.Error(), messageSecret) {
		t.Fatalf("Send error exposed message secret: %v", err)
	}
}
