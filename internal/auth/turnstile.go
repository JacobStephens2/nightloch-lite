package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const turnstileEndpoint = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// TurnstileVerifier performs Cloudflare's server-side verification.
type TurnstileVerifier struct {
	Secret   string
	Hostname string
	Action   string
	Client   *http.Client
	Endpoint string
}

func (v *TurnstileVerifier) Verify(ctx context.Context, token, ip string) error {
	if strings.TrimSpace(token) == "" || len(token) > 2048 {
		return ErrHumanCheck
	}
	endpoint := v.Endpoint
	if endpoint == "" {
		endpoint = turnstileEndpoint
	}
	client := v.Client
	if client == nil {
		client = http.DefaultClient
	}
	form := url.Values{
		"secret":   []string{v.Secret},
		"response": []string{token},
		"remoteip": []string{ip},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("create Turnstile request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("verify Turnstile: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrHumanCheck
	}
	var result struct {
		Success  bool   `json:"success"`
		Hostname string `json:"hostname"`
		Action   string `json:"action"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result); err != nil {
		return fmt.Errorf("decode Turnstile response: %w", err)
	}
	if !result.Success || (v.Hostname != "" && !strings.EqualFold(result.Hostname, v.Hostname)) || (v.Action != "" && result.Action != v.Action) {
		return ErrHumanCheck
	}
	return nil
}
