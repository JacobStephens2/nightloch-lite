#!/bin/sh
#
# nightloch-host-posture.sh - install, turn on, and take back the host's
# tunnel forwarding and DNS posture.
#
# Run as root on the droplet. The files it installs are the whole posture:
#
#   /etc/nightloch/nightloch-nat.nft           the rules
#   /etc/systemd/system/nightloch-nat.service  what loads them, at every boot
#   /etc/sysctl.d/99-nightloch-forward.conf    forwarding, across a reboot
#   /etc/wireguard/wg0.conf                    the interface, generated once
#
# Nothing here is an interactive command whose effect dies at the next reboot,
# which is the point: an unattended reboot must not leave Devices connected to
# a host that forwards nothing.
#
# Three subcommands, because preparing the posture and enabling forwarding on a
# live host are not the same act and must not be the same command:
#
#   stage      install every file and generate the interface key. Enables
#              nothing, starts nothing, forwards nothing. Safe before approval.
#   activate   turn the posture on and make it survive a reboot. This is the
#              forwarding change. It refuses without the confirmation flag that
#              says docs/ops/NAT-CHECKLIST.md has been run.
#   revert     put the host back. Takes no argument and needs no confirmation,
#              because it is what the checklist's self-cancelling `at` job runs
#              when nobody is there to answer a prompt.
#
# Exit codes match the nightloch-wg helper's, so a caller reads them the same
# way: 0 success, 2 refusal (unknown subcommand, or activation without the
# checklist confirmation), 3 the host is not in a state the operation can be
# performed against.
#
# NIGHTLOCH_POSTURE_ROOT and NIGHTLOCH_POSTURE_SOURCE exist so the tests can
# run this script against a scratch root. They are not a privilege surface:
# nothing grants sudo to this path, and an Operator running it is already root.

set -eu
umask 022

root="${NIGHTLOCH_POSTURE_ROOT:-}"
source_dir="${NIGHTLOCH_POSTURE_SOURCE:-$(dirname "$0")}"

etc="$root/etc"
ruleset_dest="$etc/nightloch/nightloch-nat.nft"
unit_dest="$etc/systemd/system/nightloch-nat.service"
sysctl_dest="$etc/sysctl.d/99-nightloch-forward.conf"
wg_dest="$etc/wireguard/wg0.conf"
wg_pub_dest="$etc/wireguard/wg0.pub"
ufw_defaults="$etc/default/ufw"

# The host's address inside the tunnel. The same fact internal/enforcement
# reserves and the nightloch-wg helper validates against; asserted to agree in
# internal/enforcement/hostposture_test.go.
host_address="10.66.0.1"
tunnel_interface="wg0"

usage() {
	cat >&2 <<'USAGE'
usage: nightloch-host-posture.sh stage
       nightloch-host-posture.sh activate --i-have-run-the-nat-checklist
       nightloch-host-posture.sh revert
USAGE
}

# refuse reports a closed door: nothing has been invoked or written.
refuse() {
	printf 'nightloch-host-posture: %s\n' "$1" >&2
	usage
	exit 2
}

# unavailable reports that the host is not in a state this operation can be
# performed against. Distinct from a refusal so an Operator can tell their own
# mistake from a host that is not ready.
unavailable() {
	printf 'nightloch-host-posture: %s\n' "$1" >&2
	exit 3
}

require_root() {
	if [ -z "$root" ] && [ "$(id -u)" -ne 0 ]; then
		unavailable "this must run as root"
	fi
}

# set_forward_policy rewrites one directive in ufw's defaults in place, keeping
# the file's own mode and every other line. ufw's forward policy is deny out of
# the box and it hooks alongside this posture rather than under it, so leaving
# it alone would mean forwarding stays dead however this table is written.
set_forward_policy() {
	policy="$1"
	if [ ! -f "$ufw_defaults" ]; then
		unavailable "$ufw_defaults is missing; is ufw installed?"
	fi
	pending="$ufw_defaults.nightloch-pending.$$"
	awk -v policy="$policy" '
		/^[[:space:]]*DEFAULT_FORWARD_POLICY=/ {
			print "DEFAULT_FORWARD_POLICY=\"" policy "\""
			found = 1
			next
		}
		{ print }
		END { if (!found) print "DEFAULT_FORWARD_POLICY=\"" policy "\"" }
	' "$ufw_defaults" >"$pending"
	cat "$pending" >"$ufw_defaults"
	rm -f "$pending"
}

cmd_stage() {
	for artifact in nightloch-nat.nft nightloch-nat.service 99-nightloch-forward.conf wg0.conf.template; do
		if [ ! -f "$source_dir/$artifact" ]; then
			unavailable "$source_dir/$artifact is missing"
		fi
	done
	require_root

	install -D -m 0644 "$source_dir/nightloch-nat.nft" "$ruleset_dest"
	install -D -m 0644 "$source_dir/nightloch-nat.service" "$unit_dest"
	install -D -m 0644 "$source_dir/99-nightloch-forward.conf" "$sysctl_dest"

	# The interface configuration is generated, never shipped. Its private key
	# is the tunnel's identity: it is made here and never leaves the host.
	#
	# An existing file is left exactly as it is. Rewriting it would discard
	# that identity and every peer nightloch-wg has added since, which is every
	# Device anyone has minted.
	if [ ! -f "$wg_dest" ]; then
		install -d -m 0700 "$(dirname "$wg_dest")"
		private_key="$(wg genkey)"
		pending="$wg_dest.nightloch-pending.$$"
		( umask 077 && : >"$pending" )
		sed "s|__PRIVATE_KEY__|$private_key|" "$source_dir/wg0.conf.template" >"$pending"
		chmod 0600 "$pending"
		mv "$pending" "$wg_dest"
		printf '%s\n' "$private_key" | wg pubkey >"$wg_pub_dest.nightloch-pending.$$"
		chmod 0644 "$wg_pub_dest.nightloch-pending.$$"
		mv "$wg_pub_dest.nightloch-pending.$$" "$wg_pub_dest"
	fi

	systemctl daemon-reload

	cat <<EOF
Staged. Nothing forwards yet and nothing is enabled.
Interface public key (TUNNEL_PUBLIC_KEY): $(cat "$wg_pub_dest")
Next: run docs/ops/NAT-CHECKLIST.md, then
  nightloch-host-posture.sh activate --i-have-run-the-nat-checklist
EOF
}

cmd_activate() {
	if [ "${1:-}" != "--i-have-run-the-nat-checklist" ]; then
		refuse "activation is the first forwarding change on a live host; run docs/ops/NAT-CHECKLIST.md first and confirm with --i-have-run-the-nat-checklist"
	fi
	for staged in "$ruleset_dest" "$unit_dest" "$sysctl_dest" "$wg_dest"; do
		if [ ! -f "$staged" ]; then
			unavailable "$staged is missing; run 'stage' first"
		fi
	done
	require_root

	# Order is the whole safety of this sequence. The redirect and the
	# masquerade are in force before the kernel forwards a single packet, so
	# there is no window in which the tunnel routes to the internet with DNS
	# still free to go anywhere.
	set_forward_policy ACCEPT
	ufw allow in on "$tunnel_interface" to "$host_address" port 53 proto udp
	ufw allow in on "$tunnel_interface" to "$host_address" port 53 proto tcp
	ufw reload

	systemctl enable --now nightloch-nat.service
	sysctl --system
	systemctl enable --now "wg-quick@$tunnel_interface.service"

	cat <<'EOF'
Active. Forwarding is on, the posture is loaded, and both come back on reboot.
Verify with: nightloch-nat-verify.sh
Take it back with: nightloch-host-posture.sh revert
EOF
}

cmd_revert() {
	require_root

	# Forwarding stops first. Everything after this is tidying; if the session
	# running this dies partway through, the host is already no longer
	# carrying tunnel traffic anywhere.
	sysctl -w net.ipv4.ip_forward=0 || true
	systemctl disable --now nightloch-nat.service || true
	nft delete table inet nightloch || true
	set_forward_policy DROP
	ufw reload || true

	# The tunnel itself is not the forwarding change, and taking wg0 down
	# would disconnect anyone reverting from inside it.
	cat <<'EOF'
Reverted. Forwarding is off and the posture is unloaded; wg0 is untouched.
The staged files are still in place, so 'activate' can turn it back on.
EOF
}

if [ $# -eq 0 ]; then
	refuse "no subcommand"
fi

subcommand="$1"
shift

case "$subcommand" in
stage)
	if [ $# -ne 0 ]; then
		refuse "stage takes no argument"
	fi
	cmd_stage
	;;
activate)
	if [ $# -gt 1 ]; then
		refuse "activate takes only the checklist confirmation"
	fi
	cmd_activate "${1:-}"
	;;
revert)
	if [ $# -ne 0 ]; then
		refuse "revert takes no argument"
	fi
	cmd_revert
	;;
*)
	refuse "unknown subcommand"
	;;
esac
