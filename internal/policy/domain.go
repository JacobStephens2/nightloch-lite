package policy

import (
	"fmt"
	"strings"
)

// MatchMode controls how a target applies to a query name.
type MatchMode int

const (
	// ExactHost matches only the exact hostname.
	ExactHost MatchMode = iota
	// DomainAndSubdomains matches the domain and any subdomain (e.g. www.example.com).
	DomainAndSubdomains
)

// Target is a DNS name with an explicit match mode.
type Target struct {
	Domain string
	Mode   MatchMode
}

// NormalizeDomain lowercases and strips a trailing dot. It does not resolve IDNA.
func NormalizeDomain(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimSuffix(s, ".")
	return s
}

// ValidateTarget checks basic hostname shape for v1 (ASCII labels).
func ValidateTarget(t Target) error {
	d := NormalizeDomain(t.Domain)
	if d == "" || len(d) > 253 {
		return fmt.Errorf("invalid domain length")
	}
	if strings.Contains(d, "://") || strings.ContainsAny(d, " /\\?#@") {
		return fmt.Errorf("domain must not be a URL")
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return fmt.Errorf("domain needs at least two labels")
	}
	for _, lab := range labels {
		if lab == "" || len(lab) > 63 {
			return fmt.Errorf("invalid label")
		}
		for i := 0; i < len(lab); i++ {
			c := lab[i]
			ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-'
			if !ok {
				return fmt.Errorf("invalid character in label")
			}
		}
		if lab[0] == '-' || lab[len(lab)-1] == '-' {
			return fmt.Errorf("label cannot start or end with hyphen")
		}
	}
	if t.Mode != ExactHost && t.Mode != DomainAndSubdomains {
		return fmt.Errorf("invalid match mode")
	}
	return nil
}

// Matches reports whether query (already normalized) is covered by t.
func (t Target) Matches(query string) bool {
	q := NormalizeDomain(query)
	d := NormalizeDomain(t.Domain)
	if q == "" || d == "" {
		return false
	}
	switch t.Mode {
	case ExactHost:
		return q == d
	case DomainAndSubdomains:
		return q == d || strings.HasSuffix(q, "."+d)
	default:
		return false
	}
}

// Overlaps reports whether two targets can match a common name (conservative).
func (t Target) Overlaps(o Target) bool {
	a, b := NormalizeDomain(t.Domain), NormalizeDomain(o.Domain)
	if a == b {
		return true
	}
	// subdomain relationship under either mode that includes subdomains
	if t.Mode == DomainAndSubdomains && (b == a || strings.HasSuffix(b, "."+a)) {
		return true
	}
	if o.Mode == DomainAndSubdomains && (a == b || strings.HasSuffix(a, "."+b)) {
		return true
	}
	if t.Mode == ExactHost && o.Mode == DomainAndSubdomains && (a == b || strings.HasSuffix(a, "."+b)) {
		return true
	}
	if o.Mode == ExactHost && t.Mode == DomainAndSubdomains && (b == a || strings.HasSuffix(b, "."+a)) {
		return true
	}
	return false
}
