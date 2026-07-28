package policy

import (
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"
)

var propZones = []string{
	"UTC",
	"America/New_York",
	"America/Los_Angeles",
	"Australia/Lord_Howe", // 30-minute DST shift
}

// TestBreakpointPredicateMatchesBruteForce verifies the finite breakpoint check
// agrees with per-minute brute force on random policy pairs (S2: multi-TZ, long horizon).
//
// Consistency check (breakpoint ≡ brute force). Security property also covered by
// unit tests: disguised loosen must be EditDeniedLoosen when partnered.
func TestBreakpointPredicateMatchesBruteForce(t *testing.T) {
	seed := time.Now().UnixNano()
	if s := os.Getenv("POLICY_PROP_SEED"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			t.Fatalf("POLICY_PROP_SEED: %v", err)
		}
		seed = n
	}
	t.Logf("POLICY_PROP_SEED=%d", seed)
	rng := rand.New(rand.NewSource(seed))

	// Dates a few days before US spring-forward / fall-back so horizon spans DST.
	nowChoices := []time.Time{
		time.Date(2026, 7, 28, 15, 0, 0, 0, time.UTC),
		time.Date(2027, 3, 11, 15, 0, 0, 0, time.UTC),  // before 2027-03-14 spring
		time.Date(2027, 10, 28, 15, 0, 0, 0, time.UTC), // before 2027-11-07 fall-back
		time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC),
	}
	horizon := 7 * 24 * time.Hour

	for i := 0; i < 200; i++ {
		now := nowChoices[rng.Intn(len(nowChoices))]
		oldP := randomPolicy(rng)
		newP := randomPolicy(rng)
		gl := randomGlobal(rng)
		oldP.Global, newP.Global = gl, gl

		// Skip invalid (should not happen with generator)
		if oldP.Validate() != nil || newP.Validate() != nil {
			continue
		}

		fast := AtLeastAsRestrictive(oldP, newP, now, horizon)
		slow := BruteForceAtLeastAsRestrictive(oldP, newP, now, horizon)
		if fast != slow {
			t.Fatalf("iter %d seed=%d now=%v: breakpoint=%v brute=%v\nold=%+v\nnew=%+v",
				i, seed, now, fast, slow, oldP, newP)
		}
	}
}

func TestRandomLoosenDetected(t *testing.T) {
	now := time.Date(2026, 7, 28, 15, 0, 0, 0, time.UTC)
	w, err := LoadWindow("UTC", 0, 12*60)
	if err != nil {
		t.Fatal(err)
	}
	base := Policy{
		Curfews: []Curfew{{
			Targets: []Target{{Domain: "curfew.only.test", Mode: DomainAndSubdomains}},
			Window:  w,
			Enabled: true,
		}},
		Global: MapGlobalList{"other.test": {}},
	}

	loose := base.Clone()
	loose.Curfews = []Curfew{{
		Targets: base.Curfews[0].Targets,
		Window:  w,
		Enabled: false,
	}}
	if AtLeastAsRestrictive(base, loose, now, DefaultHorizon) {
		t.Fatal("disabled curfew should not be at-least-as-restrictive")
	}
	if r := SubjectMayApplyEdit(base, loose, now, true); r != EditDeniedLoosen {
		t.Fatalf("partnered subject cannot disable curfew: %v", r)
	}

	withAllow := base.Clone()
	withAllow.Allowlist = []Target{{Domain: "other.test", Mode: ExactHost}}
	if AtLeastAsRestrictive(base, withAllow, now, DefaultHorizon) {
		t.Fatal("allowlist of global entry is loosen")
	}
}

func randomGlobal(rng *rand.Rand) MapGlobalList {
	m := MapGlobalList{}
	n := rng.Intn(4)
	for i := 0; i < n; i++ {
		m[randomDomain(rng)] = struct{}{}
	}
	return m
}

func randomPolicy(rng *rand.Rand) Policy {
	p := Policy{}
	tz := propZones[rng.Intn(len(propZones))]
	for i := 0; i < rng.Intn(3); i++ {
		// Minute-aligned edges, not only hour-aligned (S2).
		start := rng.Intn(24*60 - 1)
		end := rng.Intn(24*60 - 1)
		if start == end {
			end = (start + 1 + rng.Intn(180)) % (24 * 60)
			if end == start {
				end = (start + 30) % (24 * 60)
			}
		}
		w, err := LoadWindow(tz, start, end)
		if err != nil {
			continue
		}
		var targets []Target
		for j := 0; j < 1+rng.Intn(2); j++ {
			mode := ExactHost
			if rng.Intn(2) == 0 {
				mode = DomainAndSubdomains
			}
			targets = append(targets, Target{Domain: randomDomain(rng), Mode: mode})
		}
		p.Curfews = append(p.Curfews, Curfew{
			Targets: targets,
			Window:  w,
			Enabled: rng.Intn(4) != 0,
		})
	}
	for i := 0; i < rng.Intn(3); i++ {
		p.ExtraBlocks = append(p.ExtraBlocks, Target{Domain: randomDomain(rng), Mode: DomainAndSubdomains})
	}
	for i := 0; i < rng.Intn(3); i++ {
		p.Allowlist = append(p.Allowlist, Target{Domain: randomDomain(rng), Mode: ExactHost})
	}
	return p
}

func randomDomain(rng *rand.Rand) string {
	labels := []string{"alpha", "beta", "gamma", "delta", "cdn", "www", "api", "img", "test", "example"}
	a := labels[rng.Intn(len(labels))]
	b := labels[rng.Intn(len(labels))]
	return a + "." + b + ".test"
}
