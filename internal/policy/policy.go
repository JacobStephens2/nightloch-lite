package policy

import (
	"fmt"
	"time"
)

// DefaultHorizon is the at-least-as-restrictive comparison window (14 days).
const DefaultHorizon = 14 * 24 * time.Hour

// GlobalList answers whether a domain is on a subscribed block list (e.g. OISD).
// Evaluated only for domains in the predicate comparison set (how many lookups),
// but each Contains implementation MUST be sublinear in list size — typically a
// parent-label walk against a map (O(labels)), not a full scan of the list (S4).
type GlobalList interface {
	Contains(domain string) bool
}

// EmptyGlobalList never blocks via lists.
type EmptyGlobalList struct{}

func (EmptyGlobalList) Contains(string) bool { return false }

// MapGlobalList is a simple set for tests and small fixtures.
// Lookup walks parent domains so subdomain matches are O(labels), not O(|map|).
type MapGlobalList map[string]struct{}

func (m MapGlobalList) Contains(domain string) bool {
	domain = NormalizeDomain(domain)
	if domain == "" {
		return false
	}
	// Parent-domain walk: a.b.example.com → a.b.example.com, b.example.com, example.com, com
	for {
		if _, ok := m[domain]; ok {
			return true
		}
		i := indexByte(domain, '.')
		if i < 0 {
			return false
		}
		domain = domain[i+1:]
	}
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// Curfew is a scheduled set of targets.
type Curfew struct {
	Targets []Target
	Window  LocalWindow
	Enabled bool
}

// Policy is the subject-editable desired enforcement state (plus global list view).
// Periodicity: only daily LocalWindow schedules and always-on extras/allows — no
// deferred dates (unrepresentable in this type; R3).
type Policy struct {
	Curfews     []Curfew
	ExtraBlocks []Target // always-on subject blocks ($important in AGH)
	Allowlist   []Target // carve-outs for global lists only (not curfews)
	Global      GlobalList
}

func (p Policy) global() GlobalList {
	if p.Global == nil {
		return EmptyGlobalList{}
	}
	return p.Global
}

// Clone returns a deep copy so test mutations cannot share slice backings.
func (p Policy) Clone() Policy {
	out := Policy{Global: p.Global}
	if p.Curfews != nil {
		out.Curfews = make([]Curfew, len(p.Curfews))
		for i, c := range p.Curfews {
			out.Curfews[i] = Curfew{
				Window:  c.Window,
				Enabled: c.Enabled,
				Targets: append([]Target(nil), c.Targets...),
			}
		}
	}
	out.ExtraBlocks = append([]Target(nil), p.ExtraBlocks...)
	out.Allowlist = append([]Target(nil), p.Allowlist...)
	return out
}

// matchesAny reports whether domain matches any target in list.
func matchesAny(domain string, list []Target) bool {
	domain = NormalizeDomain(domain)
	for _, t := range list {
		if t.Matches(domain) {
			return true
		}
	}
	return false
}

// curfewOrExtraActive is true if an enabled curfew covers domain at t, or extra block matches.
func (p Policy) curfewOrExtraActive(domain string, t time.Time) bool {
	domain = NormalizeDomain(domain)
	if matchesAny(domain, p.ExtraBlocks) {
		return true
	}
	for _, c := range p.Curfews {
		if !c.Enabled {
			continue
		}
		if !c.Window.ActiveAt(t) {
			continue
		}
		if matchesAny(domain, c.Targets) {
			return true
		}
	}
	return false
}

// IsBlocked implements P(domain, t) per SECURITY-POLICY-DESIGN:
//
//	blocked ⇔ curfew_or_extra_block_active ∨ (global_list_hit ∧ ¬allowlist)
func (p Policy) IsBlocked(domain string, t time.Time) bool {
	domain = NormalizeDomain(domain)
	if domain == "" {
		return false
	}
	if p.curfewOrExtraActive(domain, t) {
		return true
	}
	if p.global().Contains(domain) && !matchesAny(domain, p.Allowlist) {
		return true
	}
	return false
}

// Domains returns the finite comparison set for the predicate (not the full OISD dump).
// For DomainAndSubdomains targets, a synthetic probe subdomain is included so that
// narrowing match mode (subdomains → exact host) is visible to AtLeastAsRestrictive.
func (p Policy) Domains() []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(d string) {
		d = NormalizeDomain(d)
		if d == "" {
			return
		}
		if _, ok := seen[d]; ok {
			return
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	addTarget := func(t Target) {
		add(t.Domain)
		if t.Mode == DomainAndSubdomains {
			add("nightloch-probe." + NormalizeDomain(t.Domain))
		}
	}
	for _, c := range p.Curfews {
		for _, t := range c.Targets {
			addTarget(t)
		}
	}
	for _, t := range p.ExtraBlocks {
		addTarget(t)
	}
	for _, t := range p.Allowlist {
		addTarget(t)
	}
	return out
}

// Validate checks structural validity of a policy used for authorization.
// R3 periodicity is type-enforced (no date fields on LocalWindow).
func (p Policy) Validate() error {
	for i, c := range p.Curfews {
		if !c.Enabled {
			continue
		}
		if len(c.Targets) == 0 {
			return fmt.Errorf("curfew %d: enabled but has no targets", i)
		}
		if c.Window.Location == nil {
			return fmt.Errorf("curfew %d: enabled curfew missing timezone location", i)
		}
		if c.Window.StartMinute == c.Window.EndMinute {
			return fmt.Errorf("curfew %d: empty window", i)
		}
		for j, t := range c.Targets {
			if err := ValidateTarget(t); err != nil {
				return fmt.Errorf("curfew %d target %d: %w", i, j, err)
			}
		}
	}
	for i, t := range p.ExtraBlocks {
		if err := ValidateTarget(t); err != nil {
			return fmt.Errorf("extra block %d: %w", i, err)
		}
	}
	for i, t := range p.Allowlist {
		if err := ValidateTarget(t); err != nil {
			return fmt.Errorf("allowlist %d: %w", i, err)
		}
	}
	return nil
}

// AllowOverlapsCurfew reports whether an allow target overlaps any curfew target (approval UX flag).
func (p Policy) AllowOverlapsCurfew(allow Target) bool {
	for _, c := range p.Curfews {
		if !c.Enabled {
			continue
		}
		for _, t := range c.Targets {
			if allow.Overlaps(t) {
				return true
			}
		}
	}
	return false
}
