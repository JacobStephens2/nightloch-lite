package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const resendEndpoint = "https://api.resend.com/emails"

// ResendSender transmits prepared transactional envelopes through Resend.
type ResendSender struct {
	APIKey   string
	From     string
	Client   *http.Client
	Endpoint string
}

// Send transmits envelope without including its contents in returned errors.
func (s *ResendSender) Send(ctx context.Context, envelope Envelope) error {
	endpoint := s.Endpoint
	if endpoint == "" {
		endpoint = resendEndpoint
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	payload := struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
		HTML    string   `json:"html"`
		ReplyTo string   `json:"reply_to"`
	}{
		From: s.From, To: []string{envelope.To}, Subject: envelope.Subject,
		Text: envelope.Text, HTML: envelope.HTML, ReplyTo: envelope.ReplyTo,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Resend request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create Resend request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+s.APIKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", envelope.IdempotencyKey)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("send Resend request: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Resend returned HTTP %d", response.StatusCode)
	}
	return nil
}
