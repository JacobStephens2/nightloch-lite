# Nightloch Lite

Partner-locked, scheduled DNS blocking — self-hosted WireGuard + AdGuard Home, Go control plane.

## Docs

| Doc | Purpose |
|---|---|
| [shared-understanding.md](./shared-understanding.md) | Product freeze (Slice −1 amended) |
| [DECISIONS.md](./DECISIONS.md) | Interview + Slice −1 decision log |
| [docs/SECURITY-POLICY-DESIGN.md](./docs/SECURITY-POLICY-DESIGN.md) | Perimeter, predicate, AGH model |
| [plan/plan.md](./plan/plan.md) | Vertical-slice implementation plan |
| [docs/ops/SLICE-1-RUNBOOK.md](./docs/ops/SLICE-1-RUNBOOK.md) | Create staging droplet + DNS |
| [docs/SECRETS.md](./docs/SECRETS.md) | Tokens and credentials |
| [adversarial-review.md](./adversarial-review.md) | Plan review (F1–F33) |
| [status.md](./status.md) | Project status and what’s next |

## Status

**Slices −1 and 0 complete; Slices 1, 2, and 9 are code-complete.** The live origin still needs your approved `tofu apply`, after which the [Slice 2 auth runbook](./docs/ops/SLICE-2-AUTH.md) covers the real-email demo and the [deploy runbook](./docs/ops/SLICE-9-DEPLOY.md) enables health-gated releases.

```bash
go test ./...
go build -o bin/nightloch ./cmd/nightloch

# App setup (requires a filled, uncommitted env file)
DATABASE_PATH=./nightloch.db ./bin/nightloch migrate
# source deployment env, then: ./bin/nightloch serve

# Infra (secrets required — see docs/SECRETS.md)
cd infra/opentofu && cp terraform.tfvars.example terraform.tfvars
# edit tfvars; export DIGITALOCEAN_TOKEN CLOUDFLARE_API_TOKEN
tofu init && tofu plan   # apply only when you intend to create the droplet
```

## Stack (target)

- **App:** Go + SQLite (policy predicate + magic-link auth shipped)
- **Enforcement:** WireGuard full tunnel + AdGuard Home  
- **Host:** Dedicated DigitalOcean droplet (`nyc3`, 2 GB)  
- **IaC:** OpenTofu (DO + Cloudflare DNS)  
- **Deploy:** GitHub Actions → binary on droplet  
