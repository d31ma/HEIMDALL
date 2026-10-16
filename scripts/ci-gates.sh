#!/usr/bin/env bash
# CI gates that fail the build. These check properties no compiler and no unit
# test can: that whole categories of code are absent from the repository.
#
# Run locally with: scripts/ci-gates.sh
set -euo pipefail

cd "$(dirname "$0")/.."

failures=0

fail() {
  printf '\033[31mFAIL\033[0m %s\n' "$1" >&2
  failures=$((failures + 1))
}

pass() {
  printf '\033[32mok\033[0m   %s\n' "$1"
}

# Source under gate: first-party Go only. Vendored client shims carry their
# upstream's code and are exempt, and test files legitimately mention the words
# these gates search for.
first_party_go() {
  find app -name '*.go' \
    -not -name '*_test.go' \
    -not -path 'app/node_modules/*' \
    -not -path 'app/dist/*' \
    -not -path 'app/tests/*' \
    -not -path 'app/server/repositories/store/fylo/*' \
    -not -path 'app/server/helpers/schema/chex/*'
}

go_has_package() {
  grep -Eq '^[[:space:]]*package[[:space:]]+[[:alnum:]_]+' "$1"
}

yon_source_go() {
  local source_file
  while IFS= read -r source_file; do
    if ! go_has_package "$source_file"; then
      printf '%s\n' "$source_file"
    fi
  done < <(find app/server -maxdepth 2 -type f -name '*.go' -not -name '*_test.go')
}

application_boundary_files() {
  find app/client website/client -type f 2>/dev/null || true
  yon_source_go
}

source_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{ print $1 }'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{ print $1 }'
    return
  fi
  printf '%s\n' 'CHECKSUM_TOOL_UNAVAILABLE'
}

website_storage_hits() {
  local preference_file='website/client/shared/utils/ThemePreferenceUtils.js'
  local preference_sha='c4edc2fb074b0613300722de3c130571c9f85686539b68ef53e22ced4a50791b'
  local writer_file='website/client/services/ThemeService.js'
  local writer_sha='5dc2a81beb40ac5bd855d4076a9e0886f9c7e414d4a22491ba12fda3d42dd368'
  local site_file
  if [ -f "$preference_file" ] && [ "$(source_sha256 "$preference_file")" != "$preference_sha" ]; then
    printf '%s\n' 'the public website theme preference helper differs from its audited hd-site-theme contract'
  fi
  if [ -f "$writer_file" ] && [ "$(source_sha256 "$writer_file")" != "$writer_sha" ]; then
    printf '%s\n' 'the public website theme writer differs from its audited light/dark caller'
  fi
  while IFS= read -r site_file; do
    if grep -Eq '(localStorage|sessionStorage)[[:space:]]*[.[]' "$site_file" && [ "$site_file" != "$preference_file" ]; then
      printf '%s\n' "public website storage access is outside the audited theme preference helper: $site_file"
    fi
    if grep -Eq 'ThemePreferenceUtils[[:space:]]*[.][[:space:]]*write[[:space:]]*\(' "$site_file" && [ "$site_file" != "$writer_file" ]; then
      printf '%s\n' "a new public website theme storage writer is unaudited: $site_file"
    fi
  done < <(find website/client -type f 2>/dev/null || true)
}

application_session_files() {
  find app/client -type f 2>/dev/null || true
  yon_source_go
}

application_boundary_hits() {
  local expression="$1"
  application_boundary_files | xargs grep -nE "$expression" 2>/dev/null || true
}

# ---------------------------------------------------------------------------
# Gate 1 — no authentication or authorization logic.
#
# SESAME decides. A handler that hashes a password or compares a role name has
# created a second authority over security state, which is the one thing this
# codebase may never contain.
# ---------------------------------------------------------------------------
crypto_hits=$(first_party_go | xargs grep -nE \
  'argon2|bcrypt|scrypt|pbkdf2|golang\.org/x/crypto/(argon2|bcrypt|scrypt|pbkdf2)' \
  || true)
if [ -n "$crypto_hits" ]; then
  fail "password hashing found in the control plane; SESAME owns credentials"
  printf '%s\n' "$crypto_hits" >&2
else
  pass "no password hashing"
fi

role_hits=$(first_party_go | xargs grep -nE \
  '(==|!=)[[:space:]]*"(viewer|operator|admin|owner|superuser|root)"|"(viewer|operator|admin|owner)"[[:space:]]*(==|!=)' \
  || true)
# The role bundle table names the four roles as data, which is the one legal
# mention. Anything comparing them is a decision made outside SESAME.
if [ -n "$role_hits" ]; then
  fail "a role name is compared in code; roles are grant bundles, never branches"
  printf '%s\n' "$role_hits" >&2
else
  pass "no role-name comparison"
fi

jwt_hits=$(first_party_go | xargs grep -nE \
  'jwt\.|golang-jwt|SignedString|ParseWithClaims|session_secret[[:space:]]*=' \
  || true)
if [ -n "$jwt_hits" ]; then
  fail "token minting or parsing found; SESAME issues and verifies every token"
  printf '%s\n' "$jwt_hits" >&2
else
  pass "no token minting"
fi

# ---------------------------------------------------------------------------
# Gate 2 — no secret values in anything persisted or committed.
#
# The corpus and any golden fixtures must carry ${secret:...} references only.
# ---------------------------------------------------------------------------
secret_hits=$(grep -rnE \
  'AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----|xox[baprs]-[0-9A-Za-z-]{10,}|ghp_[0-9A-Za-z]{36}' \
  testdata schemas api 2>/dev/null || true)
if [ -n "$secret_hits" ]; then
  fail "a literal credential is present in committed fixtures"
  printf '%s\n' "$secret_hits" >&2
else
  pass "no literal credentials in fixtures"
fi

# ---------------------------------------------------------------------------
# Gate 3 — no cloud SDK outside an adapter or the secret resolver.
#
# Adapters are the only code that knows a cloud *runtime* exists, and
# app/server/helpers/secrets is the one place that knows cloud *secret stores* exist —
# the Phase 5 deliverable put it there deliberately. Everything else stays
# cloud-blind. Enforced from Phase 0 so it is never retrofitted across a
# codebase that already leaked.
# ---------------------------------------------------------------------------
sdk_hits=$(first_party_go \
  | grep -v '^app/server/services/provider/' \
  | grep -v '^app/server/helpers/secrets/' \
  | xargs grep -nE 'aws-sdk-go|Azure/azure-sdk|cloud\.google\.com/go|google\.golang\.org/api' 2>/dev/null \
  || true)
if [ -n "$sdk_hits" ]; then
  fail "a cloud SDK is imported outside app/server/services/provider/ or app/server/helpers/secrets/"
  printf '%s\n' "$sdk_hits" >&2
else
  pass "no cloud SDK outside adapters"
fi

# ---------------------------------------------------------------------------
# Gate 4 — no deprecated compose→cloud integration.
# ---------------------------------------------------------------------------
deprecated_hits=$(first_party_go | xargs grep -nE 'docker compose up.*(ecs|aci)|docker-compose-ecs|compose-cli' || true)
if [ -n "$deprecated_hits" ]; then
  fail "Docker's deprecated compose→ECS/ACI integration is referenced"
  printf '%s\n' "$deprecated_hits" >&2
else
  pass "no deprecated compose→cloud integration"
fi

# ---------------------------------------------------------------------------
# Gate 5 — no subprocess invoked through a shell.
# ---------------------------------------------------------------------------
shell_hits=$(first_party_go | xargs grep -nE 'exec\.Command\("(sh|bash|zsh|cmd|powershell)"' || true)
if [ -n "$shell_hits" ]; then
  fail "a subprocess is invoked through a shell"
  printf '%s\n' "$shell_hits" >&2
else
  pass "no shell-invoked subprocesses"
fi

# ---------------------------------------------------------------------------
# Gate 6 — no dumping-ground packages.
# ---------------------------------------------------------------------------
dumping=$(find app/server -type d \
  \( -name utils -o -name helpers -o -name common -o -name shared -o -name misc -o -name base -o -name manager \) \
  -not -path 'app/server/helpers' \
  -not -path 'app/server/utils' \
  || true)
if [ -n "$dumping" ]; then
  fail "a dumping-ground package exists"
  printf '%s\n' "$dumping" >&2
else
  pass "no utils/helpers/common/shared/misc/base/manager packages"
fi

# ---------------------------------------------------------------------------
# Gate 7 — browsers and the Yon BFF hold no authority.
#
# Tachyon attaches a session and forwards. A role name, a permission check, or
# a decision about what a user may do would be a second authority over a
# question SESAME already answered (ADR 0009).
# ---------------------------------------------------------------------------
if [ -d app ]; then
  web_authz=$(application_boundary_hits \
    '(==|!=)[[:space:]]*.(viewer|operator|admin|owner).|\bcanSync\b|\bhasPermission\b|\bisAdmin\b' \
    )
  if [ -n "$web_authz" ]; then
    fail "the browser or Yon BFF is making an authorization decision"
    printf '%s\n' "$web_authz" >&2
  else
    pass "no authorization logic in browsers or the Yon BFF"
  fi

  # The session must never be readable by script — a token in web storage is a
  # credential an injected script can exfiltrate. Matching a member access
  # rather than the bare word keeps the gate from firing on a comment that
  # explains the rule, which is how the first version of it failed.
  web_storage=$(application_session_files | xargs grep -nE '(localStorage|sessionStorage)[[:space:]]*[.[]' 2>/dev/null || true)
  web_storage+="$(website_storage_hits)"
  if [ -n "$web_storage" ]; then
    fail "the browser or Yon BFF stores something in web storage; the session belongs in an httpOnly cookie"
    printf '%s\n' "$web_storage" >&2
  else
    pass "no browser-readable session storage"
  fi

  # TLS verification must never be skippable.
  web_tls=$(application_boundary_hits 'rejectUnauthorized[[:space:]]*:[[:space:]]*false|NODE_TLS_REJECT_UNAUTHORIZED|InsecureSkipVerify[[:space:]]*:[[:space:]]*true' \
    )
  if [ -n "$web_tls" ]; then
    fail "the browser or Yon BFF can skip TLS verification"
    printf '%s\n' "$web_tls" >&2
  else
    pass "no way to skip TLS verification"
  fi
fi

check_native_test_source() {
  local test_file="$1"
  local source_folder="app/server/${test_file#tests/server/}"
  local source_file source_package test_package
  case "$test_file" in
    *_test.go) ;;
    *) return 1 ;;
  esac
  source_folder="${source_folder%/*}"
  if ! go_has_package "$test_file"; then
    return 1
  fi
  test_package=$(sed -nE 's/^[[:space:]]*package[[:space:]]+([[:alnum:]_]+).*/\1/p' "$test_file" | head -n 1)
  while IFS= read -r source_file; do
    source_package=$(sed -nE 's/^[[:space:]]*package[[:space:]]+([[:alnum:]_]+).*/\1/p' "$source_file" | head -n 1)
    if [ -n "$source_package" ] && { [ "$test_package" = "$source_package" ] || [ "$test_package" = "${source_package}_test" ]; }; then
      return 0
    fi
  done < <(find "$source_folder" -maxdepth 1 -type f -name '*.go' 2>/dev/null || true)
  return 1
}

check_all_web_layout() {
  local layout_failure=0
  local source_file forbidden_path legacy_imports
  for forbidden_path in internal server cmd web go.mod go.sum; do
    if [ -e "$forbidden_path" ] || [ -L "$forbidden_path" ]; then
      fail "root $forbidden_path is not a supported HEIMDALL source location; backend code belongs under app/server"
      layout_failure=1
    fi
  done
  while IFS= read -r source_file; do
    fail "Go application source is outside app: $source_file"
    layout_failure=1
  done < <(find . \( -path './app' -o -path './testdata' -o -path './tests/server' -o -path './.git' -o -path './.agents' -o -path './.codex' -o -path './.aws' \) \
    -prune -o -type f -name '*.go' -print)

  while IFS= read -r source_file; do
    fail "native Go tests belong under sibling tests/server, not production app/server: $source_file"
    layout_failure=1
  done < <(find app/server -type f -name '*_test.go')
  if [ -e app/tests ] || [ -L app/tests ]; then
    fail "application tests belong under sibling tests/app, not app/tests"
    layout_failure=1
  fi
  while IFS= read -r source_file; do
    if ! check_native_test_source "$source_file"; then
      fail "native test source must mirror a production package and declare its package label: $source_file"
      layout_failure=1
    fi
  done < <(find tests/server -type f -name '*.go' 2>/dev/null || true)

  while IFS= read -r source_file; do
    case "${source_file##*/}" in
      *Controller.go) ;;
      *) fail "Yon controller files must end with Controller.go: $source_file"; layout_failure=1 ;;
    esac
  done < <(find app/server/controllers -maxdepth 1 -type f -name '*.go')

  while IFS= read -r source_file; do
    if go_has_package "$source_file"; then
      case "$source_file" in
        app/server/commands/heimdall/*|app/server/commands/fake-engine/*|app/server/services/api/*|\
        app/server/services/agent/*|app/server/services/auth/*|app/server/services/dispatch/*|app/server/services/enroll/*|\
        app/server/services/observe/*|app/server/services/provider/*|app/server/services/reconcile/*|app/server/services/registry/*|app/server/services/render/*|\
        app/server/utils/cron/*|app/server/utils/diff/*|app/server/utils/sequence/*|\
        app/server/helpers/git/*|app/server/helpers/schema/*|app/server/helpers/secrets/*|\
        app/server/models/spec/*|app/server/repositories/store/*) ;;
        *) fail "native Go package is outside its authorized app/server role and domain: $source_file"; layout_failure=1 ;;
      esac
    else
      case "${source_file%/*}" in
        app/server/controllers|app/server/services|app/server/helpers|app/server/models) ;;
        *) fail "Yon fragment is outside immediate controller/service/helper/model roles: $source_file"; layout_failure=1 ;;
      esac
    fi
  done < <(find app -type f -name '*.go' -not -name '*_test.go' \
    -not -path 'app/tests/*' -not -path 'app/node_modules/*' -not -path 'app/dist/*')

  legacy_imports=$(grep -rnE \
    '^[[:space:]]*(import[[:space:]]+)?([[:alnum:]_.]+[[:space:]]+)?"github\.com/d31ma/heimdall/(internal|server)(/|")' \
    app/server tests/server --include='*.go' 2>/dev/null || true)
  if [ -n "$legacy_imports" ]; then
    fail "application source imports a legacy root internal/server package path"
    printf '%s\n' "$legacy_imports" >&2
    layout_failure=1
  fi
  if [ "$layout_failure" -eq 0 ]; then
    pass "all backend code occupies app/server roles with explicit native Go/Yon boundaries"
  fi
}

check_all_web_layout

check_web_backend() {
  local web_layout_failure=0
  local web_file web_authority_imports web_data_logic
  if [ ! -d app ]; then
    return
  fi
  if [ -e app/internal ] || [ -L app/internal ]; then
    fail "app/internal is not a supported production backend location"
    web_layout_failure=1
  fi

  if [ -e app/server/shared ] || [ -L app/server/shared ]; then
    fail "app/server/shared is not a supported production backend location; data types belong in models"
    web_layout_failure=1
  fi

  while IFS= read -r web_file; do
    case "$web_file" in
      app/playwright.config.js|app/tachyon-behaviors.d.ts|app/tachyon-env.d.ts|website/playwright.config.js|website/tachyon-env.d.ts|website/tachyon-behaviors.d.ts) ;;
      *) fail "web executable JavaScript/TypeScript belongs in client or test tooling: $web_file"; web_layout_failure=1 ;;
    esac
  done < <(find app website -type f \( -name '*.js' -o -name '*.ts' -o -name '*.mjs' -o -name '*.cjs' \) \
    -not -path 'app/client/*' -not -path 'app/tests/*' -not -path 'app/scripts/*' \
    -not -path 'website/client/*' -not -path 'website/tests/*' -not -path 'website/scripts/*' \
    -not -path 'app/node_modules/*' -not -path 'app/dist/*')

  while IFS= read -r web_file; do
    case "$web_file" in
      app/server/go.mod|app/server/go.sum) ;;
      *) fail "a second application Go module exists: $web_file"; web_layout_failure=1 ;;
    esac
  done < <(find app website tests -type f \( -name go.mod -o -name go.sum \) \
    -not -path 'app/node_modules/*' -not -path 'app/dist/*')

  web_authority_imports=$(application_boundary_hits \
    'github\.com/d31ma/heimdall/(internal|server)(/|")|github\.com/d31ma/heimdall/app/server/|github\.com/d31ma/(sesame|fylo)')
  if [ -n "$web_authority_imports" ]; then
    fail "the browser or Yon BFF imports native control-plane, SESAME, or FYLO implementation"
    printf '%s\n' "$web_authority_imports" >&2
    web_layout_failure=1
  fi

  web_data_logic=$(yon_source_go | grep '^app/server/models/' | \
    xargs grep -nE '^[[:space:]]*func[[:space:]]|^[[:space:]]*var[[:space:]]|^[[:space:]]*const[[:space:]]' 2>/dev/null || true)
  while IFS= read -r web_file; do
    if awk '
      /^[[:space:]]*import[[:space:]]*\(/ { importing = 1; next }
      importing && /^[[:space:]]*\)/ { importing = 0; next }
      importing || /^[[:space:]]*import[[:space:]]+/ {
        if (match($0, /"[^"]+"/)) {
          name = substr($0, RSTART + 1, RLENGTH - 2); split(name, parts, "/");
          if (parts[1] ~ /\./) { found = 1 }
        }
      }
      END { exit !found }
    ' "$web_file"; then
      fail "a Yon BFF fragment imports a non-standard-library package: $web_file"
      web_layout_failure=1
    fi
  done < <(yon_source_go)
  if [ -n "$web_data_logic" ]; then
    fail "Yon BFF models contain executable code or global state"
    printf '%s\n' "$web_data_logic" >&2
    web_layout_failure=1
  fi
  if [ "$web_layout_failure" -eq 0 ]; then
    pass "browser and immediate Yon BFF source preserve their forwarding-only authority boundary"
  fi
}

check_web_backend

if [ "$failures" -gt 0 ]; then
  printf '\n%d gate(s) failed\n' "$failures" >&2
  exit 1
fi

printf '\nall gates passed\n'
