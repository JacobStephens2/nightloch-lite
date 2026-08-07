// Package config loads and validates Nightloch's process configuration.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const defaultResendFrom = "Nightloch <alerts@nightloch.app>"

// Config contains the configuration required by the HTTP control plane.
type Config struct {
	BaseURL         *url.URL
	DatabasePath    string
	HTTPAddr        string
	ResendAPIKey    string
	ResendFrom      string
	TurnstileSecret string
	TurnstileSite   string
}

// DatabasePath returns the configured SQLite path for commands that only need
// database access, such as migrate.
func DatabasePath() (string, error) {
	path := strings.TrimSpace(os.Getenv("DATABASE_PATH"))
	if path == "" {
		return "", errors.New("DATABASE_PATH is required")
	}
	if path == ":memory:" {
		return "", errors.New("DATABASE_PATH must be a durable file")
	}
	return filepath.Clean(path), nil
}

// Load reads and validates the environment for the serve command.
func Load() (Config, error) {
	databasePath, err := DatabasePath()
	if err != nil {
		return Config{}, err
	}

	baseURL, err := parseBaseURL(os.Getenv("BASE_URL"))
	if err != nil {
		return Config{}, err
	}

	httpAddr := strings.TrimSpace(os.Getenv("HTTP_ADDR"))
	if httpAddr == "" {
		httpAddr = "127.0.0.1:8080"
	}
	if err := validateLoopbackAddr(httpAddr); err != nil {
		return Config{}, err
	}

	config := Config{
		BaseURL:         baseURL,
		DatabasePath:    databasePath,
		HTTPAddr:        httpAddr,
		ResendAPIKey:    strings.TrimSpace(os.Getenv("RESEND_API_KEY")),
		ResendFrom:      strings.TrimSpace(os.Getenv("RESEND_FROM")),
		TurnstileSecret: strings.TrimSpace(os.Getenv("TURNSTILE_SECRET_KEY")),
		TurnstileSite:   strings.TrimSpace(os.Getenv("TURNSTILE_SITE_KEY")),
	}
	if config.ResendFrom == "" {
		config.ResendFrom = defaultResendFrom
	}

	var missing []string
	for name, value := range map[string]string{
		"RESEND_API_KEY":       config.ResendAPIKey,
		"TURNSTILE_SECRET_KEY": config.TurnstileSecret,
		"TURNSTILE_SITE_KEY":   config.TurnstileSite,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("required environment variables are missing: %s", strings.Join(missing, ", "))
	}
	return config, nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("BASE_URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse BASE_URL: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return nil, errors.New("BASE_URL must be an absolute https URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("BASE_URL must not contain credentials, a path, query, or fragment")
	}
	parsed.Path = ""
	return parsed, nil
}

func validateLoopbackAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("HTTP_ADDR must be host:port: %w", err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("HTTP_ADDR must bind to a loopback address; Caddy is the public listener")
	}
	return nil
}
