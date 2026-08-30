package enforcement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Resolver is the enforcement plane's AdGuard Home dependency, injected
// exactly as the tunnel and the mail provider are, so every state it can be in
// is reachable in a test without a droplet.
//
// It reports state and changes nothing. The rules AdGuard Home enforces are a
// generated projection written by a later slice
// (docs/SECURITY-POLICY-DESIGN.md §4); widening this interface to write one is
// that slice's decision rather than something health quietly acquires.
type Resolver interface {
	// Status reports whether the resolver is running and filtering. An error
	// means the answer is unknown — it is never flattened into "not
	// filtering", because an unreachable resolver and a resolver that has
	// stopped enforcing call for different responses.
	Status(ctx context.Context) (ResolverStatus, error)
}

// ResolverStatus is what AdGuard Home says about itself. All three facts have
// to hold for a Device's DNS to be filtered: the service running, protection
// on, and the filtering engine enabled. Nightloch never turns filtering off to
// loosen a Policy — that is what per-client rules are for (AGENTS.md hard rule
// 3) — so any of them being false is a fault rather than a configuration.
type ResolverStatus struct {
	Running           bool
	ProtectionEnabled bool
	FilteringEnabled  bool
}

// Enforcing reports whether this state filters a Device's DNS, and names every
// setting that is off when it does not. It sits on the state rather than on
// the reader so the list of settings is stated once, next to the fields it
// reads: a fourth fact added above would otherwise be reportable and still
// silently absent from the answer.
func (s ResolverStatus) Enforcing() (bool, []string) {
	var off []string
	if !s.Running {
		off = append(off, "not running")
	}
	if !s.ProtectionEnabled {
		off = append(off, "protection off")
	}
	if !s.FilteringEnabled {
		off = append(off, "filtering off")
	}
	return len(off) == 0, off
}

// The two ways the resolver fails, kept distinct for the same reason the
// tunnel's three are: a resolver Nightloch cannot reach is an unknown, and a
// resolver that answers while enforcing nothing is a Protectee browsing
// unprotected with everything appearing to work.
var (
	// ErrResolverUnreachable means the resolver did not answer, or answered
	// something this adapter cannot read as a status.
	ErrResolverUnreachable = errors.New("AdGuard Home is unreachable")
	// ErrResolverNotFiltering means the resolver answered and is not
	// enforcing the Baseline list.
	ErrResolverNotFiltering = errors.New("AdGuard Home is not filtering")
)

// ResolverSettings is the deployment's AdGuard Home administration endpoint.
// Nightloch holds AdGuard Home's admin credentials rather than a scoped token,
// because AdGuard Home issues no scoped tokens (docs/SECURITY-POLICY-DESIGN.md
// §4), which is why they may not be sent in the clear.
type ResolverSettings struct {
	// BaseURL is the administration origin. In the deployment this is the
	// loopback address Caddy also proxies behind basic auth; the app talks to
	// it directly rather than through Caddy.
	BaseURL string
	// Username and Password are AdGuard Home's admin basic auth.
	Username string
	Password string
}

// ErrInvalidResolverSettings rejects an administration endpoint the app either
// could not reach or must not send admin credentials to.
var ErrInvalidResolverSettings = errors.New("invalid AdGuard Home settings")

// resolverTimeout bounds one status read. It is the adapter's own bound rather
// than only the caller's: a hung resolver must not hold a request open even if
// a future caller forgets to pass a deadline.
const resolverTimeout = 5 * time.Second

// AdGuardHome is the production Resolver: AdGuard Home's control API over HTTP
// basic auth.
type AdGuardHome struct {
	baseURL  *url.URL
	username string
	password string
	client   *http.Client
}

// NewAdGuardHome validates the administration endpoint once, so Status is
// total. Plain HTTP is accepted only to loopback: anywhere else it would put
// the enforcer's full admin credentials on the wire in the clear.
func NewAdGuardHome(settings ResolverSettings) (*AdGuardHome, error) {
	raw := strings.TrimSpace(settings.BaseURL)
	if raw == "" {
		return nil, fmt.Errorf("%w: no administration URL", ErrInvalidResolverSettings)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidResolverSettings, err)
	}
	if parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("%w: %q is not an absolute http or https URL", ErrInvalidResolverSettings, raw)
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return nil, fmt.Errorf(
			"%w: plain HTTP to %q would send AdGuard Home's admin credentials in the clear",
			ErrInvalidResolverSettings, parsed.Host)
	}
	if settings.Username == "" || settings.Password == "" {
		return nil, fmt.Errorf("%w: administration requires a username and password", ErrInvalidResolverSettings)
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return &AdGuardHome{
		baseURL:  parsed,
		username: settings.Username,
		password: settings.Password,
		client:   &http.Client{Timeout: resolverTimeout},
	}, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

// Status reads the two control endpoints that together say whether a Device's
// DNS is being filtered. Both are required: protection can be on while the
// filtering engine is disabled, and either alone would report enforcement that
// is not happening.
func (a *AdGuardHome) Status(ctx context.Context) (ResolverStatus, error) {
	var service struct {
		Running    bool `json:"running"`
		Protection bool `json:"protection_enabled"`
	}
	if err := a.get(ctx, "/control/status", &service); err != nil {
		return ResolverStatus{}, err
	}
	var filtering struct {
		Enabled bool `json:"enabled"`
	}
	if err := a.get(ctx, "/control/filtering/status", &filtering); err != nil {
		return ResolverStatus{}, err
	}
	return ResolverStatus{
		Running:           service.Running,
		ProtectionEnabled: service.Protection,
		FilteringEnabled:  filtering.Enabled,
	}, nil
}

// maxResolverBody bounds what is read from the control API. A status document
// is a few hundred bytes; anything larger is not one, and reading it would let
// a misdirected endpoint hold the health check open with a body.
const maxResolverBody = 1 << 20

// get reads one control endpoint. Every failure is ErrResolverUnreachable: a
// refused connection, a rejected credential, and an answer that is not the
// control API all mean the same thing here — nothing was learned about
// filtering — and none of them may be reported as a resolver that is up.
func (a *AdGuardHome) get(ctx context.Context, path string, into any) error {
	endpoint := a.baseURL.JoinPath(path).String()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrResolverUnreachable, err)
	}
	request.SetBasicAuth(a.username, a.password)
	response, err := a.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrResolverUnreachable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s answered %s", ErrResolverUnreachable, path, response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResolverBody))
	if err != nil {
		return fmt.Errorf("%w: read %s: %w", ErrResolverUnreachable, path, err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("%w: %s did not answer the control API: %w", ErrResolverUnreachable, path, err)
	}
	return nil
}
