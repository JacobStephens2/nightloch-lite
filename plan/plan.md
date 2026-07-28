# Nightloch Lite — Vertical-Slice Implementation Plan

**Status:** Plan revised after adversarial review (implement on go-ahead)  
**Canonical product freeze:** [shared-understanding.md](./shared-understanding.md)  
**Decision log:** [DECISIONS.md](./DECISIONS.md)  
**Adversarial review:** [adversarial-review.md](./adversarial-review.md) (Opus 5 [xhigh] via Claude Code)  
**Slice −1 review:** [reviews/slice-neg-1-review.md](./reviews/slice-neg-1-review.md)  
**As of:** 2026-07-28  
**Revision:** Incorporates findings F1–F33 and the re-cut in review §7.  
**Slice −1:** **Done** (2026-07-28); **R1–R11** applied in SECURITY-POLICY-DESIGN + freeze docs.

**Precedence:** SECURITY-POLICY-DESIGN → shared-understanding → DECISIONS → **this plan** (sequencing only on conflict).

---

## How to read this plan

Work proceeds as **vertical slices**: each ships a thin path through the stack that is **demoable end-to-end**.

| Rule | Meaning |
|---|---|
| **Done means demoable** | Someone can click/SSH/use a device and see the outcome — not “documented dry-run” |
| **Thin then thicken** | Smallest real path first; **security properties ship with the exposure**, not in a final “hardening” dump |
| **Ops gates stay explicit** | Live NAT / DNS cutover / droplet create need human approval |
| **Design before enforcement productization** | Slice **−1** answers perimeter/policy/integrity questions before Slice 0 ships code that bakes wrong assumptions |
| **Grill where decisions are, not everywhere** | Slices carrying unresolved *product* decisions open with a [decision grill](#decision-grills); the rest open with a 5-minute gate |

Suggested dogfood pair: two emails you control (protected + partner).

---

## Decision grills

The original 60-question interview (`grill-me` skill → DECISIONS Q1–Q59) held up well: Slice −1 amended only 9 of 59, and **two-thirds of those were forced by external facts** (WireGuard blackholes, AGH cannot scope filter lists per client, AGH query-log `search` is substring) rather than by any change of intent. Those were the adversarial review's job, not an interview's.

The three amendments a better grill *would* have caught — Q34′ (edge end), Q18′ (enroll gate), Q11′ (zero partners) — share a shape. They are not "what should X do?" but questions that live *between* branches of a decision tree. So the second pass asks different questions, and only where decisions remain.

### Gate (every slice, ~5 minutes)

Before starting a slice, list the decisions it forces that are **not** already in DECISIONS / SECURITY-POLICY-DESIGN. Empty list → no grill, start work. Non-empty → grill it first, and fold answers into the decision docs before writing code.

### Slices that get a full grill

| Slice | Why | Depth |
|---|---|---|
| **3** Partner graph | Perimeter UX, notification recipients + copy, mutual edges | Full |
| **5** Device enroll | Gate refusal copy only — mostly settled | Gate only |
| **6** Curfews + allow path | The product. Edit UX, allowlist request flow, timezone ownership | Full |
| **7** Domain logs | Most privacy-sensitive surface; specified in one sentence today | Full |
| **8** Alerts + coverage | Copy, cadence, batching, unsubscribe — all undecided | Full |

Slices 0, 1, 2, 4, 9, 10, 11 are engineering against settled decisions. Use the gate; expect it to come back empty.

### Second-pass question archetypes

The first interview walked the decision tree depth-first. These are the questions that fall between branches:

1. **Negative space** — what happens when the count is **zero**? (Zero partners was undefined through 59 questions.)
2. **Composition** — two locked decisions are each fine; what does their *combination* now permit? (Open signup ∧ self-enroll = free VPN exit for strangers.)
3. **Exit paths** — how does each party leave this state, how fast, and who is told?
4. **Who-gets-told** — for every state change: which recipients, through which channel, with what wording.
5. **Adversary-as-user** — "the protected user wants out tonight and will use only authorized actions." Walk the path. *(Highest-yield question in the set — it produced F5, F6, and F8.)*

### Standing gap: UX and copy

No screen, email body, or error message is specified anywhere in these documents. Slice 6's central flow — user hits an OISD false positive → requests an allowlist entry → partner approves — is fully specified as *mechanism* and entirely unspecified as *experience*. Same for what a partner sees in Slice 7 and what a coverage report looks like in Slice 8. **If only one more grill happens, grill the UX of Slices 6–8** rather than re-litigating architecture.

### Cost note

Decision churn is not free. The docs already had a precedence problem (R7) and four stale `DECISIONS.md` rows (R6). Every grill round produces more decision records to keep consistent — which is the reason to run four grills rather than eleven.

---

## Plan amendments (from adversarial review)

These were folded into DECISIONS / shared-understanding / SECURITY-POLICY-DESIGN in **Slice −1**.

| Topic | Original lean | Amended plan default | Review |
|---|---|---|---|
| Fail mode | “path down → fail open” | **VPN off → unprotected (open).** **Server unreachable → device blackholes until user disables VPN (closed).** | F1 |
| Quiet peer | 1h alert in curfew only | **Alert on actions; report coverage continuously** while any device enrolled. Real-time quiet alert de-emphasized / high bar only | F2, F3 |
| Digests (Q13) | None in v1 | **Coverage report** (daily or weekly) + optional “alive” mail — integrity signal *and* deliverability probe | F2, F3, F23 |
| Baseline disable | Partner may disable wholesale | **No whole-baseline off.** Per-domain **allowlist** only (`@@\|\|domain^$client=IP`) | F10, F27 |
| Partner end (user-initiated) | Immediate | **72h cooling-off**; partner keeps powers; user may cancel | F6 |
| Partner end (partner-initiated) | Immediate | **Immediate** (unchanged) | F6 |
| Zero partners | Undefined | **User may self-loosen when unpartnered**; teeth = cooling-off when removing partners | F7 |
| Last device revoke | 72h cooling-off | **Immediate** all device revokes; coverage + alert | R4 |
| Allow vs curfew | Unspecified | **Curfew wins** (`$important`); allows carve global lists only | R2 |
| Predicate / OISD | List outside P | **Global list membership in P** (lazy on comparison set) | R1 |
| Partner add | No alert | **Notify all existing partners**; partners **see roster** (not edit) | F5 |
| Device enroll | Any signed-in user | **Gated** (operator allowlist for v1) | F9 |
| From address | `nightloch@stephens.page` | **`alerts@nightloch.app`** (monitored; SPF/DKIM/DMARC) | F29, R11 |
| TLS Slice 1 | apex “or” staging | **Path A: staging hostname** (`lite.nightloch.app`); apex cutover Slice 11 | F20 |
| Token storage | Unspecified | **Hash at rest** (magic link, session, invite) | F21 |
| AGH rules model | “Per-user baseline lists” | **Global OISD subscription** + **per-client `$client` rules** for extras/allows/curfews; never `filtering_enabled: false` | F10, F14 |

---

## Target architecture (reminder)

```text
Android/Mac ──WG full tunnel──► vpn.nightloch.app:51820
                                    │
                         DO nyc3 droplet (s-1vcpu-2gb)
                         ├── wg0 + NAT
                         ├── REDIRECT :53 → AGH; REJECT :853 (tunnel)
                         ├── AdGuard Home (DNS on wg0 only; global lists + $client rules)
                         ├── nightloch-lite :127.0.0.1  (single-writer reconciler)
                         └── Caddy → lite / agh (basic auth)

OpenTofu: droplet, firewall, SSH key, CF DNS (unproxied), cloud-init
GitHub Actions: build/deploy binary (health-gated rollback)
Resend (@nightloch.app) + Turnstile + external dead-man heartbeat
stephens.page: forced-command pull of encrypted SQLite snapshot
```

### AGH policy mapping (authoritative for Slices 5–6)

| Layer | Mechanism | Scope |
|---|---|---|
| OISD NSFW | Subscribed filter list | **Global** — always on for all clients |
| DoH/DoT bypass mitigation | HaGeZi Encrypted DNS/VPN/TOR/Proxy list + NXDOMAIN `use-application-dns.net` | Global |
| User baseline additions | `\|\|domain^$client=<ip>` | Per client IP |
| Partner/user allowlist | `@@\|\|domain^$client=<ip>` | Per client IP |
| Timed curfews | `$client` rules written/removed at boundaries (or AGH blocked-services schedule if Instagram is in catalogue — verify in Slice 4) | Per client IP |

- SQLite is **source of truth**; AGH custom rules are a **generated projection** (banner rule #1: do not edit).
- All `set_rules` through **one serialized writer**; after write, **read-back** and diff for drift (F11).
- AGH auth: HTTP Basic only (no API tokens) — lite holds full admin credentials (threat model note).

### Integrity model (authoritative for Slice 8)

| Kind | Mechanism |
|---|---|
| **Actions** (real-time email) | Partner invite/accept/add; user-initiated cooling-off start; edge end; loosen / allowlist approve; enroll/revoke; log-share off; account-delete request; reconcile/AGH failure; desired≠enforced drift; DoT:853 attempts if detected |
| **Coverage** (report, not nightly false-alarm) | Per enrolled peer: fraction of time with recent handshake, **always** (not only curfew). Daily/weekly partner email |
| **Watcher** | External dead-man’s-switch (healthchecks.io or stephens cron): ping only if reconcile OK + AGH up + DB writable |

`PersistentKeepalive = 25` on all client configs (NAT + better signal; still imperfect under Doze — hence coverage reports).

### Fail mode (honest)

| Scenario | Client behavior | Product stance |
|---|---|---|
| User turns VPN off | Unprotected (open) | Expected bypass; coverage drops; action alerts if they also mutate policy |
| Droplet / path down | **Blackhole** until user disables VPN (Android/macOS full-tunnel) | **Not** “fail open”; document in enroll UI; uptime is P0 |
| Always-on Android “block without VPN” | Extra steps to restore internet | Document platform traps |

---

## Slice map (overview)

| Slice | Name | Rough size | Demo when done |
|---|---|---|---|
| **−1** | Security & policy design | 0.5–1 day | Amended docs + predicate spec + perimeter rules written (no product code required) |
| **0** | Repo skeleton + policy package | 0.5 day | `go test` including property tests for tighten/loosen |
| **1** | Live origin (staging) | 1–2 days | `https://lite.nightloch.app` OK page; CF grey-cloud; daily DO backups in tofu |
| **2** | Sign in | 1–2 days | Magic link → hashed session; CSRF; `/health` |
| **9** | Continuous deploy | 0.5–1 day | Push to `main` → health-gated binary deploy *(ordered here intentionally)* |
| **3** | Partner graph + audit | 2–3 days | Invite/accept; cooling-off end; partner-add alerts; roster |
| **4** | Tunnel + baseline spine | **3–5 days** | Operator peer; OISD; DNS intercept; reboot drill documented |
| **5** | Device enroll (gated) | 2–3 days | Allowlisted user mints peer; others cannot |
| **6** | Curfews + reconcile + allow path | 3–4 days | Controlled domain boundary demo; then Instagram; partner allowlist |
| **7** | Domain logs | 1–2 days | Tenant-isolated logs; IP quarantine honored |
| **8** | Alerts + coverage + dead-man | 2–3 days | Action emails + coverage report + external heartbeat |
| **10** | Backups + real restore | 1–2 days | Encrypted pull; **restore on throwaway droplet**; record RTO |
| **11** | Cutover & dogfood | 1 week dogfood | Apex live; verify security; kill-app drill |

Sizes are **over-scope detectors**, not schedules. If Slice 4 is estimated at “half a day,” the estimate is wrong (review §9).

---

## Slice −1 — Security & policy design (no application code)

**Status: complete (2026-07-28)**

### Deliverables (done)

- [x] Amend [shared-understanding.md](./shared-understanding.md) and [DECISIONS.md](./DECISIONS.md)
- [x] Full design: [docs/SECURITY-POLICY-DESIGN.md](./docs/SECURITY-POLICY-DESIGN.md) — §8 answers, predicate + examples, perimeter, AGH model, fail/integrity, staging/mail
- [x] Flynn = peer v1; `guardian` reserved
- [x] [current-decision.md](./current-decision.md) → next Slice 0

### Demo / done

A reader can answer “who can loosen when?” and “what does AGH store?” from SECURITY-POLICY-DESIGN without guessing.

### Out of scope

Application code (starts Slice 0).

---

## Slice 0 — Repo skeleton + policy package

**Status: complete (2026-07-28)**

**Goal:** Real Go module; highest-risk pure logic tested first.

### Deliverables (done)

- [x] `go.mod` (`github.com/JacobStephens2/nightloch-lite`)
- [x] `cmd/nightloch/`, `internal/policy/`, `infra/opentofu/`, `deploy/`, `.github/workflows/ci.yml`
- [x] `Makefile`: `test`, `build`
- [x] **`internal/policy`:** `IsBlocked`, `AtLeastAsRestrictive`, `SubjectMayApplyEdit`; curfews win over allows; global list lazy in P; probe subdomains for match-mode narrowing
- [x] Unit tests (worked examples) + property test (breakpoint ≡ brute force; multi-TZ / DST horizon)
- [x] Slice 0 review S1–S9: DST-safe `AddDate` breakpoints; deny-closed Validate; deploy under `/opt/nightloch`; CI vet/fmt/ldflags

### Demo / done

```bash
go test ./...
go build -o bin/nightloch ./cmd/nightloch
```

Property tests check breakpoint ≡ brute force; unit tests check **disguised loosen → denied** when partnered.

---

## Slice 1 — Live origin (staging hostname)

**Goal:** Droplet + grey-cloud DNS + Caddy with **stock** HTTP-01 (no custom Cloudflare DNS plugin).

### Deliverables

- OpenTofu:
  - Droplet `nyc3` `s-1vcpu-2gb`
  - **`backups = true`** with explicit `backup_policy { plan = "daily" }` ([F24](#))
  - Firewall: 22, 80, 443, UDP 51820
  - SSH key; console password set and stored **before** network experiments ([F32](#))
  - Cloudflare A **unproxied**: `lite.nightloch.app`, `agh.nightloch.app`, `vpn.nightloch.app` (apex left on old stack until Slice 11)
  - Resend DNS for **`nightloch.app`**: SPF/DKIM/DMARC records ([F29](#))
- cloud-init:
  - Deploy user; **swap**; **disable unattended-upgrade auto-reboot** ([F4](#))
  - Stock Caddy; static OK page on `lite.nightloch.app`
- Secrets doc: DO token, CF token (zone DNS edit), SSH key
- Draft NAT ops checklist (not executed)

### Demo / done

Browser: valid cert on `https://lite.nightloch.app`.  
`dig +short vpn.nightloch.app` → droplet IP, unproxied.

### Gates

- Human-approved `tofu apply`.
- **Path A only** — no “or cut apex” in this slice.

### Out of scope

App binary, WG, AGH.

---

## Slice 2 — Sign in (magic link)

**Goal:** Auth loop with security properties that create exposure.

### Deliverables

- SQLite: `users`, `magic_link_tokens`, `sessions` — **tokens/sessions stored hashed** ([F21](#))
- UI: email + Turnstile; consume link
- Cookie: HttpOnly, Secure, SameSite=Lax, **host-only** (no `Domain=.nightloch.app`)
- CSRF middleware on all state-changing POSTs ([F26](#))
- Token TTL 15m single-use; session 30d
- Rate limit **per email + per IP** (mail-bomb / From-reputation load-bearing)
- Resend From: `Nightloch <…@nightloch.app>` once domain verified; else temporary documented fallback
- Config: `BASE_URL`, secrets via env
- systemd + Caddy reverse proxy to app
- **Honest `/health`**: DB writable (+ later AGH/reconcile checks as those exist) ([F25](#))

### Demo / done

Two browsers, two emails → signed in / sign out.  
Confirm cookie not sent to `agh.` host.

### Out of scope

Partners, devices.

---

## Slice 9 — Continuous deploy *(early)*

**Goal:** Ship binary safely so later slices iterate fast. Number kept as “9” for review cross-refs; **execute immediately after Slice 2**.

### Deliverables

- GHA: test + linux/amd64 build on `main`
- Deploy: rsync binary, **migrate as explicit step** (not boot auto-migrate), restart systemd
- **Health gate** 30s: on failure restore previous binary and restart ([F25](#))
- Migrations: forward-only additive; pre-migrate SQLite snapshot kept on server
- Secrets documented; previous binary retained

### Demo / done

Version string change → push → production footer/`/health` shows new version; forced bad binary rolls back.

---

## Slice 3 — Partner graph + audit stream

**Goal:** CE-style partners with perimeter security, not metadata-only edges.

### Deliverables

- Tables: `partner_edges` (**`edge_type`**: `peer` \| `guardian`, only `peer` used), `partner_invites` (hashed token, log_sharing, expires 7d)
- **`events` audit table from day one** ([F28](#)): `id, occurred_at, actor_user_id, subject_user_id, action, target, before_json, after_json`
- Flows:
  - Invite (+ log-sharing checkbox); accept; list partners / protecting
  - **Notify all existing partners** on invite sent / accepted ([F5](#))
  - **Roster visible to partners** (who else protects subject; since when) — no edit
  - Partner-initiated end: **immediate**
  - User-initiated end: **cooling-off (72h)**; email partner; powers retained; user can cancel; enforce policy throughout ([F6](#))
- AuthZ: `CanLoosen` / `CanViewLogs` — **zero partners ⇒ subject may loosen self** ([F7](#)); with partners ⇒ only partners loosen
- Prompt after signup: “Add a partner” (unpartnered = weak product)
- Onboarding notes: partner allowlist / From domain

### Demo / done

A invites B → B accepts → existing partner C (if any) emailed → roster shows both.  
A requests remove B → B emailed → **72h later** edge ends (or A cancels earlier).  
Audit rows for each mutation.

### Out of scope

Curfew UI, devices.

---

## Slice 4 — Tunnel + baseline block (enforcement spine)

**Goal:** Packets and DNS posture correct with **operator-minted** peer. Most expanded slice.

### Deliverables

- WireGuard `wg0` `10.66.0.1/24`, listen 51820; `nightloch-wg` helper + sudoers
- **Ops checklist executed** with:
  - Snapshot before first NAT change; **self-cancelling firewall** job; DO web console password ready ([F32](#))
  - `ip_forward` + MASQUERADE
  - **REDIRECT** TCP+UDP 53 from `10.66.0.0/24` → `10.66.0.1` ([F16](#))
  - **REJECT** TCP+UDP 853 from tunnel ([F15](#)); product copy for Android Private DNS strict-mode fallout
- AGH:
  - Bind DNS **only** on `10.66.0.1`
  - OISD NSFW; **HaGeZi Encrypted DNS bypass** list; NXDOMAIN `use-application-dns.net`
  - **Query log retention = 7 days** explicitly (not 90d default) ([F12](#))
  - Admin via Caddy + basic auth on `agh.nightloch.app`
  - Memory/restart policy; filters_update conservative ([F4](#)); unit after `wg0`
- Client config: endpoint `vpn.nightloch.app:51820`, DNS `10.66.0.1`, full tunnel, **`PersistentKeepalive = 25`**
- Enroll UI copy draft: server-down blackhole behavior + recovery steps per platform ([F1](#))
- **Tests / drills:**
  - Reboot droplet with phone on tunnel; document recovery ([F1](#))
  - IPv6 leak checks on dual-stack Mac (`curl -6`, `scutil --dns`, dig AAAA of blocked name) ([F19](#))
  - Android Private DNS = hostname → observe; confirm REJECT/messaging path
  - Send DNS to `8.8.8.8` over tunnel → still hits AGH via REDIRECT
  - Day of normal browsing: **record OISD false positives** ([F27](#))
- Investigate AGH **blocked-services** for Instagram + schedule polarity ([F14](#))

### Demo / done

Phone: WG on → OISD sample domain blocked; general web works; WG off → open.  
Checklist artifacts (reboot notes, IPv6 results, false-positive list) checked into `docs/ops/` or wiki.

### Gates

- Explicit approval before NAT.
- No public open resolver on `0.0.0.0:53`.

### Out of scope

In-app peer mint (Slice 5); timed curfew automation (Slice 6).

---

## Slice 5 — Device enroll (gated)

**Goal:** In-app peers without open VPN exit.

### Deliverables

- **Enrollment allowlist** (env list of emails or flag) — unauthorized: “invite-only during beta” ([F9](#))
- Tables: peers/devices (user_id, pubkey, ipv4, name, created_at); **IP quarantine** (`reusable_after = now + retention + 1d`) ([F13](#))
- Allocate IPs carefully (avoid trivial querylog substring collisions where cheap — e.g. start at `.11+`) ([F12](#))
- UI: add device → one-time config/QR; enroll email to partners
- AGH client entry keyed by **tunnel IP**; `filtering_enabled` always true
- Apply **$client** rules for user baseline additions (not a per-user list copy of OISD)
- Revoke: remove wg peer + AGH client; quarantine IP
- **Account/device teardown** transaction sketch implemented for revoke/delete paths ([F8](#)): no orphan peers
- Device revoke: **immediate** (including last device); action alert + coverage

### Demo / done

Allowlisted user enrolls Android without SSH → works → partner emailed.  
Non-allowlisted account **cannot** mint a peer.  
Revoke + reallocate after quarantine: new owner sees no old logs (prep for Slice 7 test).

---

## Slice 6 — Curfews + asymmetry + reconcile + allow path

**Goal:** Timed policy with AGH-correct model and real asymmetry.

### Deliverables

- Tables: curfews, targets, baseline_extra_domains, **allowlist entries** (no `baseline_enabled` wholesale flag)
- **Predicate-based** authZ for subject edits (Slice 0 package); partner may loosen
- UI: curfew CRUD; add baseline domain; **request allowlist** → partner approve ([F27](#), [F10](#))
- Reconciler:
  - Build full custom-rule blob from SQLite only
  - Single-writer; banner rule; **read-back drift** event
  - **Boundary scheduler** (sleep until next transition) + 5-minute backstop; apply blocks **~60s early** ([F17](#))
  - Optional: native blocked-services schedule for Instagram if verified
- Template: Instagram + subdomains 20:00–08:00 America/New_York — **second** acceptance after controlled domain
- UI honesty: “changes ~1 minute; open apps may lag on DNS TTL”
- Zero-partner self-loosen path tested

### Demo / done

1. **Controlled domain** (e.g. subdomain you own): block/allow at boundary, deterministic.  
2. Then Instagram: document partial CDN/app behavior ([F31](#)).  
3. User cannot disguised-loosen; partner can allowlist false positive and shorten window.

### Out of scope

Passes, MITM.

---

## Slice 7 — Domain logs

**Goal:** Multi-tenant-safe visibility.

### Deliverables

- Proxy AGH querylog; **re-filter every row** by exact client IP equality ([F12](#))
- Bound by device `created_at` ([F13](#))
- Optional quoted search as optimization only
- UI: self logs; partner if log_sharing; toggle + email on disable
- **Test:** seed `.2` and `.20` (or equivalent); assert no cross-read

### Demo / done

Browse on WG → own domains listed. Isolation test green. Partner with sharing on/off behaves correctly.

---

## Slice 8 — Integrity alerts + coverage + dead-man

**Goal:** Actionable alerts; usable coverage; something watches the watcher.

### Deliverables

- Alerts as **projection of `events`** ([F28](#)) via Resend
- Action emails: loosen, allowlist, enroll/revoke, log-share off, partner invite/accept, cooling-off start/end, account-delete request, reconcile/AGH failure, drift, optional :853 detect
- **Coverage report** email (daily or weekly): tunnel uptime per peer while enrolled ([F2](#), [F3](#))
- Status UI: last reconcile, last handshake, coverage summary
- **External dead-man**: conditional heartbeat ([F23](#))
- Resend **bounce webhooks** → UI “cannot reach partner” ([F29](#))
- No reliance on “1h quiet during curfew” as primary alarm (optional high-bar secondary only after empirical keepalive study)

### Demo / done

Partner shortens curfew → email.  
Kill app 1h → **external** notification ([F23](#)).  
Coverage mail received (proves deliverability pipe).

---

## Slice 10 — Backups + real restore

**Goal:** Survivable loss; demo is restore, not prose.

### Deliverables

- DO daily backups remain on
- **Hourly** encrypted snapshot: `VACUUM INTO` (or `.backup`); WAL mode deliberate ([F24](#))
- stephens pull via **forced-command** SSH key; script emits ciphertext only ([F22](#))
- Encrypt to offline age/gpg recipient; private key not on either host if practical
- Retention: e.g. 24 hourly + 7 daily on stephens (disk-aware)
- **Demo:** `tofu apply` throwaway droplet → restore SQLite → deploy binary → sign in, verify partners/curfews/devices → `tofu destroy`; **record wall-clock RTO** ([F30](#))
- Confirm backup path not web-reachable (`curl` test)

### Demo / done

RTO number written in ops notes; restore proven once.

---

## Slice 11 — Cutover & dogfood (verify, don’t invent security)

**Goal:** Production names; week of use; verify properties created earlier.

### Deliverables

- Lower apex TTL to **60s** ≥1 day before cutover ([F20](#))
- Point `nightloch.app` → droplet; re-verify `BASE_URL`, cookies, Turnstile domains
- UI docs: fail modes, Private DNS, false-positive allow flow
- Dogfood week checklist (below)
- Security **verification** pass (not first implementation of CSRF/etc.)

### Exit criteria — v1 dogfood complete

**Original**

- [ ] Magic link on phone browser  
- [ ] Partner invite/accept  
- [ ] Full tunnel + baseline NSFW block  
- [ ] Device enroll without SSH (allowlisted)  
- [ ] Timed curfew enforces (controlled domain + Instagram notes)  
- [ ] Partner-only loosen proven  
- [ ] Domain logs self (+ partner when shared)  
- [ ] GHA deploy works  
- [ ] Backup on stephens + DO backups on  

**Added from review**

- [ ] Droplet reboot behavior observed/documented per platform  
- [ ] Android strict Private DNS attempted and handled  
- [ ] Hardcoded `8.8.8.8` intercepted  
- [ ] Restore executed on fresh droplet with recorded RTO  
- [ ] Two-tenant query-log isolation test passes  
- [ ] Partner cooling-off drill end-to-end  
- [ ] Unauthorized account cannot mint a peer  
- [ ] External heartbeat fires when app killed  
- [ ] Partner-add notifies existing partners  
- [ ] Coverage report received at least once  
- [ ] **Asymmetry + cooling-off unvalidated until a second human holds the partner account** (solo dogfood = plumbing only) ([R8](./reviews/slice-neg-1-review.md#r8))

---

## Cross-cutting concerns

| Concern | Approach |
|---|---|
| Config | Env / systemd `EnvironmentFile`; configurable `BASE_URL` for staging→apex |
| Migrations | Explicit deploy step; additive only; snapshot before migrate |
| Logging | Structured journald; no full query bodies in app logs |
| Secrets | Never commit; AGH basic auth = full admin — protect env |
| Testing | Policy property tests; AGH client fake; isolation tests; smoke scripts |
| UI | Server-rendered Go templates |
| AGH client | Thin interface; mockable; IP as security principal |
| Idempotency | Full-blob reconcile; single writer |
| Journal mode | SQLite **WAL** |

---

## Sequencing

```text
−1 → 0 → 1 → 2 → 9 → 3 → 4 → 5 → 6 → 7 → 8 → 10 → 11
              ↘________↗
         (9 early so 3–8 deploy fast)
```

- **4** may overlap late **3** only for operator peer experiments; product enroll stays **5**.  
- **10** after real data (**2+**); full restore drill before relying on dogfood.  
- Do **not** defer CSRF, token hashing, enroll gate, or log isolation to Slice 11.

---

## Explicitly deferred (still out of v1)

- Temporary passes  
- TLS MITM / search terms  
- Native iOS/macOS apps  
- Guardian edge behavior (column only; no UX)  
- Postgres / multi-region HA  
- Partner must approve new partners (optional hard mode; roster+alert is v1 default)  
- One AGH instance per user  

---

## F18 reference — tighten/loosen predicate (summary)

An edit by the protected user is allowed iff the new policy is **at least as restrictive** as the old over horizon H:

For all domains in the union of old/new targets, and all times in `[now, now+H]` sampled at policy breakpoints (UTC):

`blocked ⊒ allowed` — new may only add blocks, not remove them relative to old.

Timezone changes, subdomain narrowing, target deletes, disables, and splits are handled by evaluating absolute time and domain union — not wall-clock string compares.

Partner-edge end and account deletion are **perimeter** operations (cooling-off). Device revocation is immediate (not the predicate).

---

## Implementation go-ahead

This file is the implementation plan, not authorization to spend cloud money or enable NAT.

**Start next:** Slice **0**, then **1** with explicit `tofu apply` approval.

Slice **−1** is complete; plan amendments are recorded in DECISIONS / shared-understanding / SECURITY-POLICY-DESIGN.
