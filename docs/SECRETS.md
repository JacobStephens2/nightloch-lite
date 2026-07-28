# Secrets & credentials (Slice 1+)

**Never commit** `terraform.tfvars`, state files, API tokens, or console passwords.

## DigitalOcean

| Item | How |
|---|---|
| API token | [API → Tokens](https://cloud.digitalocean.com/account/api/tokens) — read/write for droplets, firewalls, SSH keys |
| Env | `export DIGITALOCEAN_TOKEN=dop_v1_...` |

## Cloudflare

| Item | How |
|---|---|
| API token | Create token with **Zone → DNS → Edit** and **Zone → Zone → Read** for zone `nightloch.app` |
| Env | `export CLOUDFLARE_API_TOKEN=...` |
| Zone ID | Dashboard → nightloch.app → Overview → Zone ID → `cloudflare_zone_id` in tfvars |

## SSH

| Item | How |
|---|---|
| Public key | Path in `ssh_public_key_path` (e.g. `~/.ssh/id_ed25519.pub`) |
| Private key | Local only; used to SSH as `nightloch@<droplet_ip>` |
| Console password | `tofu output -raw console_password` after apply — store in password manager; change on first login (DO web console needs a password) |

## Resend (Slice 1 DNS optional; required for Slice 2 mail)

| Item | How |
|---|---|
| API key | Resend dashboard → API keys |
| Domain | Add `nightloch.app`; copy DKIM/SPF/verify TXT into tfvars with `manage_resend_dns = true` |
| From | `Nightloch <alerts@nightloch.app>` (monitored) once domain verified |

## Later slices (not Slice 1)

| Item | Slice |
|---|---|
| Turnstile site/secret keys | 2 |
| Session signing secret | 2 |
| AGH basic auth password | 4 |
| Enroll allowlist | 5 |
| Deploy SSH key for GHA | 9 |
| age/gpg backup recipient | 10 |

## OpenTofu local files (gitignored)

```text
infra/opentofu/terraform.tfvars
infra/opentofu/.terraform/
infra/opentofu/*.tfstate
infra/opentofu/*.tfstate.*
```
