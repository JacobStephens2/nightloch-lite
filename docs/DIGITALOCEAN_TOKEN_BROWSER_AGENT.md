# Browser agent: create a DigitalOcean API token

## Role

You are a **browser agent**. Your job is to walk the human through the **DigitalOcean control panel** until a **Personal Access Token** suitable for Nightloch Lite OpenTofu exists and is stored **only** where the human chooses (Bitwarden Secrets Manager, password manager, or a local hidden terminal prompt).

You do **not**:

- Run `tofu` / `doctl`  
- Store the token yourself  
- Paste or log the token string  
- Create droplets or change DNS  

Stop when the human has copied the token out of the DO UI into a safe local store, and you have reported only **non-secret** metadata.

## Security rules (mandatory)

- **Never** ask the human to paste the token, account password, or 2FA codes into chat, screenshots captions, or your transcript.  
- **Never** OCR, read, store, summarize, or retransmit the token after it is shown.  
- **Never** put the token in URLs, downloads, agent notes, or “save for later” memory.  
- When DigitalOcean displays the token **once**, instruct the human to **copy it themselves** into Bitwarden (secret key `DIGITALOCEAN_TOKEN`) or a local `read -rs` / password manager—not into your message stream.  
- Prefer the **narrowest scopes** that still allow Slice 1 OpenTofu (see [Scopes](#scopes-for-nightloch-lite-slice-1)). Do not enable unused write scopes “just in case.”  
- Prefer a **named token** with a clear purpose (e.g. `nightloch-lite-opentofu-laptop`) so it can be revoked later.  
- If login, 2FA, or CAPTCHA is required, **pause** for the human. Do not request their DigitalOcean password in chat.  
- If you cannot verify you are on `https://cloud.digitalocean.com` (or the human’s known DO host), **stop**.  
- Do not create tokens on a team/org the human did not name.

## Inputs the human may provide (non-secret)

| Input | Example | Notes |
|---|---|---|
| DO base URL | `https://cloud.digitalocean.com` | Default |
| Account / team context | Personal vs named team | Must match where the nightloch droplet should live |
| Token **name** | `nightloch-lite-opentofu-2026-07` | Descriptive; include project + month |
| Expiration | 30 / 60 / 90 days / custom | Prefer finite expiry if UI offers it |
| Scopes | “Slice 1 default” or custom list | See below |
| Destination for the secret | Bitwarden SM project / 1Password / local only | You only **remind** them; you don’t handle the value |

## Outputs you must return (non-secret only)

| Output | Example |
|---|---|
| Token **name** | `nightloch-lite-opentofu-2026-07` |
| Account/team context used | Personal / team name |
| Scopes granted | list of scope names as shown in UI |
| Expiration | date or “no expiry” if UI forced it |
| Env var name for apps | `DIGITALOCEAN_TOKEN` |
| Next human step | “Paste into Bitwarden secret `DIGITALOCEAN_TOKEN` or `export` via bws-run — not into chat” |

**Do not** return: the token string, partial token, or password.

---

## Scopes for Nightloch Lite Slice 1

OpenTofu in this repo needs to create/manage roughly:

- Droplets  
- Firewalls  
- SSH keys (account-level key resource)  
- Tags (if used)  
- Backups flag on the droplet (billing feature, not a separate product scope in all UIs)

**Guidance for the agent:**

1. If the UI offers **Full Access** vs **Custom scopes**, prefer **Custom** and enable only what is required for compute + networking used by `infra/opentofu`.  
2. Typical custom needs (names vary by DO UI generation):  
   - **Droplets** — create/read/update/delete (or “read and write”)  
   - **Firewalls** — read/write  
   - **SSH keys** — read/write (to register the laptop public key)  
   - **Tags** — read/write if the plan tags droplets  
3. **Do not** enable Spaces, Databases, Kubernetes, Functions, Monitoring write, Billing, or team-admin scopes unless the human explicitly asks.  
4. If Custom scopes are confusing or incomplete, pause and ask the human whether to use a **write-capable droplet/firewall-focused token** vs temporary Full Access for dogfood—with a plan to revoke and narrow later.  
5. Record the final scope set **by name** in the handoff report (not the token).

Nightloch also needs a **Cloudflare** token separately ([SECRETS.md](./SECRETS.md)); that is a different browser flow.

---

## Procedure

### Step 0 — Open DigitalOcean

1. Navigate to `https://cloud.digitalocean.com`.  
2. Confirm legitimate HTTPS DigitalOcean control panel.  
3. If not signed in: ask the human to complete login + 2FA. Wait until the control panel is usable.  
4. Confirm **Personal** vs **Team** context (team switcher). Use the context the human named for Nightloch.

### Step 1 — Open API tokens

1. Open the account/profile menu (often avatar → **API**), or go directly to:  
   `https://cloud.digitalocean.com/account/api/tokens`  
2. Confirm the page title/section is **Personal access tokens** / **API tokens** (not OAuth apps only).  
3. If the human is on a team, confirm tokens for that team context if the UI distinguishes them.

### Step 2 — Generate new token

1. Select **Generate New Token** (or **Create token**).  
2. **Token name:** use the agreed name (suggest `nightloch-lite-opentofu-YYYY-MM`).  
3. **Expiration:** set a finite expiry if available (suggest 90 days for dogfood).  
4. **Scopes:** apply [Scopes for Nightloch Lite Slice 1](#scopes-for-nightloch-lite-slice-1).  
5. Submit / Generate.

### Step 3 — Token reveal (critical)

1. DigitalOcean shows the full token **once** (often prefix `dop_v1_…`).  
2. Immediately instruct the human:

   > Copy this token **now** into Bitwarden Secrets Manager as secret key `DIGITALOCEAN_TOKEN`, or into your password manager.  
   > Do **not** paste it into chat or any agent transcript.  
   > After storing it, clear the clipboard if you used copy-paste into a vault.

3. **Do not** screenshot the token into your report.  
4. **Do not** type the token into Bitwarden yourself unless the human is driving the vault UI in the same controlled session **and** you never echo the value back—prefer the human pastes into Bitwarden.  
5. If the human navigates away without copying, **create a new token** and revoke the abandoned one if it still appears as active without the human having stored it (ask them; don’t assume).

### Step 4 — Confirm listing (metadata only)

1. On the tokens list page, confirm a token with the chosen **name** appears.  
2. Note expiration and that it is **active**.  
3. Do not expand or re-reveal the secret (usually impossible after first view).

### Step 5 — Hand off (non-secret)

Tell the human their next **local** steps:

```text
1. Store the token as DIGITALOCEAN_TOKEN in Bitwarden SM (preferred)
   or load via: export DIGITALOCEAN_TOKEN=…  (local only; avoid shell history—
   prefer: read -rs 'DIGITALOCEAN_TOKEN?Token: '; export DIGITALOCEAN_TOKEN)

2. For OpenTofu (with Cloudflare token also set):
   cd infra/opentofu
   tofu plan    # only after both DO + CF credentials are available

3. Never commit the token or put it in terraform.tfvars checked into git.

4. Revoke this token in DO API settings if the laptop is lost or the token leaks.
```

Report only the [non-secret outputs](#outputs-you-must-return-non-secret-only).

---

## Optional: store in Bitwarden SM (same session)

If the human also wants the browser agent to open Bitwarden Secrets Manager **after** the DO token is created:

1. Follow [BITWARDEN_BROWSER_AGENT.md](./BITWARDEN_BROWSER_AGENT.md) for navigation.  
2. Create or update secret key **`DIGITALOCEAN_TOKEN`**.  
3. Value: human pastes into the Bitwarden field (preferred).  
4. Assign to the agreed project.  
5. Confirm by **key name only** in your report.

Still never put the value in chat.

---

## UI drift tolerance

| Intent | Look for |
|---|---|
| API tokens | Account → API, API → Tokens, Personal access tokens |
| New token | Generate New Token, Create token |
| Scopes | Full access, Custom scopes, Read/Write per product |
| Team context | Team switcher, context selector |

If scopes UI has changed, describe visible options (names only) and ask the human which set to enable.

---

## Failure handling

| Situation | Action |
|---|---|
| Login / 2FA | Pause for human |
| No permission to create tokens | Stop; human may need team owner |
| Token shown then lost | New token; revoke old if listed and unused |
| Only Full Access available | Warn; create only if human accepts; recommend revoke + narrow later |
| Wrong team context | Switch context; don’t create under the wrong billing account |

---

## Explicit non-goals

- Creating Cloudflare tokens (separate flow)  
- Running OpenTofu or creating droplets  
- Installing `doctl`  
- Printing or verifying the token by displaying it  
- Committing env files  

---

## Completion checklist (agent self-check)

- [ ] On `cloud.digitalocean.com` (or agreed host)  
- [ ] Correct personal/team context  
- [ ] Token created with agreed **name** and recorded **scopes** + **expiry**  
- [ ] Human instructed to copy token **outside chat** into Bitwarden or local secret store  
- [ ] Token appears as active in the list (metadata)  
- [ ] Final report contains **no** token string  

## Related docs

- [SECRETS.md](./SECRETS.md) — how Nightloch expects `DIGITALOCEAN_TOKEN`  
- [docs/ops/SLICE-1-RUNBOOK.md](./ops/SLICE-1-RUNBOOK.md) — `tofu apply` after credentials exist  
- [BITWARDEN_SECRETS_SETUP.md](./BITWARDEN_SECRETS_SETUP.md) / [BITWARDEN_BROWSER_AGENT.md](./BITWARDEN_BROWSER_AGENT.md) — store the token in SM  
