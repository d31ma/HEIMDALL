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
# One shared config path means one stack at a time, whatever the ports say:
# overwriting it leaves the other stack's browser staring at "the web tier is
# not configured". Checked here, before anything is built or seeded.
if [ -f web/server/data/bff.json ]; then
  echo "web/server/data/bff.json already exists — another stack is using it." >&2
  echo "Stop that stack first (it removes the file on exit), or delete the file if it is stale." >&2
  exit 1
fi

password='correct-horse-battery-staple-9271'
USER_NAME=ops
workdir=$(mktemp -d /tmp/hd-e2e-web.XXXXXX)

# HD_E2E_DEPLOYMENT puts the deployment somewhere that outlives the run —
# which is what a demo stack wants and what a test never does. An existing
# deployment is reused as it stands rather than re-initialised, so the
# applications, history, and keys survive a restart. The suite's own runs
# leave it unset and get the throwaway directory they should.
deployment=${HD_E2E_DEPLOYMENT:-"$workdir/deployment"}
persistent=${HD_E2E_DEPLOYMENT:+1}
existing=""
[ -f "$deployment/heimdall.json" ] && existing=1

# The seeded git repository is the application's source of truth, so for a
# persistent deployment it has to outlive the run too — an application
# pointing at a deleted temp directory refreshes into an error on restart.
seed_repo="$workdir/repo"
[ -n "${persistent:-}" ] && seed_repo="$deployment-repo"

# The seeded target has no endpoint, so it is the real Docker daemon on this
# machine: a sync from the suite — or from a human poking at HD_E2E_KEEP —
# starts a real container. Removing what this seed created keeps a developer's
# daemon clean, and keeps "an app with nothing running says so" true on the
# next run. Strictly label-scoped: nothing outside project alpha / app site is
# touched.
sweep_containers() {
  command -v docker >/dev/null 2>&1 || return 0
  local ids
  ids=$(docker ps -aq \
    --filter 'label=dev.delma.heimdall.managed-by=heimdall' \
    --filter 'label=dev.delma.heimdall.project=alpha' \
    --filter 'label=dev.delma.heimdall.app=site' 2>/dev/null) || return 0
  [ -n "$ids" ] && docker rm -f $ids >/dev/null 2>&1
  return 0
}

cleanup() {
  kill "${cp_pid:-}" "${web_pid:-}" 2>/dev/null || true
  wait 2>/dev/null || true
  # A persistent deployment keeps its containers: they are the demo, and
  # sweeping them would empty the page the stack exists to show.
  [ -n "${persistent:-}" ] || sweep_containers
  # bff.json is one shared path, so a second harness must not delete a first
  # one's config out from under it: remove it only if it is still the file
  # this run wrote. Found the hard way, with a review stack running.
  if [ -f web/server/data/bff.json ] && [ -f "$workdir/bff.json" ] &&
     cmp -s web/server/data/bff.json "$workdir/bff.json"; then
    rm -f web/server/data/bff.json
  fi
  rm -rf "$workdir"
}
trap cleanup EXIT

# HD_E2E_KEEP leaves the stack up instead of running the suite, which is how
# a UI change gets looked at by a human rather than only by Playwright.
keep=${HD_E2E_KEEP:-}

[ -n "${persistent:-}" ] || sweep_containers

echo "== control plane =="
go build -o "$workdir/hd" ./cmd/heimdall
export HD_PUBLIC_URL="https://127.0.0.1:$port_cp" HD_ADMIN_PASSWORD="$password"
if [ -n "$existing" ]; then
  echo "reusing the deployment at $deployment"
else
  mkdir -p "$(dirname "$deployment")"
  "$workdir/hd" init --deployment "$deployment" --admin ops >/dev/null
fi
"$workdir/hd" serve --deployment "$deployment" --addr "127.0.0.1:$port_cp" >"$workdir/cp.log" 2>&1 &
cp_pid=$!
for _ in $(seq 1 30); do
  curl -sk "https://127.0.0.1:$port_cp/healthz" >/dev/null 2>&1 && break
  sleep 1
done

if [ -n "$existing" ]; then
  echo "== skipping the seed: the deployment already has its own state =="
else
echo "== seed one application =="
export HD_PASSWORD="$password" HD_ADDR_URL="https://127.0.0.1:$port_cp" HD_CA_FILE="$deployment/keys/agent-ca.crt"
"$workdir/hd" login --deployment "$deployment" --user ops >/dev/null
bearer=$(python3 -c "import json;s=json.load(open('$deployment/session.json'));print(s['session_id']+'.'+s['session_secret'])")
api() {
  curl -s --cacert "$deployment/keys/agent-ca.crt" -H "Authorization: Bearer $bearer" \
    -H 'Content-Type: application/json' "$@"
}

mkdir -p "$seed_repo/deploy"
cat > "$seed_repo/deploy/compose.yaml" <<'YAML'
services:
  web:
    image: nginx:1.27.3
    ports: ["8080:80"]
YAML
git -C "$seed_repo" init -q --initial-branch=main
git -C "$seed_repo" add .
git -C "$seed_repo" -c user.name=e2e -c user.email=e2e@test commit -qm seed

repo=$(api -X POST -d "{\"project\":\"alpha\",\"name\":\"site\",\"url\":\"$seed_repo\",\"default_ref\":\"main\"}" \
  "https://127.0.0.1:$port_cp/api/v1/repos" | python3 -c "import json,sys;print(json.load(sys.stdin)['id'])")
target=$(api -X POST -d '{"project":"alpha","name":"local","provider":"docker"}' \
  "https://127.0.0.1:$port_cp/api/v1/targets" | python3 -c "import json,sys;print(json.load(sys.stdin)['id'])")
api -X POST -d "{\"name\":\"site\",\"repo_id\":\"$repo\",\"target_id\":\"$target\",\"path\":\"deploy\"}" \
  "https://127.0.0.1:$port_cp/api/v1/projects/alpha/apps" >/dev/null

fi

echo "== web tier =="
cat > "$workdir/bff.json" <<JSON
{"apiUrl":"https://127.0.0.1:$port_cp","caFile":"$deployment/keys/agent-ca.crt","secureCookie":false,"timeoutMs":60000}
JSON
cp "$workdir/bff.json" web/server/data/bff.json
(cd web && ty serve --port "$port_web" --no-watch >"$workdir/web.log" 2>&1) &
web_pid=$!
for _ in $(seq 1 30); do
  curl -s "http://127.0.0.1:$port_web/" >/dev/null 2>&1 && break
  sleep 1
done

if [ -n "$keep" ]; then
  echo
  echo "web:           http://127.0.0.1:$port_web"
  echo "control plane: https://127.0.0.1:$port_cp"
  echo "sign in as:    $USER_NAME / $password"
  echo "Ctrl-C to tear it all down."
  wait "$cp_pid"
  exit 0
fi

echo "== playwright =="
export HD_WEB_URL="http://127.0.0.1:$port_web" HD_E2E_PASSWORD="$password"
# Only ui.spec targets this harness; views/fullpath/site drive other stacks.
if [ "$#" -eq 0 ]; then set -- ui; fi
(cd web && ./node_modules/.bin/playwright test "$@")
