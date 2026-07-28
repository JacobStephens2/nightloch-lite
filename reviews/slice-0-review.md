# Review — Slice 0 (Repo skeleton + policy package)

**Reviewing:** unstaged working-tree changes as of 2026-07-28  
**Base:** `53256a4` *Complete slice -1 - security and policy design*  
**Scope:** first application code — Go module, `internal/policy`, scaffolding  
**Resolution:** **S1–S9 addressed** in working tree (DST breakpoints, prop tests, Validate deny-closed, deploy paths, CI, Makefile).

| Path | Lines | What |
|---|---|---|
| `internal/policy/policy.go` | 171 | `Policy`, `IsBlocked`, `Domains`, `GlobalList` |
| `internal/policy/predicate.go` | 113 | `AtLeastAsRestrictive`, `SubjectMayApplyEdit`, brute-force oracle |
| `internal/policy/schedule.go` | 87 | `LocalWindow`, `ActiveAt`, `Breakpoints` |
| `internal/policy/domain.go` | 102 | `Target`, matching, normalization, validation |
| `internal/policy/policy_test.go` | 195 | worked examples from design §2 |
| `internal/policy/predicate_prop_test.go` | 121 | property test vs brute force |
| `cmd/`, `deploy/`, `infra/`, `.github/`, `Makefile`, `go.mod` | ~80 | scaffolding |
| `plan.md`, `README.md`, `status.md (was current-decision.md)`, `.gitignore` | — | Slice 0 marked complete |

**Verification performed:** `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l .` — all clean. Findings [S1](#s1), [S3](#s3), and [S4](#s4) were confirmed by running throwaway probe tests against the package, which were then deleted; the tree is unmodified by this review.

---

## Verdict

**One blocking defect. Everything else is small.**

The package is well-built: the predicate is a faithful implementation of design §2, R1 and R2 from the Slice −1 review are correctly applied, the worked examples became real tests, and the brute-force oracle is exactly the right way to validate a breakpoint optimization. `gofmt`, `go vet`, and the full suite pass.

But the predicate is **unsound across a DST transition**, and I have a reproducing case: it returns "edit allowed" for an edit that the brute-force oracle correctly rejects as a loosening. The root cause is one line in `schedule.go` that advances days with `Add(24 * time.Hour)` on a local timestamp. The property test does not catch it because it only generates windows in `UTC` and only looks 48 hours ahead — so the one class of bug the oracle exists to find is the one class it never exercises.

That is worth fixing before this is committed, because this package is the authorization gate for every subject-initiated policy edit in the product, and a wrong `true` here is a silent bypass.

[S1](#s1) is the defect. [S2](#s2) is why the tests missed it — and is arguably the more important fix, since it is what prevents the next one.

---

## What this slice gets right

- **R1 and R2 landed correctly and are tested.** `IsBlocked` implements `curfew_or_extra ∨ (global ∧ ¬allow)` — curfews beat allowlists, allowlists carve only the global list. `TestIsBlocked_CurfewWinsOverAllow` checks both directions at one in-window and one out-of-window instant. That was the highest-risk correction from the last review and it was implemented exactly as specified.
- **The lazy `GlobalList` interface is the right shape.** It resolves R1 without dragging 150k domains into the predicate, and `Domains()` correctly bounds the comparison set.
- **The synthetic probe subdomain is a genuinely clever solution** to a real problem. Detecting a narrowing from `DomainAndSubdomains` to `ExactHost` requires a witness that exists in one policy's blocked set and not the other's; injecting `nightloch-probe.<domain>` into the comparison set produces that witness for free. `TestSubjectMayEdit_WorkedExamples` proves it works.
- **The brute-force oracle is the correct testing strategy.** Having `BruteForceAtLeastAsRestrictive` as an executable specification means the fast path can be verified rather than argued about — which is precisely how I was able to demonstrate [S1](#s1).
- **Eight of the design's thirteen worked examples are now executable tests**, including the subtle ones (narrowing, window splitting with a gap, allowlisting a global-listed domain, unpartnered self-loosen).
- **`SubjectMayApplyEdit` puts the partnered check in one place** rather than scattering it through future handlers. That is the right seam.
- **Scaffolding is appropriately thin.** Placeholders are honest — `nightloch-wg.example.sh` exits 1 with "not installed", the Caddyfile is commented out, `infra/opentofu/` is an empty `.gitkeep`. Nothing pretends to work.

---

## Findings

### S1

**The predicate is unsound across DST transitions — reproducing case included.** — **Blocker** · `internal/policy/schedule.go:82`

```go
for d := day; !d.After(lastDay); d = d.Add(24 * time.Hour) {
	add(d.Add(time.Duration(w.StartMinute) * time.Minute))
	add(d.Add(time.Duration(w.EndMinute) * time.Minute))
}
```

`d` is a wall-clock local midnight, but `Add(24 * time.Hour)` adds 24 *absolute* hours. On a spring-forward day the local day is only 23 hours long, so after the transition `d` is no longer midnight — it is 01:00 — and every breakpoint derived from it is shifted by an hour. The shift is permanent for the rest of the horizon.

Observed for a `20:00–08:00 America/New_York` window spanning the 2027-03-14 transition:

```
2027-03-14T01:00:00Z  local=Sat 2027-03-13 20:00 EST     correct
2027-03-13T13:00:00Z  local=Sat 2027-03-13 08:00 EST     correct
2027-03-15T01:00:00Z  local=Sun 2027-03-14 21:00 EDT  <- NOT a window edge
2027-03-14T13:00:00Z  local=Sun 2027-03-14 09:00 EDT  <- NOT a window edge
2027-03-16T01:00:00Z  local=Mon 2027-03-15 21:00 EDT  <- NOT a window edge
...every subsequent day also off by one hour
```

Because the predicate only samples at breakpoints, and the breakpoints no longer coincide with the real toggles, a policy difference confined to the mis-sampled hour is invisible.

**Reproducing case** — this is the "change the timezone" edit that design §2 lists as an example the predicate must reject:

```go
// Old: 20:00–21:00 America/New_York (observes DST)
// New: 20:00–21:00 Etc/GMT+5      (fixed UTC−5, no DST)
// Identical before 2027-03-14; diverge by one hour after it.
now := time.Date(2027, 3, 11, 15, 0, 0, 0, time.UTC)
AtLeastAsRestrictive(oldP, newP, now, 7*24*time.Hour)            // => true   (edit allowed)
BruteForceAtLeastAsRestrictive(oldP, newP, now, 7*24*time.Hour)  // => false  (it is a loosening)
```

The real loosening begins at `2027-03-15T00:00:00Z` (Sun 20:00 EDT). The nearest sampled breakpoints are `01:00Z` and `02:00Z` — both drifted past it. `SubjectMayApplyEdit(..., partnered=true)` returns `true`, so a partnered subject may apply this edit without a partner.

**Scope, stated honestly.** I also ran the shipped property generator against `America/New_York` with the horizon spanning the transition — 400 random pairs, zero disagreements. When both policies share a timezone they drift identically, and the coarse hour-aligned windows the generator produces stay detectable at other sample points. So this is not "the predicate is broadly broken." It is a real, demonstrated soundness hole that needs a specific shape to trigger: **a timezone-changing edit, with a DST transition inside the 14-day horizon.** That is roughly 28 days a year for any given user — and changing the timezone is one of the enumerated attacks the predicate was built to stop.

**Fix** — advance by calendar day, not by duration:

```go
for d := day; !d.After(lastDay); d = d.AddDate(0, 0, 1) {
```

`AddDate` normalizes through `time.Date`, so it lands on the next local midnight regardless of DST. Two related details worth handling in the same change:

- **Non-existent local times.** On spring-forward, 02:00–03:00 does not exist. `time.Date` normalizes forward (02:30 → 03:30), which is reasonable, but a window starting in that gap deserves a test asserting the chosen behavior rather than inheriting it.
- **Ambiguous local times.** On fall-back, 01:00–02:00 occurs twice. Go picks the first. A window edge at 01:30 therefore toggles at the first occurrence and the second is not a breakpoint. Add both, or document that the earlier instant wins.

After fixing, re-run the timezone-change case above — it should return `false`.

---

### S2

**The property test cannot find the bug class it exists to find.** — **High** · `internal/policy/predicate_prop_test.go`

Three independent limits, each of which alone would have hidden [S1](#s1):

| Limit | Line | Effect |
|---|---|---|
| `LoadWindow("UTC", ...)` for every generated window | `:89` | No DST zone is ever exercised |
| `horizon := 48 * time.Hour` | `:15` | Too short to span a transition even in a DST zone |
| `rand.NewSource(42)`, fixed | `:12` | The same 200 cases every run, forever |

The comment at `:14` — *"Shorter horizon keeps brute force fast; logic is the same"* — is the load-bearing assumption, and it is false in exactly the case that matters. The logic is the same only in a zone where local days are always 24 hours long.

There is a fourth, smaller gap: `randomPolicy` generates only hour-aligned windows (`rng.Intn(24) * 60`), so no minute-level boundary is ever tested.

**Fix:**

1. **Generate windows across several timezones**, including at least one DST-observing zone: `[]string{"UTC", "America/New_York", "America/Los_Angeles", "Australia/Lord_Howe"}`. (Lord Howe has a 30-minute DST shift — a good adversarial case.)
2. **Choose `now` from a set that includes dates a few days before both US transitions**, and use a horizon long enough to span them. Seven days suffices; the full 14 is better, and brute force at minute granularity over 14 days × ~10 domains is still well under a second.
3. **Allow minute-aligned window edges**, not just hour-aligned.
4. **Do not hard-code the seed.** Seed from `time.Now()` (or an env var) and `t.Logf` the seed on entry so a failure is reproducible. A fixed seed means the test explores exactly one sample of the input space for the life of the project.
5. **Consider native fuzzing.** Converting this to a `testing.F` fuzz target with the brute-force oracle as the invariant is a natural fit and would explore the space continuously rather than 200 times per run.

Worth noting: the plan diff **deleted** the line *"Property tests fail if a disguised loosen is classified as tighten"* from Slice 0's deliverables, leaving only "breakpoint ≡ brute force." Those are different properties. Breakpoint-vs-brute-force is a *consistency* check — it verifies the optimization matches the reference, but if both share a wrong model of `P` they are wrong together. The security property lives in the worked-example unit tests. Both matter; restoring that line to the plan would keep the distinction visible.

---

### S3

**Validation exists, is never called, and every failure mode is silent fail-open.** — **High** · `policy.go:149`, `domain.go:32`, `schedule.go:41`

`ValidatePeriodicity` and `ValidateTarget` have **zero callers and zero tests** — confirmed by grep across the package. They are the only validation in a package whose entire job is authorization.

Meanwhile every malformed input degrades to *not blocked*:

```
(a) LoadWindow("UTC", 0, 0) err=<nil>
    00:00-00:00 curfew blocks instagram.com?  false   <- user asked for 24h coverage
(b) nil-Location curfew blocks?               false   <- silently no protection
    ValidatePeriodicity catches it? err=enabled curfew missing timezone location
                                                      <- but nothing calls it
(c) ValidateTarget("https://evil.example/path") err=domain must not be a URL
    Policy accepts it anyway                          <- constructed without validation
```

Three distinct problems:

**(a) `00:00–00:00` silently means "never," not "always."** `schedule.go:11` documents this, so it is intentional — but it is the most natural way a user expresses a 24-hour curfew, and the result is zero protection with no error. In a product whose premise is that restrictions hold, a plausible user input that silently yields no restriction is the wrong default. Either reject `StartMinute == EndMinute` in `LoadWindow`, or add an explicit `AlwaysOn` representation. Rejecting is simpler and forces the caller to choose.

**(b) A nil `Location` disables the curfew silently.** `ActiveAt` returns `false` when `w.Location == nil`. Today every `Policy` is built in a test via `LoadWindow`, which errors on a bad zone — so this is latent. It becomes live in Slice 6, when policies are hydrated from SQLite rows: a bad or empty `tz` column, or a swallowed error, produces a curfew that exists in the database, renders in the UI, and enforces nothing.

**(c) `ValidateTarget` guards nothing.** A `Target` holding a URL, an empty string, or a 300-character label is accepted by `Policy` and simply never matches.

**Fix — make the security-critical entry points refuse to operate on invalid input:**

1. Call `ValidatePeriodicity` (and per-target `ValidateTarget`) at the top of `AtLeastAsRestrictive` / `SubjectMayApplyEdit`, and **return `false` — deny the edit — if either policy is invalid.** Denying on malformed input is the correct default; a subject should not get an approved edit because their policy failed to parse.
2. Consider returning `(bool, error)` from `SubjectMayApplyEdit` so callers can distinguish "denied because loosening" from "denied because malformed." The first is a normal UI message; the second is an alert-worthy bug.
3. Rename `ValidatePeriodicity` — it checks for a missing `Location`, not periodicity. `Validate` is fine. (The actual R3 periodicity constraint is enforced by the type: `LocalWindow` has no date fields, so deferred dates are unrepresentable. That is the strongest possible enforcement and worth a comment saying so.)
4. Add tests for both validators, including the `00:00–00:00` case once its behavior is decided.

---

### S4

**`MapGlobalList.Contains` is O(n) per lookup and will not survive contact with OISD.** — Medium · `policy.go:24-37`

```go
for d := range m {
	t := Target{Domain: d, Mode: DomainAndSubdomains}
	if t.Matches(domain) { return true }
}
```

Every miss scans the entire map. The type is documented as *"a simple set for tests and small fixtures,"* which is fair — but `GlobalList`'s doc comment at `:10-11` says *"Evaluated lazily only for domains in the comparison set,"* which reads as a performance guarantee it does not provide. The laziness bounds *how many* domains are tested, not the cost of each test.

Concretely, with OISD at ~150k entries, a 14-day horizon with two curfews (~57 breakpoints × ~10 domains ≈ 570 lookups) would run on the order of 85 million `Target.Matches` calls per predicate evaluation — seconds of CPU on every policy edit.

Not a defect now. Worth recording before Slice 6 writes the real implementation:

**The correct shape is a parent-domain walk, not a scan.** For query `a.b.example.com`, test `a.b.example.com`, `b.example.com`, `example.com`, `com` against a plain `map[string]struct{}` — O(labels), typically ≤ 5 lookups, independent of list size. Add a note to `GlobalList`'s doc comment stating that implementations must be sublinear in list size, so the constraint is visible to whoever writes the OISD-backed version.

---

### S5

**The systemd unit puts the binary — and by default the SQLite database — under a web root.** — Medium · `deploy/nightloch.service.example:8,11`

```ini
WorkingDirectory=/var/www/nightloch.app
ExecStart=/var/www/nightloch.app/bin/nightloch
```

`deploy/Caddyfile.example` sets `root * /var/www/nightloch.app/public`, so today the binary sits one level *above* the served directory and is not exposed. But the working directory is where SQLite will land by default in Slice 2, and both `shared-understanding.md` and the adversarial review require that database and backup paths not be web-reachable. Placing them inside `/var/www` makes that property depend on the Caddyfile never gaining a `root * /var/www/nightloch.app` line — one plausible edit away from serving `nightloch.db` over HTTPS.

**Fix:** move to `/opt/nightloch` (binary, working directory, database) and reserve `/var/www/nightloch.app/public` for static assets only. Free now, awkward after Slice 2 ships with data on disk.

While in this file, two cheap additions for a service that will hold session tokens and sudoers rights to `wg`:

```ini
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/opt/nightloch
```

Note `NoNewPrivileges=true` interacts with the Slice 4/5 sudoers helper — it will block `sudo`. Decide the mechanism now (a privileged helper socket avoids the conflict entirely) rather than discovering it mid-Slice-5.

---

### S6

**CI is thinner than it looks, and the smoke test cannot fail.** — Medium · `.github/workflows/ci.yml:22`

```yaml
./bin/nightloch version || true
```

The `|| true` means the only execution check in the pipeline is unconditionally green. If the binary segfaults on startup, CI passes.

Also missing:

- **`go vet ./...`** — free, catches real bugs, and already passes locally.
- **A `gofmt` gate** — the tree is clean today; a check keeps it that way.
- **`-race`** — unnecessary for a pure package, but Slice 6's single-writer reconciler is exactly the code where it earns its keep. Adding it now means it is already there.
- **`-ldflags "-X main.version=${{ github.sha }}"`** — `main.version` exists and defaults to `"dev"`, and Slice 9's demo is *"version string change → push → production shows new version."* Wiring it in Slice 0 costs one line.

**Fix:** drop `|| true`, add vet and a fmt gate, and set `-ldflags` on the build.

---

### S7

**`Makefile` hard-codes an Apple Silicon Homebrew path.** — Low · `Makefile:3`

```make
export PATH := /opt/homebrew/bin:$(PATH)
```

Local environment leaking into a committed file. It is wrong on Intel macOS (`/usr/local/bin`), pointless on Linux and in CI (which installs Go via `setup-go`), and it silently shadows whichever Go the developer intended. If a specific toolchain is needed, `go.mod`'s `go 1.26.5` directive plus a `toolchain` line is the portable mechanism.

**Fix:** delete the line. If it exists to work around a local PATH problem, fix that in the shell profile instead.

---

### S8

**Test hygiene — three small things.** — Low · `internal/policy/policy_test.go`

1. **`:58` / `:154`** — `loc, _ := time.LoadLocation(...)` is assigned, never used, and then discarded with `_ = loc`. Delete both lines. (`go vet` does not flag it because the blank assignment counts as a use.)
2. **Shallow struct copies share backing arrays.** `shorter := base` followed by replacing `shorter.Curfews` is safe today because the whole slice is replaced. But `shorter.Curfews[0].Enabled = false` would silently mutate `base` and produce a test that passes for the wrong reason. A `func (p Policy) clone() Policy` helper, or building each variant from a constructor, removes the trap before someone falls into it.
3. **`LoadLocation` errors are discarded** (`loc, _ :=`) in several tests. Use `mustWindow`-style helpers consistently so missing tzdata surfaces as a clear failure rather than a nil-Location silent pass — which, per [S3](#s3)(b), is exactly the fail-open case.

---

### S9

**`main.go` exits 0 on its non-serving path.** — Low · `cmd/nightloch/main.go:19-20`

The binary prints to stderr and calls `os.Exit(0)`. Paired with `deploy/nightloch.service.example` (`Type=simple`, `Restart=on-failure`), a service started from this template exits immediately and systemd treats it as a clean shutdown — no restart, no failure, an `inactive (dead)` unit that looks intentional.

Correct for Slice 0, where nothing should be running. Worth a one-line comment in the service template noting it is inert until Slice 2, so a future reader does not spend twenty minutes debugging a unit that is behaving as designed.

---

## Before committing

Blocking:

1. **[S1](#s1)** — change `Add(24 * time.Hour)` to `AddDate(0, 0, 1)` in `schedule.go:82`, and add the timezone-change-across-DST case as a regression test. A one-word fix for a demonstrated authorization bypass.

Strongly recommended in the same commit:

2. **[S2](#s2)** — multi-timezone generation, a horizon that spans a transition, and a non-fixed logged seed. Without this the next timing bug is equally invisible.
3. **[S3](#s3)** — wire the validators into `SubjectMayApplyEdit` and deny on invalid input; decide what `00:00–00:00` means.

Worth doing now because they get more expensive later: [S5](#s5) (move off `/var/www` before SQLite lands), [S6](#s6) (drop `|| true`, add vet, wire `-ldflags`).

Small: [S4](#s4) doc note, [S7](#s7) delete the PATH line, [S8](#s8) test cleanup, [S9](#s9) service comment.

---

## Closing

The judgment this slice was built on — write the predicate first, before any HTTP or database code, and test it against a brute-force oracle — was right, and it paid off immediately: the oracle is what made [S1](#s1) provable rather than arguable. A slice that started with handlers and migrations would have buried this bug under a month of other work and found it, if at all, as a mysterious "why did the curfew not apply in March."

The gap is narrower than it looks. The implementation models `P` correctly; one line computes *when* to sample it wrongly, and the test that should have caught that was scoped to a timezone where the bug cannot manifest. Fix both and this package is in good shape to carry the authorization decisions in Slices 5–6.

One process note worth carrying forward: [S3](#s3) is the third finding across these reviews whose failure mode is *silent fail-open* — after [F1](../adversarial-review.md#f1) (blackhole described as fail-open) and [F3](../adversarial-review.md#f3) (baseline with no tamper detection). For this product specifically, "when in doubt, do not block" is the wrong default at every layer. A short convention — **invalid or unknown state denies the edit and raises an alert, never silently permits** — is worth writing into the design doc before Slice 2 starts adding handlers that will each face this choice independently.
