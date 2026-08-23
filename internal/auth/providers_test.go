package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
