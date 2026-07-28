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
