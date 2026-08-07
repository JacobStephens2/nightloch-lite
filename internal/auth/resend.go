package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
)

const resendEndpoint = "https://api.resend.com/emails"

// ResendMailer delivers Nightloch transactional email through Resend.
type ResendMailer struct {
	APIKey   string
	From     string
	Client   *http.Client
	Endpoint string
}

func (m *ResendMailer) SendMagicLink(ctx context.Context, to, link string) error {
	endpoint := m.Endpoint
	if endpoint == "" {
		endpoint = resendEndpoint
	}
	client := m.Client
	if client == nil {
		client = http.DefaultClient
	}
	escapedLink := html.EscapeString(link)
	payload := struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
		HTML    string   `json:"html"`
	}{
		From:    m.From,
		To:      []string{to},
		Subject: "Your Nightloch sign-in link",
		Text:    "Sign in to Nightloch (this link expires in 15 minutes and can be used once):\n\n" + link,
		HTML:    `<p>Sign in to Nightloch. This link expires in 15 minutes and can be used once.</p><p><a href="` + escapedLink + `">Sign in to Nightloch</a></p>`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Resend request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create Resend request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.APIKey)
	req.Header.Set("Content-Type", "application/json")
	linkDigest := sha256.Sum256([]byte(link))
	req.Header.Set("Idempotency-Key", "nightloch-magic-"+hex.EncodeToString(linkDigest[:]))
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send Resend request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return fmt.Errorf("Resend returned HTTP %d", response.StatusCode)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	return nil
}
