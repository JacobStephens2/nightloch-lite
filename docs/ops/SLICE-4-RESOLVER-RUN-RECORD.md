# Resolver live-run record - Slice 4 / issue #41

This records the observed Resolver rollout on droplet `nightloch-lite`
(`134.209.165.82`) on **2026-08-30**.  It supplements
[SLICE-4-RESOLVER.md](./SLICE-4-RESOLVER.md); prepared artifacts and host-only
tests are not recorded here as Device observations.

## Installation and activation

- [x] `wg-quick@wg0.service` was active on `10.66.0.1/24` and every governing
      NAT named fact passed.  The only pre-install result was the expected
      non-governing `resolver_listening` warning.
- [x] The merged PR #51 Linux binary was installed at
      `/opt/nightloch/bin/nightloch`; `resolver-query-log-retention` returned
      `168h`, derived from `enforcement.QueryLogRetention`.
- [x] The existing staging Caddy site was preserved.  Its Caddyfile gained only
      the empty Resolver-site import and validated before installation; the
      previous file remains at `/etc/caddy/Caddyfile.before-resolver-e222b95`.
- [x] The Operator entered the shared Resolver credential through hidden local
      input.  The plaintext travelled over SSH stdin and was written only to
      `/etc/nightloch/nightloch.env`; Caddy received only its bcrypt hash.
      Both root-owned files were observed as mode `0600`, and no credential was
      printed, logged, committed, or passed in an argument.
- [x] `nightloch-resolver-posture.sh install` installed checksum-pinned AdGuard
      Home `v0.107.79`, fetched both initial Baseline-list caches, and appended
      their SHA-256 receipts.  The service was then observed as
      `disabled`/`inactive`, so installation had started nothing.
- [x] The persisted AdGuard Home and Caddy configurations validated before
      `start` enabled anything.  The service then became active and enabled.

## Live named facts and boundaries

After startup readiness, every Resolver named fact passed:

- `resolver_service`
- `dns_listener`
- `admin_listener`
- `baseline_list_subscriptions`
- `filter_updates_manual`
- `query_log_retention`
- `firefox_canary`

The NAT verifier also passed every named fact, including
`resolver_listening`.  Separate live queries selected one loaded rule from each
Baseline-list cache: both the OISD NSFW rule and HaGeZi bypass rule returned the
Resolver's blocking answer, while `example.com` resolved normally.

Off-host observations:

- `dig @134.209.165.82 example.com` timed out with no DNS answer.
- Public TCP port 3000 was closed.
- Unauthenticated `https://agh.nightloch.app/` returned Caddy `401`.
- `https://lite.nightloch.app/` continued to return `200` from the staging
  static site.
- `https://lite.nightloch.app/health` returned `404`, not a fabricated health
  result: the Nightloch application service and database have not been
  deployed yet.

## Live verifier defect found and fixed

AdGuard Home accepted the rendered seven-day value `168h`, then normalized the
persisted `querylog.interval` to the equivalent `7d`.  The first live verifier
compared those strings literally and therefore reported a false failure even
though retention was exactly seven days.

The minimized reproduction is now a regression test.  The verifier converts
supported hour/day representations to hours before comparing them; it accepts
`168h` and `7d` as equal while the existing test still rejects the vendor's
90-day value.  The corrected verifier was installed with the previous version
retained at
`/usr/local/sbin/nightloch-resolver-verify.sh.before-duration-normalization`.

## Unattended reboot

The droplet rebooted from boot ID
`28aa681b-929d-41c7-bd43-632d0ceacb91` to
`a5763c58-6ced-4be6-a67e-91a8faf131da`.  SSH disconnected and returned without
an interactive recovery command.

`wg-quick@wg0.service` entered active at monotonic `15.124784s`, followed by
`AdGuardHome.service` at `15.378421s`, preserving the required order.  AdGuard
Home's administration listener came up first.  Its startup address processing
made a reverse-DNS query to `127.0.0.53` that timed out after 20 seconds; TCP
and UDP DNS then bound at monotonic `36.716724s` without a service restart.
This delay is recorded for the real-Device recovery drill rather than hidden by
an early retry.

After DNS readiness, both complete verifier suites passed, ordinary DNS
resolved, public DNS still gave no answer, the Resolver route still returned
`401`, and the staging origin still returned `200`.

## Still blocked on a real Device

The host has no `nightloch-wg` helper, application database/service, User, or
WireGuard Device peer.  Consequently this run does **not** claim any Device
criterion from issue #42.  The following still require a real phone and
dual-stack Mac on the tunnel:

- tunnel-on/tunnel-off blocking and ordinary browsing;
- recovery observed from a connected phone during a droplet reboot;
- hard-coded public DNS interception from inside the tunnel;
- Android strict Private DNS behavior;
- IPv6 leak checks and the one-day false-positive observation.
