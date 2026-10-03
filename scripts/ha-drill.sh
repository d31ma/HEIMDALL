#!/usr/bin/env bash
# The failover drill: a standby waits while the active holds the deployment,
# and takes over when the active dies. FYLO's exclusive root lock is the
# leader election; `sesame doctor` is the standby's probe.
set -euo pipefail
cd "$(dirname "$0")/.."

work=$(mktemp -d /tmp/hd-ha.XXXXXX)
trap 'pkill -f "hd-ha serve" 2>/dev/null || true; rm -rf "$work"' EXIT

go build -o "$work/hd-ha" ./cmd/heimdall
export HD_PUBLIC_URL=https://127.0.0.1:18643 HD_ADMIN_PASSWORD='pw-for-ha-drill-123456789'
"$work/hd-ha" init --deployment "$work/dep" --admin ops >/dev/null

"$work/hd-ha" serve --deployment "$work/dep" --addr 127.0.0.1:18643 >"$work/active.log" 2>&1 &
active=$!
for _ in $(seq 1 30); do curl -sk https://127.0.0.1:18643/healthz >/dev/null 2>&1 && break; sleep 1; done

"$work/hd-ha" serve --standby --deployment "$work/dep" --addr 127.0.0.1:18644 >"$work/standby.log" 2>&1 &
sleep 7
if ! grep -q "an active control plane holds" "$work/standby.log"; then
  echo "HA DRILL FAILED: the standby did not wait" >&2; exit 1
fi

kill "$active"
for _ in $(seq 1 14); do
  sleep 5
  curl -sk https://127.0.0.1:18644/healthz >/dev/null 2>&1 && {
    echo "HA drill passed: the standby took over after the active died."
    exit 0
  }
done
echo "HA DRILL FAILED: the standby never took over" >&2
exit 1
