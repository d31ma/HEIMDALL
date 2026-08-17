#!/usr/bin/env bash
# Vendors DuVay's built CSS into the web tier.
#
# DuVay is distributed as hosted files rather than a package. A control plane
# is routinely deployed air-gapped, so linking a CDN would make the UI depend
# on egress it may not have — and Tachyon's default content-security-policy is
# `default-src 'self'`, which would block it anyway. Vendoring keeps the whole
# thing same-origin.
#
# Only the CSS is taken. The <w-*> web-component bundle is 754 KiB and this UI
# uses DuVay's classes rather than its custom elements, so it would be weight
# with no use. Revisit when a page needs a component that has no class form.
#
# Usage: scripts/vendor-duvay.sh [path-to-DELMA-checkouts]
set -euo pipefail

cd "$(dirname "$0")/.."
source_root="${1:-..}"
destinations="web/client/shared/assets/duvay website/client/shared/assets/duvay"

source_file="$source_root/DUVAY/dist/duvay.min.css"
if [ ! -f "$source_file" ]; then
  echo "missing $source_file — pass the directory holding the DELMA checkouts" >&2
  exit 1
fi

for destination in $destinations; do
  mkdir -p "$destination"
  cp "$source_file" "$destination/duvay.min.css"
done

version="unknown"
if [ -f "$source_root/DUVAY/package.json" ]; then
  version=$(python3 -c "import json;print(json.load(open('$source_root/DUVAY/package.json')).get('version','unknown'))")
fi
for destination in $destinations; do
cat > "$destination/VENDOR.md" <<EOF
# Vendored: DuVay

Source: \`DUVAY/dist/duvay.min.css\` (version $version)

Copied by \`scripts/vendor-duvay.sh\`. Do not edit — change it upstream and
re-run the script. Only the CSS is vendored; see the script for why the
web-component bundle is not.
EOF
done

printf 'vendored duvay.min.css (%s) into %s\n' "$version" "$destination"
