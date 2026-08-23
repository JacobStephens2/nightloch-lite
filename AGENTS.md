# AGENTS.md — Nightloch Lite

Brief orientation for coding agents. Prefer this file for **where to look** and **what not to break**; product law lives in the linked docs.

## What this is

Self-hosted **partner-locked DNS protection**: full-tunnel WireGuard + AdGuard Home, controlled by a small **Go** app (SQLite). Protectees cannot freely loosen their Policy while Partnered; Partners can. Not the old multi-tenant Nightloch/Cloudflare control plane.

## Read first (in order)

| Doc | Why |
|---|---|
| [CONTEXT.md](./CONTEXT.md) | Ubiquitous language — use these terms in code, UI, and prose |
| [status.md](./status.md) | Project status and what’s next |
| [HANDOFF.md](./HANDOFF.md) | Latest session handoff for fresh agents |
| [shared-understanding.md](./shared-understanding.md) | Product freeze |
| [docs/SECURITY-POLICY-DESIGN.md](./docs/SECURITY-POLICY-DESIGN.md) | Perimeter, tighten/loosen, AGH model, fail modes |
| [plan/plan.md](./plan/plan.md) | Vertical-slice implementation plan |

Skip full re-reads of [DECISIONS.md](./DECISIONS.md) / adversarial reviews unless changing a locked decision.

## Language (do not invent synonyms)

- **Protectee** — User under restraint (not “Subject”)
- **Partner** / **Partnership** / **Roster** — who protects a Protectee
- **Device** — enrolled tunnel identity (not “peer” in product language)
- **Policy**, **Curfew**, **Baseline list**, **Extra block**, **Allowlist entry**, **Coverage**
- UI: Protectee sees **Your Partners**; Partner sees **People you protect**

## Layout

```text
cmd/nightloch/          # binary entry (HTTP later)
internal/policy/        # tighten/loosen predicate (Slice 0) — pure, well-tested
internal/partnership/   # Slice 3 relationship perimeter: invitations, Activity,
                        # authorization, durable per-recipient mail jobs
infra/opentofu/         # DO droplet + CF DNS (Slice 1)
deploy/                 # systemd/Caddy/WG helper examples
docs/                   # security design, secrets, ops runbooks
plan/                   # plan.md + adversarial review
reviews/                # slice reviews
CONTEXT.md              # glossary
```

## Implementation status

| Slice | Status |
|---|---|
| −1 Security/policy design | Done (docs) |
| 0 Policy package + skeleton | Done (`go test ./...`) |
| 1 Live origin (OpenTofu + Caddy staging) | **Code in repo**; live demo needs human `tofu apply` |
| 2 Magic-link sign-in | **Code in repo**; live demo pending origin |
| 9 Continuous deploy | **Code in repo**; live demo pending origin |
| 3 Partner graph + Activity | **3A in progress** — `internal/partnership/` owns invitations, acceptance, Partnerships, Activity, authorization, and durable mail jobs |
| 4+ WG, AGH, Devices, Policy, … | Not started — follow plan |

## Commands

```bash
go test ./...
go build -o bin/nightloch ./cmd/nightloch
# Infra: see docs/ops/SLICE-1-RUNBOOK.md and docs/SECRETS.md
# Do not tofu apply without explicit user approval (creates paid droplet + DNS).
```

## Hard rules

1. **Deny-closed on Policy edits** — invalid or unknown state does not silently permit a Protectee loosen (`SubjectMayApplyEdit` / future Protectee-named APIs).
2. **Fail modes** — VPN off → unprotected; server down with tunnel up → client blackhole (not “fail open”). Document in Device enroll UI when built.
3. **AGH** — global Baseline lists + `$client` rules; never `filtering_enabled: false` to “turn off baseline”; curfews/extra blocks use `$important` so allows don’t lift them.
4. **No open VPN** — Device enrollment is gated (Operator enrollment allowlist in v1).
5. **Secrets** — never commit tokens, `terraform.tfvars`, or state. Prefer Bitwarden SM (`docs/BITWARDEN_SECRETS_SETUP.md`); never paste secrets into chat.
6. **Cloud/NAT** — no `tofu apply`, no live NAT/iptables without explicit user go-ahead (`docs/ops/NAT-CHECKLIST.md`).
7. **Scope** — v1 has no passes, no TLS MITM/search terms, no custom mobile apps, no enforcement on stephens.page.

## When changing the domain

Update [CONTEXT.md](./CONTEXT.md) when terms are resolved. ADRs only for hard-to-reverse, surprising, trade-off decisions (`docs/adr/` when needed). Prefer linking freeze docs over forking new “source of truth” files.

## Agent skills

### Issue tracker

Issues and specs are tracked in this repository’s GitHub Issues using the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Triage uses the canonical `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, and `wontfix` labels. See `docs/agents/triage-labels.md`.

### Domain docs

This is a single-context repository with `CONTEXT.md` at the root and repository-wide ADRs under `docs/adr/`. See `docs/agents/domain.md`.
