#!/usr/bin/env bash
# End-to-end for the web tier: stands up a real control plane and ty serve,
# seeds one application, and runs the Playwright suite against them.
#
# Needs: go, ty, node/bun, sesame, fylo on PATH. Leaves nothing running.
set -euo pipefail
cd "$(dirname "$0")/.."

port_cp=${HD_E2E_CP_PORT:-18443}
port_web=${HD_E2E_WEB_PORT:-9310}

# A stale listener on the control-plane port makes every later step fail
# confusingly — the health check passes against the wrong instance and login
# dies on a CA mismatch. Refuse to start instead.
for port in "$port_cp" "$port_web"; do
  if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "port $port is already in use; set HD_E2E_CP_PORT/HD_E2E_WEB_PORT" >&2
    exit 1
  fi
done
password='correct-horse-battery-staple-9271'
workdir=$(mktemp -d /tmp/hd-e2e-web.XXXXXX)
deployment="$workdir/deployment"

cleanup() {
  kill "${cp_pid:-}" "${web_pid:-}" 2>/dev/null || true
  wait 2>/dev/null || true
  rm -rf "$workdir" web/server/data/bff.json
}
trap cleanup EXIT

echo "== control plane =="
go build -o "$workdir/hd" ./cmd/heimdall
export HD_PUBLIC_URL="https://127.0.0.1:$port_cp" HD_ADMIN_PASSWORD="$password"
"$workdir/hd" init --deployment "$deployment" --admin ops >/dev/null
"$workdir/hd" serve --deployment "$deployment" --addr "127.0.0.1:$port_cp" >"$workdir/cp.log" 2>&1 &
cp_pid=$!
for _ in $(seq 1 30); do
  curl -sk "https://127.0.0.1:$port_cp/healthz" >/dev/null 2>&1 && break
  sleep 1
done

echo "== seed one application =="
export HD_PASSWORD="$password" HD_ADDR_URL="https://127.0.0.1:$port_cp" HD_CA_FILE="$deployment/keys/agent-ca.crt"
"$workdir/hd" login --deployment "$deployment" --user ops >/dev/null
bearer=$(python3 -c "import json;s=json.load(open('$deployment/session.json'));print(s['session_id']+'.'+s['session_secret'])")
api() {
  curl -s --cacert "$deployment/keys/agent-ca.crt" -H "Authorization: Bearer $bearer" \
    -H 'Content-Type: application/json' "$@"
}

mkdir -p "$workdir/repo/deploy"
cat > "$workdir/repo/deploy/compose.yaml" <<'YAML'
services:
  web:
    image: nginx:1.27.3
    ports: ["8080:80"]
YAML
git -C "$workdir/repo" init -q --initial-branch=main
git -C "$workdir/repo" add .
git -C "$workdir/repo" -c user.name=e2e -c user.email=e2e@test commit -qm seed

repo=$(api -X POST -d "{\"project\":\"alpha\",\"name\":\"site\",\"url\":\"$workdir/repo\",\"default_ref\":\"main\"}" \
  "https://127.0.0.1:$port_cp/api/v1/repos" | python3 -c "import json,sys;print(json.load(sys.stdin)['id'])")
target=$(api -X POST -d '{"project":"alpha","name":"local","provider":"docker"}' \
  "https://127.0.0.1:$port_cp/api/v1/targets" | python3 -c "import json,sys;print(json.load(sys.stdin)['id'])")
api -X POST -d "{\"name\":\"site\",\"repo_id\":\"$repo\",\"target_id\":\"$target\",\"path\":\"deploy\"}" \
  "https://127.0.0.1:$port_cp/api/v1/projects/alpha/apps" >/dev/null

echo "== web tier =="
cat > web/server/data/bff.json <<JSON
{"apiUrl":"https://127.0.0.1:$port_cp","caFile":"$deployment/keys/agent-ca.crt","secureCookie":false,"timeoutMs":60000}
JSON
(cd web && ty serve --port "$port_web" --no-watch >"$workdir/web.log" 2>&1) &
web_pid=$!
for _ in $(seq 1 30); do
  curl -s "http://127.0.0.1:$port_web/" >/dev/null 2>&1 && break
  sleep 1
done

echo "== playwright =="
export HD_WEB_URL="http://127.0.0.1:$port_web" HD_E2E_PASSWORD="$password"
# Only ui.spec targets this harness; views/fullpath/site drive other stacks.
if [ "$#" -eq 0 ]; then set -- ui; fi
(cd web && ./node_modules/.bin/playwright test "$@")
