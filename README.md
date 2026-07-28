# Nightloch Lite

Partner-locked, scheduled DNS blocking — self-hosted WireGuard + AdGuard Home, Go control plane.

## Docs

| Doc | Purpose |
|---|---|
| [shared-understanding.md](./shared-understanding.md) | Confirmed product freeze |
| [DECISIONS.md](./DECISIONS.md) | Interview decision log |
| [plan.md](./plan.md) | Vertical-slice implementation plan |
| [current-decision.md](./current-decision.md) | Latest status pointer |

## Status

Shared understanding confirmed. Implementation not started (see plan slices 0+).

## Stack (target)

- **App:** Go + SQLite  
- **Enforcement:** WireGuard full tunnel + AdGuard Home  
- **Host:** Dedicated DigitalOcean droplet (`nyc3`, 2 GB)  
- **IaC:** OpenTofu (DO + Cloudflare DNS)  
- **Deploy:** GitHub Actions → binary on droplet  
