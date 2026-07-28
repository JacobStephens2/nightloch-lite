# Nightloch Lite — Vertical-Slice Implementation Plan

**Status:** Plan only (shared understanding confirmed; implement on go-ahead)  
**Canonical product freeze:** [shared-understanding.md](./shared-understanding.md)  
**Decision log:** [DECISIONS.md](./DECISIONS.md)  
**As of:** 2026-07-28

---

## How to read this plan

Work proceeds as **vertical slices**: each slice ships a thin path through the stack that is **demoable end-to-end**, not a horizontal layer (“all DB tables,” then “all UI”).

| Rule | Meaning |
|---|---|
| **Done means demoable** | Someone can click/SSH/use a device and see the outcome |
| **Thin then thicken** | Prefer the smallest real path; polish and edge cases in later slices |
| **No slice depends on unfinished “frameworks”** | Shared packages appear only when a slice needs them |
| **Ops gates stay explicit** | Live NAT / DNS cutover / droplet create need human approval even mid-plan |

Suggested demo account pair for dogfood: two emails you control (protected + partner).

---

## Target architecture (reminder)

```text
Android/Mac ──WG full tunnel──► vpn.nightloch.app:51820
                                    │
                         DO nyc3 droplet (s-1vcpu-2gb)
                         ├── wg0 + NAT
                         ├── AdGuard Home (per-client DNS)
                         ├── nightloch-lite :127.0.0.1
                         └── Caddy → nightloch.app, agh.nightloch.app

OpenTofu: droplet, firewall, SSH key, CF DNS (unproxied), cloud-init
GitHub Actions: build/deploy binary
Resend + Turnstile: auth & alerts
stephens.page: backup pull
```

---

## Slice map (overview)

| Slice | Name | Demo when done |
|---|---|---|
| **0** | Repo skeleton | `go test` / empty module builds locally |
| **1** | Live origin | `https://nightloch.app` (or temporary IP + CF grey-cloud) serves a page via Caddy on the droplet |
| **2** | Sign in | Request magic link → email → session cookie → “you’re signed in” |
| **3** | Partner edge | A invites B → B accepts → A sees partner; B sees “protecting A” |
| **4** | Tunnel + baseline block | Phone on WG resolves through AGH; OISD NSFW domain fails; Instagram still works outside curfew |
| **5** | Device enroll in-app | User creates peer in UI → installs config → lite maps IP ↔ user; partner emailed |
| **6** | Curfew + asymmetry | Instagram blocked 20:00–08:00 ET; user can tighten; partner can loosen; reconcile applies to AGH |
| **7** | Domain logs | User (and partner if sharing on) sees AGH query log for their clients |
| **8** | Integrity alerts | Quiet peer / drift / loosen / enroll / log-share-off emails fire correctly |
| **9** | Continuous deploy | Push to `main` → binary on droplet restarted |
| **10** | Backups | DO backups on; stephens.page holds a recent SQLite snapshot |
| **11** | Hardening & cutover | Old nightloch.app stack retired; ops checklist complete; dogfood week |

Slices **0–1** are infrastructure-first but still vertical (code/IaC → real HTTPS).  
Slices **4–6** are the product core.  
**11** is exit criteria for “v1 dogfood ready,” not infinite polish.

---

## Slice 0 — Repo skeleton

**Goal:** Private repo is a real Go module with layout, lint/test hooks, and docs already present.

### Deliverables

- `go.mod` module path (e.g. `github.com/JacobStephens2/nightloch-lite`)
- Layout sketch:
  - `cmd/nightloch/` — main
  - `internal/...` — app packages (empty or stubs)
  - `infra/opentofu/` — empty stack placeholders
  - `deploy/` — systemd unit template, Caddyfile template, `nightloch-wg` helper stub
  - `.github/workflows/` — placeholder CI (build only, no deploy secrets yet)
- `Makefile` or scripts: `test`, `build` (`GOOS=linux GOARCH=amd64`)
- Keep [shared-understanding.md](./shared-understanding.md), [DECISIONS.md](./DECISIONS.md), this [plan.md](./plan.md)

### Demo / done

```bash
go test ./...
go build -o bin/nightloch ./cmd/nightloch
```

### Out of scope

Droplet, real auth, AGH.

---

## Slice 1 — Live origin (OpenTofu → Caddy)

**Goal:** Dedicated DO droplet exists; Cloudflare grey-cloud names point at it; Caddy terminates TLS.

### Deliverables

- OpenTofu:
  - `digitalocean_droplet` (`nyc3`, `s-1vcpu-2gb`, backups enabled when ready)
  - Firewall: 22/tcp, 80/tcp, 443/tcp, **51820/udp**
  - SSH key
  - Cloudflare A records **unproxied**: `nightloch.app`, `agh.nightloch.app`, `vpn.nightloch.app` (may stage on a temporary hostname if cutover deferred)
- cloud-init / provision:
  - Non-root deploy user
  - Caddy installed; minimal site “Nightloch” OK page on `nightloch.app`
  - Placeholder reverse proxy for future app port
- **Ops checklist (draft)** for later NAT (do not enable full-tunnel NAT until checklist signed off)
- Document secrets: `DIGITALOCEAN_TOKEN`, `CLOUDFLARE_API_TOKEN`, SSH private key for apply

### Demo / done

Browser opens `https://nightloch.app` (or agreed staging host) with valid cert and static OK page.  
`dig +short vpn.nightloch.app` returns droplet IP; Cloudflare proxy is off.

### Gates

- Human runs `tofu apply` (or agent with explicit approval).
- If old Nightloch still owns apex: use staging hostname until Slice 11, **or** cut apex only when ready.

### Out of scope

App binary, WireGuard peers, AGH rules, magic links.

---

## Slice 2 — Sign in (magic link)

**Goal:** Full auth loop on the live (or local-with-tunnel) origin.

### Deliverables

- SQLite open/migrate: `users`, `magic_link_tokens`, `sessions`
- HTTP handlers + minimal HTML:
  - “Enter email” + Turnstile
  - Consume link → set session cookie (HttpOnly, Secure, SameSite)
- Resend client: From `Nightloch <nightloch@stephens.page>`
- Token: 15 minutes, single-use; session: 30 days
- Rate limit: per email + per IP
- Config via env (Resend key, Turnstile keys, `BASE_URL`, session secret)
- systemd unit running lite on `127.0.0.1:PORT`; Caddy proxies `/` to it

### Demo / done

Two different browsers: request link on each email → land on authenticated home (“signed in as …”). Sign out works.

### Out of scope

Partners, devices, blocking.

---

## Slice 3 — Partner graph

**Goal:** CE-style invite/accept/end without any enforcement yet.

### Deliverables

- Tables: `partner_edges`, `partner_invites` (token, log_sharing_requested, expires_at)
- Flows:
  - Invite by email (checkbox: DNS log sharing)
  - Accept (login/create via magic link if needed)
  - List “my partners” / “people I protect”
  - Either party ends edge; email both
  - Pending invite expiry 7 days (cron or on-read cleanup)
- Authorization helpers: `CanLoosen(actor, subject)`, `CanViewLogs(actor, subject)`

### Demo / done

Email A invites B with log-sharing on → B accepts → A sees B as partner → B opens “protecting A”. End edge → both lose access; emails sent (or logged in dev if mail mocked).

### Out of scope

Curfew mutations, devices.

---

## Slice 4 — Tunnel + baseline block (enforcement spine)

**Goal:** Full-tunnel WireGuard + AGH blocks OISD NSFW for a **manually** created peer; product may still be thin.

This is the first **protection** slice. Prefer getting packets right before automating peers in-app.

### Deliverables

- Host:
  - WireGuard `wg0` `10.66.0.1/24`, listen `51820`
  - **Ops checklist executed:** `ip_forward`, NAT/MASQUERADE, firewall confirmed not breaking Caddy
  - AGH listening DNS **only on `10.66.0.1`** (not public open resolver)
  - OISD NSFW list `https://nsfw.oisd.nl/` enabled
  - AGH admin behind Caddy + basic auth at `agh.nightloch.app`
- One **operator-minted** peer for Android (and optionally Mac)
- Client config: endpoint `vpn.nightloch.app:51820`, DNS `10.66.0.1`, AllowedIPs full tunnel
- `nightloch-wg` helper + sudoers stub (even if only used by operator scripts at first)
- Smoke script: `dig @10.66.0.1` from server; from phone: known adult domain fails, general web works

### Demo / done

Phone: WG on → adult domain from list does not load; WG off → loads (fail-open honesty).  
Mac optional same day.

### Gates

- Explicit approval before enabling NAT on the droplet.
- Confirm no public recursion on `0.0.0.0:53`.

### Out of scope

In-app enroll, timed Instagram curfew, multi-user automation (single test peer OK).

---

## Slice 5 — Device enroll in-app

**Goal:** Protected user mints a peer in the UI; lite owns IP allocation and AGH client identity.

### Deliverables

- Tables: `devices` / `wg_peers` (user_id, public_key, ipv4, name, created_at)
- UI: list devices, “Add device” → one-time config/QR display
- Call `nightloch-wg` helper: add peer, show dump for handshakes
- On enroll: create AGH “client” (or equivalent) bound to that IP
- Apply **per-user baseline** desired state for that client (OISD + custom adds)
- Email all partners of user on enroll
- Partner (and user) can revoke device → remove peer + AGH client mapping

### Demo / done

Signed-in user adds “Android” → installs config without SSH → tunnel works → partner receives enroll email → revoke removes access.

### Out of scope

Curfew schedule UI (baseline only is enough if Slice 4 list is on).

---

## Slice 6 — Curfews + tighten/loosen + reconcile

**Goal:** Timed Instagram curfew works; asymmetry enforced; reconciler is source of truth for AGH client rules.

### Deliverables

- Tables: `curfews`, `curfew_targets`, `baseline_extra_domains`, `baseline_enabled`
- Policy engine:
  - Canonicalize domains; subdomain match mode
  - Timezone-aware windows (cross-midnight); `America/New_York` default example
  - Classify edits: tighten vs loosen
- AuthZ: subject may tighten own; partner of subject may loosen/tighten
- UI: create/edit curfew; add baseline domain; partner disable baseline / remove domain
- Reconciler:
  - Desired rules per client from baseline + active curfews
  - Run every 5 minutes + kick on write
  - Idempotent AGH API updates
- Seed/template: Instagram + subdomains, 20:00–08:00 ET (user applies, not forced at signup)
- New users: baseline on by default

### Demo / done

Inside window: `instagram.com` blocked on enrolled device.  
Outside window: allowed.  
Protected user cannot shorten window; partner can.  
User can add a domain to baseline; cannot remove OISD without partner.

### Out of scope

Passes, digests, MITM.

---

## Slice 7 — Domain logs

**Goal:** Query log visibility without copying into SQLite.

### Deliverables

- Lite proxies/filters AGH query log API by client IPs for a user
- UI: “Recent domains” for self
- Partner view only if edge `log_sharing` enabled
- Toggle log sharing; email partner on disable
- Document AGH retention ~7 days

### Demo / done

Browse a few sites over WG → domains appear for user. Partner with sharing on sees them; after toggle off + email, partner UI denies log access but can still loosen curfews.

---

## Slice 8 — Integrity alerts

**Goal:** Fail-open stays honest via email.

### Deliverables

- Event/audit table (append-only enough for dedupe keys)
- Alerts (Resend):
  - Curfew/baseline loosened (by whom)
  - Device enrolled / revoked
  - Log sharing disabled
  - AGH unreachable / reconcile failure
  - Desired ≠ enforced drift (if detectable)
  - WG peer quiet ≥ 1 hour during active curfew (per peer per night dedupe)
- Status page: last reconcile OK, peer last handshake (from `wg show`)

### Demo / done

Disable WG for >1 hour in curfew window → partner email with “may have been interrupted”.  
Partner shortens curfew → integrity email.  
Force AGH down briefly → failure alert (or next reconcile cycle).

---

## Slice 9 — Continuous deploy

**Goal:** Main branch ships the binary without manual scp rituals.

### Deliverables

- GitHub Actions: test + `GOOS=linux GOARCH=amd64` build on push to `main`
- Deploy job: rsync/scp binary, restart systemd (deploy key / SSH secret)
- Document required secrets and rollback (previous binary keep)

### Demo / done

Trivial version string change → push → production `/health` or footer shows new version within minutes.

---

## Slice 10 — Backups

**Goal:** Survive droplet loss without losing SQLite.

### Deliverables

- DO droplet backups remain enabled in OpenTofu
- stephens.page: cron **pull** consistent SQLite snapshot (and optional encrypted secrets/WG server private key)
- Retention: small N (disk-aware)
- Restore runbook: new droplet from OpenTofu + restore DB + redeploy binary

### Demo / done

Fresh snapshot file appears on stephens after cron; restore dry-run documented once.

---

## Slice 11 — Hardening & cutover

**Goal:** Dogfood-ready v1 on real names; old stack retired carefully.

### Deliverables

- Final CF cutover for `nightloch.app` if still staged
- Security pass: cookies, CSRF on POSTs, Turnstile, no open resolver, AGH not public without auth
- Load/failure notes: VPN off behavior documented in UI
- Dogfood checklist: Android + Mac, two accounts, Instagram night window, partner loosen drill
- Optional: second peer, false-positive handling for OISD (partner allow path)

### Demo / done

One full week of personal use without SSH surgery; partner can help loosen when needed; backups green.

### Exit criteria for “v1 dogfood complete”

- [ ] Magic link auth works on phone browser  
- [ ] Partner invite/accept works  
- [ ] Full tunnel + baseline NSFW block  
- [ ] Device enroll without server SSH  
- [ ] Instagram curfew enforces on schedule  
- [ ] Partner-only loosen proven  
- [ ] Domain logs for self (+ partner when shared)  
- [ ] Quiet-peer email observed once in test  
- [ ] GHA deploy works  
- [ ] Backup file on stephens + DO backups on  

---

## Cross-cutting concerns (apply as slices need them)

| Concern | Approach |
|---|---|
| Config | Env vars / dotenv for local; systemd `EnvironmentFile` in prod |
| Migrations | Simple SQL files or embed migrations; run on boot |
| Logging | Structured logs to journald; no query bodies in app logs |
| Secrets | Never commit; GHA + server env; Resend via existing patterns where possible |
| Testing | Unit: policy tighten/loosen, schedule windows; integration: AGH client fake; few smoke scripts on server |
| UI | Server-rendered HTML first (Go templates); no SPA requirement |
| AGH API | Thin client interface; mockable for tests |
| Idempotency | Reconcile safe to re-run; peer add rejects duplicates |

---

## Suggested sequencing & parallelization

```text
0 → 1 → 2 → 3
         ↘
           4 → 5 → 6 → 7 → 8
1 ──────────────────────────→ 9
6+ ─────────────────────────→ 10 → 11
```

- **2 and 3** before any multi-user enforcement.  
- **4** can start after **1** (even parallel to 2–3 if operator-driven) but productize peers only in **5**.  
- **9** can land once **2** runs under systemd (deploy early, iterate faster).  
- **10** after SQLite has real data (**2+**).  
- **11** last.

---

## Explicitly deferred (do not pull into early slices)

- Temporary passes  
- TLS MITM / search terms  
- Native apps  
- Digests / SMS  
- Postgres  
- Multi-region HA  
- Fancy admin beyond AGH basic auth  

---

## Implementation go-ahead

This file is the **implementation plan**, not authorization to run `tofu apply` or spend money.

When implementing:

1. Complete slices in order unless a dependency allows parallel work above.  
2. Update [current-decision.md](./current-decision.md) only if product decisions change (prefer amending DECISIONS + shared-understanding).  
3. Prefer smallest demo that proves the slice before expanding UI polish.

**Start command for humans/agents:** begin at **Slice 0**, then **Slice 1** with explicit cloud apply approval.
