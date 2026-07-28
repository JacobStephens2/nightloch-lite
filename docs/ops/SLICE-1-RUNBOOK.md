# Slice 1 runbook — live origin

Creates a dedicated DigitalOcean droplet + Cloudflare grey-cloud DNS for staging.

## Prerequisites

1. DO API token, CF API token, zone ID for `nightloch.app` — see [SECRETS.md](../SECRETS.md)
2. OpenTofu ≥ 1.6: `brew install opentofu`
3. SSH public key on this machine

## Apply (human gate)

```bash
cd infra/opentofu
cp terraform.tfvars.example terraform.tfvars
# edit terraform.tfvars

export DIGITALOCEAN_TOKEN=...
export CLOUDFLARE_API_TOKEN=...

tofu init
tofu plan
# review plan carefully, then:
tofu apply
```

After apply:

```bash
tofu output droplet_ipv4
tofu output -raw console_password   # save to password manager
tofu output ssh_example
```

Wait 2–5 minutes for cloud-init (Caddy package install).

```bash
ssh nightloch@$(tofu output -raw droplet_ipv4)
sudo tail -f /var/log/nightloch-firstboot.log
# or: cloud-init status --wait
```

## Demo checks

```bash
# DNS unproxied
dig +short lite.nightloch.app
dig +short vpn.nightloch.app
# both should equal droplet_ipv4

# HTTPS (stock Caddy HTTP-01)
curl -fsS https://lite.nightloch.app | head
# expect: Origin OK — Slice 1 staging.
```

## What this slice does *not* do

- Does not cut over apex `nightloch.app` (Slice 11)
- Does not install WireGuard, AGH, or the Go app service
- Does not enable NAT / full-tunnel routing (see [NAT-CHECKLIST.md](./NAT-CHECKLIST.md))

## Destroy (careful)

```bash
tofu destroy
```

Removes droplet, firewall, managed DNS records created by this module.
