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

// Send transmits envelope without including its contents in returned errors,
// and returns Resend's own id for the accepted message. That id is what a
// later delivery, bounce, or complaint callback names, so a caller that stores
// it can attribute the callback to exactly one recipient's message.
func (s *ResendSender) Send(ctx context.Context, envelope Envelope) (Receipt, error) {
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
		return Receipt{}, fmt.Errorf("encode Resend request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Receipt{}, fmt.Errorf("create Resend request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+s.APIKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", envelope.IdempotencyKey)
	response, err := client.Do(request)
	if err != nil {
		return Receipt{}, fmt.Errorf("send Resend request: %w", err)
	}
	defer response.Body.Close()
	accepted, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return Receipt{}, fmt.Errorf("read Resend response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Receipt{}, fmt.Errorf("Resend returned HTTP %d", response.StatusCode)
	}
	// A message id is not required for the send to have happened, so a
	// response Nightloch cannot parse is still a success. It only means this
	// message has no delivery callbacks to attribute, which the retry-derived
	// delivery state already covers.
	var result struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(accepted, &result)
	return Receipt{ProviderMessageID: result.ID}, nil
}
