#!/bin/sh
# Starts an OmniPing image the way Docker does by default (a data folder that
# Docker created as root), pings 127.0.0.1 and checks that the server works,
# answers its health check, and dropped root.
# Usage: .github/smoke-test.sh omniping:test
set -eu
image=$1
data=$(mktemp -d)
sudo chown root:root "$data"

cleanup() {
	docker logs omniping-smoke || true
	docker rm -f omniping-smoke >/dev/null 2>&1 || true
	sudo rm -rf "$data"
}
trap cleanup EXIT

docker run -d --name omniping-smoke -p 18080:8080 -v "$data:/data" "$image"
url=http://127.0.0.1:18080

echo "waiting for the page"
for _ in $(seq 30); do
	curl -fsS "$url/healthz" >/dev/null 2>&1 && break
	sleep 1
done
curl -fsS "$url/healthz"
docker exec omniping-smoke /usr/local/bin/omniping healthcheck
echo "health check ok"

curl -fsS "$url/api/info" | jq -e '.mode != "not available"' >/dev/null
echo "pinging with: $(curl -fsS "$url/api/info" | jq -r .mode)"
curl -fsS "$url/api/info" | jq -e '.install == "docker"' >/dev/null

curl -fsS -X POST -H 'Content-Type: application/json' -d '{"text":"127.0.0.1 loopback"}' "$url/api/targets" >/dev/null
sleep 6
answered=$(curl -fsS "$url/api/samples" | jq '[.series[0].r[]? | select(. >= 0)] | length')
echo "127.0.0.1 answered $answered times"
[ "$answered" -ge 3 ]

uid=$(docker exec omniping-smoke sh -c 'grep ^Uid: /proc/$(pidof omniping)/status' | awk '{print $2}')
echo "server runs as user $uid"
[ "$uid" = 1000 ]

owner=$(sudo stat -c %u "$data/config.json")
echo "config.json belongs to user $owner"
[ "$owner" = 1000 ]

docker stop omniping-smoke >/dev/null
echo "smoke test passed"
