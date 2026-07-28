# Nightloch Lite — Shared Understanding

**Status:** **Shared understanding confirmed** (2026-07-28)  
**Implementation:** Not authorized until owner says to proceed  
**Decision log:** [DECISIONS.md](./DECISIONS.md)  
**Current pointer:** [current-decision.md](./current-decision.md)

---

## One-paragraph product

Nightloch-lite is a **Go** control plane on a **dedicated DigitalOcean** droplet (`nyc3`, 2 GB) that drives **AdGuard Home** over **WireGuard full tunnel**. Users sign up with **magic links** (Resend + Turnstile), invite **accountability partners** (Covenant Eyes–style relationship graph), self-enroll devices, and run an **always-on OISD NSFW baseline** plus **timed curfews** (e.g. Instagram 8 pm–8 am Eastern). Only partners may loosen restrictions. DNS **domain** logs are visible with consent. No TLS MITM / search-term capture, no temporary passes, no custom mobile apps in v1. **OpenTofu** owns cloud + DNS + OS bootstrap; **GitHub Actions** deploys the binary; backups are **DO droplet backups + pull to stephens.page**.

---

## Problem this solves

DNS-level domain blocking on a schedule (and always-on adult baseline), with asymmetric control: the protected person cannot loosen their own locks; accountability partner(s) can. Self-hosted, simpler than the original multi-tenant Nightloch + Cloudflare Gateway control plane.

---

## Stack and hosting

| Layer | Choice |
|---|---|
| Control plane | **Go** + **SQLite** |
| DNS enforcer | **AdGuard Home** |
| Device path | **WireGuard full tunnel** (stock client apps only) |
| Host | **Dedicated DigitalOcean droplet** (not stephens.page) |
| Size / region | **`s-1vcpu-2gb`**, **`nyc3`** (~$12/mo list) |
| TLS / reverse proxy | **Caddy** (automatic HTTPS) |
| Public DNS | **Cloudflare DNS**, managed by OpenTofu; records **unproxied** (grey cloud) |
| Product UI | `https://nightloch.app` |
| AGH ops UI | `https://agh.nightloch.app` (operator only; auth in front of AGH) |
| WireGuard endpoint | `vpn.nightloch.app:51820` |
| Email | **Resend**; From: `Nightloch <nightloch@stephens.page>` |
| Captcha | **Cloudflare Turnstile** on magic-link request |
| IaC | **OpenTofu** (`digitalocean` + `cloudflare` providers) from day one |
| CI/CD | **GitHub Actions** → build `linux/amd64` → SSH/rsync → systemd restart |
| Repo | Private `JacobStephens2/nightloch-lite` |
| Backups | **DigitalOcean droplet backups** + **pull** SQLite (and optional encrypted secrets/WG server key) to **stephens.page** |

### Architecture sketch

```text
Devices (WireGuard full tunnel)
    │ UDP 51820 → vpn.nightloch.app  (Cloudflare DNS, unproxied)
    ▼
DigitalOcean droplet (nyc3, s-1vcpu-2gb)
    ├── wg0 10.66.0.1/24  + NAT to internet
    ├── AdGuard Home (DNS on wg0 only; per-client rules)
    ├── nightloch-lite (Go, SQLite, localhost)
    └── Caddy → nightloch.app, agh.nightloch.app

OpenTofu: droplet, firewall, SSH key, CF A records, cloud-init
GitHub Actions: deploy binary
Resend: magic links + integrity alerts
stephens.page: backup pull target
```

---

## Accounts and partners

- **Open signup** via email magic link (token: **15 minutes, single-use**).
- Session duration: **30 days**.
- Rate limits + **Turnstile** on request-link.
- **No global role at signup.** An account is a person.
- **Partner relationship:** user A emails invite → B accepts → edge “B is partner for A.”
- Same person may **have** partners and **be** a partner for others (CE / Victory Shield style).
- **Dogfood first:** Operator on **Android + MacBook**; two emails for restricted + partner testing; a second Protectee later.

### Partner powers (for a user they protect)

- View status / enforcement health  
- Tighten or loosen curfews  
- Revoke enrolled devices  
- View **DNS domain query log** when log-sharing is enabled for that edge  

Partners may **not:** manage the user’s other partners, delete the user’s account, or use AGH as a product surface.

### Log sharing

- Explicit **checkbox** when sending a partner invite.  
- Per-edge **toggle** afterward (partner may still loosen without logs).  
- **Email partner** when the protected user **disables** log sharing.  
- Protected user **can** view their own domain log.

### Partner lifecycle

- Email invite → explicit accept.  
- Either party may end the relationship; notify the other.  
- Pending invites expire in **7 days**.

---

## Policy model

| Kind | Behavior |
|---|---|
| **Baseline** | Always-on **OISD NSFW** list: `https://nsfw.oisd.nl/` |
| **Baseline edits** | User may **add** domains; only **partner** may remove/disable baseline |
| **Curfews** | Timed blocks; first intended example: `instagram.com` + subdomains, **20:00–08:00** `America/New_York` |
| **Asymmetry** | Restricted user may **tighten**; only partner may **loosen** |
| **Passes** | **None** in v1 |
| **New account defaults** | Baseline **on**; no automatic Instagram curfew; no devices/partners until enrolled/invited |

### Honest product limit

Enforcement and partner visibility are **DNS domain names only**.  
Search engine query strings and URL paths are **out of scope** (no TLS MITM in v1).

---

## Runtime behavior

| Topic | Decision |
|---|---|
| Reconcile | Every **5 minutes** + immediately on curfew/device writes |
| Quiet peer alert | **1 hour** without WG handshake during an active curfew; dedupe; copy: “may have been interrupted” |
| Fail mode | **No kill-switch**; VPN off or path down → fail **open**; integrity emails |
| Multi-user isolation | Unique WG client IP per peer; **AGH per-client** rules |
| Query logs | Served via **AGH API** (authorized by lite); AGH retention ~**7 days**; not primary-copied into SQLite |
| WG addressing | `wg0`, server `10.66.0.1/24`, clients `10.66.0.2+`, port **51820/udp**, DNS `10.66.0.1`, **IPv6 off** |
| WG privileges | App runs unprivileged; **small root helper + sudoers** for allowlisted `wg` ops on `wg0` |
| NAT | `ip_forward` + MASQUERADE required for full tunnel; apply only after **ops checklist** approval |
| Device enroll | Restricted user **self-enrolls**; **email partner** on every enroll |
| Integrity email | Loosen, drift, AGH down, quiet peer, enroll, log-sharing off, etc. **No digests** in v1 |

---

## Ownership split

| Owner | Responsibility |
|---|---|
| **OpenTofu** | Droplet, firewall (22, 80, 443, UDP 51820), SSH key, Cloudflare A records (`nightloch.app`, `agh.nightloch.app`, `vpn.nightloch.app` unproxied), cloud-init bootstrap (user, Caddy, AGH, WireGuard packages) |
| **GitHub Actions** | Build and deploy Go binary; restart service |
| **nightloch-lite** | Users, sessions, partner graph, curfews, peer lifecycle, AGH reconcile, alerts |
| **stephens.page** | Backup pull target; not on the VPN data path |
| **Operator** | AGH admin UI, secrets, checklist before NAT/firewall production changes |

Secrets live in CI/env (and sops-style patterns where reused); **not in git**.

### Backups (detail)

- Enable **DigitalOcean droplet backups** (whole-VM restore).  
- **stephens.page** periodically **pulls** a consistent SQLite snapshot (optional encrypted WG server key / env); limited retention (stephens disk is tight).  
- Prefer pull-from-stephens over broad push rights onto stephens.  
- Backup paths must not be web-reachable.

---

## Cutover notes

- **`nightloch.app`:** leave existing/old stack until lite is ready; then point Cloudflare A (unproxied) at the new droplet.  
- Cloudflare **orange-cloud proxy must stay off** for names used as WireGuard endpoints (and generally for this design’s direct Caddy origin).  
- stephens.page remains for mail secrets habits and backups only.

---

## Explicit non-goals (v1)

- Cloudflare Zero Trust / Gateway as the enforcer  
- TLS MITM, full-URL, or search-term accountability  
- Temporary network “passes”  
- Custom iOS / macOS Nightloch apps  
- Running enforcement (WG/AGH/NAT) on stephens.page  
- PostgreSQL / LISTEN-NOTIFY as a requirement  
- Multi-region HA  

---

## Confirmation

- **2026-07-28:** Owner confirmed **shared understanding** (this document + [DECISIONS.md](./DECISIONS.md)).
- **Implementation** remains a separate go-ahead (“you may implement” / equivalent).
