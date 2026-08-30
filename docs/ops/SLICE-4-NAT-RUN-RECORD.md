# NAT checklist run record - Slice 4 / issue #40

What follows is the state of every step of [NAT-CHECKLIST.md](./NAT-CHECKLIST.md) as of
**2026-08-30**. It is a record, not a plan: a step is either done, with what was done and
what it showed, or it is not done, with what it is waiting on. Nothing is marked done
because it was prepared.

The Slice 1 droplet now exists at `134.209.165.82`. The host-side portion of the first live
run completed on **2026-08-30**: the Operator confirmed the out-of-band DigitalOcean
Recovery Console, approved and personally ran activation, and the host posture survived an
unattended reboot.
There is not yet a Device, so the three checks that can only be answered from a Device
remain blocked rather than inferred from the host.  Issue #41's Resolver posture is now
live and its observations are recorded in
[SLICE-4-RESOLVER-RUN-RECORD.md](./SLICE-4-RESOLVER-RUN-RECORD.md).

## Done

| Step | What was done | Result |
|---|---|---|
| Posture written as files | `deploy/nightloch-nat.nft`, `deploy/nightloch-nat.service`, `deploy/99-nightloch-forward.conf`, `deploy/wg0.conf.template` | Installed as files by `nightloch-host-posture.sh stage`; nothing is a command whose effect ends at the next reboot |
| Ruleset accepted by nftables | `nft --check -f deploy/nightloch-nat.nft` on nftables v1.0.9 | Exit 0. The file parses; `priority filter - 10`, `redirect to :53`, `reject with tcp reset`, and the create-then-delete replacement idiom are all accepted as written |
| Ruleset owns only its own table | Asserted in `internal/enforcement/hostposture_test.go` | No `flush ruleset` anywhere in the file. ufw's tables are untouched by every load |
| Rules load before the tunnel exists | Same tests; `iif`/`oif` are asserted absent | Every match is `iifname`/`oifname`, so the ruleset loads on a boot where wg-quick has not run |
| Reject reaches the packet before ufw drops it | Same tests | Both filter chains hook at `priority filter - 10` |
| Reboot survival is structural | Same tests | `WantedBy=multi-user.target` on the unit, the sysctl drop-in, and `systemctl enable --now` on both units at activation |
| One tunnel port everywhere | Same tests | `51820` in the cloud firewall (`infra/opentofu/main.tf`), the interface template, `deploy/nightloch.env.example`, and the ufw allowance. Slice 1's firewall carries the tunnel without revision |
| The interface configuration is one the helper manages | `TestTheRenderedInterfaceConfigurationIsOneTheHelperWillManage` runs the real `nightloch-wg` against the rendered template | `add-peer` succeeds and the `[Interface]` section survives verbatim |
| Staging changes nothing | `TestStagingInstallsThePostureWithoutTurningAnythingOn` | Stage enables nothing, starts nothing, and never calls `sysctl --system` or `nft -f` |
| Activation cannot happen by accident | `TestActivationRefusesWithoutTheChecklistConfirmation` | Refused, exit 2, no command invoked and ufw's forward policy still `DROP` |
| The way back exists and needs no operator | `TestRevertTakesTheHostBackToWhereItWasBeforeForwarding` | `revert` takes no argument, turns forwarding off first, and leaves `wg0` up |
| Verifier is read-only | `TestTheVerifierIsReadOnly` | Every invocation is a read; the script is safe to run against a live host by anyone, including an agent |
| Verifier catches each failure by name | Six failure tests | Forwarding off, 853 dropped rather than rejected, DNS listening on `0.0.0.0`, a posture that would not survive a reboot, DNS not redirected, and listeners it could not read at all each fail by their own named fact |
| A missing resolver is reported, not hidden and not fatal | `TestTheVerifierReportsAMissingResolverWithoutFailingACorrectPosture` | `resolver_listening` warns while AdGuard Home has not landed. A redirect to a port nothing answers on is worth saying and is not this posture being wrong, so it governs nothing - the shape `/health` already uses for a non-governing check |
| The interface key never reaches an argument list | `TestStagingNeverPutsThePrivateKeyInAnArgumentList` | Every local user can read every argv on the host; the substitution is shell builtins over a `0600` file |
| Activation's claimed order is the order it runs in | `TestActivationTurnsThePostureOnAndMakesItSurviveAReboot` | The rules load before `ufw reload`, and `sysctl --system` runs after it, so ufw's own `/etc/ufw/sysctl.conf` pass cannot be what decides whether the host forwards |
| Revert puts back what was there | `TestRevertRestoresTheForwardPolicyTheHostActuallyHad`, `TestRevertTakesTheHostBackToWhereItWasBeforeForwarding` | The wg0 DNS allowances are withdrawn, and the recorded prior forward policy is restored rather than `DROP` assumed |

## Live host-side run

- [x] DigitalOcean Recovery Console login confirmed for `nightloch`; `whoami` returned
      `nightloch`. This was the out-of-band QEMU console, not the network-dependent Web
      Console.
- [x] Snapshot `nightloch-lite-pre-nat-2026-08-30` completed before staging: 4.51 GB in
      NYC3. Automated backups were enabled but none existed yet.
- [x] Two SSH sessions were opened from the Operator laptop and left open through
      activation.
- [x] The untouched verifier result was recorded: forwarding, `wg0`, the NAT rules, and
      reboot persistence were absent; IPv6 forwarding was off; no Resolver was listening.
- [x] `stage` installed the posture and generated the interface key without enabling a
      unit, loading a rule, or changing forwarding. `/etc/wireguard/wg0.conf` was
      `0600 root:root`; the only other key artifact was the public `wg0.pub`.
- [x] Put `TUNNEL_PUBLIC_KEY` and the published endpoint in
      `/etc/nightloch/nightloch.env`. The value was read from the live interface and only
      the public half was stored.
- [ ] Restart `nightloch.service` after the service and its remaining mail/challenge
      secrets are deployed. The unit still does not exist, so no restart is claimed.
- [x] A live verifier defect was reproduced before activation: Ubuntu 24.04's
      loopback-only `systemd-resolved` listeners (`127.0.0.53%lo:53` and
      `127.0.0.54:53`) were mislabeled public. A regression test now carries the real
      `ss` output, the verifier ignores IPv4 loopback listeners, every verifier test
      passes, and the corrected script was installed before proceeding.
- [x] `nightloch-nat-revert.timer` was scheduled for ten minutes and confirmed active
      before activation.
- [x] The Operator explicitly approved the first forwarding change and personally ran
      `activate --i-have-run-the-nat-checklist`. Activation completed without error.
- [x] The second SSH session responded, a fresh key-authenticated SSH connection from the
      laptop succeeded, and `https://lite.nightloch.app` still served before the revert
      timer was cancelled.
- [x] Every governing named fact passed on the live host. Before issue #41,
      `resolver_listening` reported the expected non-governing warning; after the live
      Resolver install it reports `PASS`.
- [x] Off-host `dig +time=3 +tries=1 @134.209.165.82 example.com` timed out with no answer.
- [x] The revert timer was stopped only after all preceding checks passed and was confirmed
      inactive.
- [x] A host-only reboot ran at 20:48 UTC. Without an interactive command after boot,
      `ip_forward`, `wg0`, the Nightloch nftables table, and both enabled units returned;
      the verifier passed, HTTPS served, and off-host port 53 still gave no answer. This did
      not satisfy the separate checklist drill that requires a Device on the tunnel.

## Not done - blocked on a live Device

`wg show wg0 peers` still returns no peers. AdGuard Home now listens on `10.66.0.1:53`,
but these checks need an enrolled Device; the host cannot answer them honestly. Carry the
Device-only observations into issue #42.

- [ ] Reboot the droplet with a Device on the tunnel; verify that the Device reconnects and
      browsing works.
- [ ] From a Device, DNS aimed at `8.8.8.8` is redirected to the Resolver and blocked.
- [ ] From a Device, strict Private DNS fails visibly rather than hanging.

## What an agent may do next without approval

`sudo nightloch-nat-verify.sh` remains read-only. `stage` remains safe to run again: it
reinstalls the posture files without replacing the existing interface configuration or its
key. A future activation is still the human Operator's and still refuses without the
checklist confirmation flag.
