#!/usr/bin/env bash
# Vendors DuVay's built CSS into the web tier and the website.
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
# The version is pinned by content. Upstream publishes only /latest/ — there
# is no version-addressed URL, the release tag carries no asset, and dist/ is
# not in the repository — so "pinned" here means: this exact stylesheet, by
# digest, or the build stops. Without it CI silently rebuilds against whatever
# DuVay shipped that morning, and a restyled control plane arrives with no
# commit to review.
#
# Updating is deliberate: run with DUVAY_UPDATE=1, read what the diff does to
# the UI, then commit the new version and digest below.
#
# Usage: scripts/vendor-duvay.sh [path-to-DELMA-checkouts]
set -euo pipefail

cd "$(dirname "$0")/.."

# The pin. Both lines change together, and only on purpose.
DUVAY_VERSION="26.34.03"
DUVAY_SHA256="ef6dd576db2228e4f4da45646cd66eabbfc3b431a99eed4a7a25ff5cbf84865d"

source_root="${1:-..}"
destinations="web/client/shared/assets/duvay website/client/shared/assets/duvay"

source_file="$source_root/DUVAY/dist/duvay.min.css"
origin="checkout $source_file"
if [ -f "$source_file" ]; then
  # A checkout can be ahead of the pin — that is how the next update starts —
  # so say which version it holds rather than assuming it matches.
  if [ -f "$source_root/DUVAY/package.json" ]; then
    checkout_version=$(python3 -c \
      "import json;print(json.load(open('$source_root/DUVAY/package.json')).get('version','unknown'))")
    if [ "$checkout_version" != "$DUVAY_VERSION" ]; then
      echo "note: the DUVAY checkout is $checkout_version, the pin is $DUVAY_VERSION" >&2
    fi
  fi
else
  # No local checkout (CI runners). Fetch the same sheet the docs link; the
  # digest below is what makes that safe.
  echo "no DUVAY checkout at $source_root; fetching the hosted sheet"
  source_file=$(mktemp /tmp/duvay.XXXXXX.css)
  origin="https://d31ma.github.io/DUVAY/latest/duvay.min.css"
  curl -fsSL -o "$source_file" "$origin"
fi

actual=$(shasum -a 256 "$source_file" | awk '{print $1}')
if [ "$actual" != "$DUVAY_SHA256" ]; then
  if [ -z "${DUVAY_UPDATE:-}" ]; then
    cat >&2 <<EOF
DuVay does not match the pin.

  source:   $origin
  expected: $DUVAY_SHA256  (version $DUVAY_VERSION)
  actual:   $actual

Upstream has published a different stylesheet. That is a UI change, so it
wants a look and a commit rather than a silent rebuild:

  DUVAY_UPDATE=1 scripts/vendor-duvay.sh

then set DUVAY_VERSION and DUVAY_SHA256 in this script to the values it
prints, and check the rendered UI before committing.
EOF
    exit 1
  fi
  echo "DUVAY_UPDATE set: accepting $actual" >&2
fi

for destination in $destinations; do
  mkdir -p "$destination"
  cp "$source_file" "$destination/duvay.min.css"
done

version="$DUVAY_VERSION"
if [ -n "${DUVAY_UPDATE:-}" ] && [ -n "${checkout_version:-}" ]; then
  version="$checkout_version"
fi
for destination in $destinations; do
cat > "$destination/VENDOR.md" <<EOF
# Vendored: DuVay

Source: \`DUVAY/dist/duvay.min.css\` (version $version)
Digest: \`sha256:$actual\`

Copied by \`scripts/vendor-duvay.sh\`, which pins that digest — upstream
publishes no version-addressed URL, so the content is the pin. Do not edit:
change it upstream, then re-pin deliberately with \`DUVAY_UPDATE=1\`. Only the
CSS is vendored; see the script for why the web-component bundle is not.
EOF
done

printf 'vendored duvay.min.css (%s, sha256:%s) into both frontends\n' "$version" "${actual:0:12}"
if [ -n "${DUVAY_UPDATE:-}" ]; then
  printf 'pin these in scripts/vendor-duvay.sh:\n  DUVAY_VERSION="%s"\n  DUVAY_SHA256="%s"\n' \
    "$version" "$actual"
fi
