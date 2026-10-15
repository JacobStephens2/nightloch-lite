# Nightloch Lite — Shared Understanding

**Status:** **Shared understanding confirmed** (2026-07-28); **amended by Slice −1** (2026-07-28); **R1–R11 review fixes**  
**Implementation:** Slice −1 complete; further slices on go-ahead  
**Decision log:** [DECISIONS.md](./DECISIONS.md)  
**Security/policy design:** [docs/SECURITY-POLICY-DESIGN.md](./docs/SECURITY-POLICY-DESIGN.md)  
**Plan:** [plan.md](./plan.md)  
**Adversarial review:** [adversarial-review.md](./adversarial-review.md)

**Precedence:** [SECURITY-POLICY-DESIGN](./docs/SECURITY-POLICY-DESIGN.md) (perimeter/policy/AGH/fail/integrity) → **this file** (product freeze) → [DECISIONS](./DECISIONS.md) (history) → [plan](./plan.md) (sequencing). On conflict, left wins.

---

## One-paragraph product

Nightloch-lite is a **Go** control plane on a **dedicated DigitalOcean** droplet (`nyc3`, 2 GB) that drives **AdGuard Home** over **WireGuard full tunnel**. Users sign up with **magic links** (Resend + Turnstile), invite **accountability partners** (Covenant Eyes–style graph, peer edges), enroll devices only if **allowlisted**, and run a **global always-on OISD NSFW baseline** plus **timed curfews** and **per-client allowlists**. Partners (or the user when unpartnered) may loosen; DNS **domain** logs are visible with consent. Integrity = **action alerts** + **coverage reports** + **external dead-man** — not noisy “phone asleep” alarms. **OpenTofu** owns cloud + DNS + bootstrap; **GitHub Actions** deploys the binary; backups = **DO daily backups + encrypted pull to stephens.page**.

---

## Problem this solves

DNS-level domain blocking on a schedule (and always-on adult baseline), with asymmetric control while partnered: the protected person cannot silently loosen locks; partners can. Self-hosted, simpler than original Nightloch + Cloudflare Gateway.

---

## Stack and hosting

| Layer | Choice |
|---|---|
| Control plane | **Go** + **SQLite** |
| DNS enforcer | **AdGuard Home** (global lists + `$client` custom rules) |
| Device path | **WireGuard full tunnel** (stock apps; `PersistentKeepalive = 25`) |
| Host | **Dedicated DigitalOcean droplet** (not stephens.page) |
| Size / region | **`s-1vcpu-2gb`**, **`nyc3`** |
| TLS | **Caddy** (stock HTTP-01) |
| Staging UI | **`https://lite.nightloch.app`** until apex cutover |
| Production UI | **`https://nightloch.app`** (Slice 11 cutover) |
| AGH ops UI | **`https://agh.nightloch.app`** (operator + basic auth) |
| WireGuard endpoint | **`vpn.nightloch.app:51820`** |
| Email | **Resend** from **`Nightloch <alerts@nightloch.app>`** (monitored; SPF/DKIM/DMARC) |
| Captcha | **Cloudflare Turnstile** |
| IaC | **OpenTofu** (DO + Cloudflare) |
| CI/CD | **GitHub Actions** → health-gated binary deploy |
| Repo | Public `JacobStephens2/nightloch-lite` (MIT) |
| Backups | **DO daily backups** + **forced-command pull** of encrypted SQLite to stephens.page |

### Architecture sketch

```text
Android/Mac ──WG full tunnel──► vpn.nightloch.app:51820
                                    │
                         DO nyc3 droplet
                         ├── wg0 + NAT
                         ├── REDIRECT :53 → AGH; REJECT :853
                         ├── AdGuard Home (DNS on 10.66.0.1 only)
                         ├── nightloch-lite (single-writer reconciler)
                         └── Caddy → lite / agh

OpenTofu · GHA · Resend · external dead-man · stephens backup pull
```

---

## Accounts and partners

- **Open signup** via magic link (15 min single-use; sessions 30 days; tokens **hashed at rest**).
- Turnstile + rate limits (per email + IP).
- Account = person; **no global role at signup**.
- **Peer** partner edges via email invite → accept (`edge_type=peer`; `guardian` reserved, not implemented).
- **Device enrollment gated** (operator allowlist in v1). Signup/partner-accept remain open.
- Dogfood: Operator on Android + MacBook; two emails; a second Protectee later as **peer** unless reclassified.

### Partner powers (active edge on protectee S)

- View status / coverage / enforcement health  
- Tighten or loosen S’s policy; approve allowlist requests  
- Revoke S’s devices  
- View DNS domain log if log-sharing enabled on that edge  
- **See the Protectee’s Roster** (who else protects them; not edit others’ Partnerships)  

Partners may **not:** delete S’s account; use AGH admin UI; manage other partners’ edges.

### Log sharing

- Checkbox on invite; toggle later; **email partner when disabled**.  
- Protectee may view own domain log.

### Partner lifecycle

Authoritative detail: [SECURITY-POLICY-DESIGN §3](./docs/SECURITY-POLICY-DESIGN.md#3-partner-perimeter-rules).

| Action | Rule |
|---|---|
| Invite / accept | Notify **all existing** Partners on the Protectee’s Roster; they can see the updated Roster |
| Partner ends edge | **Immediate** |
| Protectee ends edge | **72h cooling-off** (see design doc) |
| Device revoke | **Immediate** (including last device) |
| Log-share disable | **Immediate** (consent exemption; email partner) |
| Pending invite | Accept, **decline**, or expire in **7 days** |
| Zero active partners | Protectee may **self-loosen**; nudge “add a partner” |

### Account deletion

Protectee may delete account via **72h cooling-off** + separate per-recipient notices to related parties. Starting deletion requires typing `DELETE` after an exact consequence summary, immediately ends every Partnership in which the deleting User is the Partner, cancels all pending invitations involving them, and prevents new invitation sends or acceptances; canceling deletion restores none of these. Ordinary rules continue for the deleting User’s Policy, Devices, and Log sharing, while only that User may cancel the fixed timer. The deleting User and active Partners protecting them see the exact deadline. Those Partnerships and Partners’ ordinary powers remain active until deletion completes, followed by transactional teardown of Devices, AGH clients, IPs (quarantine), and those remaining Partnerships. Completion email is attributable; retained former-Partner receipts use **Deleted User**.

---

## Policy model

| Kind | Behavior |
|---|---|
| **Baseline (OISD NSFW)** | Global subscription for all clients; **cannot** disable wholesale per user |
| **Baseline extras** | User may **add** block domains (per-client rules) |
| **Allowlist** | User **requests**; **partner approves** (or self if unpartnered); per-client `@@` rules |
| **Curfews** | Timed blocks; e.g. Instagram + subdomains **20:00–08:00** `America/New_York` |
| **Asymmetry (partnered)** | Protectee edits allowed only if **at-least-as-restrictive** (predicate, H=14d); partners may loosen |
| **Asymmetry (unpartnered)** | Protectee may loosen freely |
| **Passes** | None in v1 |
| **New account** | Global baseline on; no auto Instagram curfew; no devices until allowlisted enroll |

Full predicate and examples: [docs/SECURITY-POLICY-DESIGN.md](./docs/SECURITY-POLICY-DESIGN.md) §2.

### Honest product limits

- **DNS domain names only** — not URL paths or search terms (no TLS MITM).  
- **Encrypted DNS / determined bypass** on owned hardware is **mitigated, not eliminated** (REDIRECT 53, REJECT 853, bypass lists).  
- **VPN off** removes all filtering.  
- **Server unreachable** with full tunnel typically **blackholes** the device until the user disables the VPN (not “fail open”).  
- **DNS-level app blocks** (e.g. Instagram) can be partial due to CDNs and client caches.  
- **Mutual peer edges** make asymmetry cosmetic by mutual consent: if A and B partner each other, either can loosen the other on request (inherent to peer accountability, same class as Covenant Eyes).  
- **A colluding Partner can loosen immediately.** Adding that Partner is visible to the existing Roster but does not require their approval in v1. Nightloch provides signal and a delay on removing independent Partners; it does not prevent collusion or a Protectee from turning off the VPN.  
- **Solo dogfood** (one human, two inboxes) validates plumbing, not that a real second person will honor cooling-off or notice partner-add alerts.

---

## Runtime behavior

| Topic | Decision |
|---|---|
| Reconcile | Boundary scheduler (blocks ~60s early) + 5‑minute backstop + kick on write; single-writer AGH `set_rules`; read-back drift check |
| Integrity | **Alert on actions**; **coverage reports** while any device enrolled; **external dead-man** |
| Fail mode | VPN off → open; path/server down → client blackhole until VPN disabled |
| Multi-tenant DNS | Unique WG IP per peer; `$client` rules; query-log exact-IP re-filter; IP quarantine ≥ retention+1d |
| Query logs | AGH API; retention **7 days** (set explicitly); not primary-copied to SQLite |
| WG | `10.66.0.1/24`, 51820/udp, keepalive 25; helper+sudoers; IPv6 finalized after Slice 4 test |
| NAT / DNS force | Checklist: forward, MASQUERADE, REDIRECT :53, REJECT :853 |
| Enroll | Allowlisted self-serve; email partners; UI explains blackhole-on-outage |

---

## Ownership split

| Owner | Responsibility |
|---|---|
| **OpenTofu** | Droplet (daily backups policy), firewall, SSH, CF records (`lite`/`nightloch`/`agh`/`vpn` unproxied), cloud-init, Resend DNS for nightloch.app |
| **GitHub Actions** | Build/deploy binary; migrate step; health-gated rollback |
| **nightloch-lite** | Users, partners, policy, peers, AGH projection, action alerts, coverage, health pings |
| **stephens.page** | Forced-command backup pull only |
| **Operator** | Enroll allowlist, AGH UI (read-only for rules), NAT checklist, dead-man config |

Secrets: env/CI only; hash sessions/magic-links/invites; encrypt backups before they leave the droplet.

---

## Cutover notes

- Build on **`lite.nightloch.app`**; cut **`nightloch.app`** in Slice 11 (TTL 60s day prior).  
- Cloudflare records for WG/product origin stay **unproxied**.  
- Re-verify `BASE_URL`, cookies, Turnstile at cutover.

---

## Explicit non-goals (v1)

- Cloudflare Gateway as enforcer  
- TLS MITM / search-term accountability  
- Temporary passes  
- Custom mobile apps  
- Enforcement on stephens.page  
- Guardian edge UX (column only)  
- Postgres / multi-region HA  
- Partner-must-approve-new-partner (optional later; Roster visibility + alert is v1)

---

## Confirmation

- **2026-07-28:** Owner confirmed shared understanding.  
- **2026-07-28:** Slice **−1** applied adversarial-review amendments into this freeze + [docs/SECURITY-POLICY-DESIGN.md](./docs/SECURITY-POLICY-DESIGN.md).  
- Further implementation: follow [plan.md](./plan.md) from Slice **0**.
