# ADR 0002 — SESAME owns every security decision

**Status:** accepted (Phase 0)

## Context

A control plane that can deploy software is a high-value target. The usual
failure is not a missing check but a second one: a handler that re-derives a
permission, a cache that answers when the authority is down, a role name
compared in a branch.

SESAME opens no port. It is a binary the application spawns and speaks NDJSON
to, so the application owns every route. That inverts the usual identity
deployment.

## Decision

HEIMDALL contains no authentication or authorization logic. No password
hashing, no session table, no token minting, no role comparison. `heimdall
serve` supervises one SESAME child process, and every route resolves to
exactly one `authorize.decide` call in middleware before any handler runs.

Fail-closed is defined precisely, because "deny on error" is ambiguous about
which error:

| Situation | Outcome | Status |
|---|---|---|
| Engine answers `allow` | Allow | handler runs |
| Engine answers anything else | Deny | `403` + SESAME's `reason_code` |
| Engine returns a `ProtocolError` | Deny | `403` — the engine answered, and the answer is no |
| Dead pipe, timeout, closed client | Unavailable | `503` |
| Route names an action outside the vocabulary | Deny | `403` |

The 403/503 split is not cosmetic. A 403 during an engine outage sends an
operator to audit their own grants during the exact minutes they should be
restarting a process.

Only `heimdall serve` may hold an engine. A Tachyon Yon route that spawned its
own would be a second authority over shared security state, which is why
Tachyon stays a pure proxy.

## Consequences

- Resource strings use SESAME's grammar, not the plan's draft: colon-separated
  segments of lowercase alphanumerics, `-`, `_`, and `.`, with `*` legal only
  as a trailing segment. `project:alpha:app:checkout`, covered by a grant on
  `project:alpha:*`. The `/` in the plan's `project:alpha/app:checkout` is
  rejected by `ValidatePattern`, so it was never a viable form.
- The four shipped roles are seed grants, not code. `scripts/ci-gates.sh`
  fails the build if any first-party file compares a role name, hashes a
  password, or mints a token — and that gate is tested to fire.
- HEIMDALL's GA gate is downstream of SESAME's. SESAME is developer preview:
  no 72-hour native gate, no independent security review, not OpenID
  certified, SAML proven only against pinned Keycloak 26.0. The version is
  pinned exactly in CI, and Phase 6 hardening is scheduled to land with
  SESAME's production-evidence matrix rather than after it.
- Authorization tests run against a real compiled `sesame` binary and a real
  deployment. A fake would return `allow` and prove nothing.
