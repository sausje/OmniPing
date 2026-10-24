#!/bin/sh
# Lets systemd forget the removed service.
set -e

[ -d /run/systemd/system ] || exit 0
systemctl daemon-reload >/dev/null 2>&1 || true
