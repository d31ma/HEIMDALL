#!/usr/bin/env bash
# Installs the three binaries HEIMDALL's tests drive: fylo, sesame, and chex.
#
# The tests run against real engines rather than fakes — an authorization test
# against a stub would happily return "allow" and prove nothing — so CI needs
# them on PATH. SESAME pins the FYLO executable it drives by absolute path, so
# the two versions are upgraded as a pair, never independently.
#
# Local use: scripts/install-deps.sh   (installs into ~/.local/bin)
set -euo pipefail

destination="${DEPS_BIN:-$HOME/.local/bin}"
mkdir -p "$destination"

# install <repo> <version>, where version is a tag such as v26.33.02, or the
# literal "latest".
install_release() {
  local repo="$1" version="$2" base
  # GitHub asset URLs differ by one segment: latest keeps a /download/
  # between "latest" and the asset; a pinned tag puts /download/ before it.
  if [ "$version" = "latest" ]; then
    base="https://github.com/d31ma/$repo/releases/latest/download"
  else
    base="https://github.com/d31ma/$repo/releases/download/$version"
  fi
  echo "installing $repo@$version"
  if curl -fsSL "$base/install.sh" | INSTALL_DIR="$destination" sh; then
    return
  fi
  # Not every sibling ships an install.sh — SESAME's releases carry plain
  # platform binaries (sesame-<os>-<arch>). Fall back to that convention.
  local os arch name
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m)
  case "$arch" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac
  name=$(echo "$repo" | tr '[:upper:]' '[:lower:]')
  echo "no install.sh in the $repo release; fetching $name-$os-$arch directly"
  curl -fsSL -o "$destination/$name" "$base/$name-$os-$arch"
  chmod +x "$destination/$name"
}

install_release FYLO "${FYLO_VERSION:-latest}"
install_release SESAME "${SESAME_VERSION:-latest}"
install_release CHEX "${CHEX_VERSION:-latest}"
install_release TACHYON "${TACHYON_VERSION:-latest}"

# sops (with age) backs the sops/ secret scheme. Optional at runtime — a
# deployment that never writes a ${secret:sops:...} reference never needs
# it — so absence is a warning here and a skip in the tests that use it.
if ! command -v sops >/dev/null 2>&1; then
  echo "warning: sops is not installed; ${SOPS_HINT:-brew install sops age (or your package manager)} enables sops/ secret references" >&2
fi

export PATH="$destination:$PATH"
if [ -n "${GITHUB_PATH:-}" ]; then
  echo "$destination" >> "$GITHUB_PATH"
fi

# Fail here rather than inside a test, where a missing binary shows up as a
# skip and a green build that proved nothing.
fylo version
sesame version
chex --help > /dev/null

echo "fylo, sesame, and chex are installed in $destination"
