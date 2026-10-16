#!/usr/bin/env bash
set -euo pipefail
export HD_E2E_MODE=paths HD_FULLPATH=1
export HD_E2E_WEB_PORT=${HD_E2E_WEB_PORT:-9303}
exec "$(dirname "$0")/e2e-web.sh" "$@"
