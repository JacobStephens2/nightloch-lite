# Keep the NAT posture in its own nftables table

The host has to do four things to packets: forward tunnel traffic to the internet under its
own address, redirect port 53 from the tunnel to the resolver, reject port 853 from the
tunnel, and still be a host with a firewall on it. Three tools on this droplet can express
those rules - ufw, wg-quick's `PostUp`, and nftables directly - and the obvious choices are
the two wrong ones.

**Not wg-quick `PostUp`.** Putting the MASQUERADE and the redirect in `wg0.conf` is the
common recipe, and it ties the DNS posture's lifetime to the interface's. `wg-quick down wg0`
then removes the redirect, which is the exact moment it matters least whether the tunnel is
up: a Device that reconnects during the window between the interface coming back and the
rules being reapplied resolves wherever it likes. It also puts rules inside a file the
`nightloch-wg` helper rewrites on every mint and every removal, so a membership change and a
packet-posture change become one edit to one file. They are not one thing and should not
share a failure.

**Not ufw's `before.rules`.** ufw persists a `*nat` block there, and it is the only surface
ufw offers for this. It means the posture is expressed as a patch to a file the ufw package
owns and upgrades, reviewed as a diff against Ubuntu's copy rather than read as itself, and
it is not testable as an artifact.

So the posture is one nftables table, `inet nightloch`, in `deploy/nightloch-nat.nft`, loaded
by `nightloch-nat.service` at boot. Three consequences are load-bearing enough to state.

**It never flushes a ruleset it does not own.** The `/etc/nftables.conf` Ubuntu ships opens
with `flush ruleset`, and ufw's tables are in that ruleset. A posture written that way erases
the host firewall at every boot, in the order the units happen to start. This file creates
its table empty and deletes it before defining it, which replaces exactly this posture
atomically and touches nothing else. `nftables.service` itself is masked at first boot for
the same reason. The test asserts the file contains no global flush, because the failure is
silent and only visible at the next reboot.

**It matches `wg0` by name, not by index.** `iif` resolves an interface index while the file
is parsed, so a ruleset written that way refuses to load on any boot where wg-quick has not
run yet - and that is most of them, since the unit is deliberately ordered `Before` it.
`iifname` compares at match time. Loading before the tunnel exists is what closes the window
that the `PostUp` approach opens.

**Its filter chains hook ahead of ufw, at `priority filter - 10`.** ufw's input policy is
deny. A reject for port 853 that hooked after ufw would never be reached, and the Device
would experience a drop: strict Private DNS resolving nothing, forever, with no error. The
whole reason 853 is rejected rather than dropped is that a Device must fail visibly, which
means the reject has to be first or it is not a reject at all.

Forwarding itself is a sysctl drop-in rather than part of the table, because it is the
kernel's state and not a rule, and because the two want opposite orderings when the posture
is turned on: rules first, forwarding last. `nightloch-host-posture.sh activate` enforces that
order, so there is no instant in which the host forwards tunnel traffic with DNS still free
to go anywhere.

The cost of all this is one more unit and one more file to install, and a posture that ufw
cannot show you - `ufw status` will not list any of it. That is the trade: `nft list table
inet nightloch` is the place it lives, and `deploy/nightloch-nat-verify.sh` is what reads it
back and says whether it holds.
