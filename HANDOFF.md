# Session handoff — Nightloch Lite

**Date:** 2026-07-30  
**Repo:** https://github.com/JacobStephens2/nightloch-lite (private)  
**Local path:** `/Users/jacob/GitHub/JacobStephens2/nightloch-lite`  
**Related (do not confuse):** `/Users/jacob/GitHub/JacobStephens2/nightloch` is the **old** bloated product — greenfield is **nightloch-lite** only.

> **2026-08-06 update:** Slice 2 magic-link auth is code-complete: SQLite forward migration, hashed single-use links/sessions, Resend, Turnstile, persistent email/IP quotas, `__Host-` cookies, CSRF, sign-out, and writable-DB `/health`. Slice 9 is also code-complete: consistent pre-migration snapshot, serialized artifact deploy, explicit migration, 30-second health gate, and binary rollback. Live demos wait on the Slice 1 origin and provider/GitHub environment configuration; see `docs/ops/SLICE-2-AUTH.md` and `docs/ops/SLICE-9-DEPLOY.md`. Slice 3 is next and begins with its required full experience grill.

This handoff is for a **fresh agent**. Do **not** re-derive product design from chat history. Read artifacts below.

---

## Suggested skills

Invoke these when the matching work starts (user-level under `~/.grok/skills/` unless noted):

| Skill | When |
|---|---|
| **`/domain-modeling`** | Any glossary, UI label, or authZ role naming change — update [CONTEXT.md](./CONTEXT.md) immediately |
| **`/review`** or adversarial pass (external Opus/Claude as user prefers) | Before `tofu apply` on Slice 1; after non-trivial code slices |
| **`/check-work`** | After Slice 0-style code or policy package changes |
| **grill / grill-with-docs** (`~/GitHub/JacobStephens2/skills/grilling`, `grill-with-docs`) | Before expanding Slice 2+ product surface if decisions are fuzzy |
| **Bitwarden browser agent docs** (not a slash skill) | Web-only SM or cloud token UI work — use docs under `docs/*_BROWSER_AGENT.md` |

Do **not** invent new product terms without domain-modeling / CONTEXT updates.

---

## What this project is (one line)

Self-hosted, partner-locked DNS curfews: WireGuard full tunnel + AdGuard Home + Go control plane. Details: [shared-understanding.md](./shared-understanding.md), language: [CONTEXT.md](./CONTEXT.md).

---

## Where we are

Living pointer: **[status.md](./status.md)** (renamed from `current-decision.md`).

| Slice | State | Artifacts |
|---|---|---|
| **−1** Security & policy design | Done | [docs/SECURITY-POLICY-DESIGN.md](./docs/SECURITY-POLICY-DESIGN.md), freeze + DECISIONS amendments; reviews in [reviews/slice-neg-1-review.md](./reviews/slice-neg-1-review.md) (R1–R11 addressed) |
| **0** Policy package + skeleton | Done | `internal/policy/`, `cmd/nightloch/`, CI; [reviews/slice-0-review.md](./reviews/slice-0-review.md) (S1–S9 addressed). `go test ./...` should pass |
| **1** Live origin | **Code complete; not applied** | [infra/opentofu/](./infra/opentofu/), [scripts/slice1-up.sh](./scripts/slice1-up.sh), [docs/ops/SLICE-1-RUNBOOK.md](./docs/ops/SLICE-1-RUNBOOK.md). **No droplet until human `tofu apply`** |
| **2** Magic-link sign-in | **Code complete; live demo pending origin** | `internal/auth/`, `internal/database/`, `internal/webapp/`, [docs/ops/SLICE-2-AUTH.md](./docs/ops/SLICE-2-AUTH.md) |
| **9** Continuous deploy | **Code complete; live demo pending origin** | `.github/workflows/ci.yml`, [docs/ops/SLICE-9-DEPLOY.md](./docs/ops/SLICE-9-DEPLOY.md) |

Plan source of truth: [plan/plan.md](./plan/plan.md) (post adversarial re-cut). Original adversarial review: [plan/adversarial-review.md](./plan/adversarial-review.md) (or root path if present).

Agent orientation: [AGENTS.md](./AGENTS.md).

---

## Immediate next work (prefer this order)

### A. Finish Slice 1 live demo (blocked on human + secrets)

1. Human shell with Bitwarden already working (see Secrets below).  
2. Prefer:  
   ```zsh
   source ~/.zshrc && bws-auth   # if needed
   cd ~/GitHub/JacobStephens2/nightloch-lite
   ./scripts/slice1-up.sh plan   # fetches zone id, writes tfvars (no tokens), tofu plan
   # review plan, then:
   ./scripts/slice1-up.sh apply
   ```  
3. **Do not** `bws-run zsh -c 'cd … && tofu plan'` from `$HOME` without `cd` first — wrong cwd → “No configuration files”.  
4. **Agents cannot** complete `bws-run` without `BWS_ACCESS_TOKEN` in **that** process. Putting the access token *inside* the SM project does **not** bootstrap agents (chicken-and-egg). Token stays session/bootstrap only.  
5. Optional: adversarial review of Slice 1 **code** before apply (recommended in-session).  
6. After apply: store console password locally (`tofu output -raw console_password`); verify `https://lite.nightloch.app` and grey-cloud DNS per runbook.

### B. Next code: Slice 3 (Partner graph + audit)

Auth and the health-gated deploy are ready. Slice 3 must begin with the full product-experience grill required by the plan before its schema or UI is implemented.

---

## Secrets & credentials (metadata only — no values)

| Item | Non-secret fact |
|---|---|
| Bitwarden SM project | `nightloch-lite` |
| Project UUID | `19dfca05-8d2f-491b-981a-b4950159eb9c` |
| Machine account | `jacob-macbook-local-dev`, **Can read** |
| Access token name / expiry | `jacob-macbook-interactive-2026-07` / ~2026-10-26 |
| Secrets in project (keys only) | `DIGITALOCEAN_TOKEN`, `CLOUDFLARE_API_TOKEN` |
| Local helpers | `bws-auth`, `bws-shell`, `bws-run`, `bws-logout` in `~/.zshrc` |
| `bws` CLI | `~/.local/bin/bws` 2.1.0 (checksum-verified install earlier) |

**Verified by human:** `bws-auth` works; `bws project list` shows project; in `bws-shell`, both DO and CF tokens report as **set** (values never printed).

Docs:

- [docs/BITWARDEN_SECRETS_SETUP.md](./docs/BITWARDEN_SECRETS_SETUP.md)  
- [docs/BITWARDEN_BROWSER_AGENT.md](./docs/BITWARDEN_BROWSER_AGENT.md)  
- [docs/DIGITALOCEAN_TOKEN_BROWSER_AGENT.md](./docs/DIGITALOCEAN_TOKEN_BROWSER_AGENT.md)  
- [docs/CLOUDFLARE_TOKEN_BROWSER_AGENT.md](./docs/CLOUDFLARE_TOKEN_BROWSER_AGENT.md)  
- [docs/SECRETS.md](./docs/SECRETS.md)  

**Never** print secret values, dump `env`, or put tokens in chat/files/commits.

---

## Domain language (session outcomes)

Canonical glossary: [CONTEXT.md](./CONTEXT.md).

Notable locks from this conversation arc:

- **Protectee** (not Subject) for the restrained party  
- **Partnership** (not partner edge)  
- **Roster** = Partners who protect a Protectee  
- UI: Protectee → “Your Partners”; Partner → “People you protect”  
- Policy cluster: Policy, Curfew, Baseline list, Extra block, Allowlist entry, Coverage, Tighten/Loosen, Cooling-off, Operator, Enrollment allowlist, Action alert, Coverage report  

If terms change, use **`/domain-modeling`** and edit CONTEXT immediately.

---

## Hard constraints (do not re-litigate casually)

Summarized in [AGENTS.md](./AGENTS.md) and freeze/design docs. Especially:

- No `tofu apply` / live NAT without explicit human approval  
- Slice 1 Path A: staging `lite.nightloch.app`; apex cutover is later  
- Deny-closed Policy edits; AGH model (global lists + `$client`; no baseline-off via `filtering_enabled: false`)  
- Device enroll gated; no open VPN exit  
- Fail modes: VPN off = open; server down + tunnel up = blackhole  

---

## Repo hygiene note

Substantial work may be **uncommitted** or only partially pushed relative to the long session (docs, Slice 0 code, Slice 1 infra, CONTEXT, AGENTS, status). Before large further edits, run `git status` and align with the human on commit/push.

---

## Out of scope for the next agent unless asked

- Re-running the full design interview  
- Duplicating freezes into new mega-docs  
- Storing `BWS_ACCESS_TOKEN` as a secret inside the same SM project (does not enable agent bootstrap)  
- Working in the old `nightloch` repo for this product  
