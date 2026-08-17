#!/usr/bin/env bash
# The disaster-recovery drill, exactly as the runbook prescribes it: a cold
# backup of a live deployment restores to a control plane that still verifies
# a session issued before the disaster. Run it whenever the backup format or
# the deployment layout changes.
set -euo pipefail
cd "$(dirname "$0")/.."

password='correct-horse-battery-staple-9271'
work=$(mktemp -d /tmp/hd-dr.XXXXXX)
trap 'pkill -f "hd-dr serve" 2>/dev/null || true; rm -rf "$work"' EXIT

go build -o "$work/hd-dr" ./cmd/heimdall
export HD_PUBLIC_URL=https://127.0.0.1:18543 HD_ADMIN_PASSWORD="$password"

echo "== a live control plane with a session =="
"$work/hd-dr" init --deployment "$work/live" --admin ops >/dev/null
"$work/hd-dr" serve --deployment "$work/live" --addr 127.0.0.1:18543 >"$work/serve1.log" 2>&1 &
for _ in $(seq 1 30); do curl -sk https://127.0.0.1:18543/healthz >/dev/null 2>&1 && break; sleep 1; done

export HD_PASSWORD="$password" HD_ADDR_URL=https://127.0.0.1:18543 HD_CA_FILE="$work/live/keys/agent-ca.crt"
"$work/hd-dr" login --deployment "$work/live" --user ops >/dev/null
bearer=$(python3 -c "import json;s=json.load(open('$work/live/session.json'));print(s['session_id']+'.'+s['session_secret'])")

echo "== the disaster: stop, back up, destroy =="
pkill -f "hd-dr serve"; sleep 2
"$work/hd-dr" backup --deployment "$work/live" --output "$work/cold.tar.gz"
rm -rf "$work/live"

echo "== restore and verify the pre-existing session =="
"$work/hd-dr" restore --deployment "$work/restored" --input "$work/cold.tar.gz"
"$work/hd-dr" serve --deployment "$work/restored" --addr 127.0.0.1:18543 >"$work/serve2.log" 2>&1 &
for _ in $(seq 1 30); do curl -sk https://127.0.0.1:18543/healthz >/dev/null 2>&1 && break; sleep 1; done

status=$(curl -s -o /dev/null -w '%{http_code}' \
  --cacert "$work/restored/keys/agent-ca.crt" \
  -H "Authorization: Bearer $bearer" https://127.0.0.1:18543/api/v1/targets)
if [ "$status" != "200" ]; then
  echo "DR DRILL FAILED: the restored control plane answered $status to a pre-disaster session" >&2
  exit 1
fi
echo "DR drill passed: a pre-disaster session verifies against the restored control plane."
