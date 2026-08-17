#!/usr/bin/env bash
# The full path audit: stands up a control plane, a standalone fake Docker
# Engine, and the web tier, seeds two apps and two revisions, then drives
# every UI path with Playwright — login failure, drift, self-heal, rollback
# with its confirm step, suspension, control-plane outage, sign-out.
# The device-approve flows are exercised by scripts/e2e-device.sh-style
# orchestration and in the api tests; they need one-shot codes.
set -euo pipefail
cd "$(dirname "$0")/.."

password='correct-horse-battery-staple-9271'
work=$(mktemp -d /tmp/hd-paths.XXXXXX)
trap 'pkill -f "hd-paths serve" 2>/dev/null || true; pkill -f fake-engine 2>/dev/null || true; \
     pkill -f "ty serve" 2>/dev/null || true; rm -rf "$work" web/server/data/bff.json web/server/routes/audit-login' EXIT

go build -o "$work/hd-paths" ./cmd/heimdall
go build -o "$work/fake-engine" ./cmd/fake-engine
"$work/fake-engine" > "$work/engine-url" & sleep 1
engine=$(cat "$work/engine-url")

export HD_PUBLIC_URL=https://127.0.0.1:18443 HD_ADMIN_PASSWORD="$password" HD_SYNC_INTERVAL=10s
"$work/hd-paths" init --deployment "$work/dep" --admin ops >/dev/null
"$work/hd-paths" serve --deployment "$work/dep" --addr 127.0.0.1:18443 >"$work/cp.log" 2>&1 &
for _ in $(seq 1 30); do curl -sk https://127.0.0.1:18443/healthz >/dev/null 2>&1 && break; sleep 1; done

export HD_PASSWORD="$password" HD_ADDR_URL=https://127.0.0.1:18443 HD_CA_FILE="$work/dep/keys/agent-ca.crt"
"$work/hd-paths" login --deployment "$work/dep" --user ops >/dev/null
bearer=$(python3 -c "import json;s=json.load(open('$work/dep/session.json'));print(s['session_id']+'.'+s['session_secret'])")
api() { curl -s --cacert "$HD_CA_FILE" -H "Authorization: Bearer $bearer" -H 'Content-Type: application/json' "$@"; }

mkdir -p "$work/repo/deploy"
printf 'services:\n  web:\n    image: nginx:1.27.3\n    ports: ["8080:80"]\n  cache:\n    image: redis:7.4-alpine\n' > "$work/repo/deploy/compose.yaml"
git -C "$work/repo" init -q --initial-branch=main && git -C "$work/repo" add . && git -C "$work/repo" -c user.name=e2e -c user.email=e@e commit -qm "v1: initial"
repo=$(api -X POST -d "{\"project\":\"shop\",\"name\":\"site\",\"url\":\"$work/repo\",\"default_ref\":\"main\"}" https://127.0.0.1:18443/api/v1/repos | python3 -c "import json,sys;print(json.load(sys.stdin)['id'])")
target=$(api -X POST -d "{\"project\":\"shop\",\"name\":\"prod-1\",\"provider\":\"docker\",\"endpoint\":\"$engine\"}" https://127.0.0.1:18443/api/v1/targets | python3 -c "import json,sys;print(json.load(sys.stdin)['id'])")
api -X POST -d "{\"name\":\"site\",\"repo_id\":\"$repo\",\"target_id\":\"$target\",\"path\":\"deploy\"}" https://127.0.0.1:18443/api/v1/projects/shop/apps >/dev/null
api -X POST -d '{}' https://127.0.0.1:18443/api/v1/projects/shop/apps/site/sync >/dev/null
sed -i '' 's/nginx:1.27.3/nginx:1.27.4/' "$work/repo/deploy/compose.yaml" 2>/dev/null || sed -i 's/nginx:1.27.3/nginx:1.27.4/' "$work/repo/deploy/compose.yaml"
git -C "$work/repo" add . && git -C "$work/repo" -c user.name=e2e -c user.email=e@e commit -qm "v2: bump nginx"
api -X POST -d '{}' https://127.0.0.1:18443/api/v1/projects/shop/apps/site/sync >/dev/null
api -X POST -d "{\"name\":\"legacy\",\"repo_id\":\"$repo\",\"target_id\":\"$target\",\"path\":\"deploy\"}" https://127.0.0.1:18443/api/v1/projects/shop/apps >/dev/null
api -X POST -d '{}' https://127.0.0.1:18443/api/v1/projects/shop/apps/legacy/suspend >/dev/null

mkdir -p web/server/routes/audit-login web/server/data
cp scripts/testdata/audit-login.yon.js web/server/routes/audit-login/yon.js
cat > web/server/data/bff.json <<JSON
{"apiUrl":"https://127.0.0.1:18443","caFile":"$work/dep/keys/agent-ca.crt","secureCookie":false,"timeoutMs":60000,"auditLogin":{"identifier":"ops","password":"$password"}}
JSON
(cd web && ty serve --port 9303 --no-watch >"$work/web.log" 2>&1) &
for _ in $(seq 1 30); do curl -s http://127.0.0.1:9303/ >/dev/null 2>&1 && break; sleep 1; done

# NOTE: the P12 outage test SIGSTOPs "hd-paths serve" by name.
sed_pattern='hdemo serve'
ENGINE_URL="$engine" HD_PATHS_BIN=hd-paths \
  sh -c 'cd web && ENGINE_URL="$ENGINE_URL" ./node_modules/.bin/playwright test tests/fullpath.spec.js --timeout 120000'
