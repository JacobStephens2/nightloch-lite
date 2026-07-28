# Current decision

**As of:** 2026-07-28  
**Plan:** [plan/plan.md](./plan/plan.md)

---

## Slice 1 — code complete

OpenTofu module, cloud-init, secrets/runbook/NAT checklist are in-repo.  
**Live droplet is not created until you run `tofu apply`.**

```bash
# See docs/ops/SLICE-1-RUNBOOK.md
cd infra/opentofu
cp terraform.tfvars.example terraform.tfvars   # fill in
export DIGITALOCEAN_TOKEN=...
export CLOUDFLARE_API_TOKEN=...
tofu init && tofu plan && tofu apply
```

---

## Next

1. **You:** apply Slice 1 (cloud spend + DNS) when ready  
2. **Code:** Slice 2 (magic-link auth) can start in parallel on the skeleton  
