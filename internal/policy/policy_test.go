package policy

import (
	"testing"
	"time"
)

func mustWindow(t *testing.T, tz string, start, end int) LocalWindow {
	t.Helper()
	w, err := LoadWindow(tz, start, end)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestIsBlocked_CurfewWinsOverAllow(t *testing.T) {
	w := mustWindow(t, "America/New_York", 20*60, 8*60) // 20:00–08:00
	loc := mustLoc(t, "America/New_York")
	inside := time.Date(2026, 7, 28, 21, 0, 0, 0, loc)
	outside := time.Date(2026, 7, 28, 12, 0, 0, 0, loc)

	p := Policy{
		Curfews: []Curfew{{
			Targets: []Target{{Domain: "instagram.com", Mode: DomainAndSubdomains}},
			Window:  w,
			Enabled: true,
		}},
		Allowlist: []Target{{Domain: "instagram.com", Mode: DomainAndSubdomains}},
		Global:    MapGlobalList{"instagram.com": {}},
	}
	if !p.IsBlocked("www.instagram.com", inside) {
		t.Fatal("curfew must block in-window even with allowlist")
	}
	if p.IsBlocked("www.instagram.com", outside) {
		t.Fatal("outside curfew, allow should carve global list")
	}
}

func TestIsBlocked_AllowCarvesGlobalOnly(t *testing.T) {
	p := Policy{
		Global:    MapGlobalList{"cdn.example.com": {}},
		Allowlist: []Target{{Domain: "cdn.example.com", Mode: ExactHost}},
	}
	now := time.Date(2026, 7, 28, 15, 0, 0, 0, time.UTC)
	if p.IsBlocked("cdn.example.com", now) {
		t.Fatal("allowlist should carve global list")
	}
	p2 := Policy{Global: MapGlobalList{"cdn.example.com": {}}}
	if !p2.IsBlocked("cdn.example.com", now) {
		t.Fatal("global list should block without allow")
	}
}

func TestMapGlobalList_ParentWalk(t *testing.T) {
	m := MapGlobalList{"example.com": {}}
	if !m.Contains("a.b.example.com") {
		t.Fatal("expected parent walk hit")
	}
	if m.Contains("example.org") {
		t.Fatal("unexpected hit")
	}
}

func TestSubjectMayEdit_WorkedExamples(t *testing.T) {
	now := time.Date(2026, 7, 28, 15, 0, 0, 0, time.UTC)
	w := mustWindow(t, "America/New_York", 20*60, 8*60)
	base := Policy{
		Curfews: []Curfew{{
			Targets: []Target{{Domain: "instagram.com", Mode: DomainAndSubdomains}},
			Window:  w,
			Enabled: true,
		}},
		Global: MapGlobalList{"cdn.example.com": {}},
	}

	longer := base.Clone()
	longer.Curfews = []Curfew{{
		Targets: base.Curfews[0].Targets,
		Window:  mustWindow(t, "America/New_York", 19*60, 8*60),
		Enabled: true,
	}}
	if r := SubjectMayApplyEdit(base, longer, now, true); r != EditAllowed {
		t.Fatalf("lengthen: got %v", r)
	}

	shorter := base.Clone()
	shorter.Curfews = []Curfew{{
		Targets: base.Curfews[0].Targets,
		Window:  mustWindow(t, "America/New_York", 21*60, 8*60),
		Enabled: true,
	}}
	if r := SubjectMayApplyEdit(base, shorter, now, true); r != EditDeniedLoosen {
		t.Fatalf("shorten partnered: got %v", r)
	}
	if r := SubjectMayApplyEdit(base, shorter, now, false); r != EditAllowed {
		t.Fatalf("shorten unpartnered: got %v", r)
	}

	off := base.Clone()
	off.Curfews = []Curfew{{
		Targets: base.Curfews[0].Targets,
		Window:  w,
		Enabled: false,
	}}
	if r := SubjectMayApplyEdit(base, off, now, true); r != EditDeniedLoosen {
		t.Fatalf("disable curfew: got %v", r)
	}

	withAllow := base.Clone()
	withAllow.Allowlist = []Target{{Domain: "cdn.example.com", Mode: ExactHost}}
	if r := SubjectMayApplyEdit(base, withAllow, now, true); r != EditDeniedLoosen {
		t.Fatalf("allowlist OISD domain: got %v", r)
	}

	extra := base.Clone()
	extra.ExtraBlocks = []Target{{Domain: "bad.example", Mode: DomainAndSubdomains}}
	if r := SubjectMayApplyEdit(base, extra, now, true); r != EditAllowed {
		t.Fatalf("extra block: got %v", r)
	}

	narrow := base.Clone()
	narrow.Curfews = []Curfew{{
		Targets: []Target{{Domain: "instagram.com", Mode: ExactHost}},
		Window:  w,
		Enabled: true,
	}}
	if r := SubjectMayApplyEdit(base, narrow, now, true); r != EditDeniedLoosen {
		t.Fatalf("narrow match mode: got %v", r)
	}

	split := Policy{
		Curfews: []Curfew{
			{
				Targets: base.Curfews[0].Targets,
				Window:  mustWindow(t, "America/New_York", 20*60, 23*60),
				Enabled: true,
			},
			{
				Targets: base.Curfews[0].Targets,
				Window:  mustWindow(t, "America/New_York", 1*60, 8*60),
				Enabled: true,
			},
		},
		Global: base.Global,
	}
	if r := SubjectMayApplyEdit(base, split, now, true); r != EditDeniedLoosen {
		t.Fatalf("split with gap: got %v", r)
	}
}

// TestTimezoneChangeAcrossDST is the S1 regression: wall-clock same numbers in
// America/New_York vs fixed UTC-5 diverge after spring-forward; must be a loosen.
func TestTimezoneChangeAcrossDST(t *testing.T) {
	now := time.Date(2027, 3, 11, 15, 0, 0, 0, time.UTC)
	horizon := 7 * 24 * time.Hour
	// 20:00–21:00 local
	oldW := mustWindow(t, "America/New_York", 20*60, 21*60)
	// Etc/GMT+5 is fixed UTC−5 (POSIX sign inverted)
	newW := mustWindow(t, "Etc/GMT+5", 20*60, 21*60)

	oldP := Policy{
		Curfews: []Curfew{{
			Targets: []Target{{Domain: "instagram.com", Mode: DomainAndSubdomains}},
			Window:  oldW,
			Enabled: true,
		}},
	}
	newP := Policy{
		Curfews: []Curfew{{
			Targets: []Target{{Domain: "instagram.com", Mode: DomainAndSubdomains}},
			Window:  newW,
			Enabled: true,
		}},
	}

	if BruteForceAtLeastAsRestrictive(oldP, newP, now, horizon) {
		t.Fatal("oracle: expected loosening across DST")
	}
	if AtLeastAsRestrictive(oldP, newP, now, horizon) {
		t.Fatal("breakpoint predicate must reject timezone change across DST (S1)")
	}
	if r := SubjectMayApplyEdit(oldP, newP, now, true); r != EditDeniedLoosen {
		t.Fatalf("partnered subject must not apply TZ change: %v", r)
	}
}

func TestValidate_EmptyWindowRejected(t *testing.T) {
	_, err := LoadWindow("UTC", 0, 0)
	if err == nil {
		t.Fatal("expected error for 00:00–00:00")
	}
}

func TestValidate_NilLocationDeniesEdit(t *testing.T) {
	now := time.Date(2026, 7, 28, 15, 0, 0, 0, time.UTC)
	bad := Policy{
		Curfews: []Curfew{{
			Targets: []Target{{Domain: "example.com", Mode: ExactHost}},
			Window:  LocalWindow{Location: nil, StartMinute: 0, EndMinute: 60},
			Enabled: true,
		}},
	}
	good := Policy{
		ExtraBlocks: []Target{{Domain: "example.com", Mode: ExactHost}},
	}
	if r := SubjectMayApplyEdit(bad, good, now, true); r != EditDeniedInvalid {
		t.Fatalf("invalid old policy: got %v", r)
	}
	if r := SubjectMayApplyEdit(good, bad, now, false); r != EditDeniedInvalid {
		t.Fatalf("invalid new policy unpartnered: got %v", r)
	}
}

func TestValidateTarget_URL(t *testing.T) {
	err := ValidateTarget(Target{Domain: "https://evil.example/path", Mode: ExactHost})
	if err == nil {
		t.Fatal("expected URL rejection")
	}
}

func TestAllowOverlapsCurfew(t *testing.T) {
	w := mustWindow(t, "UTC", 0, 60)
	p := Policy{
		Curfews: []Curfew{{
			Targets: []Target{{Domain: "instagram.com", Mode: DomainAndSubdomains}},
			Window:  w,
			Enabled: true,
		}},
	}
	if !p.AllowOverlapsCurfew(Target{Domain: "cdn.instagram.com", Mode: ExactHost}) {
		t.Fatal("expected overlap")
	}
	if p.AllowOverlapsCurfew(Target{Domain: "example.com", Mode: ExactHost}) {
		t.Fatal("no overlap expected")
	}
}

func TestOvernightWindow(t *testing.T) {
	w := mustWindow(t, "America/New_York", 20*60, 8*60)
	loc := mustLoc(t, "America/New_York")
	cases := []struct {
		hour, min int
		want      bool
	}{
		{20, 0, true},
		{23, 30, true},
		{3, 0, true},
		{7, 59, true},
		{8, 0, false},
		{12, 0, false},
		{19, 59, false},
	}
	for _, tc := range cases {
		tm := time.Date(2026, 7, 28, tc.hour, tc.min, 0, 0, loc)
		if got := w.ActiveAt(tm); got != tc.want {
			t.Fatalf("%02d:%02d got %v want %v", tc.hour, tc.min, got, tc.want)
		}
	}
}

func TestBreakpoints_CalendarDayAcrossSpringForward(t *testing.T) {
	w := mustWindow(t, "America/New_York", 20*60, 8*60)
	// Span the 2027-03-14 US spring-forward
	from := time.Date(2027, 3, 13, 0, 0, 0, 0, time.UTC)
	to := time.Date(2027, 3, 16, 0, 0, 0, 0, time.UTC)
	bps := w.Breakpoints(from, to)
	// After transition, 20:00 EDT = 00:00 UTC next calendar day... actually 20:00 EDT = 00:00 UTC
	// 20:00 EST = 01:00 UTC. After DST, 20:00 EDT = 00:00 UTC.
	// Ensure we still have a breakpoint at local 20:00 on 2027-03-14 (EDT).
	loc := mustLoc(t, "America/New_York")
	want := time.Date(2027, 3, 14, 20, 0, 0, 0, loc).UTC().Truncate(time.Minute)
	found := false
	for _, bp := range bps {
		if bp.Equal(want) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing correct 20:00 EDT breakpoint %v in %v", want, bps)
	}
}
