#!/bin/sh
#
# Install and start Nightloch's AdGuard Home Resolver posture.
#
# `install` writes the pinned binary and every persistent artifact, renders a
# first configuration, and starts nothing.  `start` validates both AdGuard Home
# and Caddy before enabling the Resolver for this boot and future reboots.
#
# NIGHTLOCH_RESOLVER_ROOT and NIGHTLOCH_RESOLVER_SOURCE let the Go tests run
# the real script against a scratch root.  They grant no privilege on a host;
# an Operator invoking this file is already root.

set -eu
umask 022

root="${NIGHTLOCH_RESOLVER_ROOT:-}"
source_dir="${NIGHTLOCH_RESOLVER_SOURCE:-$(dirname "$0")}"

etc="$root/etc"
opt="$root/opt"
var="$root/var"

config_dest="$etc/AdGuardHome/AdGuardHome.yaml"
unit_dest="$etc/systemd/system/AdGuardHome.service"
caddy_site_dest="$etc/caddy/sites/nightloch-agh.caddy"
caddy_dropin_dest="$etc/systemd/system/caddy.service.d/nightloch.conf"
caddy_env_dest="$etc/nightloch/caddy.env"
password_hash_file="$etc/nightloch/adguard-password.bcrypt"
caddyfile="$etc/caddy/Caddyfile"
binary_dest="$opt/AdGuardHome/AdGuardHome"
nightloch_binary="$opt/nightloch/bin/nightloch"
work_dest="$var/lib/AdGuardHome"

# A release is an explicit host change, not something AdGuard Home performs
# from its UI.  The checksum is the linux/amd64 asset digest published with
# v0.107.79 on 2026-08-18.
adguard_version="v0.107.79"
adguard_archive="AdGuardHome_linux_amd64.tar.gz"
adguard_sha256="c48f4a43000665484c5ec28177de11a004759b620dae8f77b2aabefc9ef3687f"
adguard_url="https://github.com/AdguardTeam/AdGuardHome/releases/download/$adguard_version/$adguard_archive"

usage() {
	cat >&2 <<'USAGE'
usage: nightloch-resolver-posture.sh install
       nightloch-resolver-posture.sh start
USAGE
}

refuse() {
	printf 'nightloch-resolver-posture: %s\n' "$1" >&2
	usage
	exit 2
}

unavailable() {
	printf 'nightloch-resolver-posture: %s\n' "$1" >&2
	exit 3
}

require_root() {
	if [ -z "$root" ] && [ "$(id -u)" -ne 0 ]; then
		unavailable "this must run as root"
	fi
}

require_file() {
	if [ ! -f "$1" ]; then
		unavailable "$1 is missing"
	fi
}

read_password_hash() {
	require_file "$password_hash_file"
	password_hash="$(tr -d '\r\n' <"$password_hash_file")"
	case "$password_hash" in
	\$2a\$* | \$2b\$* | \$2y\$*) ;;
	*) unavailable "$password_hash_file is not a bcrypt hash; generate it with 'caddy hash-password --algorithm bcrypt'" ;;
	esac
}

read_query_log_retention() {
	if [ ! -x "$nightloch_binary" ]; then
		unavailable "$nightloch_binary is missing; deploy the Nightloch binary first"
	fi
	query_log_retention="$($nightloch_binary resolver-query-log-retention)"
	hours="${query_log_retention%h}"
	case "$hours" in
	'' | *[!0-9]*) unavailable "Nightloch reported an invalid Resolver query-log retention: $query_log_retention" ;;
	esac
	if [ "$query_log_retention" = "$hours" ]; then
		unavailable "Nightloch reported an invalid Resolver query-log retention: $query_log_retention"
	fi
}

render_initial_config() {
	template="$source_dir/AdGuardHome.yaml.template"
	require_file "$template"
	pending="$config_dest.nightloch-pending.$$"
	trap 'rm -f "$pending"' EXIT HUP INT TERM
	( umask 077 && : >"$pending" )
	hash_replacements=0
	retention_replacements=0
	while IFS= read -r line || [ -n "$line" ]; do
		case "$line" in
		*__ADGUARD_PASSWORD_HASH__*)
			line="${line%%__ADGUARD_PASSWORD_HASH__*}$password_hash${line#*__ADGUARD_PASSWORD_HASH__}"
			hash_replacements=$((hash_replacements + 1))
			;;
		esac
		case "$line" in
		*__QUERY_LOG_RETENTION__*)
			line="${line%%__QUERY_LOG_RETENTION__*}$query_log_retention${line#*__QUERY_LOG_RETENTION__}"
			retention_replacements=$((retention_replacements + 1))
			;;
		esac
		printf '%s\n' "$line" >>"$pending"
	done <"$template"
	if [ "$hash_replacements" -ne 1 ] || [ "$retention_replacements" -ne 1 ]; then
		unavailable "$template must contain each Resolver placeholder exactly once"
	fi
	chmod 0600 "$pending"
	mv "$pending" "$config_dest"
	trap - EXIT HUP INT TERM
}

install_binary() {
	scratch="$(mktemp -d "${TMPDIR:-/tmp}/nightloch-adguard.XXXXXX")"
	trap 'rm -rf -- "$scratch"' EXIT HUP INT TERM
	archive="$scratch/$adguard_archive"
	curl --fail --location --silent --show-error --output "$archive" "$adguard_url"
	printf '%s  %s\n' "$adguard_sha256" "$archive" | sha256sum --check --status -
	tar -xzf "$archive" -C "$scratch"
	install -m 0755 "$scratch/AdGuardHome/AdGuardHome" "$binary_dest"
	rm -rf -- "$scratch"
	trap - EXIT HUP INT TERM
}

write_caddy_environment() {
	pending="$caddy_env_dest.nightloch-pending.$$"
	trap 'rm -f "$pending"' EXIT HUP INT TERM
	( umask 077 && printf 'ADGUARD_PASSWORD_HASH=%s\n' "$password_hash" >"$pending" )
	mv "$pending" "$caddy_env_dest"
	trap - EXIT HUP INT TERM
}

cmd_install() {
	for artifact in AdGuardHome.yaml.template AdGuardHome.service AdGuardHome.caddy caddy-nightloch.service.conf; do
		require_file "$source_dir/$artifact"
	done
	require_root
	read_password_hash
	if [ -f "$config_dest" ] && ! grep -Fq -f "$password_hash_file" "$config_dest"; then
		unavailable "the password hash does not match the persisted Resolver configuration; follow the credential-rotation runbook rather than changing one layer alone"
	fi
	read_query_log_retention
	require_file "$caddyfile"
	if ! grep -Fq 'import /etc/caddy/sites/*.caddy' "$caddyfile"; then
		unavailable "$caddyfile does not import /etc/caddy/sites/*.caddy; install deploy/Caddyfile.example first"
	fi

	if ! getent passwd adguardhome >/dev/null 2>&1; then
		useradd --system --user-group --home-dir /var/lib/AdGuardHome --shell /usr/sbin/nologin adguardhome
	fi

	install -d -m 0755 "$opt/AdGuardHome" "$etc/systemd/system" "$etc/caddy/sites" "$etc/systemd/system/caddy.service.d"
	install -d -m 0750 "$etc/AdGuardHome" "$work_dest" "$etc/nightloch"
	install_binary
	install -m 0644 "$source_dir/AdGuardHome.service" "$unit_dest"
	install -m 0644 "$source_dir/AdGuardHome.caddy" "$caddy_site_dest"
	install -m 0644 "$source_dir/caddy-nightloch.service.conf" "$caddy_dropin_dest"
	write_caddy_environment

	# AdGuard Home persists later control-API changes here, including the
	# generated per-Protectee rules a later slice adds.  A repeated install
	# updates the binary and units but never erases that state.
	if [ ! -f "$config_dest" ]; then
		render_initial_config
	fi
	# The service validates the config as its unprivileged user before systemd
	# gets a chance to apply ConfigurationDirectory ownership.  It therefore
	# needs traversal on the directory as well as ownership of the file.
	chown adguardhome:adguardhome "$etc/AdGuardHome" "$config_dest" "$work_dest"
	systemctl daemon-reload

	cat <<EOF
Installed AdGuard Home $adguard_version and staged the Resolver posture.
Nothing was started or enabled.  Review the rendered configuration, then run:
  nightloch-resolver-posture.sh start
EOF
}

cmd_start() {
	require_root
	for staged in "$binary_dest" "$config_dest" "$unit_dest" "$caddy_site_dest" "$caddy_dropin_dest" "$caddy_env_dest" "$caddyfile"; do
		require_file "$staged"
	done
	if ! systemctl is-active --quiet wg-quick@wg0.service; then
		unavailable "wg-quick@wg0.service is not active; the Resolver has no address to bind"
	fi

	# Both parsers must accept the exact persisted files before either public
	# routing or a reboot-persistent service changes.
	runuser -u adguardhome -- "$binary_dest" --no-check-update --check-config --config "$config_dest" --work-dir "$work_dest"
	caddy validate --config "$caddyfile" --envfile "$caddy_env_dest"

	# Keep enablement and restart separate: on a later pinned-binary upgrade,
	# `enable --now` would leave the already-running old process untouched.
	systemctl enable AdGuardHome.service
	systemctl restart AdGuardHome.service
	systemctl reload caddy

	cat <<'EOF'
The Resolver is active and enabled for reboot.  Verify the live facts with:
  nightloch-resolver-verify.sh
EOF
}

if [ "$#" -ne 1 ]; then
	refuse "exactly one subcommand is required"
fi

case "$1" in
install) cmd_install ;;
start) cmd_start ;;
*) refuse "unknown subcommand '$1'" ;;
esac
