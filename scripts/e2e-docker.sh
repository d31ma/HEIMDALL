#!/usr/bin/env bash
# End-to-end against a live Docker Engine.
#
# The unit tests cover this shape with a fake Engine so they run everywhere.
# This runs the real thing: real fylo, real sesame, real git, real containers.
# It is the gate that catches what a fake cannot — an Engine API field that
# changed shape, a label filter that does not behave as documented, a pull
# that needs credentials.
#
# Everything lives under /tmp. A FYLO root must never sit on a sync
# filesystem: its locking assumes local rename semantics, and the failure mode
# is silent corruption discovered much later.
set -euo pipefail

ROOT="${E2E_ROOT:-/tmp/heimdall-e2e}"
ADDR="${E2E_ADDR:-127.0.0.1:18099}"
URL="http://${ADDR}"
PASSWORD='e2e-correct-horse-battery-staple-9271'

cleanup() {
  local status=$?
  if [ -n "${SERVE_PID:-}" ]; then kill "$SERVE_PID" 2>/dev/null || true; fi
  # Remove only what this run created; the label is the whole guarantee that
  # nothing else on the host is touched.
  docker ps -aq --filter 'label=dev.delma.heimdall.managed-by=heimdall' \
    --filter 'label=dev.delma.heimdall.app=e2e' 2>/dev/null | xargs -r docker rm -f >/dev/null 2>&1 || true
  if [ "$status" -ne 0 ]; then
    echo; echo "FAILED. Control-plane log:"; tail -40 "$ROOT/serve.log" 2>/dev/null || true
  fi
  exit "$status"
}
trap cleanup EXIT

step() { printf '\n\033[1m==> %s\033[0m\n' "$1"; }
fail() { printf '\033[31mFAIL\033[0m %s\n' "$1" >&2; exit 1; }

for binary in docker git fylo sesame chex; do
  command -v "$binary" >/dev/null || fail "$binary is not on PATH"
done
docker version >/dev/null 2>&1 || fail "the Docker daemon is not running"

step "Building heimdall"
cd "$(dirname "$0")/.."
go build -ldflags "-X main.version=$(cat VERSION)" -o "$ROOT.bin" ./cmd/heimdall 2>/dev/null || {
  mkdir -p "$(dirname "$ROOT.bin")"; go build -o "$ROOT.bin" ./cmd/heimdall
}
HEIMDALL="$ROOT.bin"

step "Initialising a scratch deployment at $ROOT"
rm -rf "$ROOT"
"$HEIMDALL" init --deployment "$ROOT" >/dev/null
TENANT=$(python3 -c "import json;print(json.load(open('$ROOT/heimdall.json'))['tenant_id'])")

step "Creating an operator principal"
PRINCIPAL=$(sesame principal create --tenant-id "$TENANT" --kind human \
  --identifier-namespace username --identifier-value e2e \
  --deployment "$ROOT/sesame" 2>/dev/null | python3 -c "import json,sys;print(json.load(sys.stdin)['principal_id'])")
printf '{"protocol_version":"1","request_id":"pw","operation":"authenticator.set_password","parameters":{"principal_id":"%s","password":"%s"}}\n' \
  "$PRINCIPAL" "$PASSWORD" | sesame exec --loop --deployment "$ROOT/sesame" >/dev/null 2>&1
ROLE=$(sesame role create --tenant-id "$TENANT" --name e2e-operator \
  --permissions "app:read=*,app:create=*,app:sync=*,app:rollback=*,app:prune=*,target:read=*,target:create=*,repo:read=*,repo:create=*,secret:bind=*,audit:read=*,observe:metrics=*,observe:logs=*,observe:events=*" \
  --deployment "$ROOT/sesame" 2>/dev/null | python3 -c "import json,sys;print(json.load(sys.stdin)['role_id'])")
sesame grant create --tenant-id "$TENANT" --role-id "$ROLE" --principal-id "$PRINCIPAL" \
  --deployment "$ROOT/sesame" >/dev/null 2>&1

step "Creating a fixture compose repository"
REPO="$ROOT/repo"
mkdir -p "$REPO/deploy"
cat > "$REPO/deploy/compose.yaml" <<'YAML'
services:
  web:
    image: nginx:1.27.3-alpine
    restart: unless-stopped
YAML
git -C "$REPO" init -q --initial-branch=main
git -C "$REPO" add .
GIT_AUTHOR_NAME=e2e GIT_AUTHOR_EMAIL=e2e@example.com \
GIT_COMMITTER_NAME=e2e GIT_COMMITTER_EMAIL=e2e@example.com \
  git -C "$REPO" commit -qm "initial"

step "Starting the control plane"
"$HEIMDALL" serve --deployment "$ROOT" --addr "$ADDR" > "$ROOT/serve.log" 2>&1 &
SERVE_PID=$!
for _ in $(seq 1 40); do
  if curl -fsS "$URL/readyz" >/dev/null 2>&1; then break; fi
  sleep 0.5
done
curl -fsS "$URL/readyz" >/dev/null || fail "the control plane never became ready"

export HD_PASSWORD="$PASSWORD" HD_ADDR_URL="$URL"
"$HEIMDALL" login --deployment "$ROOT" --user e2e >/dev/null
BEARER=$(python3 -c "import json;s=json.load(open('$ROOT/session.json'));print(s['session_id']+'.'+s['session_secret'])")
api() { curl -fsS -X "$1" -H "Authorization: Bearer $BEARER" -H 'Content-Type: application/json' ${3:+-d "$3"} "$URL$2"; }

step "Registering the repository, target, and application"
REPO_ID=$(api POST /api/v1/repos "" "{\"project\":\"alpha\",\"name\":\"e2e\",\"url\":\"$REPO\",\"default_ref\":\"main\"}" | python3 -c "import json,sys;print(json.load(sys.stdin)['id'])")
TARGET_ID=$(api POST /api/v1/targets "" '{"project":"alpha","name":"local","provider":"docker","endpoint":"unix:///var/run/docker.sock"}' | python3 -c "import json,sys;print(json.load(sys.stdin)['id'])")
api POST /api/v1/projects/alpha/apps "" "{\"name\":\"e2e\",\"repo_id\":\"$REPO_ID\",\"target_id\":\"$TARGET_ID\",\"path\":\"deploy\"}" >/dev/null

step "Deploying"
"$HEIMDALL" sync --deployment "$ROOT" --project alpha --app e2e
docker ps --filter 'label=dev.delma.heimdall.app=e2e' --format '{{.Names}}' | grep -q . \
  || fail "no container is running after a successful sync"

step "Asserting the container knows the commit that put it there"
COMMIT=$(git -C "$REPO" rev-parse HEAD)
LABELLED=$(docker ps --filter 'label=dev.delma.heimdall.app=e2e' \
  --format '{{.Label "dev.delma.heimdall.revision"}}' | head -1)
[ "$LABELLED" = "$COMMIT" ] || fail "container reports revision $LABELLED, want $COMMIT"

step "Asserting the application converged"
"$HEIMDALL" diff --deployment "$ROOT" --project alpha --app e2e | grep -q Synced \
  || fail "the application did not converge after a sync"

step "Removing a container out of band and asserting drift is visible"
docker ps -q --filter 'label=dev.delma.heimdall.app=e2e' | xargs -r docker rm -f >/dev/null
STATUS=$("$HEIMDALL" diff --deployment "$ROOT" --project alpha --app e2e)
echo "$STATUS" | grep -q OutOfSync || fail "an out-of-band removal did not show as OutOfSync"
echo "$STATUS" | grep -qi missing || fail "the removed service is not reported as Missing"

step "Restoring by re-syncing"
"$HEIMDALL" sync --deployment "$ROOT" --project alpha --app e2e >/dev/null
"$HEIMDALL" diff --deployment "$ROOT" --project alpha --app e2e | grep -q Synced \
  || fail "the re-sync did not restore the removed container"

step "Changing the image and syncing"
sed -i.bak 's/1.27.3-alpine/1.27.2-alpine/' "$REPO/deploy/compose.yaml" && rm -f "$REPO/deploy/compose.yaml.bak"
git -C "$REPO" add .
GIT_AUTHOR_NAME=e2e GIT_AUTHOR_EMAIL=e2e@example.com \
GIT_COMMITTER_NAME=e2e GIT_COMMITTER_EMAIL=e2e@example.com \
  git -C "$REPO" commit -qm "downgrade nginx"
"$HEIMDALL" sync --deployment "$ROOT" --project alpha --app e2e >/dev/null
docker ps --filter 'label=dev.delma.heimdall.app=e2e' --format '{{.Image}}' | grep -q '1.27.2' \
  || fail "the new image is not running"

step "Rolling back to the first revision"
"$HEIMDALL" sync --deployment "$ROOT" --project alpha --app e2e --revision "$COMMIT" >/dev/null
docker ps --filter 'label=dev.delma.heimdall.app=e2e' --format '{{.Image}}' | grep -q '1.27.3' \
  || fail "the rollback did not restore the original image"

step "Asserting every mutating call is attributable"
RECORDS=$(api GET /api/v1/audit)
echo "$RECORDS" | grep -q "$PRINCIPAL" || fail "no audit record names the principal"
echo "$RECORDS" | grep -q '"policy_version"' || fail "audit records carry no policy version"
echo "$RECORDS" | grep -q 'app:sync' || fail "the sync was not audited"

step "Asserting no secret value reached any document"
if grep -rq "$PASSWORD" "$ROOT/fylo-root" 2>/dev/null; then
  fail "a credential reached a persisted document"
fi

printf '\n\033[32mPASS\033[0m — deploy, drift, restore, update, rollback, and audit all verified against a live Docker Engine\n'
