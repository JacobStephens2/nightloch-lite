# Security & Policy Design (Slice −1)

**Status:** Complete for Slice −1 (2026-07-28); **amended for [slice-neg-1-review](../reviews/slice-neg-1-review.md)** (R1–R11)  
**Source:** [adversarial-review.md](../adversarial-review.md) §7–8 + [plan.md](../plan.md) amendments  
**Code:** none in this slice — implement in later slices (`internal/policy`, partner state machine, AGH reconciler)

**Precedence:** For perimeter, policy classification, AGH mapping, fail modes, and integrity, **this document wins**. Then [shared-understanding.md](../shared-understanding.md) (product freeze) → [DECISIONS.md](../DECISIONS.md) (history + amendments) → [plan.md](../plan.md) (sequencing). On conflict, leftmost wins; others are bugs. Prefer linking over restating.

---

## 1. Answers to review §8 (before Slice 0)

### Perimeter

| # | Question | Decision |
|---|---|---|
| 1 | User-initiated partner edge end? | **Yes, with 72-hour cooling-off.** Partner-initiated end is **immediate**. |
| 2 | Notify existing partners on new partner? Roster? | **Yes** — email all existing partners on invite **sent** and **accepted**. Partners **see roster** (who protects subject, since when); they **cannot** edit others’ edges. |
| 3 | Zero partners — can user loosen? | **Yes.** Self-loosen allowed when the subject has **no active partner edges**. Restraint teeth come from cooling-off when leaving a partnered state, not permanent self-lock. |
| 4 | User account deletion? | **Yes, via 72h cooling-off.** Notify all partners (both directions: people they protect and partners who protect them). Teardown is transactional: revoke all peers from `wg0` + AGH, quarantine IPs, end all edges with notice, then delete account. |
| 5 | User revoke own device? | **All subject-initiated device revokes are immediate** (including the last device). Stolen/lost phone must not wait 72h. Perimeter signal: **action alert + coverage → 0%** on next report ([R4](../reviews/slice-neg-1-review.md#r4)). |
| 6 | Device enrollment gated? | **Yes for v1.** Operator allowlist (e.g. env `ENROLL_ALLOWLIST` emails). Signup and partner-accept stay open. Unauthorized: “Device enrollment is invite-only during beta.” **v2 follow-up:** `enrollment_allowed` on user row (UI toggle) so adding a user is not a deploy/restart ([R9](../reviews/slice-neg-1-review.md#r9)). |

### Policy semantics

| # | Question | Decision |
|---|---|---|
| 7 | Wholesale baseline disable per user? | **No.** OISD stays global. Exceptions are **per-domain allowlist** only (`@@\|\|domain^$client=<ip>`). |
| 8 | Tighten/loosen rule? | **At-least-as-restrictive predicate** over horizon **H = 14 days** (§2). |
| 9 | False-positive allow? | **User requests → partner approves** (or self-approve if zero partners). Becomes per-client allow rule. |

### Integrity

| # | Question | Decision |
|---|---|---|
| 10 | Fail mode per scenario? | **VPN off → unprotected (open).** **Server/path unreachable with tunnel up → blackhole (closed)** until user disables VPN. Not the same row. |
| 11 | Coverage when no curfew? | **Continuous** while any device enrolled — not curfew-only. |
| 12 | Digests / Q13? | **Reopened.** Ship **coverage report** (daily or weekly) + optional “alive” proof. Primary integrity for tunnel presence is **coverage trend**, not noisy 1h quiet alarms. |
| 13 | What watches the app? | **External dead-man’s-switch** (e.g. healthchecks.io or stephens cron). Ping only if last reconcile OK **and** AGH API up **and** DB writable. |
| 14 | Sending domain? | **`nightloch.app`** (Resend + SPF/DKIM/DMARC). From local-part: **`alerts@nightloch.app`** (monitored / routed to operator inbox) — not `noreply@` ([R11](../reviews/slice-neg-1-review.md#r11)). |

### Enforcement

| # | Question | Decision |
|---|---|---|
| 15 | Intercept 53 / block 853? | **Yes, together.** REDIRECT TCP+UDP 53 from `10.66.0.0/24` → `10.66.0.1`. REJECT TCP+UDP 853 from tunnel. |
| 16 | Android strict Private DNS? | **Detect / explain / product copy.** Blocking 853 breaks strict mode with no plaintext fallback — surface “Private DNS cannot be accessed; set Private DNS to Off or Automatic” rather than silent blackhole-only. Alert on 853 attempts when detectable. |
| 17 | IPv6? | **Test in Slice 4** on dual-stack before finalizing. Plan default remains “no `::/0` until tested”; if Mac leaks filter-bypass, either add `::/0` + server v6 or document disable-client-v6. |

### Scope

| # | Question | Decision |
|---|---|---|
| 18 | Flynn relationship? | **Peer accountability for v1** (consenting adults). Schema: `edge_type` ∈ {`peer`,`guardian`}; **only `peer` implemented**. Guardian (asymmetric termination / forced log share) is future — do not conflate with peer. |
| 19 | TLS Path A or B? | **Path A — staging hostname** `lite.nightloch.app` until Slice 11 apex cutover. Stock Caddy HTTP-01. |

---

## 2. Tighten / loosen predicate

### Model

Policy for a subject is a pure function over absolute time:

```text
P(subject, domain, t) → blocked | allowed
```

where `t` is a UTC instant, and `domain` is a canonical registrable/host form (see canonicalization rules in implementation).

**Blocked** is strictly more restrictive than **allowed**:

```text
blocked ⊒ allowed
```

### Subject-initiated edit rule

A subject may apply an edit producing `P_new` from `P_old` **iff** the new policy is **at least as restrictive everywhere** over horizon **H = 14 days**:

```text
∀ domain ∈ (Domains(P_old) ∪ Domains(P_new)),
∀ t ∈ Breakpoints(P_old, P_new, [now, now+H]):
    P_new(domain, t) ⊒ P_old(domain, t)
```

`Breakpoints` = `{now}` ∪ all curfew/allowlist boundary instants of either policy in the horizon (piecewise-constant policies ⇒ finite check).

If the predicate fails, the edit requires an **active partner** of the subject (or is allowed under zero-partner self-loosen).

### Soundness constraint on H ([R3](../reviews/slice-neg-1-review.md#r3))

`H = 14d` is sound **only if** no policy element first changes state after `now + H`.

**v1 data model restriction:** schedules are **daily or weekly wall-clock windows** only (plus always-on extras/allows). **Forbidden:** effective-from, effective-until, one-shot future dates, or any non-weekly recurrence that introduces a first transition beyond H.

- Property tests must assert every representable policy is fully periodic within H.
- Any future feature that adds deferred effective dates must drop the fixed horizon and compare over **all** breakpoints (still finite for pure weekly/daily policies), not merely lengthen H.
- DST transitions **inside** H are covered by UTC breakpoints; a timezone edit made far from a transition may not “see” a later DST change within H — acceptable under the periodicity constraint; document if deferred-date features return.

### What counts as Domains(P)

- All curfew target domains (exact-host and domain+subdomains as match mode)
- All baseline **extra** block domains
- All **allowlist** domains
- **Not** the full 150k OISD dump as an explicit domain set

### Global lists and P ([R1](../reviews/slice-neg-1-review.md#r1))

**Global list membership *is* part of `P`**, evaluated **lazily** only for domains in  
`Domains(P_old) ∪ Domains(P_new)` (tens of names, not 150k).

- The subject cannot edit OISD/HaGeZi subscriptions.
- Adding an **allowlist** entry for a name that is (or would be) blocked by a global list is a **loosen**:  
  `P_old` may be `blocked` (list hit, no allow); `P_new` is `allowed` (allow wins over list) → predicate **fails** for a partnered subject.
- The subject may still *request* an allowlist; application remains partner-gated when partnered (§1 Q9). The predicate is the single authority on tighten vs loosen for policy edits.

### Composition order (logical) — **curfews win over allows** ([R2](../reviews/slice-neg-1-review.md#r2))

For a client IP at time `t`, a name is **blocked** iff:

```text
blocked  ⇔  curfew_or_extra_block_active(name, t)
           ∨  (global_list_hit(name) ∧ ¬allowlist(name))
```

**Decided product rule (v1):**

1. **Active curfew or subject extra-block** for that name → **blocked**, **even if** an allowlist entry exists.  
   Reconciler emits those rules with **`$important`** so AGH `@@` exceptions do **not** override them.
2. **Global list** hit → blocked **unless** a client allowlist matches (allows carve out OISD/HaGeZi false positives only).

In other words: **allows punch holes in global lists, not in active curfews.**

**Approval UX:** when a partner (or unpartnered subject) approves an allowlist domain that **overlaps an existing curfew target** (same host or parent under domain+subdomains mode), the UI **must flag** that the allow will **not** lift the curfew — only list/false-positive coverage outside curfew hours (and non-overlapping names).

### Worked examples

Assume `now` = 2026-07-28 15:00 UTC, subject in `America/New_York`, one curfew Instagram domain+subdomains 20:00–08:00 local. OISD contains `cdn.example.com`.

| Edit | Subject allowed? | Why |
|---|---|---|
| Add curfew on `example.com` 24/7 | **Yes** | Strictly more blocks |
| Lengthen Instagram window 19:00–08:00 | **Yes** | More blocked hours |
| Shorten Instagram window 21:00–08:00 | **No** (needs partner) | Some times become allowed |
| Delete Instagram target | **No** | Loosen |
| Disable curfew | **No** | Loosen |
| Change TZ `America/New_York` → `America/Los_Angeles` keeping wall clocks 20:00–08:00 | **No** if any UTC instant becomes allowed that was blocked | Evaluate in UTC |
| Narrow match domain+subdomains → exact host only | **No** | Subdomains become allowed |
| Typo target to `instagrarn.com` | **No** | Effective instagram.com no longer blocked |
| Split 20:00–08:00 into 20:00–23:00 and 01:00–08:00 | **No** | Gap newly allowed |
| Add allowlist `cdn.example.com` (OISD-only block) | **No** | `P_old=blocked` via list, `P_new=allowed` → loosen ([R1](../reviews/slice-neg-1-review.md#r1)) |
| Add allowlist `instagram.com` while curfew exists | **No** for subject; if partner applies, **curfew still blocks** in-window (`$important`) ([R2](../reviews/slice-neg-1-review.md#r2)) |
| Request allowlist (pending) | N/A | Not in P until applied |
| Partner approves OISD false-positive allow | Partner only | Explicit loosen of list coverage |
| Zero partners: shorten window / self-allow | **Yes** | Unpartnered self-loosen |

### DST

A wall-clock window 20:00–08:00 `America/New_York` is **11 hours** on spring-forward and **13 hours** on fall-back. **Intended:** wall-clock local rules, not fixed duration. Predicate uses absolute UTC breakpoints.

### Outside the predicate

| Operation | Rule |
|---|---|
| Partner invite / accept / end | Perimeter (§3) |
| Device enroll / revoke | Perimeter (§3); **all revokes immediate** ([R4](../reviews/slice-neg-1-review.md#r4)) |
| Account delete | Cooling-off + teardown |
| Log sharing toggle | **Intentional exemption** from cooling-off ([R5](../reviews/slice-neg-1-review.md#r5)): consent to surveillance is **instantly revocable**; email partner on disable (receipt, not delay). Not a P() edit |
| Operator AGH UI edits | Forbidden in practice; reconciler overwrites |

### Testing requirement (Slice 0+)

- Property-based tests: random `P_old` / `P_new`; predicate vs brute-force sampling over H.
- Include cases where **only** global-list membership differs via allowlist.
- Include allow + active curfew: P remains blocked in-window.
- Assert data-model periodicity invariant (no deferred effective dates).

---

## 3. Partner perimeter rules

### Edge types

| `edge_type` | v1 | Semantics |
|---|---|---|
| `peer` | **Implemented** | Mutual-consent accountability; subject can request end (cooling-off); log share opt-in |
| `guardian` | Schema only | Future: guardian cannot be removed by subject on same terms; log share may be forced — **not built in v1** |

Flynn later: default assumption **peer** unless a separate guardian product decision is recorded.

### Lifecycle

```text
invite_sent ──accept──► active
     │                    │
     │                    ├── partner_ends ──────────────► inactive (immediate)
     │                    ├── subject_requests_end ──────► cooling_off (72h) ──► inactive
     │                    │         └── subject_cancels ─► active
     ├── decline ───────► declined (terminal)
     └── 7d timeout ────► expired (terminal)
```

- **Decline:** invitee may decline explicitly. **Notify the inviting subject** (not other partners). No edge created.
- **Expired:** no further action required; subject may re-invite.

### Notifications (integrity — partners of subject)

| Event | Recipients |
|---|---|
| Invite sent | All **current** partners of subject (not the invitee) |
| Invite accepted | All **prior** partners of subject (**required**); new partner optional |
| Invite declined | **Subject only** |
| Cooling-off started | Partner being removed + other partners of subject |
| Edge ended (any path) | Both parties + other partners of subject |
| Partner-initiated end | Subject immediately |

### Roster

Any partner of S may list: `{partner_user_id, since, edge_type, log_sharing}` for all active edges protecting S. **No** edit of others’ edges.

### Powers while `active`

Partner of S may:

- Tighten or loosen S’s policy (predicate does not apply to partner)
- Approve allowlist requests
- Revoke S’s devices (immediate; still audit)
- View DNS logs if `log_sharing` on that edge
- See roster

Partner may **not:** delete S’s account; change other partners’ edges; access AGH admin.

### Zero-partner state

- Subject may tighten and **loosen** freely (including allowlist self-approve).
- Product should **nudge** “add a partner” after signup / before enroll.
- Enrolling devices while unpartnered is allowed **if** enroll allowlisted — coverage reports still useful to self; partner emails N/A.

### Cooling-off (72 hours)

Applies to:

- Subject-initiated **partner edge** termination  
- Subject-initiated **account deletion**  

Does **not** apply to:

- Partner-initiated edge end  
- **Any** subject- or partner-initiated **device revocation** (always immediate — [R4](../reviews/slice-neg-1-review.md#r4); stolen-device path; VPN-off is already the instant perimeter escape)  
- **Log-sharing disable** — **deliberate exception** ([R5](../reviews/slice-neg-1-review.md#r5)): consent to partner surveillance is **instantly revocable** on principle; partner gets an **immediate email** (receipt). Accountability for “went dark on logs” is that alert + coverage, not a 72h hold.  
- Partner revoking devices  

During cooling-off: **all policy remains enforced**; partner retains full powers; UI shows countdown; subject may cancel.

### Account deletion teardown (order)

1. Enter cooling-off; notify all related parties  
2. On fire: revoke every peer (`wg` + AGH client); quarantine each IP  
3. End all edges both directions (subject as protected and as partner); notify  
4. Cancel pending invites  
5. Delete sessions/tokens; delete user row (or soft-delete)  
6. Audit event `account_deleted`

### Sock-puppet mitigation (v1)

- Partner-add **alerts** + **roster** (F5)  
- Optional later: existing partner must approve new partner — **not** required for v1  

---

## 4. AGH rule-generation model

### Source of truth

| Store | Role |
|---|---|
| SQLite | Authoritative desired state (users, peers/IPs, curfews, extras, allowlists, edges) |
| AGH subscribed lists | Global OISD NSFW + HaGeZi bypass list (and similar) |
| AGH `UserRules` blob | **Generated projection only** — full replace via single writer |

### Never do

- `filtering_enabled: false` per client (kills curfews + lists)  
- Per-user copies of the 150k OISD list as custom rules  
- Read AGH rules back to *compute* desired state (read-back only for **drift verify**)  
- Rely on `$ctag` for per-user policy (fixed vocabulary)  
- Key `$client` by ClientID (unsupported) — **key by tunnel IP**

### Generated custom rules (sketch)

```text
! GENERATED BY nightloch-lite — DO NOT EDIT. Destroyed on next reconcile (≤5 min or boundary).
! generation=<id> subject_rules=...

# per peer IP 10.66.0.N
# Curfew + extra blocks use $important so @@ allows cannot lift them ([R2]).
||extra-block.example^$client=10.66.0.N,important
||instagram.com^$client=10.66.0.N,important
# ... only while curfew active; boundary writer adds/removes

# Allows carve out global lists only (no $important on list hits).
@@||allowed-false-positive.example^$client=10.66.0.N
```

### Reconcile

- Serialize all `set_rules` (mutex / single goroutine)  
- On write kick + every 5 minutes backstop + **boundary timer** (next transition − 60s for block edge)  
- After write: GET status, diff `user_rules`, emit drift event if mismatch  
- Operator AGH UI: **read-only in practice** for filtering rules  

### Query log

- Retention **explicitly 7 days** in AGH config  
- App re-filters every row by **exact** client IP  
- Bound by device `created_at`  
- IP **quarantine** ≥ retention + 1 day before reuse  

### Threat note

Lite holds AGH **admin** Basic auth (no scoped API tokens). App compromise ⇒ enforcer compromise. Protect env; no public AGH without Caddy auth.

---

## 5. Fail modes & integrity

### Fail modes

| Scenario | Behavior | Partner signal |
|---|---|---|
| User disables VPN / kills app | Traffic leaves unfiltered (**open**) | Coverage drops in report; not necessarily instant alarm |
| Droplet down / WG endpoint dead / bad route | Full-tunnel client **blackholes** internet (**closed**) until user disables VPN | Coverage → 0; external dead-man if app also dead; document in enroll UI |
| AGH down, WG up | DNS broken inside tunnel (often looks like no internet) | Reconcile/AGH health fail; dead-man if app detects |
| App crash-loop | Last AGH rules still enforce; **no** new reconcile/alerts from app | **External dead-man** must fire |

### Integrity channels

| Channel | Content |
|---|---|
| **Action alerts** (email) | Loosen, allowlist approve, enroll/revoke, log-share off, partner invite/accept, cooling-off, edge end, account-delete request, drift, AGH/reconcile failure, optional :853 |
| **Coverage report** | Per enrolled peer: % time with recent handshake (keepalive 25s configs); daily or weekly to partners |
| **Dead-man** | External service emails operator (+ partner if configured) if healthy pings stop |
| **Bounces** | Resend webhooks → UI “cannot reach partner” |

### Client config defaults

- `PersistentKeepalive = 25`  
- Full tunnel `0.0.0.0/0` (IPv6 per Slice 4 test)  
- DNS `10.66.0.1`  
- Enroll screen must state blackhole-on-server-down and recovery steps (Android Always-on / macOS On-Demand traps)

### DNS bypass mitigations (honest: not perfect)

- REDIRECT :53; REJECT :853  
- HaGeZi list; Firefox canary NXDOMAIN  
- Limitation text beside “DNS domains only”: **encrypted DNS / determined bypass on owned hardware is not fully preventable**

---

## 6. Staging & mail

| Item | Decision |
|---|---|
| Product UI during build | `https://lite.nightloch.app` until Slice 11 |
| Apex | Old stack until cutover; TTL 60s day before |
| Mail From | `Nightloch <alerts@nightloch.app>` (monitored) once Resend-verified — not `noreply@` ([R11](../reviews/slice-neg-1-review.md#r11)) |
| Turnstile / cookies | Bound to active `BASE_URL` host |

---

## 7. Traceability

| Design section | Primary review findings |
|---|---|
| §1 Answers | F1–F10, F15–F20, F23, F29, F33 |
| §2 Predicate | F18 |
| §3 Perimeter | F5–F9, F33 |
| §4 AGH model | F10–F14, F17, F27 |
| §5 Integrity / fail | F1–F4, F15–F16, F23, F29 |
| §6 Staging / mail | F20, F29 |

---

## 8. Slice −1 completion checklist

- [x] §8 questions answered with defaults  
- [x] Predicate specified with examples + DST  
- [x] Partner perimeter + cooling-off + zero-partner + deletion  
- [x] AGH generation model (global lists + `$client`)  
- [x] Fail mode corrected (open vs blackhole)  
- [x] Integrity = actions + coverage + dead-man  
- [x] Flynn = peer v1; `guardian` reserved  
- [x] Path A staging TLS  
- [x] Fold into [shared-understanding.md](../shared-understanding.md) + [DECISIONS.md](../DECISIONS.md)  
- [x] [current-decision.md](../current-decision.md) points at next slice (0)  
- [x] [R1](../reviews/slice-neg-1-review.md#r1)–[R11](../reviews/slice-neg-1-review.md#r11) addressed (predicate, precedence, H soundness, revoke, log-share exemption, DECISIONS hygiene, doc precedence, dogfood caveat, enroll v2 note, lifecycle states, From local-part)  
