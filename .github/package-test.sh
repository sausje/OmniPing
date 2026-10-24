#!/bin/sh
# Installs the .deb the way a user does, and checks that the service starts
# by itself, pings without being root, keeps its data in /var/lib/omniping,
# keeps it over an upgrade, follows /etc/default/omniping, and stops when the
# package is removed.
# Usage: .github/package-test.sh dist/omniping_amd64.deb
set -eu
deb=$(realpath "$1")
url=http://127.0.0.1:8080

fail() {
	echo "FAILED: $*"
	sudo journalctl -u omniping --no-pager | tail -n 50 || true
	exit 1
}

up() {
	for _ in $(seq 30); do
		curl -fsS "$url/healthz" >/dev/null 2>&1 && return 0
		sleep 1
	done
	return 1
}

sudo apt-get install -y "$deb"
up || fail "the page did not come up after installing"
[ "$(systemctl is-enabled omniping)" = enabled ] || fail "the service does not start at boot"
echo "pinging with: $(curl -fsS "$url/api/info" | jq -r .mode)"
curl -fsS "$url/api/info" | jq -e '.install == "package"' >/dev/null || fail "it does not know it was installed as a package"

uid=$(ps -o uid= -C omniping | head -n 1 | tr -d ' ')
echo "runs as user $uid"
[ -n "$uid" ] && [ "$uid" != 0 ] || fail "runs as root"

curl -fsS -X POST -H 'Content-Type: application/json' -d '{"text":"127.0.0.1 loopback"}' "$url/api/targets" >/dev/null
sleep 6
answered=$(curl -fsS "$url/api/samples" | jq '[.series[0].r[]? | select(. >= 0)] | length')
echo "127.0.0.1 answered $answered times"
[ "$answered" -ge 3 ] || fail "127.0.0.1 did not answer"
sudo test -f /var/lib/omniping/config.json || fail "no config.json in /var/lib/omniping"

# An upgrade (here the same package again) restarts it and keeps the target.
sudo apt-get install -y --reinstall "$deb"
up || fail "not back after an upgrade"
curl -fsS "$url/api/state" | jq -e '.targets | length == 1' >/dev/null || fail "the target is gone after an upgrade"

# The port in /etc/default/omniping is used.
sudo sed -i 's/^OMNIPING_PORT=.*/OMNIPING_PORT=8090/' /etc/default/omniping
sudo systemctl restart omniping
url=http://127.0.0.1:8090
up || fail "the port in /etc/default/omniping was not used"

sudo apt-get remove -y omniping
if systemctl is-active --quiet omniping; then fail "still running after removing it"; fi
sudo test -f /var/lib/omniping/config.json || fail "removing the package deleted the settings"
echo "package test passed"
