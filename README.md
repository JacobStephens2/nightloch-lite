# Nightloch Lite

Partner-locked, scheduled DNS blocking — self-hosted WireGuard + AdGuard Home, Go control plane.

## Docs

| Doc | Purpose |
|---|---|
| [shared-understanding.md](./shared-understanding.md) | Product freeze (Slice −1 amended) |
| [DECISIONS.md](./DECISIONS.md) | Interview + Slice −1 decision log |
| [docs/SECURITY-POLICY-DESIGN.md](./docs/SECURITY-POLICY-DESIGN.md) | Perimeter, predicate, AGH model |
| [plan.md](./plan.md) | Vertical-slice implementation plan |
| [adversarial-review.md](./adversarial-review.md) | Plan review (F1–F33) |
| [current-decision.md](./current-decision.md) | Latest status pointer |

## Status

Shared understanding confirmed. **Slice −1 complete.** Next: **Slice 0** (see plan).

## Stack (target)

- **App:** Go + SQLite  
- **Enforcement:** WireGuard full tunnel + AdGuard Home  
- **Host:** Dedicated DigitalOcean droplet (`nyc3`, 2 GB)  
- **IaC:** OpenTofu (DO + Cloudflare DNS)  
- **Deploy:** GitHub Actions → binary on droplet  
