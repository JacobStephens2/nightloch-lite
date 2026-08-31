# Slice 4 drill run record - issue #42

This is the durable record for the Slice 4 gate as of **2026-08-31**.  It records what was
observed, links observations already preserved by the host-specific run records, and leaves
every human-only criterion open until it actually runs.  Configuration intent is not a
drill result.

## Gate at a glance

| Criterion | State | Evidence |
|---|---|---|
| Tunnel on blocks an OISD sample while ordinary browsing works; tunnel off opens both | Observed | [The macOS Device run](./SLICE-4-NAT-RUN-RECORD.md#live-device-run---macos) records the public answer before activation, the blocked answer and working HTTPS while active, and the restored public answer after deactivation |
| Android phone reboot and strict Private DNS | Not run | Deferred to [issue #54](https://github.com/JacobStephens2/nightloch-lite/issues/54); the available phone's required VPN remains in place |
| IPv6 leak check from a dual-stack Mac and explicit resulting posture | Not run | The network available on 2026-08-31 did not provide the Mac a native public IPv6 path; the non-qualifying observation is below |
| DNS aimed at a public resolver still reaches the Resolver | Observed | [The macOS Device run](./SLICE-4-NAT-RUN-RECORD.md#live-device-run---macos) records blocked and Firefox-canary answers through an explicit `8.8.8.8` destination |
| One ordinary-browsing day records OISD false positives | Not run | The first Device had existed for only part of an evening; no shorter or synthetic interval is being called a day |
| Instagram catalogue membership and schedule polarity | Observed | The live read-only API and the exact deployed-version implementation were inspected at 2026-08-31 01:18 UTC; details are below |
| Every result has an ops artifact | In progress | Completed results are in this record and the linked NAT/Resolver run records; the criteria that have not run cannot yet have result artifacts |
| Enrollment recovery copy reconciled with the phone reboot | Not run | The Android observation and copy reconciliation remain together in issue #54 |

## Instagram blocked-service observation

The live Resolver reported AdGuard Home **v0.107.79**.  An authenticated, read-only request
to its loopback administration API returned **139** catalogue entries from
`GET /control/blocked_services/all`.  Exactly one entry had id `instagram`: its display
name was **Instagram**, its group was `social_network`, and it carried 72 rules.  The rules
included `instagram.com`, `cdninstagram.com`, `ig.me`, and `igcdn.com`.

The live `GET /control/blocked_services/get` response contained no selected ids and only
`time_zone: Local` in its empty schedule.  This check therefore did not enable Instagram,
change a schedule, or otherwise mutate the Resolver.

The schedule polarity in the exact deployed release is **pause, not active block**:

- [AdGuard Home v0.107.79 applies the selected service rules only when the configured
  schedule does *not* contain the current time](https://github.com/AdguardTeam/AdGuardHome/blob/v0.107.79/internal/filtering/blocked.go#L124-L136).
- [The same release labels the control "Pause service blocking" and describes the ranges as
  inactivity periods](https://github.com/AdguardTeam/AdGuardHome/blob/v0.107.79/client/src/__locales/en.json#L629-L640).

Consequently, a configured window is time when selected services are **not blocked**;
outside the window, the selected catalogue rules apply.  This records the mechanism and
its polarity only.  It deliberately makes no decision about whether the Curfew slice
should use it.

## IPv6 precondition observation - not the leak drill

At **2026-08-31 01:18 UTC**, before attempting the issue's IPv6 leak steps:

- `scutil --nwi` reported `No IPv6 states found`; the active Wi-Fi interface had IPv4 and
  DNS reachability only.
- A route lookup for a public Cloudflare IPv6 address returned `not in table`.
- AAAA lookups succeeded, which proved only that the IPv4 DNS resolver could return AAAA
  records.  A `curl -6` request reported an IPv4-mapped remote address, not a native public
  IPv6 route from the Mac.

This network was therefore not dual-stack and could not answer whether the installed
`::/0` route blackholes IPv6 instead of leaking it.  No IPv6 posture is finalized from
these facts.  Repeat the actual `curl -6`, `scutil --dns`, route, and blocked-name AAAA
checks when the Mac has a native public IPv6 address and route, then record the resulting
posture as the issue requires.

## Remaining human observations

- Complete issue #54 on a compatible Android Device without displacing its required VPN,
  then reconcile the enrollment screen's Android recovery copy against the exact visible
  failure and recovery.
- Use a genuinely dual-stack Mac network for the IPv6 leak drill and record the explicit
  resulting posture.
- Complete a real day of ordinary browsing on the Nightloch Device and list every OISD
  false positive encountered, including an explicit `none observed` if that is the result.

Until those observations exist, issue #42 remains a partially executed gate.
