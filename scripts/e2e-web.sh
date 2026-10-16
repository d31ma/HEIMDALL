#!/usr/bin/env bash
set -euo pipefail
export PYTHONDONTWRITEBYTECODE=1
umask 077

source_root=$(cd "$(dirname "$0")/.." && pwd -P)
mode=${HD_E2E_MODE:-web}
case "$mode" in web|paths) ;; *) echo 'HD_E2E_MODE must be web or paths.' >&2; exit 1;; esac
port_cp=${HD_E2E_CP_PORT:-18443}
port_web=${HD_E2E_WEB_PORT:-9310}
compiler=${HD_E2E_TACHYON_BIN:-$(command -v ty || true)}
manifest=${HD_E2E_COMPILER_MANIFEST:?set HD_E2E_COMPILER_MANIFEST to the qualified compiler receipt}
modules=${HD_E2E_NODE_MODULES:-"$source_root/app/node_modules"}
work_root=${HD_E2E_WORK_ROOT:-/tmp}
evidence_base=${HD_E2E_EVIDENCE_DIR:-}
compiler=$(python3 - "$compiler" <<'PY_COMPILER'
import pathlib,sys
print(pathlib.Path(sys.argv[1]).resolve())
PY_COMPILER
)
work_root=$(python3 - "$work_root" <<'PY_WORK_ROOT'
import pathlib,sys
print(pathlib.Path(sys.argv[1]).resolve())
PY_WORK_ROOT
)
if [ -n "$evidence_base" ]; then
  evidence_base=$(python3 - "$evidence_base" <<'PY_EVIDENCE_ROOT'
import pathlib,sys
print(pathlib.Path(sys.argv[1]).resolve())
PY_EVIDENCE_ROOT
)
fi
modules=$(python3 - "$modules" <<'PY_MODULES'
import pathlib,sys
print(pathlib.Path(sys.argv[1]).absolute())
PY_MODULES
)

if [ -n "${HD_E2E_DEPLOYMENT:-}" ]; then
  echo 'This harness creates isolated fixture state; persistent deployments require a separate demo workflow.' >&2
  exit 1
fi
for argument in "$@"; do
  case "$argument" in *fullpath*) [ "$mode" = paths ] || { echo 'Use scripts/e2e-paths.sh for the isolated full-path fixtures.' >&2; exit 1; };; esac
done
python3 - "$source_root" "$compiler" "$manifest" "$modules" "$work_root" "$port_cp" "$port_web" "$evidence_base" <<'PY'
import hashlib,json,os,pathlib,platform,re,socket,subprocess,sys
source,compiler,manifest,modules,base=map(pathlib.Path,sys.argv[1:6])
manifest_bytes=manifest.read_bytes();receipt=json.loads(manifest_bytes)
pin=os.environ.get('HD_E2E_COMPILER_MANIFEST_SHA256',os.environ.get('HD_TACHYON_MANIFEST_SHA256',''))
if pin and hashlib.sha256(manifest_bytes).hexdigest()!=pin:
    raise SystemExit('Compiler manifest does not match its pinned digest.')
checkpoint=receipt.get('checkpoint');status=receipt.get('status')
fully_qualified=receipt.get('qualified') is True and status in (None,'qualified') and receipt.get('pending')==[]
scoped_qualified=(status=='qualified-scoped' and receipt.get('qualified',True) is True
    and isinstance(receipt.get('change_scope'),str) and bool(receipt['change_scope'].strip())
    and re.fullmatch('[0-9a-f]{40}',receipt.get('base_qualified_commit',''))
    and receipt.get('pending',[])==[])
if type(checkpoint) is not int or checkpoint<29 or not (fully_qualified or scoped_qualified) or not re.fullmatch('[0-9a-f]{40}',receipt.get('source_commit','')):
    raise SystemExit('A qualified checkpoint >=29 receipt with exact source/scope provenance is required; pending candidates are refused.')
digest=receipt.get('artifact_sha256',receipt.get('sha256',''));size=receipt.get('artifact_size_bytes',receipt.get('size_bytes'))
if receipt.get('sha256',digest)!=digest or receipt.get('size_bytes',size)!=size:
    raise SystemExit('Conflicting qualified artifact fields are refused.')
data=compiler.read_bytes()
if not re.fullmatch('[0-9a-f]{64}',digest) or type(size) is not int or size<=0 or hashlib.sha256(data).hexdigest()!=digest or len(data)!=size:
    raise SystemExit('Compiler bytes do not match the qualified receipt.')
system=platform.system().lower();arch={'x86_64':'amd64','amd64':'amd64','aarch64':'arm64','arm64':'arm64'}.get(platform.machine().lower())
actual=None
if len(data)>=64 and data[:4]==b'\x7fELF' and data[4:6]==b'\x02\x01':actual=('linux',{62:'amd64',183:'arm64'}.get(int.from_bytes(data[18:20],'little')))
elif len(data)>=32 and data[:4]==b'\xcf\xfa\xed\xfe':actual=('darwin',{0x01000007:'amd64',0x0100000c:'arm64'}.get(int.from_bytes(data[4:8],'little')))
if arch is None or actual!=(system,arch):raise SystemExit('The qualified compiler must match this runner native platform.')
resolved=base.resolve()
if resolved==source.resolve() or source.resolve() in resolved.parents or any(word in str(resolved) for word in ('CloudStorage','Google Drive','Dropbox','OneDrive')):
    raise SystemExit('HD_E2E_WORK_ROOT must be outside the checkout and sync storage.')
if not resolved.is_dir():raise SystemExit('HD_E2E_WORK_ROOT must already exist.')
if sys.argv[8]:
    evidence=pathlib.Path(sys.argv[8]).resolve()
    if evidence==source.resolve() or source.resolve() in evidence.parents or any(word in str(evidence) for word in ('CloudStorage','Google Drive','Dropbox','OneDrive')):
        raise SystemExit('HD_E2E_EVIDENCE_DIR must be outside the checkout and sync storage.')
    if evidence.exists() and not evidence.is_dir():raise SystemExit('HD_E2E_EVIDENCE_DIR must be a directory.')
environment=os.environ.copy();environment['GOTOOLCHAIN']='local'
try:
    version=subprocess.run(['go','version'],env=environment,text=True,capture_output=True,check=True,timeout=10).stdout
except (OSError,subprocess.SubprocessError):
    raise SystemExit('Yon requires an installed local Go 1.27 or newer toolchain; automatic toolchain downloads do not satisfy this check.')
match=re.search(r'\bgo(\d+)\.(\d+)(?:[.\s]|$)',version)
if not match or tuple(map(int,match.groups()))<(1,27):
    raise SystemExit('Yon requires an installed local Go 1.27 or newer toolchain with GOTOOLCHAIN=local; the control-plane Go 1.26 wrapper is insufficient.')
locked=json.loads((source/'app/package-lock.json').read_text())['packages']['node_modules/@playwright/test']['version']
if any(word in str(modules) for word in ('CloudStorage','Google Drive','Dropbox','OneDrive')):
    raise SystemExit('HD_E2E_NODE_MODULES must point outside sync storage.')
installed=json.loads((modules/'@playwright/test/package.json').read_text())['version']
if installed!=locked:raise SystemExit('Playwright dependencies do not match the frozen lockfile.')
if sys.argv[6]==sys.argv[7]:raise SystemExit('Control-plane and web ports must differ.')
for raw in sys.argv[6:8]:
    port=int(raw)
    if not 1024<=port<=65535:raise SystemExit('Fixture ports must be between 1024 and 65535.')
    with socket.socket() as listener:listener.bind(('127.0.0.1',port))
PY

workdir=$(mktemp -d "$work_root/hd-e2e-web.XXXXXX")
evidence_run=
deployment="$workdir/deployment"
app="$workdir/app"
owned_pids=()
completed=false
cleanup() {
  local result=$1 pid cleanup_status=0
  if [ "$result" -ne 0 ] && [ "${HD_E2E_DIAGNOSTICS:-}" = 1 ]; then
    python3 - "$source_root/scripts" "$workdir" <<'PY_DIAGNOSTIC_STOP'
import importlib.util,json,pathlib,signal,sys,time
module=pathlib.Path(sys.argv[1])/'e2e-process.py'
spec=importlib.util.spec_from_file_location('fixture_process',module)
helper=importlib.util.module_from_spec(spec);spec.loader.exec_module(helper)
process_table,identity=helper.process_table,helper.identity
work=pathlib.Path(sys.argv[2]);record=work/'control-plane.pid.identity.json'
if record.is_file():
    known=json.loads(record.read_text());current=process_table().get(known['pid'])
    if current is not None and identity(current)==identity(known) and not current['state'].startswith('Z'):
        import os
        os.kill(known['pid'],signal.SIGQUIT)
        time.sleep(.3)
PY_DIAGNOSTIC_STOP
  fi
  if [ "${#owned_pids[@]}" -gt 0 ]; then
    for pid in $(jobs -pr) $(jobs -ps); do
      case " ${owned_pids[*]} " in *" $pid "*) kill -TERM "$pid" 2>/dev/null || true;; esac
    done
    for pid in "${owned_pids[@]}"; do wait "$pid" 2>/dev/null || true; done
  fi
  if [ -n "$evidence_run" ]; then
    python3 - "$workdir" "$evidence_run" "$result" "$mode" <<'PY_EVIDENCE' || echo 'Could not retain all redacted fixture evidence.' >&2
import datetime,json,os,pathlib,re,sys
source,destination=map(pathlib.Path,sys.argv[1:3])
secrets={os.environ.get('HD_E2E_PASSWORD','')}
session=source/'deployment/session.json'
if session.is_file():
    try:
        values=json.loads(session.read_text())
        secrets.update(str(values.get(key,'')) for key in ('session_id','session_secret','access_token','refresh_token'))
        secrets.add(str(values.get('session_id',''))+'.'+str(values.get('session_secret','')))
    except (OSError,ValueError):pass
secrets.discard('');secrets.discard('.')
def redact(content):
    for secret in sorted(secrets,key=len,reverse=True):content=content.replace(secret,'[REDACTED]')
    content=re.sub(r'(?i)(Bearer\s+)\S+',r'\1[REDACTED]',content)
    content=re.sub(r'(?i)([\"\']?(?:password|session_id|session_secret|hd_session|access_token|refresh_token|authorization)[\"\']?\s*[:=]\s*)[^\s,}]+',r'\1[REDACTED]',content)
    return re.sub(r'-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----','[REDACTED PRIVATE KEY]',content,flags=re.S)
# Only hashes, bounded command metadata, and explicitly selected build/test logs survive.
# Never copy deployment state, credentials, configuration, runtime logs, or key files.
allowed=('source-receipt.json','command-receipts.jsonl','process-receipts.jsonl','api-requests.jsonl','build-control-plane.log','build-fixture-engine.log','bundle.log','playwright.log')
for name in allowed:
    file=source/name
    if file.is_file():
        content=redact(file.read_text(errors='replace'))
        content=content.replace(str(source),'<private-fixture>')
        (destination/name).write_text(content)
server=source/'control-plane.log'
if server.is_file():
    events=[];stack=[]
    for line in server.read_text(errors='replace').splitlines():
        try:
            event=json.loads(line)
            if event.get('msg') in ('heimdall serving','authorization','audit write failed'):
                events.append({key:event[key] for key in ('time','level','msg','action','outcome','reason_code','method','path') if key in event})
        except ValueError:
            stripped=line.strip()
            if re.fullmatch(r'goroutine [0-9]+ \[.*\]:',stripped):stack.append(stripped)
            elif stripped.endswith(')') and '(' in stripped and not stripped.startswith(('panic:','fatal error:')):
                name=stripped.rsplit('(',1)[0]
                if re.fullmatch(r'[A-Za-z0-9_./*()[\]{}<>+ -]+',name):stack.append(name+'(arguments omitted)')
            elif re.search(r'\.go:[0-9]+',stripped):
                match=re.search(r'([^/\s]+\.go:[0-9]+)',stripped)
                if match:stack.append(match.group(1))
    (destination/'server-events.json').write_text(json.dumps(events,indent=2))
    (destination/'server-stack-frames.json').write_text(json.dumps(stack,indent=2))
commands=[]
receipt=source/'command-receipts.jsonl'
if receipt.is_file():
    for line in receipt.read_text().splitlines():
        try:commands.append(json.loads(line))
        except ValueError:pass
tests=[command for command in commands if command.get('label')=='playwright']
(destination/'test-receipt.json').write_text(json.dumps({'mode':sys.argv[4],'script_exit_code':int(sys.argv[3]),'tests_executed':bool(tests),'tests_passed':bool(tests) and tests[-1].get('exit_code')==0 and tests[-1].get('outcome')=='completed','finished_at':datetime.datetime.now(datetime.timezone.utc).isoformat()},indent=2))
PY_EVIDENCE
  fi
  rm -rf "$workdir" || cleanup_status=$?
  if [ -n "$evidence_run" ]; then
    python3 - "$evidence_run" "$workdir" "$cleanup_status" "${owned_pids[*]:-}" <<'PY_CLEANUP' || echo 'Could not retain the cleanup receipt.' >&2
import datetime,json,os,pathlib,sys
remaining=[]
for raw in sys.argv[4].split():
    try:os.kill(int(raw),0);remaining.append(int(raw))
    except ProcessLookupError:pass
    except PermissionError:remaining.append(int(raw))
fixture_pids=[]
records=pathlib.Path(sys.argv[1])/'process-receipts.jsonl'
if records.is_file():
    for line in records.read_text().splitlines():
        try:
            pid=int(json.loads(line)['pid'])
            try:os.kill(pid,0);fixture_pids.append(pid)
            except ProcessLookupError:pass
            except PermissionError:fixture_pids.append(pid)
        except (ValueError,KeyError):pass
state_retained=pathlib.Path(sys.argv[2]).exists()
receipt={'cleanup_exit_code':int(sys.argv[3]),'cleanup_complete':int(sys.argv[3])==0 and not state_retained and not remaining and not fixture_pids,'runtime_state_retained':state_retained,'remaining_supervisor_pids':remaining,'remaining_fixture_pids':fixture_pids,'credentials_or_keys_copied':False,'finished_at':datetime.datetime.now(datetime.timezone.utc).isoformat()}
(pathlib.Path(sys.argv[1])/'cleanup-receipt.json').write_text(json.dumps(receipt,indent=2))
PY_CLEANUP
    echo "Redacted fixture evidence: $evidence_run"
  fi
  return "$result"
}
trap 'result=$?; if [ "$completed" != true ] && [ "$result" -eq 0 ]; then result=1; fi; trap - EXIT; cleanup "$result" || true; exit "$result"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
if [ -n "$evidence_base" ]; then
  mkdir -p "$evidence_base"
  evidence_run=$(mktemp -d "$evidence_base/hd-e2e-$mode.XXXXXX")
fi

start_owned() {
  local name=$1
  shift
  python3 "$source_root/scripts/e2e-process.py" owned "$workdir/$name.log" "$workdir/process-receipts.jsonl" "$name" 0 "$workdir/$name.pid" "$@" &
  owned_pids+=("$!")
}
run_bounded() {
  local seconds=$1 name=$2
  shift 2
  python3 "$source_root/scripts/e2e-process.py" bounded "$workdir/$name.log" "$workdir/command-receipts.jsonl" "$name" "$seconds" '' "$@"
}
wait_ready() {
  local url=$1 certificate=${2:-} attempt
  for attempt in $(seq 1 30); do
    if [ -n "$certificate" ]; then
      if curl --fail --silent --max-time 1 --cacert "$certificate" "$url" >/dev/null 2>&1; then return; fi
    elif curl --fail --silent --max-time 1 "$url" >/dev/null 2>&1; then return; fi
    sleep 1
  done
  echo 'An isolated fixture did not become ready within 30 attempts.' >&2
  return 1
}

python3 - "$source_root/app" "$app" "$modules" "$manifest" <<'PY'
import hashlib,json,pathlib,shutil,sys
source,destination,modules,manifest=map(pathlib.Path,sys.argv[1:])
def ignore(directory,names):
    return [name for name in names if name in {'node_modules','dist','.tachyon','.tac-cache','test-results','playwright-report'} or name=='.DS_Store' or name=='bff.json']
shutil.copytree(source,destination,ignore=ignore,symlinks=True)
test_source=source.parent/'tests'
test_destination=destination.parent/'tests'
test_destination.mkdir()
shutil.copy2(test_source/'package.json',test_destination/'package.json')
shutil.copytree(test_source/'app',test_destination/'app',symlinks=True)
records=[]
for path in sorted(test_destination.rglob('*')):
    if path.is_symlink():raise SystemExit('Fixture test staging refuses source symlinks.')
    if path.is_file():
        relative=path.relative_to(test_destination)
        digest=hashlib.sha256(path.read_bytes()).hexdigest()
        if digest!=hashlib.sha256((test_source/relative).read_bytes()).hexdigest():raise SystemExit('Test transfer changed a file.')
        records.append({'path':str(path.relative_to(destination.parent)),'sha256':digest})
for path in sorted(destination.rglob('*')):
    if path.is_symlink():raise SystemExit('Fixture source staging refuses source symlinks.')
    if path.is_file():
        relative=path.relative_to(destination)
        digest=hashlib.sha256(path.read_bytes()).hexdigest()
        if digest!=hashlib.sha256((source/relative).read_bytes()).hexdigest():raise SystemExit('Source transfer changed a file.')
        records.append({'path':str(relative),'sha256':digest})
(destination/'node_modules').symlink_to(modules.resolve(),target_is_directory=True)
receipt=json.loads(manifest.read_text())
(destination.parent/'source-receipt.json').write_text(json.dumps({'source_files':records,'compiler_source':receipt['source_commit'],'compiler_sha256':receipt.get('artifact_sha256',receipt.get('sha256')),'compiler_manifest_sha256':hashlib.sha256(manifest.read_bytes()).hexdigest(),'compiler_qualification':receipt.get('status',receipt.get('original_status','qualified')),'compiler_change_scope':receipt.get('change_scope'),'base_qualified_commit':receipt.get('base_qualified_commit')},indent=2))
PY

password=$(python3 -c 'import secrets;print("local-fixture-"+secrets.token_hex(24))')
export HD_ADMIN_PASSWORD="$password" HD_PASSWORD="$password" HD_E2E_PASSWORD="$password"
export HD_PUBLIC_URL="https://127.0.0.1:$port_cp"
export HD_ADDR_URL="$HD_PUBLIC_URL"
export HD_CA_FILE="$deployment/keys/agent-ca.crt" HD_WEB_URL="http://127.0.0.1:$port_web"
export AWS_EC2_METADATA_DISABLED=true HD_E2E_USER=ops HD_SYNC_INTERVAL=10s
export GOMAXPROCS=${GOMAXPROCS:-2}

cd "$source_root"
run_bounded 600 build-control-plane go -C app/server build -mod=readonly -p 2 -o "$workdir/heimdall" ./commands/heimdall
run_bounded 300 build-fixture-engine go -C app/server build -mod=readonly -p 2 -o "$workdir/fake-engine" ./commands/fake-engine
python3 - "$source_root" "$workdir" <<'PY_BUILD_PROVENANCE'
import hashlib,json,pathlib,subprocess,sys
source,work=map(pathlib.Path,sys.argv[1:])
receipt=work/'source-receipt.json';record=json.loads(receipt.read_text())
paths=subprocess.check_output(['git','ls-files','--cached','--others','--exclude-standard','--','app/server'],cwd=source,text=True).splitlines()
record['control_plane_source_head']=subprocess.check_output(['git','rev-parse','HEAD'],cwd=source,text=True).strip()
record['control_plane_source_hashes']={name:hashlib.sha256((source/name).read_bytes()).hexdigest() for name in sorted(set(paths)) if name.endswith('.go') and not name.endswith('_test.go') and (source/name).is_file()}
record['runtime_binaries_sha256']={name:hashlib.sha256((work/name).read_bytes()).hexdigest() for name in ('heimdall','fake-engine')}
record['go_toolchain']=subprocess.check_output(['go','version'],text=True).strip()
receipt.write_text(json.dumps(record,indent=2))
PY_BUILD_PROVENANCE
start_owned fixture-engine "$workdir/fake-engine"
for _ in $(seq 1 30); do [ -s "$workdir/fixture-engine.log" ] && break; sleep 1; done
engine=$(head -n 1 "$workdir/fixture-engine.log")
case "$engine" in http://127.0.0.1:*) ;; *) echo 'The fixture Engine did not return a loopback endpoint.' >&2; exit 1;; esac
run_bounded 60 initialize "$workdir/heimdall" init --deployment "$deployment" --admin ops
start_owned control-plane "$workdir/heimdall" serve --deployment "$deployment" --addr "127.0.0.1:$port_cp"
wait_ready "$HD_PUBLIC_URL/healthz" "$HD_CA_FILE"
export HD_E2E_CP_PID=$(cat "$workdir/control-plane.pid") ENGINE_URL="$engine"
run_bounded 60 login "$workdir/heimdall" login --deployment "$deployment" --user ops
bearer=$(python3 - "$deployment/session.json" <<'PY'
import json,sys
session=json.load(open(sys.argv[1]));print(session['session_id']+'.'+session['session_secret'])
PY
)
api() { python3 "$source_root/scripts/e2e-request.py" "$workdir/api-requests.jsonl" "$workdir/api-response.private" --fail --silent --show-error --max-time 30 --cacert "$HD_CA_FILE" -H "Authorization: Bearer $bearer" -H 'Content-Type: application/json' "$@"; }
mkdir -p "$workdir/repo/deploy"
project=alpha
if [ "$mode" = paths ]; then
  project=shop
  printf 'services:\n  web:\n    image: nginx:1.27.3\n    ports: ["8080:80"]\n  cache:\n    image: redis:7.4-alpine\n' > "$workdir/repo/deploy/compose.yaml"
else
  printf 'services:\n  web:\n    image: fixture-only:1\n' > "$workdir/repo/deploy/compose.yaml"
fi
git -C "$workdir/repo" init -q --initial-branch=main
git -C "$workdir/repo" add .
git -C "$workdir/repo" -c user.name=e2e -c user.email=e2e@test commit -qm seed
repo_body=$(python3 - "$workdir/repo" "$project" <<'PY_REPO'
import json,sys
print(json.dumps({'project':sys.argv[2],'name':'site','url':sys.argv[1],'default_ref':'main'}))
PY_REPO
)
repo=$(api -X POST -d "$repo_body" "$HD_PUBLIC_URL/api/v1/repos" | python3 -c "import json,sys;print(json.load(sys.stdin)['id'])")
target_body=$(python3 - "$engine" "$project" <<'PY_TARGET'
import json,sys
print(json.dumps({'project':sys.argv[2],'name':'local','provider':'docker','endpoint':sys.argv[1]}))
PY_TARGET
)
target=$(api -X POST -d "$target_body" "$HD_PUBLIC_URL/api/v1/targets" | python3 -c "import json,sys;print(json.load(sys.stdin)['id'])")
api -X POST -d "{\"name\":\"site\",\"repo_id\":\"$repo\",\"target_id\":\"$target\",\"path\":\"deploy\"}" "$HD_PUBLIC_URL/api/v1/projects/$project/apps" >/dev/null
if [ "$mode" = paths ]; then
  api -X POST -d '{}' "$HD_PUBLIC_URL/api/v1/projects/shop/apps/site/sync" >/dev/null
  python3 - "$workdir/repo/deploy/compose.yaml" <<'PY_REVISION'
import pathlib,sys
file=pathlib.Path(sys.argv[1]);file.write_text(file.read_text().replace('nginx:1.27.3','nginx:1.27.4'))
PY_REVISION
  git -C "$workdir/repo" add .
  git -C "$workdir/repo" -c user.name=e2e -c user.email=e2e@test commit -qm revision-two
  api -X POST -d '{}' "$HD_PUBLIC_URL/api/v1/projects/shop/apps/site/sync" >/dev/null
  api -X POST -d "{\"name\":\"legacy\",\"repo_id\":\"$repo\",\"target_id\":\"$target\",\"path\":\"deploy\"}" "$HD_PUBLIC_URL/api/v1/projects/shop/apps" >/dev/null
  api -X POST -d '{}' "$HD_PUBLIC_URL/api/v1/projects/shop/apps/legacy/suspend" >/dev/null
  export HD_FULLPATH=1
fi
if [ "${HD_E2E_SETUP_ONLY:-}" = 1 ]; then
  completed=true
  echo 'Isolated API setup probe completed; browser tests were not requested.'
  exit 0
fi
unset bearer HD_ADMIN_PASSWORD HD_PASSWORD

python3 - "$app/server/data/bff.json" "$HD_PUBLIC_URL" "$HD_CA_FILE" "$mode" <<'PY'
import json,os,pathlib,sys
file=pathlib.Path(sys.argv[1]);file.parent.mkdir(parents=True,exist_ok=True)
configuration={'apiUrl':sys.argv[2],'caFile':sys.argv[3],'secureCookie':False,'timeoutMs':25000}
if sys.argv[4]=='paths':configuration['auditLogin']={'identifier':os.environ['HD_E2E_USER'],'password':os.environ['HD_E2E_PASSWORD']}
file.write_text(json.dumps(configuration))
PY
cd "$app"
run_bounded 180 bundle "$compiler" bundle "$app" --out-dir dist --no-incremental
start_owned web "$compiler" serve "$app" --host 127.0.0.1 --port "$port_web" --out-dir dist --no-bundle --no-watch --no-self-restart
wait_ready "$HD_WEB_URL/"
if [ -n "${HD_E2E_KEEP:-}" ]; then
  python3 - "$workdir/fixture-login.json" <<'PY_LOGIN'
import json,os,pathlib,sys
pathlib.Path(sys.argv[1]).write_text(json.dumps({'identifier':os.environ['HD_E2E_USER'],'password':os.environ['HD_E2E_PASSWORD']}))
PY_LOGIN
  echo "Isolated web fixture: $HD_WEB_URL; credentials and state remain private under $workdir. Ctrl-C stops only this run."
  wait "${owned_pids[${#owned_pids[@]}-1]}"
  completed=true
  exit 0
fi
if [ "$#" -eq 0 ]; then
  if [ "$mode" = paths ]; then set -- 'fullpath\.spec\.js$'; else set -- 'ui\.spec\.js$'; fi
fi
run_bounded 360 playwright "$modules/.bin/playwright" test "$@" --workers 1 --retries 0
completed=true
echo 'The isolated web E2E suite passed.'
