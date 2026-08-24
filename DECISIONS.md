# Nightloch Lite — Product & Architecture Decisions

Interview decisions for greenfield nightloch-lite.  
**Status:** Locked through Q59; **shared understanding confirmed** 2026-07-28; **Slice −1 amendments applied** 2026-07-28.  
**Implementation:** Slice −1 done; next = Slice 0.  
**Last updated:** 2026-07-28

Canonical freeze: [shared-understanding.md](./shared-understanding.md)  
Security/policy design: [docs/SECURITY-POLICY-DESIGN.md](./docs/SECURITY-POLICY-DESIGN.md)  
Plan: [plan.md](./plan.md) · Review: [adversarial-review.md](./adversarial-review.md)

**Precedence:** SECURITY-POLICY-DESIGN (perimeter/policy/AGH/fail/integrity) → shared-understanding → **this file** (history) → plan. On conflict, left wins.

---

## Slice −1 amendments (reopened / refined)

Supersede conflicting interview rows below when implementing. Full rationale in adversarial review + SECURITY-POLICY-DESIGN.

| ID | Topic | New decision |
|---|---|---|
| Q6′ | Fail mode | **VPN off → open (unprotected).** **Server/path down with tunnel up → blackhole until user disables VPN.** Not “path down → open.” |
| Q13′ | Digests | **Reopened.** Ship **coverage reports** (daily/weekly) + dead-man “alive” path. Not “no digests.” |
| Q22′ | Quiet peer | Deprioritize 1h curfew-only alarm. **Alert on actions; measure coverage whenever a device is enrolled.** |
| Q28′ / baseline | Baseline off | **No wholesale per-user baseline disable.** Partner/user allowlist = per-domain only. |
| Q31′ | Baseline edits | User may **add** blocks; partner (or unpartnered self) may **allowlist** domains — not “remove OISD list.” |
| Q34′ | Edge end | **Partner → immediate.** **Protectee → 72h cooling-off** (cancelable; policy stays on). |
| Q18′ / enroll | Device enroll | **Gated** by operator allowlist (v1). Open signup remains. |
| Q39′ | From address | **`@nightloch.app`** via Resend (SPF/DKIM/DMARC). stephens.page retired as product From. |
| Q11′ | Zero partners | **Self-loosen allowed** when unpartnered. |
| F18 | Tighten/loosen | **At-least-as-restrictive predicate**, H=14 days UTC breakpoints; global lists in P (lazy); allows ≠ curfew lift (`$important`) |
| F5 | Partner add | Notify all existing Partners on the Protectee’s **Roster**; Roster is **visible** to those Partners. |
| F8 | Account delete | Allowed with **72h cooling-off** + transactional teardown. |
| F9 | Open VPN | Enroll gate closes free exit; not open full-tunnel for every signup. |
| F10/F14 | AGH model | **Global OISD** + **`$client` rules**; never `filtering_enabled: false` for “baseline off.” |
| F15/F16 | DNS force | REDIRECT :53; REJECT :853; HaGeZi + Firefox canary. |
| F20 | TLS | **Path A:** `lite.nightloch.app` staging until Slice 11. |
| F21 | Tokens | **Hash** magic links, sessions, invite tokens at rest. |
| F23 | Dead-man | **External** heartbeat required. |
| F33 | Flynn | **`edge_type=peer`** in v1; `guardian` reserved only. |
| R4 | Device revoke | **All** subject device revokes **immediate** (no last-device cooling-off). |
| R5 | Log-share off | **Immediate** by design (consent); not cooling-off. |
| R11 | From local-part | **`alerts@nightloch.app`** (monitored). |

---

## Slice 3 experience grill (2026-08-17–2026-08-23; complete)

| ID | Topic | Decision |
|---|---|---|
| S3-01 | Relationship identity | Identify Users by verified email throughout Slice 3; no display names. |
| S3-02 | Zero / one Partner experience | At zero Partners, persistently and plainly state that the Partner lock is inactive and offer one primary **Add a Partner** action; do not send nag emails. At one, state that the Partner lock is active and show the Partner prominently; adding more is optional. |
| S3-03 | Roster ceiling | No cap on active Partnerships in v1. Separately cap and rate-limit outstanding Partner invitations. Warn that every additional Partner can loosen the Protectee’s Policy. |
| S3-04 | Reciprocal Partnerships | They remain independent and do not change either Partnership’s powers. Disclose the reciprocal effect when inviting and accepting, then mark it in the Roster. |
| S3-05 | Activity | Slice 3 ships human-readable **Activity**, not only stored events. The Protectee and active Partners see attributable Partnership changes with actor, target, and exact time; raw event JSON is not product UI. |
| S3-06 | Pre-accept disclosure | Before acceptance, show the inviter’s verified email, existing-Partner count, Partner powers, log-sharing choice, and expiry—but not existing Partners’ identities. Show the full Roster immediately after acceptance. |
| S3-07 | Cooling-off visibility | Show the exact end time and countdown to the Protectee and every active Partner on that Protectee’s Roster, including in Activity. |
| S3-08 | End now | The Partner being removed may explicitly agree to end immediately; audit it as Partner-initiated termination and notify the Protectee and remaining Roster. No other Partner may waive it. |
| S3-09 | Delivery failure during Cooling-off | The clock continues. Retry the Action alert, record the failure in Activity, and show a prominent cannot-reach-Partner warning to both parties and the remaining Roster. Email delivery is an integrity signal, not authorization. |
| S3-10 | Invitation authentication | The seven-day Partner invitation is not authentication. Its landing page may be read from the bearer link, but accept/decline requires a signed-in User whose verified email matches the invite; otherwise send a fresh 15-minute sign-in link and return afterward. |
| S3-11 | Informed acceptance | Before acceptance, list the Partner powers plainly and use an explicit **Accept Partnership** action; no legalistic checkbox. Domain-log access is identified as conditional on log sharing. |
| S3-12 | Pending controls | The Protectee may cancel; the invitee may accept or decline; existing Partners only observe. Reject self-invitations, duplicate pending invitations, and an invitation duplicating an active directional Partnership; permit the reverse direction. |
| S3-13 | Invitation limits | At most five pending Partner invitations per Protectee; at most ten send/resend operations per rolling 24 hours; a particular invitation may be resent once per 24 hours without extending its seven-day expiry. |
| S3-14 | Unified Activity | Activity is one chronology per Protectee. Slice 3 adds Partnership entries; later Policy, Device, log-sharing, and enforcement actions join it. Activity visibility does not grant DNS-domain-log access. |
| S3-15 | New Partner history | A Partner sees the acceptance entry, later Activity, and the current Roster—not Activity from before their acceptance or former Partners. The Protectee retains complete Activity. |
| S3-16 | Access after end | A former Partner immediately loses Roster and ongoing Activity access, but retains a read-only receipt for their own ended Partnership and no information about remaining Partners or later actions. |
| S3-17 | Activity permanence | Activity entries are immutable; corrections append entries. Retain entries while the relevant Protectee exists. On User deletion, remove the deleted User’s email from retained entries and display **Deleted User** while preserving action and time. |
| S3-18 | Timer attribution | Show timer completion as a Nightloch action linked to the Protectee’s original Cooling-off request; do not attribute timer execution to the Protectee. |
| S3-19 | Action-alert voice | Calm and factual: identify actor, change, time/effective time, consequence, and available response. Never infer motive or describe an action as an attempted bypass. |
| S3-20 | Partnership mail recipients | Invite sent: invitee + current Partners; resend: invitee only; accepted: Protectee + prior Partners + new-Partner confirmation; declined: Protectee only; canceled: invitee + current Partners; expired: Activity only; Cooling-off started/canceled: affected Partner + other current Partners; ended: both parties + remaining Partners. Initiators otherwise get in-app receipts. |
| S3-21 | Alert disclosure | Relationship Action alerts include verified actor and target emails, exact time, effective time when applicable, Log sharing offered, and reciprocal effect. Deliver separately per recipient; never expose the recipient list. |
| S3-22 | Mail failure | Commit the mutation, Activity, and durable per-recipient delivery jobs together. Deliver asynchronously with retry and visible status; delivery failure does not roll back or pause Partnership state. |
| S3-23 | Bounce scope | Slice 3 handles delivery/bounce webhooks for Partnership mail so cannot-reach-Partner warnings are real; Slice 8 generalizes the mechanism. |
| S3-24 | Reply routing | Partner invitations set Reply-To to the inviting Protectee’s verified email. Action alerts reply to monitored `alerts@nightloch.app` and say so in the footer. |
| S3-25 | First-contact explanation | Describe Nightloch as partner-locked DNS protection that blocks domains on enrolled Devices; while a Protectee has Partners, only Partners may loosen Policy. State that turning off a Device’s VPN removes protection. |
| S3-26 | Acceptance disclosures | Disclose that the invitee’s verified email becomes visible to current/future Partners in Roster and Activity; identify Log sharing state and its immediate revocability; explain Partner-immediate versus Protectee-72-hour ending. |
| S3-27 | Time rendering | Email uses explicit UTC. UI shows browser-local time with timezone and exposes UTC alongside it. Never show an unlabeled time. |
| S3-28 | Email actions | Email links only to review screens. Accept, decline, cancel, and end require authenticated CSRF-protected confirmation POSTs; no bearer-link mutation. |
| S3-29 | Terminal invitations | A valid token shows the precise canceled, expired, accepted, or declined outcome. An unknown or malformed token reveals only that the invitation is unavailable. |
| S3-30 | Activity prose | Active voice with verified emails, stable factual action sentences, exact times, and separate delivery status. System completion names Nightloch and links back to the original requester. |
| S3-31 | Mail rendering | Multipart text and HTML, descriptive links, full URLs in plain text, and no tracking pixels. Use delivery/bounce webhooks rather than open tracking. |
| S3-32–39 | Partnership copy | Invitation email and landing page; invite-sent, acceptance, cancellation, decline, Cooling-off start/cancel, and all Partnership-ended variants approved as written in [Slice 3 Partnership experience](./docs/SLICE-3-PARTNERSHIP-EXPERIENCE.md). **Amended 2026-08-23:** the Partnership-ended body closes with a recipient-specific link — the Roster and Activity for the Protectee and remaining Partners, and their retained receipt for the Partner whose Partnership ended, who no longer has Roster or Activity access (S3-16). |
| S3-40 | Concurrent Cooling-off | A Protectee may request Cooling-off for multiple Partnerships concurrently; each gets its own 72-hour countdown from request time. |
| S3-41 | Invite during Cooling-off | Permit it under the v1 no-veto model, with a strong warning, Action alerts, and Activity. If accepted, the new Partner immediately receives full powers. |
| S3-42 | Pending invitation | It does not activate the Partner lock or affect `CanLoosen`; say plainly that the lock remains inactive until acceptance. |
| S3-43 | Final Partner | Before starting Cooling-off for the final active Partnership, state that the Partner lock will become inactive at the exact deadline and the Protectee will be able to loosen Policy. Show the same consequence to Roster viewers. |
| S3-44 | Mixed-role dashboard | Separate **Your protection** from **People you protect**. A User’s own Partner-lock status remains explicit even when they protect others; Reciprocal Partnerships appear independently in both areas. |
| S3-45 | Relationship lists | Active Roster rows show email, since time, Log sharing, reciprocal state, Partner reachability, and Cooling-off. Pending invitations are separate and show invitee, inviter, sent/expiry, proposed Log sharing, reciprocal effect, and delivery; other Partners inspect but do not mutate. |
| S3-46 | Confirmation friction | Consequence-specific confirmations. Immediate Partner end / **End now** requires typing `END`; Protectee removal names its 72-hour deadline/final-Partner consequence; other mutations confirm once. No password re-entry with a valid session. |
| S3-47 | Reachability states | **Delivery delayed** for retryable failure; **Nightloch cannot reach this Partner** for hard bounce/complaint; later success clears the warning but remains in Activity. A bounced invitation is **Invitation not delivered** and a wrong address requires cancel + new invitation. |
| S3-48 | Activity navigation | Newest-first pagination; category, actor, and date-range filters; no free-text search or export in v1. Apply the same tools inside each viewer’s authorized time window. |
| S3-49 | Activity details | Never render raw before/after JSON. Optional human-readable details contain only fields authorized for that audience. |
| S3-50 | Authorized escape | Accept the v1 limit: VPN shutdown is immediate; a colluding Partner may loosen immediately; adding that Partner is visible but not vetoed; concurrent removal of all Partners takes 72 hours. Nightloch provides signal and delay, not prevention against a colluding Partner or VPN shutdown. |
| S3-51 | Independent authority | Every active Partner acts independently; no quorum. Invitation and Roster UI say **Every Partner can act independently. Only add someone you trust.** |
| S3-52 | Suspicious-invitation response | Existing Partners inspect Activity and contact the Protectee but receive no veto, freeze, removal, Operator-escalation, or special response workflow in Slice 3/v1. Any such workflow is deferred to a later phase; ordinary Partner powers arrive in their existing vertical slices. |
| S3-53 | Log-sharing default | Off. Checkbox: **Share my DNS domain log with this Partner. I can turn this off immediately; Nightloch will tell them.** |
| S3-54 | Invitation block | **Superseded by S3-83.** Invitation blocks are deferred outside Slice 3/v1. |
| S3-55 | Muting perimeter mail | Partnership integrity messages cannot be muted or unsubscribed while the Partnership remains active. Do not add list-unsubscribe headers. |
| S3-56 | Email identity changes | No rebinding in v1. A new email is a new User and needs a new Partnership; the old Partnership ends normally. Never transfer Partner powers silently. |
| S3-57 | Alert batching | Never batch or intentionally delay Partnership-perimeter Action alerts; one event, delivery job, deadline, and message per Partnership mutation. |
| S3-58 | Unreachable Partner powers | Partner reachability does not change authorization or lifecycle timing. Powers and countdowns remain active while warnings persist. |
| S3-59 | Vertical increments | Implement Slice 3 as **3A First Partnership**, **3B Full Roster**, then **3C Audit and abuse hardening**. Each is independently demoable but remains part of Slice 3. |
| S3-60 | 3A exposure gate | Until 3B, permit only one active-or-pending Partner protecting each Protectee. Schema supports many from day one; the temporary gate is application behavior. |
| S3-61 | Slice completion | 3A is a checkpoint, not Slice 3 completion. Finish 3B and 3C before the next product slice so safeguards ship with exposure. |
| S3-62 | Deferred response powers | Veto, freeze, report, Operator escalation, existing-Partner approval, and Invitation blocks are deferred. Alert delivery, pending-invitation visibility, Roster transparency, and Activity remain in Slice 3. |
| S3-63 | Final architecture in 3A | Use the final many-to-many Partnership, event, delivery, and authorization boundaries in 3A; do not create a temporary one-Partner data model. |
| S3-64 | Staging checkpoints | Deploy and demo 3A and 3B independently. Keep the one-Partner gate through 3A and lift it only after the 3B multi-Partner alert demo passes. |
| S3-65 | Partnership lifetime | A Partnership is the durable relationship created on acceptance and retained through active, Cooling-off, and ended states. A Partner invitation is separate; a later relationship between the same Users is a new Partnership. |
| S3-66 | Pending invitation changes | Partner invitations are immutable. Resend repeats the same invitation without extending expiry; changing the invitee or proposed Log sharing requires cancellation and a new invitation. |
| S3-67 | Deleting User as Partner | When account-deletion Cooling-off starts, immediately end every Partnership in which the deleting User is the Partner and send the ordinary ending signals. Canceling account deletion does not restore them. Partnerships protecting the deleting User remain active until deletion completes. |
| S3-68 | Racing transitions | The first valid terminal or lifecycle transition committed by the server wins. A losing concurrent action makes no mutation and shows the resulting state, actor, and time where that viewer is authorized to see them. |
| S3-69 | Re-partnering | Former parties may form a new Partnership. It is a new durable record and Activity-visibility window; the old Partnership remains ended with a separate receipt and the returning Partner cannot see the gap. |
| S3-70 | Invitations during account deletion | Starting account-deletion Cooling-off immediately cancels all pending Partner invitations involving the deleting User and prevents new sends or acceptances. Canceling deletion does not restore invitations. |
| S3-71 | Receipt after Protectee deletion | A former Partner retains a minimal pseudonymized receipt after the Protectee deletes their User: **Deleted User**, deletion as the ending cause, and exact time, with no email, Roster, or Activity. |
| S3-72 | Deleting-Partner end disclosure | When account deletion immediately ends Partnerships in which the deleting User is the Partner, the ordinary ending recipients are told factually that the Partner requested User deletion and the Partnership therefore ended immediately. |
| S3-73 | Account-deletion recipients | Start: active Partners protecting the deleting User. Immediate outbound Partnership end: that Protectee, the deleting Partner, and that Protectee’s remaining Roster. Invitation cancellation: both direct parties and previously alerted relevant Roster. Cancellation: only active Partners still protecting the User. Completion: Partners still protecting the deleted User, each receiving a separate pseudonymized receipt. Never combine recipient lists or disclose unrelated relationships. |
| S3-74 | Powers during account-deletion Cooling-off | Ordinary rules continue for the deleting User’s own protection: they may cancel deletion, tighten Policy, revoke Devices, and disable Log sharing; loosening still follows Partner-lock rules. They cannot send or accept Partner invitations, and their Partner powers over other Protectees ended when deletion started. |
| S3-75 | Account-deletion timer authority | Only the deleting User may cancel account deletion. No Partner or Operator may accelerate, delay, or cancel its 72-hour product timer. Partners retain their ordinary powers over that Protectee’s Policy and Devices until deletion completes. |
| S3-76 | Account-deletion confirmation | Starting deletion requires an exact consequence summary and typing `DELETE`. It distinguishes immediate outbound-Partnership and invitation cancellation from the User, Device, and inbound-Partnership teardown at the exact 72-hour deadline, and warns that canceling deletion restores none of the immediate changes. |
| S3-77 | Account-deletion countdown | Show a persistent exact-deadline banner with **Cancel deletion** on the deleting User’s **Your protection** area and a read-only warning to every active Partner protecting them. Do not expose it beyond Users entitled to discrete ending notices. |
| S3-78 | Identity after deletion | The completion Action email may name the deleted User’s verified email for attributable real-time notice. After completion, retained product UI and receipts use **Deleted User**; previously delivered email cannot be recalled or pseudonymized. |
| S3-79–81 | Account-deletion copy | Account-deletion start, cancellation, completion, deletion-caused immediate Partnership end, and deletion-caused invitation cancellation messages are approved as written in [Slice 3 Partnership experience](./docs/SLICE-3-PARTNERSHIP-EXPERIENCE.md). |
| S3-82 | Invitation inbox | A signed-in User sees every pending Partner invitation addressed to their verified email in **Partner invitations** and may review or respond without the emailed bearer link. The same disclosures, authorization, and confirmation rules apply. |
| S3-83 | Invitation-block scope | Defer the entire Invitation-block feature, including decline-and-block and block-management settings, outside Slice 3/v1. Slice 3 retains ordinary decline; do not implement a block without its unblock path. |
| S3-84 | Cooling-off churn scope | Keep 72-hour Cooling-off in Slice 3 as the load-bearing Partner-lock exit mechanism. Defer a dedicated start/cancel churn limiter; ordinary state constraints and required unbatched Action alerts remain. |
| S3-85 | Final-Partnership transition | If Roster changes make an existing Cooling-off Partnership the final active Partnership, append Activity and alert the Protectee and current Roster that its unchanged deadline will deactivate the Partner lock. A later acceptance explicitly says the lock will remain active after that scheduled end. |
| S3-86 | Final-Partnership transition copy | The Action alert sent when an existing Cooling-off Partnership becomes final, plus the acceptance-alert addition when another Partner later joins, is approved as written in [Slice 3 Partnership experience](./docs/SLICE-3-PARTNERSHIP-EXPERIENCE.md). |

---

## Product intent

**Job:** DNS-level domain blocking on a schedule, changeable only by accountability partners (asymmetric control), self-hosted, simpler than original Nightloch.

**Not in v1:** TLS MITM / search-term visibility, temporary passes, custom iOS/macOS apps, Cloudflare Gateway as enforcer.

---

## Locked decisions (by interview number)

### People & testing

| # | Topic | Decision |
|---|---|---|
| 1 | First protectee | **You (Jacob)** as guinea pig; devices **Android + MacBook** (Flynn later) |
| 2 | Partner control for test | Two separate accounts for dogfood (restricted + partner) |
| 25–27 | Multi-user model | **Full Covenant Eyes–style graph in v1**: accounts are people; partner edges via invite; open signup |
| 26 | Signup vs partner role | No global “restricted vs partner” at register; partner = accepted invite edge |
| 27 | Abuse bar | Magic link + rate limits + **Cloudflare Turnstile** |
| 23–24 | Superseded | Shared invite code + role-at-signup — **replaced** by open signup + graph |

### Auth & sessions

| # | Topic | Decision |
|---|---|---|
| 3 | Auth | **Email magic link** via **Resend** |
| 39 | From address | **Superseded by Q39′** — `@nightloch.app` |
| 40 | Magic-link token | **15 minutes, single-use** |
| 20 | Session length | **30 days** |
| 35–36 | DNS log consent | Invite **checkbox** for log sharing; per-edge **toggle** later; **email partner when logs turned off** |
| 41 | Own logs | User **can** see own DNS domain log |

### Enforcement path

| # | Topic | Decision |
|---|---|---|
| 4 | Device path | **WireGuard** on all protected devices |
| 5 | Tunnel scope | **Full tunnel** (`AllowedIPs = 0.0.0.0/0`) |
| 6 | Fail mode | **Superseded by Q6′** — see Slice −1 amendments |
| 7 | DNS enforcer | **AdGuard Home** |
| 8 / 42 / 45 | AGH admin | Ops-only UI at **`https://agh.nightloch.app`**; **Apache Basic Auth was stephens-era** — on dedicated box use **Caddy + Basic Auth** (or equivalent) in front of AGH; AGH own password also set. **Not** a product user surface |
| 18–19 | Peers | **Allowlisted** self-enroll; **email partners** on enroll (**F9 / Q18′**) |
| 32 | Multi-user isolation | **Per-user WG IP + AGH per-client rules** |
| 33 | Query log retention | **Superseded:** no SQLite copy; AGH retention **must be set explicitly to 7 days** (default is 90) |
| 44 | WG privileges | App as non-root; **small root helper + sudoers** for `wg` add/remove/show on `wg0` only |
| 38 | WG network | `wg0`, `10.66.0.1/24`, clients `10.66.0.2+`, **51820/udp**, DNS `10.66.0.1`; **IPv6 open — pending Slice 4 dual-stack test** (not “off” as settled) |
| 50 | Host NAT | **ip_forward + MASQUERADE + REDIRECT :53 + REJECT :853**; checklist approval before live change |
| 57 | WG endpoint DNS | **`vpn.nightloch.app:51820`** (not the product apex) |

### Policy (curfews & lists)

| # | Topic | Decision |
|---|---|---|
| 9–10 | First curfew | `instagram.com` + **subdomains**, **20:00–08:00** `America/New_York`, no guard skew |
| 11 | Asymmetry | **Superseded by Q11′ + F18** — predicate when partnered; self-loosen when unpartnered |
| 12 | Passes | **None** in v1 |
| 28 | Partner powers | + **Roster** visibility; allowlist approve; still no edit others’ Partnerships / no account delete |
| 29 | Visibility depth | **B + E** unchanged: DNS domains + lists; no MITM |
| 30 | List timing | Always-on **global** OISD + timed curfews + per-client allowlists |
| 31 / 49 | Baseline list | OISD `https://nsfw.oisd.nl/` **global**; extras via `$client` blocks; partner path = **allowlist** not list removal |
| 37 | New account defaults | Baseline list **on**; no auto Instagram curfew; devices/partners empty |

### Alerts & reconcile

| # | Topic | Decision |
|---|---|---|
| 13 | Email alerts | **Superseded by Q13′/Q22′** — action alerts + coverage reports + dead-man; not “no digests” |
| 21 | Reconcile cadence | **Superseded:** boundary scheduler (blocks ~60s early) + **5‑min backstop** + kick on write |
| 22 | Quiet peer | **Superseded by Q22′** — coverage reports; high-bar optional only |
| 34 | Partner lifecycle | **Superseded by Q34′/F5** — cooling-off, roster, partner-add alerts |

### Hosting & deploy

| # | Topic | Decision |
|---|---|---|
| 14–15 | Product origin | **`https://nightloch.app`**; cut DNS when lite is ready (leave old stack until then) |
| 16 | Language | **Go** |
| 17 | Database | **SQLite** (no LISTEN/NOTIFY required) |
| 43 | Deploy shape (updated for dedicated box) | Native Go binary + systemd + reverse proxy; AGH on localhost; WG on host `wg0` |
| 46–47 | CI/CD | **GitHub Actions** build `linux/amd64`; **SSH/rsync deploy** on push to `main` |
| 48 | Repo | Private **`JacobStephens2/nightloch-lite`** |
| 51 | Placement | **Dedicated droplet** — all of lite + WG + AGH (**not** stephens.page) |
| 52–54 | Cloud | **DigitalOcean** (US company preference over Hetzner); **`nyc3`**; **`s-1vcpu-2gb`** (~$12/mo) |
| 55 | TLS proxy | **Caddy** (auto HTTPS) |
| 56 | Public DNS | **Cloudflare DNS**, managed by **OpenTofu** (`cloudflare` provider); records **proxied = false** (grey cloud) so UDP WG works |
| 52+ | IaC | **OpenTofu from day one** (`digitalocean` + `cloudflare` providers) |
| 58 | OpenTofu day-one scope | **A** — cloud + base OS bootstrap in OpenTofu; app via GH Actions; peers/rules via lite runtime |
| 59 | Backups | **C** — DigitalOcean droplet backups **and** pull SQLite (+ optional encrypted secrets/WG server key) to **stephens.page** |

### OpenTofu scope (Q58 = A)

**In IaC:**

- DO droplet (`nyc3`, `s-1vcpu-2gb`)
- DO firewall (22/tcp, 80/tcp, 443/tcp, 51820/udp)
- DO SSH key
- Cloudflare A records (`nightloch.app`, `agh.nightloch.app`, `vpn.nightloch.app`) — **unproxied**
- cloud-init bootstrap (user, Caddy, AGH, WireGuard packages)

**Not in IaC:**

- Secrets (CI/env only — not in git)
- App binary / SQLite (GitHub Actions deploy)
- WG peers / AGH rules (nightloch-lite runtime)

### Backups (Q59 = C)

- **DigitalOcean droplet backups** enabled (whole-VM restore; OpenTofu flag where supported).
- **stephens.page** receives periodic **pull** of a consistent SQLite snapshot (and optionally encrypted WG server key / env); retain limited rotations (disk is tight on stephens).
- Prefer stephens **pulling** from nightloch over nightloch having broad push rights onto stephens.
- Backup paths must not be web-reachable; encrypt sensitive archives at rest when practical.

---

## Architecture (agreed shape)

```text
Devices (WireGuard full tunnel)
    │ UDP 51820 → vpn.nightloch.app
    ▼
DigitalOcean droplet (nyc3, s-1vcpu-2gb)
    ├── wg0 10.66.0.1  + NAT to internet
    ├── AdGuard Home (DNS on wg0 only; per-client rules)
    ├── nightloch-lite (Go, SQLite, 127.0.0.1)
    └── Caddy → nightloch.app, agh.nightloch.app

OpenTofu: DO resources + Cloudflare DNS
GitHub Actions: build & deploy binary
Resend: magic links + integrity mail (From: nightloch@stephens.page)
```

**Asymmetry:** partnered protectee may only tighten (predicate); partners may loosen; unpartnered may self-loosen; allowlist not wholesale baseline off.

**Honest limits:** DNS domains only; VPN off = open; server down = blackhole; DoH/DoT mitigated not eliminated; partner action alerts + coverage.

---

## Explicit non-goals (v1)

- Cloudflare Zero Trust / Gateway as enforcer  
- Search query / TLS MITM inspection  
- Temporary network passes  
- Native iOS/macOS Nightloch apps  
- Running enforcement on stephens.page  
- Guardian edge UX (schema reserve only)  
- Multi-instance HA / Postgres  

---

## Pending after Slice −1

1. **Slice 0** — repo skeleton + `internal/policy` property tests  
2. Live droplet NAT/firewall only after ops checklist approval  
3. Slice 1 `tofu apply` with human approval  

---

## Superseded along the way

| Earlier lean | Final |
|---|---|
| stephens.page hosts everything | Dedicated DO droplet |
| Hetzner for price | DigitalOcean (US company) |
| Signup invite code + role pick | Open signup + partner graph |
| Open enroll for every account | Allowlisted enroll (v1) |
| Path down = fail open | Blackhole until VPN disabled |
| No digests | Coverage reports |
| Immediate subject edge end | 72h cooling-off |
| Wholesale baseline disable | Per-domain allowlist only |
| Cloudflare Gateway | Self-hosted AGH + WG |
| From stephens.page | From nightloch.app |

---

## File history

- Created from design interview (2026-07-28).  
- Slice −1 amendments from adversarial review (2026-07-28).  
- Slice −1 review R1–R11 fixes (2026-07-28).  
- Update this file when decisions change; do not silently diverge in code.
