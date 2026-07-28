# Nightloch Lite — Product & Architecture Decisions

Interview decisions for greenfield nightloch-lite.  
**Status:** Decisions locked through Q59. **Shared understanding confirmed** 2026-07-28.  
**Implementation:** Not started (await separate go-ahead).  
**Last updated:** 2026-07-28

Canonical freeze narrative: [shared-understanding.md](./shared-understanding.md).

---

## Product intent

**Job:** DNS-level domain blocking on a schedule, changeable only by accountability partners (asymmetric control), self-hosted, simpler than original Nightloch.

**Not in v1:** TLS MITM / search-term visibility, temporary passes, custom iOS/macOS apps, Cloudflare Gateway as enforcer.

---

## Locked decisions (by interview number)

### People & testing

| # | Topic | Decision |
|---|---|---|
| 1 | First protected subject | **The Operator** as first Protectee; devices **Android + MacBook** |
| 2 | Partner control for test | Two separate accounts for dogfood (restricted + partner) |
| 25–27 | Multi-user model | **Full Covenant Eyes–style graph in v1**: accounts are people; partner edges via invite; open signup |
| 26 | Signup vs partner role | No global “restricted vs partner” at register; partner = accepted invite edge |
| 27 | Abuse bar | Magic link + rate limits + **Cloudflare Turnstile** |
| 23–24 | Superseded | Shared invite code + role-at-signup — **replaced** by open signup + graph |

### Auth & sessions

| # | Topic | Decision |
|---|---|---|
| 3 | Auth | **Email magic link** via **Resend** |
| 39 | From address | `Nightloch <nightloch@stephens.page>` |
| 40 | Magic-link token | **15 minutes, single-use** |
| 20 | Session length | **30 days** |
| 35–36 | DNS log consent | Invite **checkbox** for log sharing; per-edge **toggle** later; **email partner when logs turned off** |
| 41 | Own logs | User **can** see own DNS domain log |

### Enforcement path

| # | Topic | Decision |
|---|---|---|
| 4 | Device path | **WireGuard** on all protected devices |
| 5 | Tunnel scope | **Full tunnel** (`AllowedIPs = 0.0.0.0/0`) |
| 6 | Fail mode | **No kill-switch** productization; VPN-off = unprotected; integrity alerts |
| 7 | DNS enforcer | **AdGuard Home** |
| 8 / 42 / 45 | AGH admin | Ops-only UI at **`https://agh.nightloch.app`**; **Apache Basic Auth was stephens-era** — on dedicated box use **Caddy + Basic Auth** (or equivalent) in front of AGH; AGH own password also set. **Not** a product user surface |
| 18–19 | Peers | Restricted user **self-enrolls** devices; **email partner on every enroll** |
| 32 | Multi-user isolation | **Per-user WG IP + AGH per-client rules** |
| 33 | Query log retention | **No copy into SQLite**; partner/user UI via **AGH API**; AGH retention **~7 days** |
| 44 | WG privileges | App as non-root; **small root helper + sudoers** for `wg` add/remove/show on `wg0` only |
| 38 | WG network | `wg0`, `10.66.0.1/24`, clients `10.66.0.2+`, listen **51820/udp**, DNS `10.66.0.1`, **IPv6 off** |
| 50 | Host NAT | Full-tunnel **ip_forward + MASQUERADE** required; apply only after **written ops checklist** approval |
| 57 | WG endpoint DNS | **`vpn.nightloch.app:51820`** (not the product apex) |

### Policy (curfews & lists)

| # | Topic | Decision |
|---|---|---|
| 9–10 | First curfew | `instagram.com` + **subdomains**, **20:00–08:00** `America/New_York`, no guard skew |
| 11 | Asymmetry | Restricted may **tighten**; only **partner** may **loosen** |
| 12 | Passes | **None** in v1 |
| 28 | Partner powers | View status, tighten/loosen curfews, revoke devices; **view DNS domain logs** when sharing on; not manage other partners / not delete account |
| 29 | Visibility depth | **B + E**: DNS domain logs + porn domain lists; **no** search-term / MITM in v1 |
| 30 | List timing | **C**: always-on **baseline** (porn list) + **timed curfews** (e.g. Instagram nights) |
| 31 / 49 | Baseline list | Strategy C (feed + user add / partner remove); concrete feed **`https://nsfw.oisd.nl/`** (OISD NSFW) |
| 37 | New account defaults | Baseline list **on**; no auto Instagram curfew; devices/partners empty |

### Alerts & reconcile

| # | Topic | Decision |
|---|---|---|
| 13 | Email alerts | Integrity only: loosen, AGH unreachable, drift, WG peer quiet, enroll, log-sharing off, etc. No digests in v1 |
| 21 | Reconcile cadence | Every **5 minutes** + **immediate** on curfew/device write |
| 22 | Quiet peer | **1 hour** no handshake during active curfew; dedupe; honest “may have been interrupted” copy |
| 34 | Partner lifecycle | Email invite → explicit accept; either party ends edge; 7-day pending expiry; email both on end |

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

**Asymmetry:** tighten free; loosen / disable baseline / remove domains = partner of that user only.

**Honest limits:** DNS domains only (not URL params / search terms); VPN off fails open; partner alerts.

---

## Explicit non-goals (v1)

- Cloudflare Zero Trust / Gateway as enforcer  
- Search query / TLS MITM inspection  
- Temporary network passes  
- Native iOS/macOS Nightloch apps  
- Running enforcement on stephens.page  
- Multi-instance HA / Postgres  

---

## Pending after shared understanding

1. Owner go-ahead to **implement** (scaffold, OpenTofu, app, deploy)  
2. Live droplet NAT/firewall only after ops checklist approval

---

## Superseded along the way

| Earlier lean | Final |
|---|---|
| stephens.page hosts everything | Dedicated DO droplet |
| Hetzner for price | DigitalOcean (US company) |
| Signup invite code + role pick | Open signup + partner graph |
| Partner-only or manual WG | Restricted self-enroll + partner email |
| Cloudflare Gateway | Self-hosted AGH + WG |

---

## File history

- Created from design interview (2026-07-28).  
- Update this file when decisions change; do not silently diverge in code.
