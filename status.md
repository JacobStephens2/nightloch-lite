# Status

**As of:** 2026-08-06
**Full handoff:** [HANDOFF.md](./HANDOFF.md)

## Now

| Slice | State |
|---|---|
| −1 Security & policy design | Done |
| 0 Policy package + skeleton | Done (`go test ./...`) |
| 1 Live origin (OpenTofu + Caddy staging) | **Code in repo**; live demo needs human apply |
| 2 Magic-link sign-in | **Code in repo**; live two-email demo follows Slice 1 apply |
| 9 Continuous deploy (ordered after Slice 2) | **Code in repo**; enable only after staging service setup |

OpenTofu + `scripts/slice1-up.sh` ready. **No droplet until you apply.** Bitwarden laptop wiring verified (tokens inject; values never logged).

```bash
source ~/.zshrc && bws-auth   # if needed
cd ~/GitHub/JacobStephens2/nightloch-lite
./scripts/slice1-up.sh plan
# then: ./scripts/slice1-up.sh apply
```

## Next

1. **You:** `./scripts/slice1-up.sh plan` then `apply` (optional: adversarial review of Slice 1 code first)  
2. **Deploy:** follow [docs/ops/SLICE-2-AUTH.md](./docs/ops/SLICE-2-AUTH.md) for Resend, Turnstile, migration, and the two-email auth demo
3. **Deploy:** configure the staging GitHub environment using [docs/ops/SLICE-9-DEPLOY.md](./docs/ops/SLICE-9-DEPLOY.md), then set `DEPLOY_ENABLED=true`
4. **Code:** Slice 3 is next and begins with its required product-experience grill

## Secrets (non-secret metadata)

| Item | Value |
|---|---|
| Bitwarden SM project | `nightloch-lite` |
| Project UUID | `19dfca05-8d2f-491b-981a-b4950159eb9c` |
| Machine account | `jacob-macbook-local-dev` (Can read) |
| Token name / expires | `jacob-macbook-interactive-2026-07` / ~2026-10-26 |
| Secret keys present | `DIGITALOCEAN_TOKEN`, `CLOUDFLARE_API_TOKEN` |
| Local helpers | `bws-auth`, `bws-shell`, `bws-run`, `bws-logout` in `~/.zshrc` |

Never print secret values. Inject via `bws-run` / `bws-shell` only.

## Orient

| Doc | Role |
|---|---|
| [AGENTS.md](./AGENTS.md) | Agent onboarding |
| [CONTEXT.md](./CONTEXT.md) | Domain language |
| [shared-understanding.md](./shared-understanding.md) | Product freeze |
| [plan/plan.md](./plan/plan.md) | Full slice plan |
| [docs/BITWARDEN_SECRETS_SETUP.md](./docs/BITWARDEN_SECRETS_SETUP.md) | Full SM + CLI setup |
