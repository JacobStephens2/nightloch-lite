package policy

import (
	"fmt"
	"time"
)

// EditResult is the outcome of SubjectMayApplyEdit.
type EditResult int

const (
	// EditAllowed — subject may apply the edit (tighten, or unpartnered any edit).
	EditAllowed EditResult = iota
	// EditDeniedLoosen — partnered subject would loosen; needs partner.
	EditDeniedLoosen
	// EditDeniedInvalid — old or new policy failed Validate (deny closed).
	EditDeniedInvalid
)

func (r EditResult) Allowed() bool { return r == EditAllowed }

func (r EditResult) String() string {
	switch r {
	case EditAllowed:
		return "allowed"
	case EditDeniedLoosen:
		return "denied_loosen"
	case EditDeniedInvalid:
		return "denied_invalid"
	default:
		return fmt.Sprintf("EditResult(%d)", r)
	}
}

// AtLeastAsRestrictive reports whether newP is at least as restrictive as oldP
// over [now, now+horizon] for all domains in the union of both domain sets.
//
// Invalid policies return false (deny-closed). Prefer SubjectMayApplyEdit for
// authZ so callers can distinguish invalid from loosen.
func AtLeastAsRestrictive(oldP, newP Policy, now time.Time, horizon time.Duration) bool {
	ok, _ := atLeastAsRestrictive(oldP, newP, now, horizon)
	return ok
}

func atLeastAsRestrictive(oldP, newP Policy, now time.Time, horizon time.Duration) (bool, error) {
	if err := oldP.Validate(); err != nil {
		return false, fmt.Errorf("old policy: %w", err)
	}
	if err := newP.Validate(); err != nil {
		return false, fmt.Errorf("new policy: %w", err)
	}
	if horizon <= 0 {
		horizon = DefaultHorizon
	}
	now = now.UTC().Truncate(time.Minute)
	end := now.Add(horizon)

	domains := unionDomains(oldP, newP)
	points := unionBreakpoints(oldP, newP, now, end)

	for _, d := range domains {
		for _, t := range points {
			if t.Before(now) || t.After(end) {
				continue
			}
			oldB := oldP.IsBlocked(d, t)
			newB := newP.IsBlocked(d, t)
			// new must be ≥ old: blocked ⊒ allowed. Cannot go blocked→allowed.
			if oldB && !newB {
				return false, nil
			}
		}
	}
	return true, nil
}

// SubjectMayApplyEdit is the authZ entry point for subject-initiated policy edits.
//
// When partnered is false (zero active partners), any structurally valid edit is allowed.
// When partnered, the edit must be at least as restrictive.
// Malformed policies are always denied (EditDeniedInvalid) — never silent allow (S3).
func SubjectMayApplyEdit(oldP, newP Policy, now time.Time, partnered bool) EditResult {
	if err := oldP.Validate(); err != nil {
		return EditDeniedInvalid
	}
	if err := newP.Validate(); err != nil {
		return EditDeniedInvalid
	}
	if !partnered {
		return EditAllowed
	}
	ok, err := atLeastAsRestrictive(oldP, newP, now, DefaultHorizon)
	if err != nil {
		return EditDeniedInvalid
	}
	if ok {
		return EditAllowed
	}
	return EditDeniedLoosen
}

func unionDomains(a, b Policy) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, p := range []Policy{a, b} {
		for _, d := range p.Domains() {
			if _, ok := seen[d]; ok {
				continue
			}
			seen[d] = struct{}{}
			out = append(out, d)
		}
	}
	return out
}

func unionBreakpoints(a, b Policy, from, to time.Time) []time.Time {
	seen := map[int64]struct{}{}
	var out []time.Time
	add := func(t time.Time) {
		t = t.UTC().Truncate(time.Minute)
		if t.Before(from) || t.After(to) {
			return
		}
		k := t.Unix()
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		out = append(out, t)
	}
	add(from)
	add(to)
	for _, p := range []Policy{a, b} {
		for _, c := range p.Curfews {
			if !c.Enabled {
				continue
			}
			for _, bp := range c.Window.Breakpoints(from, to) {
				add(bp)
			}
		}
	}
	return out
}

// BruteForceAtLeastAsRestrictive samples every minute in [now, now+horizon]
// for every domain — used to validate the breakpoint predicate in tests.
// Invalid policies return false.
func BruteForceAtLeastAsRestrictive(oldP, newP Policy, now time.Time, horizon time.Duration) bool {
	if oldP.Validate() != nil || newP.Validate() != nil {
		return false
	}
	if horizon <= 0 {
		horizon = DefaultHorizon
	}
	now = now.UTC().Truncate(time.Minute)
	end := now.Add(horizon)
	domains := unionDomains(oldP, newP)
	for t := now; !t.After(end); t = t.Add(time.Minute) {
		for _, d := range domains {
			if oldP.IsBlocked(d, t) && !newP.IsBlocked(d, t) {
				return false
			}
		}
	}
	return true
}
