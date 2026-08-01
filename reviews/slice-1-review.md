# Review — Slice 1 (Live origin: OpenTofu + Caddy staging)

**Reviewing:** commits `d1cbbab..73f6f56` (Slice 1 is committed, not unstaged)
**Scope:** infrastructure-as-code, first-boot bootstrap, ops runbooks, secrets documentation
**Language:** per [CONTEXT.md](../CONTEXT.md) — Protectee, Partner, Device, Policy, Operator

| Path | Lines | What |
|---|---|---|
| `infra/opentofu/main.tf` | 184 | Droplet, firewall, SSH key, Cloudflare records, Resend DNS |
| `infra/opentofu/cloud-init.yaml.tftpl` | 137 | First-boot: user, swap, Caddy, OK page, ufw |
| `infra/opentofu/variables.tf` | 105 | 15 variables |
| `infra/opentofu/outputs.tf` | 44 | IP, console password, next steps |
| `infra/opentofu/versions.tf`, `terraform.tfvars.example`, `.terraform.lock.hcl` | — | Provider pins (CF 4.52.8, DO 2.96.0, random 3.9.0) |
| `docs/SECRETS.md`, `docs/ops/SLICE-1-RUNBOOK.md`, `docs/ops/NAT-CHECKLIST.md` | 137 | Ops documentation |
| `CONTEXT.md`, `AGENTS.md`, `status.md` | — | Glossary, agent onboarding, status pointer |

**Verification performed:** `tofu fmt -check` (clean), `tofu validate` (passes against resolved provider schemas), `.gitignore` audit, `git grep` for committed credentials (none found), confirmed no `terraform.tfvars` or `*.tfstate` exists in the tree.

---

## Verdict

**Nothing here is wrong enough to block the commit. Two things will cost you an evening on first apply, and one is a landmine planted for Slice 4.**

The module is clean, validates, is correctly gitignored, pins providers with a committed lock file, and applies the accumulated review findings faithfully — swap and no-auto-reboot from [F4](../plan/adversarial-review.md#f4), explicit daily `backup_policy` from [F24](../plan/adversarial-review.md#f24), stock Caddy on a staging host from [F20](../plan/adversarial-review.md#f20), `/opt/nightloch` from [S5](./slice-0-review.md#s5), console password from [F32](../plan/adversarial-review.md#f32).

The honesty is also right. `status.md` and the plan both say *"code complete; live demo pending human `tofu apply`"* rather than claiming Slice 1 is done. Given the plan's own "done means demoable" rule, that distinction was the correct call and it was made without prompting.

What concerns me is concentrated in the part nobody has run:

- **[T1](#t1)** — cloud-init enables `ufw`, whose default forward policy is `DROP`. Full-tunnel WireGuard needs forwarding. The NAT checklist covers `ip_forward` and MASQUERADE but not this. Slice 1 has quietly pre-planted the most common WireGuard-on-Ubuntu failure for Slice 4 to discover.
- **[T2](#t2)** — the first-boot script is one `set -euo pipefail` chain that runs `apt-get` while `unattended-upgrades` may hold the dpkg lock. A common first-boot flake leaves the droplet with **no Caddy and no host firewall**, while `tofu apply` reports success.
- **[T3](#t3)** — the Caddyfile is written *before* Caddy is installed, so whether your config survives depends on dpkg conffile handling. If it loses, Caddy serves its default page, never requests a certificate, and the Slice 1 demo fails in a way that looks like DNS.

All three are cheap to fix now and expensive to diagnose at 11pm on a droplet you just created.

**Recommendation: fix [T2](#t2) and [T3](#t3), then do one throwaway `apply` → verify → `destroy` cycle before Slice 2 lands.** At `s-1vcpu-2gb` that is roughly two cents. It converts every untested assumption in this slice into a verified one, and it doubles as the module-reproducibility check that [F30](../plan/adversarial-review.md#f30)'s restore drill will otherwise be doing for the first time under pressure.

---

## What this slice gets right

- **Prior findings were actually applied, not just referenced.** Swap sized 2G with `vm.swappiness=10`, `Unattended-Upgrade::Automatic-Reboot "false"` in its own drop-in, `backup_policy { plan = "daily" }` set explicitly rather than relying on the provider default, `/opt/nightloch` created with the Caddy root kept separately at `/var/www/nightloch.app/public`. Each traces to a specific finding and each is correct.
- **Secret hygiene is genuinely good.** `.gitignore` covers `*.tfstate*`, `*.tfvars` with a `!*.tfvars.example` exception, `.terraform/`, `*.pem`, `*.key`. No credentials in tracked files. `.terraform.lock.hcl` is committed while `.terraform/` is not — exactly right.
- **`ignore_changes = [user_data]`** is a subtle, correct call. Without it, editing the bootstrap template would destroy and recreate the droplet. (It has a consequence — see [T8](#t8) — but the choice is right.)
- **The NAT checklist opens with "Do not execute in Slice 1"** and closes with "do not run this merely because Slice 1 opened 51820/udp." That is someone anticipating how a document gets misread later.
- **`ipv6 = false`** on the droplet is consistent with Q38 and avoids the macOS leak question from [F19](../plan/adversarial-review.md#f19) reaching production before Slice 4 tests it.
- **Path A was executed without drift.** Stock Caddy from the official repo, HTTP-01, no `xcaddy`, no Cloudflare DNS plugin, apex untouched. [F20](../plan/adversarial-review.md#f20) offered two paths and warned that mixing them is where people lose a day; this picked one and stayed on it.
- **`CONTEXT.md` is a real contribution.** A ubiquitous-language document with explicit *Avoid* lists is the cheapest possible defense against the schema/UI/docs vocabulary drift that was already starting (`SubjectMayApplyEdit` vs Protectee).

---

## Findings

### T1

**`ufw` will drop forwarded packets in Slice 4, and the NAT checklist does not mention it.** — **High (Slice 4 landmine)** · `cloud-init.yaml.tftpl:123-131`, `docs/ops/NAT-CHECKLIST.md:19-26`

First boot enables `ufw` with `default deny incoming` / `default allow outgoing`. On Ubuntu, `/etc/default/ufw` ships `DEFAULT_FORWARD_POLICY="DROP"`, and enabling ufw installs a FORWARD chain that honors it.

A full-tunnel exit node forwards every packet a Device sends. The NAT checklist's intended Slice 4 rules are:

```
[ ] net.ipv4.ip_forward=1
[ ] MASQUERADE/SNAT from 10.66.0.0/24 out public iface
[ ] REDIRECT TCP+UDP 53 ...
[ ] REJECT TCP+UDP 853 ...
```

Every one of those can be applied correctly and the tunnel will still carry no traffic, because ufw drops the forwarded packets before MASQUERADE is reached. The symptom is maximally confusing: the WireGuard handshake **succeeds** (UDP 51820 is allowed inbound), `wg show` reports a healthy peer, and the Device has no internet — which is exactly the [F1](../plan/adversarial-review.md#f1) blackhole presentation the Protectee has been told to expect from an outage. You would be debugging NAT, AGH, and DNS interception while the actual cause is a firewall default set weeks earlier in a different slice.

**Fix — add to the NAT checklist now, while it is still a draft:**

```
[ ] /etc/default/ufw: DEFAULT_FORWARD_POLICY="ACCEPT"   (then: ufw reload)
[ ] ufw route allow in on wg0 out on eth0
[ ] ufw route allow in on wg0 to 10.66.0.1 port 53      # DNS to AGH
[ ] Verify: iptables -L FORWARD -n -v  (or nft list chain inet filter forward)
```

Decide deliberately between `DEFAULT_FORWARD_POLICY="ACCEPT"` plus explicit `ufw route` rules, versus dropping ufw entirely and relying on the DigitalOcean cloud firewall. Both are defensible. What is not defensible is discovering the interaction during the one procedure in this project that can lock you out of the box.

Worth noting the checklist's own "After change" section would not catch this either — it verifies SSH and `https://lite.nightloch.app`, both of which keep working because they are *input* to the host, not *forwarded* traffic.

---

### T2

**A first-boot apt lock collision leaves the droplet with no Caddy and no host firewall, while `tofu apply` reports success.** — **High** · `cloud-init.yaml.tftpl:4-5, 86-133`

Three things contend for dpkg on first boot:

1. `package_update: true` + `package_upgrade: true` (cloud-init, early)
2. `unattended-upgrades`, enabled by the `20auto-upgrades` file written in the same run
3. `apt-get update` + `apt-get install -y caddy` inside `nightloch-firstboot.sh` (runcmd, late)

Ubuntu images routinely run unattended-upgrades within the first minutes of boot. If it holds the lock when the firstboot script reaches `apt-get`, the command fails, `set -euo pipefail` aborts the script, and everything after the Caddy block never runs:

```
swap            ✓ (before the apt block)
Caddy           ✗
/opt/nightloch  ✗
systemctl caddy ✗
ufw             ✗   <-- host firewall never enabled
```

Meanwhile `tofu apply` exits 0 — the droplet resource created successfully; cloud-init's outcome is not part of its state. The only signal is a stack trace in `/var/log/nightloch-firstboot.log`, which you have to know to go read. The DigitalOcean cloud firewall still applies, so this is not an open box, but the defense-in-depth layer is silently absent and the site does not serve.

**Fix — three changes, all small:**

1. **Make apt wait rather than fail:**
   ```bash
   apt-get -o DPkg::Lock::Timeout=600 update
   apt-get -o DPkg::Lock::Timeout=600 install -y caddy
   ```
   `DPkg::Lock::Timeout` is the supported mechanism and removes the race entirely.
2. **Order the script so security-relevant steps do not depend on package installs succeeding.** Move the `ufw` block *before* the Caddy block. A box that comes up firewalled without a web server is a much better failure than the reverse.
3. **Make failure visible.** Either drop `set -e` around the optional parts and write an explicit `FAILED` marker, or add a trap:
   ```bash
   trap 'echo "FIRSTBOOT FAILED at line $LINENO" >&2' ERR
   ```
   and surface it — see [T15](#t15) for making `tofu apply` itself notice.

---

### T3

**The Caddyfile is written before Caddy is installed; whether it survives depends on dpkg conffile handling.** — Medium-High · `cloud-init.yaml.tftpl:72-84` vs `:105-113`

`write_files` runs in cloud-init's early modules; `runcmd` runs at the end. So `/etc/caddy/Caddyfile` exists on disk *before* `apt-get install -y caddy` unpacks a package that ships that exact path as a conffile.

I am not certain which way dpkg resolves this. For a fresh install where the conffile path already exists as an unowned file, dpkg may preserve the existing file (writing the package version to `.dpkg-dist`) or may prompt — and under a noninteractive frontend the default action decides it. **I could not test this without a Linux host, so treat the mechanism as unverified.** What is worth stating precisely is the failure signature if it goes the wrong way:

- Caddy serves its stock "Caddy works!" page on port 80
- `lite.nightloch.app` is not in the active config, so **no certificate is ever requested**
- `https://lite.nightloch.app` fails to connect
- `dig +short lite.nightloch.app` returns the right IP, so it looks like a TLS or DNS problem

That is a genuinely misleading hour, and every one of the runbook's demo checks fails in a way that points away from the cause.

**Fix — make the ordering deterministic instead of relying on dpkg semantics:**

```bash
apt-get -o DPkg::Lock::Timeout=600 install -y caddy
install -m 0644 /usr/local/share/nightloch/Caddyfile /etc/caddy/Caddyfile
systemctl reload caddy    # or restart on first run
```

Stage the config somewhere neutral in `write_files`, then place it after the package exists. This costs one line and removes the question.

While in the Caddyfile, two additions worth making now:

- **An ACME account email** (`email ops@nightloch.app` in a global options block). Without it there are no Let's Encrypt expiry warnings — and per [F29](../plan/adversarial-review.md#f29), mail deliverability is already load-bearing for this product.
- **HSTS.** The `header` block sets `X-Content-Type-Options`, `Referrer-Policy`, and `X-Frame-Options` but not `Strict-Transport-Security`. Caddy does not add it automatically. It is unnecessary for a static OK page and necessary the moment Slice 2 sets a session cookie — add it now so it is not a Slice 11 "hardening" item, which is precisely the pattern [F26](../plan/adversarial-review.md#f26) argued against.

---

### T4

**SSH is open to the world by default.** — Medium-High · `variables.tf:30-34`, `terraform.tfvars.example:10-11`

```hcl
variable "ssh_source_cidrs" {
  default = ["0.0.0.0/0", "::/0"]
}
```

and in the example tfvars:

```hcl
# Optional: lock SSH to your IP
# ssh_source_cidrs = ["203.0.113.10/32"]
```

The safe configuration is commented out; the default path is world-open SSH on a host that will hold the WireGuard server private key, the Protectee database, and AGH admin credentials. Key-only auth (`ssh_pwauth: false`) and fail2ban make this survivable rather than dangerous, but the default is backwards: the variable's own description says *"Prefer your IP/32 for dogfood,"* which is the module telling you it knows better than its default.

**Fix:** default to `[]` and require the Operator to supply a value. An empty list produces no SSH inbound rule at all, which fails loudly and safely — the DigitalOcean web console still provides recovery ([F32](../plan/adversarial-review.md#f32) already ensured that). If a permissive default is genuinely wanted for a first run, uncomment the restrictive line in `terraform.tfvars.example` and leave the world-open value as the thing you have to opt into.

Note the interaction with a residential IP: if you lock to `/32` and your ISP rotates you, you lock yourself out of SSH — and the console password becomes your only way in. That is the correct failure and the reason [F32](../plan/adversarial-review.md#f32) exists, but it should be stated in the runbook so it is an expected event rather than a panic.

---

### T5

**A DigitalOcean account compromise yields root without needing the SSH key, and this tradeoff is undocumented.** — Medium · `cloud-init.yaml.tftpl:17-29`

```yaml
users:
  - name: ${admin_user}
    sudo: ["ALL=(ALL) NOPASSWD:ALL"]
    lock_passwd: false
chpasswd:
  list: |
    ${admin_user}:${console_password}
```

Password login is enabled for the account, and sudo is passwordless. SSH password auth is off, so the only path is the DigitalOcean web console — which means **anyone who can reach the DO control panel gets root on the enforcement box**, with no second factor at the host level.

This is a deliberate and defensible consequence of [F32](../plan/adversarial-review.md#f32): out-of-band recovery is what saves you when a NAT change severs SSH, and console access without a password is useless. But the security consequence is not written down anywhere, and it changes what the DigitalOcean account *is*: not a hosting account, but a tier-0 credential equivalent to root on the box that holds every Protectee's DNS history.

**Fix — document it in `docs/SECRETS.md`, and mitigate cheaply:**

1. State the property plainly: *"DO account access ⇒ root. Treat the DO account as tier-0."*
2. Require hardware-key or TOTP 2FA on the DigitalOcean account, and record that as a prerequisite in the Slice 1 runbook alongside the API token.
3. Consider requiring a sudo password for the console user while keeping `NOPASSWD` off entirely — recovery still works, and console access alone no longer equals root. This costs one extra secret in the password manager.

---

### T6

**No remote state backend: a root-equivalent password lives in a local file that no backup plan covers.** — Medium · `versions.tf` (no `backend` block)

`random_password.console` is stored in plaintext in `terraform.tfstate`. There is no backend configured, so state is local to whichever machine ran `apply`. Consequences:

- The console password — which per [T5](#t5) is root — sits unencrypted on a laptop.
- Slice 10 backs up SQLite and optionally the WireGuard server key. **`tfstate` is not in that plan.** Laptop loss means the infrastructure is unmanageable *and* the recovery password is gone, at the same time.
- No locking, so a concurrent apply from a second machine can corrupt state (low risk for one Operator, but free to avoid).

**Fix — pick one:**

- **Simplest:** treat `tfstate` as a secret artifact. Encrypt it (`age`) and add it to the same pull that Slice 10 performs for SQLite. Costs one line in the backup script.
- **Better:** a DigitalOcean Spaces backend with S3-compatible config, which gives you locking and versioning. Adds a bucket and one `backend` block, and keeps state off the laptop entirely.
- **Either way:** add `tfstate` to `docs/SECRETS.md`'s inventory. It is currently listed only under "OpenTofu local files (gitignored)," which frames it as clutter rather than as a credential store.

---

### T7

**The firewall targets by both `droplet_ids` and `tags`, which is redundant and likely to produce perpetual diffs.** — Medium · `main.tf:49-52`, `main.tf:2`

```hcl
resource "digitalocean_firewall" "nightloch" {
  droplet_ids = [digitalocean_droplet.nightloch.id]
  tags        = local.tags
```

DigitalOcean resolves tag-based firewall membership server-side and reports the matched droplets back in `droplet_ids`. Setting both means the configured `droplet_ids` and the API's computed value describe overlapping sets, which is a recurring source of "plan shows changes every run" behavior in this provider. I have not run a `plan` against live infrastructure to confirm it here, so treat the drift as likely rather than certain — but the redundancy is unambiguous regardless, since either mechanism alone fully assigns the firewall.

Two related issues in `local.tags`:

```hcl
tags = ["nightloch", "nightloch-lite", var.project_name]
```

- With `project_name` at its default (`"nightloch-lite"`), this is a **duplicate entry**. The provider's set semantics collapse it, so it is currently harmless — and currently invisible.
- Because the firewall is tag-targeted, **any future droplet tagged `nightloch` silently inherits these rules**, including UDP 51820 open to the world. That is a surprising coupling for a tag that generic.

**Fix:** drop `tags` from the firewall and keep `droplet_ids` — explicit, and correct for a single-droplet deployment. Separately, make `local.tags` a deduplicated set: `toset(["nightloch", var.project_name])`.

---

### T8

**`ignore_changes = [user_data]` means bootstrap fixes silently never apply.** — Medium · `main.tf:43-46`

The lifecycle block is the right call — without it, editing `cloud-init.yaml.tftpl` would destroy and recreate the droplet. But it creates a trap that is about to matter, because [T1](#t1), [T2](#t2), and [T3](#t3) all live in that template.

Someone fixing the apt lock handling will edit the template, run `tofu apply`, see **"No changes. Your infrastructure matches the configuration."**, and reasonably conclude the fix is deployed. It is not, and there is no warning — that is precisely what `ignore_changes` suppresses.

**Fix:** state the rebuild path explicitly, both as a comment in `main.tf` and as a section in the Slice 1 runbook:

> `user_data` is first-boot only and deliberately ignored. To apply a bootstrap change you must either (a) make the equivalent change on the running host by hand and backport it to the template, or (b) `tofu taint digitalocean_droplet.nightloch` and re-apply to rebuild. Option (b) changes the droplet's IP; the managed DNS records update automatically, but any Device configs pinned to `vpn.nightloch.app` will re-resolve only after TTL.

That last clause matters from Slice 4 onward and is worth writing down before a Device exists.

---

### T9

**Enabling `manage_resend_dns` can create a second apex SPF record, which permanently breaks SPF.** — Medium · `main.tf:142-151`

```hcl
resource "cloudflare_record" "resend_spf" {
  name    = "@"
  type    = "TXT"
  content = var.resend_spf_include   # "v=spf1 include:resend.com ~all"
}
```

The apex `nightloch.app` currently fronts the **old** Nightloch stack (per the cutover notes — apex is deliberately unmanaged until Slice 11). If that stack already publishes an SPF record at the apex, this creates a second one. RFC 7208 is unambiguous: multiple SPF records for the same name is a `permerror`, and receivers treat it as a hard failure rather than falling back to either record.

The failure is silent and delayed — mail keeps sending, but an increasing share lands in spam. Given that [F29](../plan/adversarial-review.md#f29) established alert deliverability as the entire integrity model, and [F23](../plan/adversarial-review.md#f23) added a coverage report that doubles as a deliverability probe, an SPF permerror degrades the product's core guarantee while every internal indicator stays green.

**Fix:**

1. Add a prerequisite to the runbook: `dig +short TXT nightloch.app | grep spf1` — **before** setting `manage_resend_dns = true`. If a record exists, merge the includes into one record rather than adding a second.
2. Consider a `precondition` block on the resource, or at minimum a comment in `terraform.tfvars.example` at the line that enables it. The example currently says only *"paste records"*, which is where someone will be looking.
3. Related, lower stakes: DMARC at `p=none` with no `rua` (the default when `dmarc_rua` is empty) is a no-op record — it neither enforces nor reports. Set an address or skip the record.

---

### T10

**`enable_dmarc` defaults to `true` but does nothing unless `manage_resend_dns` is also true.** — Low · `variables.tf:89-93`, `main.tf:176`

```hcl
variable "enable_dmarc" { default = true }
...
count = var.manage_resend_dns && var.enable_dmarc ? 1 : 0
```

Reading `variables.tf` alone tells you DMARC is on by default. It is not, and nothing in the variable description says so. Same pattern applies to the DKIM and verification records, but those at least gate on a non-empty value, which reads as intentional.

**Fix:** either rename to `enable_dmarc_with_resend`, or note the dependency in the description: `"Create DMARC at _dmarc — requires manage_resend_dns = true"`.

---

### T11

**The NAT checklist's self-cancelling revert cannot run as written.** — Medium · `docs/ops/NAT-CHECKLIST.md:12-16`

```bash
echo "nft -f /root/fw.good" | at now + 10 minutes
```

Three prerequisites are missing:

- **`at` is not installed.** The cloud-init `packages` list is `ca-certificates, curl, gnupg, debian-keyring, debian-archive-keyring, apt-transport-https, ufw, fail2ban`. On a minimal Ubuntu image `at` is absent.
- **`atd` must be enabled and running**, which is not the case if the package was never installed.
- **`/root/fw.good` is never created.** No step in this checklist or in cloud-init produces it.

So the safety net for the single most dangerous procedure in the project — the one that can sever SSH to the box carrying every Device's internet — is currently aspirational. The checklist is marked "(draft)", which is fair, but this is the item most likely to be trusted without testing precisely because it looks like a known-good incantation.

**Fix:**

1. Add `at` to the cloud-init `packages` list now, so it exists before Slice 4 needs it.
2. Make the checklist produce the rollback artifact as a numbered step rather than assuming it:
   ```bash
   nft list ruleset > /root/fw.good        # capture known-good FIRST
   systemctl enable --now atd
   echo "nft -f /root/fw.good" | at now + 10 minutes
   atq                                     # confirm the job is queued
   ```
3. Add a verification line: *"Confirm `atq` shows the job before making any firewall change. If it does not, stop."*
4. Note the ufw interaction from [T1](#t1) — if ufw is managing the ruleset, a raw `nft` restore may not reproduce ufw's chains. Decide which tool owns the firewall before writing the rollback.

---

### T12

**UDP 51820 is open to the internet for weeks before anything listens on it.** — Low · `main.tf:72-77`, `cloud-init.yaml.tftpl:130`

The comment is honest about the reasoning — *"Open now so cutover does not need a second firewall change"* — and the actual risk is nil, since nothing binds the port and the kernel drops the packets.

But it is a small deviation from the plan's own rule that **"security properties ship with the exposure, not in a final hardening dump"** — this is the inverse, exposure shipping ahead of the feature. The cost of the alternative is one line in a `tofu apply` that Slice 4 already requires (it will be changing firewall rules regardless).

Not worth reverting on its own. Worth noting because the reasoning — "open it now so we don't have to touch it later" — is the same reasoning that produces overly permissive rules everywhere else, and this project has been unusually good about not accepting it.

---

### T13

**The console password output becomes silently wrong the moment you follow the instructions.** — Low · `outputs.tf:21-25`, `docs/SECRETS.md:26`

`SECRETS.md` says: *"store in password manager; change on first login."* If you do change it, `tofu output -raw console_password` now returns a value that no longer works, with nothing indicating that. If you do not change it, a root-equivalent password sits in plaintext state ([T6](#t6)).

Both paths have a gap and the docs pick neither.

**Fix:** decide which, and say so. The simpler option is to keep the generated password as the real one (do not rotate), protect state properly per [T6](#t6), and change the output description to *"Console password — authoritative unless rotated manually."* The stricter option is to rotate on first login and add `# after rotation this output is stale` to both the output description and `SECRETS.md`.

---

### T14

**`monitoring` and `droplet_agent` install vendor daemons on the enforcement host.** — Low · `main.tf:30-33`

Both default to `true` here. `do-agent` and the droplet agent run privileged and report to DigitalOcean. For most projects this is unremarkable; for a host whose entire purpose is to hold Protectees' DNS query history, it is worth being a deliberate choice rather than a default.

Monitoring is genuinely useful — it is the only thing currently watching whether the droplet is alive, which matters given [F23](../plan/adversarial-review.md#f23) (nothing watches the watcher) and [F4](../plan/adversarial-review.md#f4) (the droplet is a single point of failure for all Device connectivity). I would keep it.

**Fix:** none required. Add a line to the threat-model note in `SECURITY-POLICY-DESIGN` §4 recording that the DO agents are present and trusted, so it is a decision rather than an oversight. Consider whether DO's alerting can serve as a cheap first version of the [F23](../plan/adversarial-review.md#f23) dead-man's-switch — it is external to the app, which is the property that matters.

---

### T15

**Nothing verifies that cloud-init succeeded; `tofu apply` is green either way.** — Low · `main.tf:21-47`

The droplet resource completes when the API reports the VM created, well before first boot finishes. Combined with [T2](#t2) and [T3](#t3), a fully successful `apply` is compatible with a host that has no Caddy, no ufw, and no certificate.

The runbook does say to wait 2–5 minutes and check the log, which is adequate for a human following it in order. It is not adequate as a gate.

**Fix — make apply mean something:**

```hcl
resource "null_resource" "wait_for_cloud_init" {
  depends_on = [digitalocean_droplet.nightloch]
  connection {
    host = digitalocean_droplet.nightloch.ipv4_address
    user = var.admin_user
    type = "ssh"
  }
  provisioner "remote-exec" {
    inline = ["cloud-init status --wait --long"]
  }
}
```

`cloud-init status --wait` blocks until completion and exits non-zero on failure, so a broken bootstrap fails the apply instead of hiding in a log file. Provisioners are generally discouraged, and that caution is fair — but this is the narrow case they suit: a one-shot readiness check on a resource that has no other completion signal.

A lighter alternative, if you would rather avoid provisioners entirely: add a `curl -fsS https://lite.nightloch.app` step to the runbook's demo checks as a hard gate before the slice is called done, and make the firstboot script write a sentinel file that the check can look for.

---

## Cross-cutting observation

Slice 1 is the first slice whose demo cannot be satisfied by anything in the repository. Slices −1 and 0 were self-contained: docs, and a pure Go package with tests that run anywhere. This one needs money, DNS, and a human decision, and the plan correctly gated it.

The risk is what `status.md` proposes next: *"Slice 2 (magic-link auth) can proceed on the skeleton in parallel."* That is reasonable for the Go code — handlers and SQLite need no droplet. But Slice 2's actual demo is *"magic link → hashed session"* against a live origin, with `BASE_URL`, cookie domain, and Turnstile domains all bound to `lite.nightloch.app`. If Slice 1 stays unapplied, Slice 2 accumulates its own unverified assumptions on top of [T1](#t1)–[T3](#t3), and the first apply then debugs two slices at once.

**Apply Slice 1 before Slice 2's HTTP layer lands** — or at minimum run the throwaway apply/destroy cycle from the verdict. Two cents to collapse the untested surface back to zero, and it exercises module reproducibility, which nothing else does until Slice 10.

---

## Before the first apply

Fix in the template (all in `cloud-init.yaml.tftpl`, all small):

1. **[T2](#t2)** — `DPkg::Lock::Timeout=600`, move the `ufw` block before the Caddy block, add an `ERR` trap.
2. **[T3](#t3)** — install Caddy, *then* place the Caddyfile. Add ACME email and HSTS while there.
3. **[T11](#t11)** — add `at` to the packages list so Slice 4's rollback works.

Fix in the module:

4. **[T4](#t4)** — default `ssh_source_cidrs` to `[]`.
5. **[T7](#t7)** — drop `tags` from the firewall; dedupe `local.tags`.

Document (no code):

6. **[T5](#t5)** — DO account is tier-0; require 2FA.
7. **[T6](#t6)** — `tfstate` is a secret; add it to the Slice 10 backup pull.
8. **[T8](#t8)** — the `tofu taint` rebuild path for bootstrap changes.

Before Slice 4 (add to the NAT checklist now, while it is cheap):

9. **[T1](#t1)** — ufw forward policy and `ufw route` rules. This one will cost hours if it is discovered live.
10. **[T11](#t11)** — capture `/root/fw.good`, enable `atd`, verify with `atq`.

Before enabling Resend DNS: **[T9](#t9)** — check for an existing apex SPF record first.

---

## Closing

This slice is in better shape than the two reviewed before it, and the reason is visible in the diff: findings from earlier reviews were applied at the point of exposure rather than deferred. `/opt/nightloch` exists because [S5](./slice-0-review.md#s5) asked for it, before any SQLite file needs moving. Swap and no-auto-reboot exist because [F4](../plan/adversarial-review.md#f4) asked, before the first outage. The daily `backup_policy` is explicit because [F24](../plan/adversarial-review.md#f24) noted the provider default is undocumented at the point of use. That is the "security ships with the exposure" rule working as intended.

The findings above cluster almost entirely in `cloud-init.yaml.tftpl` — the one artifact that is both untested and un-retestable-in-place (per [T8](#t8), you cannot re-run it without rebuilding). That is the natural place for defects to accumulate, and it argues for the throwaway apply more than any individual finding does.

One thing worth carrying forward: [T1](#t1) is a new species. Earlier reviews found things that were *wrong* — a false claim about fail-open, a predicate that mis-sampled time, an unguarded allowlist. [T1](#t1) is a decision made correctly in Slice 1 that becomes wrong in Slice 4, and no review of Slice 4 in isolation would attribute it back here. The NAT checklist is the right place to catch that class, which makes it worth treating as a living document that each slice adds to, rather than a draft that Slice 4 finally executes.
