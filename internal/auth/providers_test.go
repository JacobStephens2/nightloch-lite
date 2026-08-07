package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResendMailerRequest(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Idempotency-Key") == "" {
			t.Error("Idempotency-Key is missing")
		}
		var payload struct {
			From string   `json:"from"`
			To   []string `json:"to"`
			Text string   `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.From != "Nightloch <alerts@nightloch.app>" || len(payload.To) != 1 || payload.To[0] != "person@example.com" {
			t.Errorf("payload = %+v", payload)
		}
		if payload.Text == "" {
			t.Error("plain-text email body is empty")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	mailer := &ResendMailer{
		APIKey: "secret", From: "Nightloch <alerts@nightloch.app>",
		Client: server.Client(), Endpoint: server.URL,
	}
	if err := mailer.SendMagicLink(context.Background(), "person@example.com", "https://lite.nightloch.app/auth/consume?token=secret"); err != nil {
		t.Fatal(err)
	}
}

func TestTurnstileVerifierRequest(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("secret") != "secret" || r.Form.Get("response") != "human" || r.Form.Get("remoteip") != "203.0.113.1" {
			t.Errorf("form = %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"hostname":"lite.nightloch.app","action":"sign-in"}`))
	}))
	defer server.Close()

	verifier := &TurnstileVerifier{
		Secret: "secret", Hostname: "lite.nightloch.app", Action: "sign-in",
		Client: server.Client(), Endpoint: server.URL,
	}
	if err := verifier.Verify(context.Background(), "human", "203.0.113.1"); err != nil {
		t.Fatal(err)
	}
}
