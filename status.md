# Status

**As of:** 2026-07-28

## Now

| Slice | State |
|---|---|
| −1 Security & policy design | Done |
| 0 Policy package + skeleton | Done (`go test ./...`) |
| 1 Live origin (OpenTofu + Caddy staging) | **Code in repo**; live demo needs human `tofu apply` |

OpenTofu module, cloud-init, secrets/runbook/NAT checklist are ready. **No droplet until you apply.**

```bash
# docs/ops/SLICE-1-RUNBOOK.md
cd infra/opentofu
cp terraform.tfvars.example terraform.tfvars   # fill in
export DIGITALOCEAN_TOKEN=...
export CLOUDFLARE_API_TOKEN=...
tofu init && tofu plan && tofu apply
```

## Next

1. **You:** apply Slice 1 (cloud spend + DNS) when ready  
2. **Code:** Slice 2 (magic-link auth) can proceed on the skeleton in parallel  

## Orient

| Doc | Role |
|---|---|
| [AGENTS.md](./AGENTS.md) | Agent onboarding |
| [CONTEXT.md](./CONTEXT.md) | Domain language |
| [shared-understanding.md](./shared-understanding.md) | Product freeze |
| [plan/plan.md](./plan/plan.md) | Full slice plan |
