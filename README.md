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
| [current-decision.md](./current-decision.md) | Latest status pointer |

## Status

**Slices −1, 0, and 1 (code) complete.** Slice 1 **live demo** needs your `tofu apply` ([runbook](./docs/ops/SLICE-1-RUNBOOK.md)).

```bash
go test ./...
go build -o bin/nightloch ./cmd/nightloch

# Infra (secrets required — see docs/SECRETS.md)
cd infra/opentofu && cp terraform.tfvars.example terraform.tfvars
# edit tfvars; export DIGITALOCEAN_TOKEN CLOUDFLARE_API_TOKEN
tofu init && tofu plan   # apply only when you intend to create the droplet
```

## Stack (target)

- **App:** Go + SQLite (`internal/policy` predicate shipped)  
- **Enforcement:** WireGuard full tunnel + AdGuard Home  
- **Host:** Dedicated DigitalOcean droplet (`nyc3`, 2 GB)  
- **IaC:** OpenTofu (DO + Cloudflare DNS)  
- **Deploy:** GitHub Actions → binary on droplet  
