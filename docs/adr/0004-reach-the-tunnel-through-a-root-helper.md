# Reach the tunnel through a root helper under a narrow sudo grant

Changing tunnel membership is a privileged operation, and the Nightloch application
process must not be privileged. The three candidates were ambient capabilities on the
service unit, a privileged helper daemon on a local socket, and a single-purpose setuid-free
script invoked under a scoped sudo grant. The service unit's own comment previously assumed
the first two; this reverses that assumption and takes the third.

`CAP_NET_ADMIN` on the unit is the widest of the three: it applies to the whole application
for its whole life, so every future handler runs with the power to reconfigure any network
interface on the host, and nothing about the grant records that only three operations were
ever intended. A helper daemon is the narrowest in principle, but it is a long-lived
privileged process with its own protocol, lifecycle, and parsing surface — an artifact whose
correctness has to be argued about rather than read.

`/usr/local/sbin/nightloch-wg` is a shell script short enough to read in one sitting. It
accepts three subcommands, validates every argument against its expected shape before
invoking anything, and refuses everything else with no side effect. The grant in
`nightloch-wg.sudoers` names that one absolute path with no wildcard, and sets `env_reset`
and its own `secure_path` for that command alone, so the unprivileged caller chooses neither
the helper's environment nor which `wg` and `ip` it finds. The escalation is bounded by
what the script will do, and that boundary is auditable by reading one file.

The cost is real and is accepted: sudo cannot raise privilege when `NoNewPrivileges` is set,
so that hardening comes off the application unit. This is the trade — a general
no-escalation guarantee is exchanged for an escalation that is specific, enumerated, and
readable. Two properties keep the trade honest, and both are deployment requirements rather
than nice-to-haves: the helper must be root-owned and not writable by the `nightloch` user,
since a writable helper turns the grant into a root shell, and the grant must never grow a
wildcard, since an argument pattern sudo cannot express safely is one the helper should be
validating itself.
