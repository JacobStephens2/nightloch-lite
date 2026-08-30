#!/bin/sh
#
# nightloch-nat-verify.sh - read the host and report whether the tunnel and NAT
# posture actually holds. It changes nothing, so it is safe to run against a
# live host at any time, including by an agent that has no authority to enable
# forwarding. Every command it issues is a read; the test asserts that.
#
# It reports one named fact per line and exits non-zero if any of them fails,
# so it is usable both as something a human reads during
# docs/ops/NAT-CHECKLIST.md and as something a script gates on.
#
# The facts are the acceptance criteria of the tunnel and NAT ticket, minus the
# two that cannot honestly be answered from the host itself: that SSH still
# works, and that the public address answers nothing on port 53 when asked from
# somewhere else. Those are in the checklist as off-host steps. What is checked
# here is the half that is checkable here: nothing is listening for DNS on
# anything but the tunnel address.
#
# NIGHTLOCH_POSTURE_ROOT lets the tests plant the kernel's own answers where
# this script reads them.

set -u

root="${NIGHTLOCH_POSTURE_ROOT:-}"

host_address="10.66.0.1"
tunnel_network="10.66.0.0/24"
tunnel_interface="wg0"
tunnel_port="51820"

failures=0

pass() {
	printf 'PASS %s - %s\n' "$1" "$2"
}

fail() {
	printf 'FAIL %s - %s\n' "$1" "$2"
	failures=$((failures + 1))
}

# read_kernel returns a /proc value, or the empty string when it cannot be
# read. An unreadable fact is never reported as a good one.
read_kernel() {
	if [ -r "$root$1" ]; then
		tr -d ' \t\n' <"$root$1"
	fi
}

check_forwarding() {
	value="$(read_kernel /proc/sys/net/ipv4/ip_forward)"
	if [ "$value" = "1" ]; then
		pass ip_forward "the host carries tunnel traffic to the internet"
	else
		fail ip_forward "net.ipv4.ip_forward is ${value:-unreadable}, so Devices reach nothing"
	fi

	value="$(read_kernel /proc/sys/net/ipv6/conf/all/forwarding)"
	if [ "$value" = "0" ]; then
		pass ipv6_forwarding_off "v6 from the tunnel blackholes rather than routing around the resolver"
	else
		fail ipv6_forwarding_off "net.ipv6.conf.all.forwarding is ${value:-unreadable}; v6 may bypass enforcement"
	fi
}

check_interface() {
	addresses="$(ip -o addr show dev "$tunnel_interface" 2>/dev/null || true)"
	port="$(wg show "$tunnel_interface" listen-port 2>/dev/null || true)"
	case "$addresses" in
	*"$host_address/24"*)
		if [ "$port" = "$tunnel_port" ]; then
			pass tunnel_interface "$tunnel_interface is up on $host_address and listening on $tunnel_port"
		else
			fail tunnel_interface "$tunnel_interface listens on ${port:-nothing}, not the port the cloud firewall opens ($tunnel_port)"
		fi
		;;
	*)
		fail tunnel_interface "$tunnel_interface does not hold $host_address/24"
		;;
	esac
}

check_rules() {
	rules="$(nft list table inet nightloch 2>/dev/null || true)"

	case "$rules" in
	*"ip saddr $tunnel_network"*masquerade*)
		pass masquerade "tunnel traffic leaves under the host's address"
		;;
	*)
		fail masquerade "no masquerade for $tunnel_network in table inet nightloch"
		;;
	esac

	redirects=0
	case "$rules" in *"udp dport 53 redirect"*) redirects=$((redirects + 1)) ;; esac
	case "$rules" in *"tcp dport 53 redirect"*) redirects=$((redirects + 1)) ;; esac
	if [ "$redirects" -eq 2 ]; then
		pass dns_redirect "DNS from the tunnel reaches the resolver on both transports"
	else
		fail dns_redirect "port 53 from the tunnel is not redirected on both TCP and UDP; a Device could resolve elsewhere"
	fi

	rejects=0
	case "$rules" in *"tcp dport 853 reject"*) rejects=$((rejects + 1)) ;; esac
	case "$rules" in *"udp dport 853 reject"*) rejects=$((rejects + 1)) ;; esac
	case "$rules" in
	*"dport 853 drop"*)
		fail dot_rejected "port 853 is dropped, so a Device on strict Private DNS hangs instead of failing visibly"
		;;
	*)
		if [ "$rejects" -eq 2 ]; then
			pass dot_rejected "DNS-over-TLS from the tunnel fails fast on both transports"
		else
			fail dot_rejected "port 853 from the tunnel is not rejected on both TCP and UDP"
		fi
		;;
	esac
}

check_persistence() {
	nat_state="$(systemctl is-enabled nightloch-nat.service 2>/dev/null || true)"
	tunnel_state="$(systemctl is-enabled "wg-quick@$tunnel_interface.service" 2>/dev/null || true)"
	if [ "$nat_state" = "enabled" ] && [ "$tunnel_state" = "enabled" ]; then
		pass reboot_persistence "the posture and the tunnel both come back unattended"
	else
		fail reboot_persistence "nightloch-nat is ${nat_state:-unknown} and wg-quick@$tunnel_interface is ${tunnel_state:-unknown}; an unattended reboot would leave Devices connected to nothing"
	fi
}

# check_public_resolver reads what is listening rather than what is configured.
# An open recursive resolver on the public address is the one failure of this
# posture that harms people who are not using it at all.
check_public_resolver() {
	listeners="$(ss -H -lntu 2>/dev/null || true)"
	exposed=""
	IFS='
'
	for line in $listeners; do
		local_address="$(printf '%s\n' "$line" | awk '{ print $5 }')"
		case "$local_address" in
		*:53) ;;
		*) continue ;;
		esac
		case "$local_address" in
		"$host_address:53") ;;
		*) exposed="$exposed $local_address" ;;
		esac
	done
	unset IFS
	if [ -z "$exposed" ]; then
		pass no_public_resolver "nothing answers DNS outside the tunnel address"
	else
		fail no_public_resolver "DNS is listening on$exposed; the public address must answer nothing on 53"
	fi
}

check_forwarding
check_interface
check_rules
check_persistence
check_public_resolver

if [ "$failures" -ne 0 ]; then
	printf '\n%s named fact(s) failed. The posture does not hold.\n' "$failures"
	exit 1
fi

printf '\nEvery named fact holds.\n'
