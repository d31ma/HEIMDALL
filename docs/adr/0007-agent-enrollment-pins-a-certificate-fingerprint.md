# ADR 0007 — Agent enrollment pins a certificate fingerprint

**Status:** accepted (Phase 1)

## Context

A `heimdall agent` connects outbound to the control plane, so no inbound port
is opened on a customer host. That is the right shape, but it leaves one
unprotected moment: the agent's very first connection, when it has a URL and
nothing else.

A plain bearer token does not fix it. An attacker who intercepts the first
connection is handed the token and can replay it. TLS chain validation does not
fix it either — a control plane on a private network is typically self-signed,
so there is no public CA chain to validate against, and operators end up
disabling verification entirely.

Portainer's edge key gets this right: it encodes the server URL, the tunnel
address, the endpoint id, **and the server's fingerprint**.

## Decision

The enrollment token carries the control plane's certificate fingerprint, and
the agent pins it before it will send anything.

The token is a single opaque string — one thing for an operator to copy, with
nothing to get wrong — holding a version, the control-plane URL, the target
id, the SHA-256 of the control plane's TLS certificate, an expiry, and an HMAC
over all of it.

- **Pin before disclose.** `PinnedTLSConfig` disables chain validation and
  replaces it: the presented leaf must hash to the pinned fingerprint. On a
  private network that is stronger than PKI, not weaker — a public chain proves
  nothing there, and the pin proves exactly the right thing.
- **Bound the window.** A token that never expires is a permanent credential
  sitting in someone's terminal history. One hour by default.
- **Verify without a lookup.** The HMAC is over the token's own fields, so
  enrollment does not depend on a store read and works during startup.
- **One refusal.** Expired, forged, and minted-for-another-control-plane all
  return an identical error. The caller is unauthenticated; distinguishable
  refusals tell an attacker which part of a guess was right.
- **Constant-time comparison.** The signature check is reachable by an
  unauthenticated caller, so a timing-variable comparison would be a forgery
  oracle.
- **Refuse to issue without a fingerprint.** A token with no pin cannot protect
  the first connection, which is the only reason the token exists.

## Consequences

- The enrollment key joins the deployment's other key material, `0600` outside
  every FYLO root, and the DR runbook backs it up with them.
- Rotating the control plane's certificate invalidates outstanding enrollment
  tokens. That is correct: the pin is the point.
- Enrollment is testable without an agent, and is —
  `TestPinnedConnectionAcceptsTheRealServerAndRefusesAnImpostor` stands up two
  self-signed servers distinguished only by the pin.
- The agent binary itself is still outstanding. This ADR fixes the part that is
  expensive to get wrong later.
