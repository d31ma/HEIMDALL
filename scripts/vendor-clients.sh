#!/usr/bin/env bash
# Refreshes the two vendored client shims from their source repositories.
#
# FYLO and CHEX are Rust projects; their Go shims are single stdlib-only files
# that are not published as Go modules, so they are copied verbatim rather
# than imported. SESAME is a real Go module and is a `go get`, never a copy.
#
# Usage: scripts/vendor-clients.sh [path-to-DELMA-checkouts]
set -euo pipefail

cd "$(dirname "$0")/.."
source_root="${1:-..}"

copy() {
  local from="$source_root/$1" to="$2"
  if [ ! -f "$from" ]; then
    echo "missing upstream file: $from" >&2
    exit 1
  fi
  cp "$from" "$to"
  echo "vendored $1 -> $to"
}

copy FYLO/clients/go/fylo.go internal/store/fylo/fylo.go
copy CHEX/clients/go/chex.go internal/schema/chex/chex.go

gofmt -l internal/store/fylo internal/schema/chex
go build ./...
echo "vendored clients refreshed; commit them with the upstream version in the message"
