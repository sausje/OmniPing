#!/bin/sh
# Starts OmniPing after a fresh install, and restarts it after an upgrade.
# Debian calls this with "configure" plus the old version on an upgrade;
# RPM with 1 for an install and 2 for an upgrade.
set -e

[ -d /run/systemd/system ] || exit 0
systemctl daemon-reload >/dev/null 2>&1 || true

if { [ "$1" = configure ] && [ -z "${2:-}" ]; } || [ "$1" = 1 ]; then
	systemctl enable --now omniping.service >/dev/null 2>&1 || true
	port=$(sed -n 's/^OMNIPING_PORT=//p' /etc/default/omniping 2>/dev/null | tail -n 1)
	echo "OmniPing is running. Open http://$(hostname):${port:-8080} in a browser."
else
	systemctl try-restart omniping.service >/dev/null 2>&1 || true
fi
