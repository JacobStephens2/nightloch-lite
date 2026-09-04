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

	"github.com/JacobStephens2/nightloch-lite/internal/enforcement"
)

const defaultResendFrom = "Nightloch <alerts@nightloch.app>"

// defaultTunnelPort is the port Slice 1's cloud firewall already opens.
const defaultTunnelPort = 51820

// defaultResolverURL is where AdGuard Home's administration listens on the
// droplet. The app reaches it directly on loopback; the Caddy site of the same
// name is the Operator's way in, behind basic auth (deploy/Caddyfile.example).
const defaultResolverURL = "http://127.0.0.1:3000"

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
	// Resolver is AdGuard Home's administration endpoint, which health reads
	// to report whether the enforcement plane is filtering. It is required
	// rather than optional: a serving process that cannot say whether the
	// Baseline list is being enforced is exactly the dishonesty this reporting
	// exists to remove, and an absent credential is a deployment that is not
	// finished rather than a resolver that is down.
	Resolver enforcement.ResolverSettings
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

// LoadTunnel reads the tunnel environment for the Operator commands that mint
// and list Devices. It is loaded on its own rather than as part of Config so
// that those commands do not also demand the mail and challenge secrets only
// serving needs.
//
// It reports what is absent or unreadable and nothing more: what a value has
// to look like to render a working Device configuration is the enforcement
// module's own rule, checked when that module is built.
//
// TUNNEL_ENDPOINT_HOST is the published tunnel hostname. A hostname rather
// than an address is what lets the droplet be rebuilt without invalidating a
// Device already installed on a phone. TUNNEL_PUBLIC_KEY is the wg0
// interface's own public key; its private half stays on the host, inside the
// interface configuration only root reads, and never reaches this process.
func LoadTunnel() (enforcement.TunnelSettings, error) {
	tunnel := enforcement.TunnelSettings{
		EndpointHost: strings.TrimSpace(os.Getenv("TUNNEL_ENDPOINT_HOST")),
		EndpointPort: defaultTunnelPort,
		PublicKey:    strings.TrimSpace(os.Getenv("TUNNEL_PUBLIC_KEY")),
	}
	if port := strings.TrimSpace(os.Getenv("TUNNEL_ENDPOINT_PORT")); port != "" {
		parsed, err := strconv.Atoi(port)
		if err != nil {
			return enforcement.TunnelSettings{}, fmt.Errorf("TUNNEL_ENDPOINT_PORT must be a port number: %w", err)
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
		return enforcement.TunnelSettings{}, fmt.Errorf("required environment variables are missing: %s", strings.Join(missing, ", "))
	}
	return tunnel, nil
}

// LoadResolver reads the resolver environment for commands that interact with
// AdGuard Home.
func LoadResolver() (enforcement.ResolverSettings, error) {
	settings := enforcement.ResolverSettings{
		BaseURL:  strings.TrimSpace(os.Getenv("ADGUARD_URL")),
		Username: strings.TrimSpace(os.Getenv("ADGUARD_USERNAME")),
		Password: strings.TrimSpace(os.Getenv("ADGUARD_PASSWORD")),
	}
	if settings.BaseURL == "" {
		settings.BaseURL = defaultResolverURL
	}
	var missing []string
	if settings.Username == "" {
		missing = append(missing, "ADGUARD_USERNAME")
	}
	if settings.Password == "" {
		missing = append(missing, "ADGUARD_PASSWORD")
	}
	if len(missing) > 0 {
		return enforcement.ResolverSettings{}, fmt.Errorf("required environment variables are missing: %s", strings.Join(missing, ", "))
	}
	return settings, nil
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

	resolver, err := LoadResolver()
	if err != nil {
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
		Resolver:            resolver,
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
