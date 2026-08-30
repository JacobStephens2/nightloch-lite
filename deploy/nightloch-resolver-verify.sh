#!/bin/sh
#
# Read the live AdGuard Home posture back as named facts.  Every command here
# is a read: this verifier is safe to run against the live host without
# authority to install, enable, refresh, or rewrite anything.

set -u

root="${NIGHTLOCH_RESOLVER_ROOT:-}"

config="$root/etc/AdGuardHome/AdGuardHome.yaml"
unit="$root/etc/systemd/system/AdGuardHome.service"
caddy_site="$root/etc/caddy/sites/nightloch-agh.caddy"
caddy_env="$root/etc/nightloch/caddy.env"
nightloch_binary="$root/opt/nightloch/bin/nightloch"
filters_dir="$root/var/lib/AdGuardHome/data/filters"

host_address="10.66.0.1"
admin_address="127.0.0.1:3000"

failures=0

pass() {
	printf 'PASS %s - %s\n' "$1" "$2"
}

fail() {
	printf 'FAIL %s - %s\n' "$1" "$2"
	failures=$((failures + 1))
}

check_service() {
	enabled="$(systemctl is-enabled AdGuardHome.service 2>/dev/null || true)"
	active="$(systemctl is-active AdGuardHome.service 2>/dev/null || true)"
	if [ "$enabled" = "enabled" ] && [ "$active" = "active" ] &&
		[ -r "$unit" ] &&
		grep -Fq 'Requires=wg-quick@wg0.service' "$unit" &&
		grep -Fq 'After=network-online.target wg-quick@wg0.service' "$unit"; then
		pass resolver_service "AdGuard Home is active, enabled, and ordered after wg0"
	else
		fail resolver_service "state is ${active:-unknown}/${enabled:-unknown}, or the installed unit is not ordered after wg0"
	fi
}

check_listeners() {
	# The dollar sign is intentionally literal Caddy placeholder syntax.
	# shellcheck disable=SC2016
	caddy_password_line='operator {$ADGUARD_PASSWORD_HASH}'
	if ! listeners="$(ss -H -lntu 2>/dev/null)"; then
		fail dns_listener "cannot read listeners; an unread port is not a tunnel-only port"
		fail admin_listener "cannot read listeners; the administration boundary is unknown"
		return
	fi

	dns_udp=""
	dns_tcp=""
	dns_exposed=""
	admin_loopback=""
	admin_exposed=""
	old_ifs="$IFS"
	IFS='
'
	for line in $listeners; do
		transport="$(printf '%s\n' "$line" | awk '{ print $1 }')"
		local_address="$(printf '%s\n' "$line" | awk '{ print $5 }')"
		case "$local_address" in
		"$host_address:53")
			case "$transport" in
			udp) dns_udp="yes" ;;
			tcp) dns_tcp="yes" ;;
			esac
			;;
		127.*:53) ;;
		*:53) dns_exposed="$dns_exposed $local_address" ;;
		esac
		case "$local_address" in
		"$admin_address") admin_loopback="yes" ;;
		*:3000) admin_exposed="$admin_exposed $local_address" ;;
		esac
	done
	IFS="$old_ifs"

	if [ -n "$dns_udp" ] && [ -n "$dns_tcp" ] && [ -z "$dns_exposed" ]; then
		pass dns_listener "TCP and UDP DNS listen only on $host_address"
	else
		fail dns_listener "DNS is missing a transport on $host_address or also listens on${dns_exposed:- no other reported address}"
	fi

	if [ -n "$admin_loopback" ] && [ -z "$admin_exposed" ] &&
		[ -r "$caddy_site" ] && [ -s "$caddy_env" ] &&
		grep -Fq 'basic_auth {' "$caddy_site" &&
		grep -Fq "$caddy_password_line" "$caddy_site" &&
		grep -Fq 'reverse_proxy 127.0.0.1:3000' "$caddy_site"; then
		pass admin_listener "administration listens on loopback and its Caddy route requires Basic auth"
	else
		fail admin_listener "administration is not loopback-only with the authenticated Caddy route installed"
	fi
}

filter_enabled() {
	wanted_url="$1"
	awk -v wanted="$wanted_url" '
		function finish() {
			if (url == wanted && enabled == "true") found = 1
		}
		/^[[:space:]]*-[[:space:]]/ {
			finish()
			url = ""
			enabled = ""
		}
		$1 == "-" && $2 == "url:" { url = $3 }
		$1 == "url:" { url = $2 }
		$1 == "-" && $2 == "enabled:" { enabled = $3 }
		$1 == "enabled:" { enabled = $2 }
		END {
			finish()
			exit found ? 0 : 1
		}
	' "$config"
}

check_baseline() {
	oisd_url="https://nsfw.oisd.nl/"
	hagezi_url="https://adguardteam.github.io/HostlistsRegistry/assets/filter_52.txt"
	if [ -r "$config" ] &&
		filter_enabled "$oisd_url" && filter_enabled "$hagezi_url" &&
		[ -s "$filters_dir/1.txt" ] && [ -s "$filters_dir/2.txt" ]; then
		pass baseline_subscriptions "the OISD NSFW and HaGeZi global lists are enabled and loaded"
	else
		fail baseline_subscriptions "one or both global Baseline subscriptions are disabled, absent, or have no loaded rules"
	fi

	if [ -r "$config" ] && grep -Eq '^[[:space:]]*filters_update_interval:[[:space:]]*0[[:space:]]*$' "$config"; then
		pass filter_updates_manual "automatic filter refresh is disabled; updates require an explicit Operator action"
	else
		fail filter_updates_manual "filters_update_interval is not zero, so an upstream change can land silently"
	fi
}

check_retention() {
	if [ ! -x "$nightloch_binary" ] || [ ! -r "$config" ]; then
		fail query_log_retention "the Nightloch binary or AdGuard Home configuration is unreadable"
		return
	fi
	retention="$($nightloch_binary resolver-query-log-retention 2>/dev/null || true)"
	configured_retention="$(awk '
		/^[^[:space:]#][^:]*:[[:space:]]*$/ {
			in_querylog = ($1 == "querylog:")
		}
		in_querylog && $1 == "interval:" {
			print $2
			exit
		}
	' "$config")"
	if [ -n "$retention" ] && [ "$configured_retention" = "$retention" ]; then
		pass query_log_retention "query history retention is $retention, derived from the address-quarantine rule"
	else
		fail query_log_retention "AdGuard Home does not retain query history for Nightloch's configured $retention"
	fi
}

check_canary() {
	# The dollar sign is intentionally literal Adblock rule syntax.
	# shellcheck disable=SC2016
	canary_rule='||use-application-dns.net^$dnsrewrite=NXDOMAIN;;'
	if [ ! -r "$config" ] || ! grep -Fq "$canary_rule" "$config"; then
		fail firefox_canary "the global Firefox canary rule is absent"
		return
	fi
	answer="$(dig +time=2 +tries=1 "@$host_address" use-application-dns.net A 2>/dev/null || true)"
	case "$answer" in
	*"status: NXDOMAIN"*) pass firefox_canary "use-application-dns.net is answered with NXDOMAIN" ;;
	*) fail firefox_canary "use-application-dns.net did not return NXDOMAIN" ;;
	esac
}

check_service
check_listeners
check_baseline
check_retention
check_canary

if [ "$failures" -ne 0 ]; then
	printf '\n%s named fact(s) failed. The Resolver posture does not hold.\n' "$failures"
	exit 1
fi

printf '\nEvery Resolver named fact holds.\n'
