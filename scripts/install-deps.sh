#!/usr/bin/env bash
# Control-plane dependencies and qualified Tachyon are separate installations.
# Local default: scripts/install-deps.sh --control-plane (into ~/.local/bin).
# Tachyon: supply HD_TACHYON_BIN, HD_TACHYON_MANIFEST, and the independently
# pinned HD_TACHYON_MANIFEST_SHA256; use --verify-tachyon for read-only checking.
set -euo pipefail

mode=${1:---control-plane}
case "$mode" in --control-plane|--tachyon|--all|--verify-tachyon) ;; *)
  echo 'usage: scripts/install-deps.sh [--control-plane|--tachyon|--all|--verify-tachyon]' >&2
  exit 1;;
esac
[ "$#" -le 1 ] || { echo 'Only one installation mode is accepted.' >&2; exit 1; }
destination=${DEPS_BIN:-$HOME/.local/bin}
destination=$(python3 - "$destination" <<'PY_DESTINATION'
import pathlib,sys
directory=pathlib.Path(sys.argv[1]).expanduser().resolve()
if any(word in str(directory) for word in ('CloudStorage','Google Drive','Dropbox','OneDrive')):
    raise SystemExit('DEPS_BIN must be outside sync storage.')
print(directory)
PY_DESTINATION
)

qualified_tachyon() {
  local action=$1
  python3 - "$action" "$destination" <<'PY_QUALIFIED'
import hashlib,json,os,pathlib,platform,re,sys,tempfile

def require(name):
    value=os.environ.get(name,'')
    if not value:raise SystemExit(name+' is required; provide a genuinely qualified native compiler handoff, not a release tag.')
    return value

binary=pathlib.Path(require('HD_TACHYON_BIN')).resolve()
manifest=pathlib.Path(require('HD_TACHYON_MANIFEST')).resolve()
manifest_digest=require('HD_TACHYON_MANIFEST_SHA256')
if not re.fullmatch('[0-9a-f]{64}',manifest_digest):raise SystemExit('The pinned manifest digest must be a lowercase SHA256.')
manifest_bytes=manifest.read_bytes()
if hashlib.sha256(manifest_bytes).hexdigest()!=manifest_digest:raise SystemExit('The compiler manifest does not match its independently pinned digest.')
receipt=json.loads(manifest_bytes)
checkpoint=receipt.get('checkpoint')
status=receipt.get('status')
fully_qualified=receipt.get('qualified') is True and status in (None,'qualified') and receipt.get('pending')==[]
scoped_qualified=(status=='qualified-scoped' and receipt.get('qualified',True) is True
    and isinstance(receipt.get('change_scope'),str) and bool(receipt['change_scope'].strip())
    and re.fullmatch('[0-9a-f]{40}',receipt.get('base_qualified_commit',''))
    and receipt.get('pending',[])==[])
if type(checkpoint) is not int or checkpoint<29 or not (fully_qualified or scoped_qualified):
    raise SystemExit('Tachyon requires a qualified checkpoint >=29 or a qualified-scoped handoff with exact scope/base provenance; pending candidates are refused.')
if not re.fullmatch('[0-9a-f]{40}',receipt.get('source_commit','')):raise SystemExit('The qualified receipt must identify its exact source commit.')
digest=receipt.get('artifact_sha256',receipt.get('sha256',''));size=receipt.get('artifact_size_bytes',receipt.get('size_bytes'))
if receipt.get('sha256',digest)!=digest or receipt.get('size_bytes',size)!=size:
    raise SystemExit('Conflicting artifact fields in the qualified receipt are refused.')
if not re.fullmatch('[0-9a-f]{64}',digest) or type(size) is not int or size<=0:raise SystemExit('The receipt must record an exact artifact SHA256 and byte size.')
data=binary.read_bytes()
if len(data)!=size or hashlib.sha256(data).hexdigest()!=digest:raise SystemExit('Compiler bytes do not match the qualified artifact receipt.')
system=platform.system().lower();machine=platform.machine().lower()
arch={'x86_64':'amd64','amd64':'amd64','aarch64':'arm64','arm64':'arm64'}.get(machine)
actual=None
if len(data)>=64 and data[:4]==b'\x7fELF' and data[4:6]==b'\x02\x01':
    actual=('linux',{62:'amd64',183:'arm64'}.get(int.from_bytes(data[18:20],'little')))
elif len(data)>=32 and data[:4]==b'\xcf\xfa\xed\xfe':
    actual=('darwin',{0x01000007:'amd64',0x0100000c:'arm64'}.get(int.from_bytes(data[4:8],'little')))
if arch is None or actual!=(system,arch):
    raise SystemExit('Qualified compiler bytes must be a native 64-bit Linux ELF or macOS Mach-O for this runner; a macOS receipt cannot qualify Linux.')
expected_source=os.environ.get('HD_TACHYON_SOURCE_COMMIT','')
if expected_source and expected_source!=receipt['source_commit']:raise SystemExit('Compiler source does not match HD_TACHYON_SOURCE_COMMIT.')
destination=pathlib.Path(sys.argv[2]).expanduser().resolve()
if sys.argv[1]=='install':
    if any(word in str(destination) for word in ('CloudStorage','Google Drive','Dropbox','OneDrive')):
        raise SystemExit('DEPS_BIN must be outside sync storage.')
    destination.mkdir(parents=True,exist_ok=True)
    target=destination/'ty'
    if target.exists() or target.is_symlink():
        if target.is_symlink() or not target.is_file() or hashlib.sha256(target.read_bytes()).hexdigest()!=digest:
            raise SystemExit('Refusing to overwrite a different existing ty; choose a fresh DEPS_BIN.')
        if not os.access(target,os.X_OK):raise SystemExit('The matching existing ty must be executable.')
    else:
        descriptor,temporary=tempfile.mkstemp(prefix='.qualified-ty-',dir=destination)
        try:
            with os.fdopen(descriptor,'wb') as output:output.write(data)
            os.chmod(temporary,0o555)
            os.replace(temporary,target)
        finally:
            if os.path.exists(temporary):os.unlink(temporary)
print('Verified '+('qualified-scoped' if scoped_qualified else 'qualified')+' Tachyon checkpoint '+str(checkpoint)+' source '+receipt['source_commit']+' artifact '+digest+' platform '+system+'/'+arch+'.')
PY_QUALIFIED
}

# Verify before any installation or network request in --all mode.
case "$mode" in --tachyon|--all|--verify-tachyon) qualified_tachyon verify;; esac
if [ "$mode" = --verify-tachyon ]; then exit 0; fi

install_release() {
  local repo=$1 version=$2 base os arch name
  if [ "$version" = latest ]; then
    base="https://github.com/d31ma/$repo/releases/latest/download"
  else
    base="https://github.com/d31ma/$repo/releases/download/$version"
  fi
  echo "installing $repo@$version"
  if curl -fsSL "$base/install.sh" | env "${repo}_VERSION=$version" INSTALL_DIR="$destination" sh; then return; fi
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m)
  case "$arch" in x86_64) arch=amd64;; aarch64) arch=arm64;; esac
  name=$(echo "$repo" | tr '[:upper:]' '[:lower:]')
  echo "no install.sh in the $repo release; fetching $name-$os-$arch directly"
  curl -fsSL -o "$destination/$name" "$base/$name-$os-$arch"
  chmod +x "$destination/$name"
}

mkdir -p "$destination"
case "$mode" in --control-plane|--all)
  # TTID first: FYLO reports dependencies.ttid.available=false without it,
  # and SESAME refuses to drive a FYLO in that state. Keep the paired pins.
  install_release TTID "${TTID_VERSION:-v26.32.03}"
  install_release FYLO "${FYLO_VERSION:-v26.33.02}"
  install_release SESAME "${SESAME_VERSION:-v26.33.02}"
  install_release CHEX "${CHEX_VERSION:-v26.32.02}"
  ;;
esac
case "$mode" in --tachyon|--all) qualified_tachyon install;; esac

export PATH="$destination:$PATH"
if [ -n "${GITHUB_PATH:-}" ]; then echo "$destination" >> "$GITHUB_PATH"; fi
case "$mode" in --control-plane|--all)
  if ! command -v sops >/dev/null 2>&1; then
    echo "warning: sops is not installed; ${SOPS_HINT:-brew install sops age (or your package manager)} enables sops/ secret references" >&2
  fi
  ttid --help >/dev/null
  fylo version
  sesame version
  chex --help >/dev/null
  echo "fylo, sesame, chex, and ttid are installed in $destination"
  ;;
esac
