#!/usr/bin/env bash
set -euo pipefail
source_root=$(cd "$(dirname "$0")/.." && pwd -P)
native_packages=(
  "./commands/..."
  "./services/api/..."
  "./helpers/git/..."
  "./helpers/schema/..."
  "./helpers/secrets/..."
  "./utils/..."
  "./models/spec/..."
  "./repositories/..."
  "./services/agent/..."
  "./services/auth/..."
  "./services/dispatch/..."
  "./services/enroll/..."
  "./services/observe/..."
  "./services/provider/..."
  "./services/reconcile/..."
  "./services/registry/..."
  "./services/render/..."
)
mode=${1:?specify test, test-packages, vet, list, build or packages}
shift
case "$mode" in
  packages) printf '%s\n' "${native_packages[@]}" ;;
  build) exec go -C "$source_root/app/server" build "$@" "${native_packages[@]}" ;;
  test|test-packages|vet|list)
    explicit_packages=0
    if [ "$mode" = test-packages ]; then
      mode=test
      explicit_packages=1
      [ "$#" -gt 0 ] || { echo 'Supply a native package scope.' >&2; exit 2; }
    fi
    overlay=$(mktemp "${TMPDIR:-/tmp}/heimdall-test-overlay.XXXXXX")
    trap 'exit_status=$?; rm -f "$overlay"; exit "$exit_status"' EXIT
    python3 "$source_root/scripts/native-test-overlay.py" "$source_root" > "$overlay"
    if [ "$explicit_packages" -eq 1 ]; then
      go -C "$source_root/app/server" "$mode" -overlay "$overlay" "$@"
    else
      go -C "$source_root/app/server" "$mode" -overlay "$overlay" "$@" "${native_packages[@]}"
    fi
    ;;
  *) exit 2 ;;
esac
