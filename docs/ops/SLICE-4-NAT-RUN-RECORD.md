# NAT checklist run record - Slice 4 / issue #40

What follows is the state of every step of [NAT-CHECKLIST.md](./NAT-CHECKLIST.md) as of
**2026-08-30**. It is a record, not a plan: a step is either done, with what was done and
what it showed, or it is not done, with what it is waiting on. Nothing is marked done
because it was prepared.

**There is no droplet.** Slice 1's `tofu apply` has not been run, so every step that reads or
changes a live host is blocked on that, and then on an explicit human go-ahead for the
forwarding change itself. No forwarding was enabled, no rule was loaded, and no host was
touched by this work.

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
| Verifier catches each failure by name | Four failure tests | Forwarding off, 853 dropped rather than rejected, DNS listening on `0.0.0.0`, and a posture that would not survive a reboot each fail by their own named fact |

## Not done - blocked on the Slice 1 apply

Every one of these needs a host. None of them can be honestly recorded any other way.

- [ ] DigitalOcean web console login confirmed working for `nightloch`
- [ ] Droplet snapshot taken, or backups confirmed green
- [ ] Second session open alongside the first
- [ ] `stage` run on the host; interface key generated; `TUNNEL_PUBLIC_KEY` filled in
- [ ] Self-cancelling revert scheduled with `systemd-run --on-active=10min`
- [ ] **Explicit human go-ahead obtained for the first forwarding change**
- [ ] `activate --i-have-run-the-nat-checklist` run by a human
- [ ] SSH still works after the change
- [ ] `https://lite.nightloch.app` still serves after the change
- [ ] `nightloch-nat-verify.sh` passes on the host
- [ ] The public address answers nothing on port 53, asked from off the host
- [ ] Revert timer cancelled after the checks passed
- [ ] Reboot drill with a Device on the tunnel; the posture returns unattended
- [ ] From a Device: DNS aimed at `8.8.8.8` still reaches the resolver
- [ ] From a Device: strict Private DNS fails visibly rather than hanging

## What an agent may do next without approval

`sudo ./deploy/nightloch-host-posture.sh stage` and `sudo nightloch-nat-verify.sh`. Neither
enables forwarding, loads a rule, or starts a unit; the tests assert that rather than the
comments claiming it. Everything past staging is the human's, and the `activate` subcommand
refuses without the checklist confirmation flag rather than trusting anyone to remember.
