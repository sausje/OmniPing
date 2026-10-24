#!/bin/sh
# Stops OmniPing when it is removed, not when it is upgraded. Debian calls
# this with "remove" or "upgrade"; RPM with 0 for a removal and 1 for an
# upgrade. The settings and history in /var/lib/omniping stay.
set -e

[ -d /run/systemd/system ] || exit 0
if [ "$1" = remove ] || [ "$1" = 0 ]; then
	systemctl disable --now omniping.service >/dev/null 2>&1 || true
fi
