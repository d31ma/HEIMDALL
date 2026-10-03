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
  find cmd internal -name '*.go' \
    -not -name '*_test.go' \
    -not -path 'internal/store/fylo/*' \
    -not -path 'internal/schema/chex/*'
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
# internal/secrets is the one place that knows cloud *secret stores* exist —
# the Phase 5 deliverable put it there deliberately. Everything else stays
# cloud-blind. Enforced from Phase 0 so it is never retrofitted across a
# codebase that already leaked.
# ---------------------------------------------------------------------------
sdk_hits=$(first_party_go \
  | grep -v '^internal/provider/' \
  | grep -v '^internal/secrets/' \
  | xargs grep -nE 'aws-sdk-go|Azure/azure-sdk|cloud\.google\.com/go|google\.golang\.org/api' 2>/dev/null \
  || true)
if [ -n "$sdk_hits" ]; then
  fail "a cloud SDK is imported outside internal/provider/"
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
dumping=$(find internal -type d \
  \( -name utils -o -name helpers -o -name common -o -name shared -o -name misc -o -name base -o -name manager \) \
  || true)
if [ -n "$dumping" ]; then
  fail "a dumping-ground package exists"
  printf '%s\n' "$dumping" >&2
else
  pass "no utils/helpers/common/shared/misc/base/manager packages"
fi

# ---------------------------------------------------------------------------
# Gate 7 — the web tier holds no authority.
#
# Tachyon attaches a session and forwards. A role name, a permission check, or
# a decision about what a user may do would be a second authority over a
# question SESAME already answered (ADR 0009).
# ---------------------------------------------------------------------------
if [ -d web ]; then
  web_authz=$(grep -rnE \
    '(==|!=)[[:space:]]*.(viewer|operator|admin|owner).|\bcanSync\b|\bhasPermission\b|\bisAdmin\b' \
    web/client web/server 2>/dev/null || true)
  if [ -n "$web_authz" ]; then
    fail "the web tier is making an authorization decision"
    printf '%s\n' "$web_authz" >&2
  else
    pass "no authorization logic in the web tier"
  fi

  # The session must never be readable by script — a token in web storage is a
  # credential an injected script can exfiltrate. Matching a member access
  # rather than the bare word keeps the gate from firing on a comment that
  # explains the rule, which is how the first version of it failed.
  web_storage=$(grep -rnE '(localStorage|sessionStorage)[[:space:]]*[.[]' \
    web/client web/server 2>/dev/null || true)
  if [ -n "$web_storage" ]; then
    fail "the web tier stores something in web storage; the session belongs in an httpOnly cookie"
    printf '%s\n' "$web_storage" >&2
  else
    pass "no browser-readable session storage"
  fi

  # TLS verification must never be skippable.
  web_tls=$(grep -rnE 'rejectUnauthorized[[:space:]]*:[[:space:]]*false|NODE_TLS_REJECT_UNAUTHORIZED' \
    web/client web/server 2>/dev/null || true)
  if [ -n "$web_tls" ]; then
    fail "the web tier can skip TLS verification"
    printf '%s\n' "$web_tls" >&2
  else
    pass "no way to skip TLS verification"
  fi
fi

if [ "$failures" -gt 0 ]; then
  printf '\n%d gate(s) failed\n' "$failures" >&2
  exit 1
fi

printf '\nall gates passed\n'
