# Slice 4 Resolver runbook

This runbook installs Nightloch's global **Baseline list** posture on AdGuard Home.  The
checked-in posture is prepared and tested without a droplet; none of the live boxes below
is complete until an Operator has run it and recorded the observed result.

The posture is deliberately narrow:

- DNS listens only on `10.66.0.1:53`, the address owned by `wg0`.
- Administration listens only on `127.0.0.1:3000`; the public route is
  `https://agh.nightloch.app` behind Caddy Basic auth.
- OISD NSFW and HaGeZi's Encrypted DNS/VPN/TOR/Proxy Bypass list are global, enabled
  subscriptions.  They apply to every Device without a per-Device rule.
- `use-application-dns.net` has one global `$dnsrewrite=NXDOMAIN` rule for Firefox's
  default encrypted-resolver canary.
- Query-log retention is rendered from `enforcement.QueryLogRetention`, which is also the
  value tunnel-address Quarantine depends on.  The YAML does not state a second duration.
- Automatic filter refresh and AdGuard Home's binary self-update are disabled.  Updating
  either is an explicit Operator change followed by the same verification as first install.

The pinned binary is AdGuard Home `v0.107.79` for linux/amd64.  The installer verifies the
release's published SHA-256 digest before installing it.  A later upgrade changes both the
version and digest in `deploy/nightloch-resolver-posture.sh` and passes the full test suite
before it reaches this host.

## Prerequisites

- `wg-quick@wg0.service` is active on `10.66.0.1/24`.
- `sudo /usr/local/sbin/nightloch-nat-verify.sh` passes.  Before this run it may still show
  the non-governing `resolver_listening` warning.
- The current Nightloch binary is at `/opt/nightloch/bin/nightloch`.  The installer calls
  its `resolver-query-log-retention` command so retention cannot drift from Quarantine.
- Caddy is installed and the deployed Caddyfile is ready to import Resolver sites.
- `/etc/nightloch/nightloch.env` exists with mode `0600` for the application service.
- Work from a checkout or transferred release directory containing all files under
  `deploy/`; `install` reads the four Resolver artifacts beside its script.

Do not run `tofu apply` or change NAT for this procedure.  The host tunnel and NAT posture
already owns those concerns.

## Prepare the shared administrator credential

AdGuard Home has no scoped application token.  The Nightloch process holds a full
administrator username and plaintext password so it can use the loopback control API.  An
application compromise therefore includes Resolver compromise; keep
`/etc/nightloch/nightloch.env` mode `0600`, never print it, and never put its values in a
shell argument, chat, log, or commit.

The administrator username is fixed as `operator` in both the AdGuard Home config and
`deploy/nightloch.env.example`:

```text
ADGUARD_USERNAME=operator
ADGUARD_PASSWORD=<stored in the secret manager; do not paste into this runbook>
```

Use the same plaintext password at both layers.  Caddy gets only its bcrypt hash.  This
command prompts on the terminal without echo and writes only the hash; the plaintext never
enters shell history or an argument list:

```bash
sudo install -d -m 0750 /etc/nightloch
sudo sh -c 'umask 077; caddy hash-password --algorithm bcrypt > /etc/nightloch/adguard-password.bcrypt'
```

- [ ] The entered plaintext matches `ADGUARD_PASSWORD` in the root-owned environment file.
- [ ] `/etc/nightloch/adguard-password.bcrypt` is `0600 root:root`.
- [ ] No plaintext credential was printed or passed with `--plaintext`.

## Install without starting

The full Caddyfile carries an empty-glob import.  Caddy documents an empty import glob as
valid, so the same file works before and after the Resolver site exists.

```bash
sudo install -m 0644 deploy/Caddyfile.example /etc/caddy/Caddyfile
sudo ./deploy/nightloch-resolver-posture.sh install
sudo install -m 0755 deploy/nightloch-resolver-posture.sh /usr/local/sbin/nightloch-resolver-posture.sh
sudo install -m 0755 deploy/nightloch-resolver-verify.sh /usr/local/sbin/nightloch-resolver-verify.sh
```

`install` downloads and checksum-verifies the pinned release, creates the matched
`adguardhome` system user/group when needed, and installs:

| Artifact | Installed path |
|---|---|
| AdGuard Home binary | `/opt/AdGuardHome/AdGuardHome` |
| Rendered config | `/etc/AdGuardHome/AdGuardHome.yaml` |
| systemd unit | `/etc/systemd/system/AdGuardHome.service` |
| Caddy site | `/etc/caddy/sites/nightloch-agh.caddy` |
| Caddy service drop-in | `/etc/systemd/system/caddy.service.d/nightloch.conf` |
| Caddy hash environment | `/etc/nightloch/caddy.env` |

The first install renders the config.  A later install never overwrites it, because later
slices add generated `user_rules` through AdGuard Home's API.  It also refuses if the hash
file and persisted AdGuard Home hash diverge, rather than changing only Caddy and silently
splitting the two credential layers.

- [ ] The command says nothing was started or enabled.
- [ ] `systemctl is-enabled AdGuardHome.service` is still `disabled` before first start.
- [ ] The rendered config is `0600 adguardhome:adguardhome`.
- [ ] Review the non-secret portions: tunnel-only DNS bind, loopback HTTP bind, two global
      subscriptions, manual filter updates, and the Firefox canary rule.
- [ ] Do not add a `$client` rule, persistent client, Device address, or Protectee Policy.

## Validate and start

```bash
sudo /usr/local/sbin/nightloch-resolver-posture.sh start
```

`start` refuses unless `wg0` is active.  It asks both the pinned AdGuard Home binary and
Caddy to validate their exact persisted files before it enables anything.  Only after both
parsers accept them does it enable/start `AdGuardHome.service` and reload Caddy.

The unit has `Requires=` and `After=` on `wg-quick@wg0.service`, is enabled under
`multi-user.target`, restarts on failure, and has a `512M` memory ceiling.  Its only ambient
capability is `CAP_NET_BIND_SERVICE` for port 53.  The command line includes
`--no-check-update`, so the admin UI cannot silently replace the pinned binary.

On first start the two subscribed lists must download before their cache files are nonempty.
Watch without printing configuration or credentials:

```bash
sudo journalctl -u AdGuardHome.service --since=-5m
sudo /usr/local/sbin/nightloch-resolver-verify.sh
```

If the first verifier run reports empty Baseline subscriptions while the download is still
in progress, read the journal and rerun it after the downloads finish.  Do not turn filtering
off to make the check pass.

The verifier is read-only and reports these governing named facts:

| Named fact | What it reads back |
|---|---|
| `resolver_service` | active, enabled, and ordered after `wg0` |
| `dns_listener` | TCP and UDP 53 on `10.66.0.1`, nowhere public |
| `admin_listener` | loopback port 3000 plus the authenticated Caddy route |
| `baseline_subscriptions` | both global lists enabled with nonempty loaded files |
| `filter_updates_manual` | `filters_update_interval: 0` |
| `query_log_retention` | persisted interval equals the application's Quarantine-owned value |
| `firefox_canary` | a live query returns `NXDOMAIN` |

## Off-host and browser checks

From somewhere other than the droplet:

```bash
dig +time=3 +tries=1 @<public-ip> example.com
curl -I https://agh.nightloch.app/
```

- [ ] The public-IP DNS query times out with no answer.
- [ ] The unauthenticated HTTPS request returns `401` from Caddy.
- [ ] A browser reaches the AdGuard Home administration UI only after Caddy Basic auth.
- [ ] `/health` reports `resolver_filtering` as enforcing.
- [ ] `sudo /usr/local/sbin/nightloch-nat-verify.sh` now reports
      `PASS resolver_listening` as well as its existing governing facts.

Do not put the password in `curl -u`, a URL, a command argument, or the run record.

## Conservative list updates

`filters_update_interval: 0` means neither subscribed list refreshes on a timer.  This is
intentional: an upstream list change cannot silently reshape the product's always-on
enforcement.  The tradeoff is an Operator-owned maintenance action.

For an explicit refresh:

1. Read the OISD and HaGeZi upstream change notes or list metadata.
2. Record the date, old/new list metadata, and why the refresh is being taken.  Record no
   browsing history and no credential.
3. Use the authenticated AdGuard Home UI to refresh the two DNS blocklists manually.
4. Run `nightloch-resolver-verify.sh`, `/health`, the off-host port-53 check, and the Device
   Baseline/ordinary-browsing checks immediately afterwards.
5. If enforcement regresses, restore the known-good host snapshot or cached filter files;
   do not disable global filtering.

Binary upgrades are separate and equally explicit: update the pinned version and published
digest in the repository, pass review/tests, run `install`, then `start` and this complete
verification again.

## Coordinated credential rotation

Rotation changes three representations together: the application plaintext in
`nightloch.env`, the exact bcrypt hash in AdGuard Home's `users.password`, and the same hash
file from which `install` renders Caddy's environment.  A normal reinstall refuses when they
diverge.

1. Generate a new bcrypt hash into a separate `0600 root:root` file using the same safe,
   interactive `caddy hash-password --algorithm bcrypt` pattern above.
2. Stop `nightloch.service` and `AdGuardHome.service`.  Caddy may stay up and will expose no
   unauthenticated backend while AdGuard Home is stopped.
3. With a root-only editor, put the new plaintext in `nightloch.env`, the exact new hash in
   AdGuard Home's `users.password`, and replace `adguard-password.bcrypt` with that same hash.
4. Run `nightloch-resolver-posture.sh install`, then `start`, restart `nightloch.service`, and
   run every named fact plus the unauthenticated `401` check.

Never rotate only one layer and never paste either plaintext value into an issue or run
record.

## What remains for issue #42

This runbook proves host configuration and read-back facts.  It does not pretend to prove
Device behavior.  The Slice 4 gate still requires observed artifacts from a real phone and
dual-stack Mac:

- tunnel on blocks an OISD sample while ordinary browsing works; tunnel off opens both;
- a Device reconnects after an unattended droplet reboot;
- DNS aimed at `8.8.8.8` from inside the tunnel still lands on this Resolver;
- Android strict Private DNS fails visibly rather than hanging;
- IPv6 leak checks, a day of OISD false-positive observations, and the Instagram
  blocked-services catalogue/schedule polarity are recorded.

Record those results under `docs/ops/` only after they are actually observed.  No host-only
test or prepared command checks a box that requires a Device.
