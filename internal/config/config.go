// Package config loads and validates Nightloch's process configuration.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const defaultResendFrom = "Nightloch <alerts@nightloch.app>"

// defaultTunnelPort is the port Slice 1's cloud firewall already opens.
const defaultTunnelPort = 51820

// Config contains the configuration required by the HTTP control plane.
type Config struct {
	BaseURL      *url.URL
	DatabasePath string
	HTTPAddr     string
	ResendAPIKey string
	ResendFrom   string
	// ResendWebhookSecret authenticates Resend's delivery callbacks. Without
	// it Partner reachability could only reflect Nightloch's own send
	// attempts, never a bounce or a complaint, so it is required.
	ResendWebhookSecret string
	TurnstileSecret     string
	TurnstileSite       string
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

// Tunnel is the deployment's server side of the WireGuard tunnel, which is
// what a minted Device's configuration has to name. It is loaded on its own
// rather than as part of Config so that the Operator commands that need it do
// not also demand the mail and challenge secrets that only serving needs.
//
// What a value has to look like to be renderable is the enforcement module's
// rule, not this package's: this only reports what is absent or unreadable.
type Tunnel struct {
	// EndpointHost is the published tunnel hostname. A hostname rather than an
	// address is what lets the droplet be rebuilt without invalidating a
	// Device that is already installed on a phone.
	EndpointHost string
	// EndpointPort is the tunnel's listening port.
	EndpointPort int
	// PublicKey is the tunnel interface's own public key. The matching private
	// key stays on the host, inside the interface configuration only root
	// reads, and never reaches this process.
	PublicKey string
}

// LoadTunnel reads the tunnel environment for the Operator commands that mint
// and list Devices.
func LoadTunnel() (Tunnel, error) {
	tunnel := Tunnel{
		EndpointHost: strings.TrimSpace(os.Getenv("TUNNEL_ENDPOINT_HOST")),
		EndpointPort: defaultTunnelPort,
		PublicKey:    strings.TrimSpace(os.Getenv("TUNNEL_PUBLIC_KEY")),
	}
	if port := strings.TrimSpace(os.Getenv("TUNNEL_ENDPOINT_PORT")); port != "" {
		parsed, err := strconv.Atoi(port)
		if err != nil {
			return Tunnel{}, fmt.Errorf("TUNNEL_ENDPOINT_PORT must be a port number: %w", err)
		}
		tunnel.EndpointPort = parsed
	}
	var missing []string
	if tunnel.EndpointHost == "" {
		missing = append(missing, "TUNNEL_ENDPOINT_HOST")
	}
	if tunnel.PublicKey == "" {
		missing = append(missing, "TUNNEL_PUBLIC_KEY")
	}
	if len(missing) > 0 {
		return Tunnel{}, fmt.Errorf("required environment variables are missing: %s", strings.Join(missing, ", "))
	}
	return tunnel, nil
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
		BaseURL:             baseURL,
		DatabasePath:        databasePath,
		HTTPAddr:            httpAddr,
		ResendAPIKey:        strings.TrimSpace(os.Getenv("RESEND_API_KEY")),
		ResendFrom:          strings.TrimSpace(os.Getenv("RESEND_FROM")),
		ResendWebhookSecret: strings.TrimSpace(os.Getenv("RESEND_WEBHOOK_SECRET")),
		TurnstileSecret:     strings.TrimSpace(os.Getenv("TURNSTILE_SECRET_KEY")),
		TurnstileSite:       strings.TrimSpace(os.Getenv("TURNSTILE_SITE_KEY")),
	}
	if config.ResendFrom == "" {
		config.ResendFrom = defaultResendFrom
	}

	var missing []string
	for name, value := range map[string]string{
		"RESEND_API_KEY":        config.ResendAPIKey,
		"RESEND_WEBHOOK_SECRET": config.ResendWebhookSecret,
		"TURNSTILE_SECRET_KEY":  config.TurnstileSecret,
		"TURNSTILE_SITE_KEY":    config.TurnstileSite,
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
