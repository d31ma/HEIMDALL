# Threat model

What HEIMDALL trusts, what it refuses to trust, and where the boundaries sit.
Each mitigation names the mechanism, most of which have tests.

## Assets

1. **The ability to deploy** — a sync runs attacker-chosen containers on
   every target it reaches. This is the crown jewel.
2. Secret values (resolved at apply time), registry credentials, the agent
   CA key, the OIDC client secret, SESAME's key material.
3. The audit ledgers (integrity, not confidentiality).

## Trust boundaries

| Boundary | Mechanism |
|---|---|
| Browser → web tier | httpOnly SameSite=Strict session cookie; the page never holds a credential |
| Web tier → control plane | Bearer session over TLS; the gateway's path allowlist prevents open relay |
| CLI → control plane | Session pair or OAuth access token, both verified by SESAME per request |
| Agent → control plane | mTLS, target identity read from VerifiedChains only; enrollment pinned by certificate fingerprint |
| Forge → control plane | HMAC over the raw webhook body; payload never parsed |
| IdP → control plane | SCIM bearer token, authenticated by SESAME per call |
| Control plane → SESAME/FYLO | Child processes over stdin/stdout; no port exists to attack |

## Attacker stories and answers

- **Steal a session from the browser.** The cookie is httpOnly; injected
  script cannot read it. CSRF cannot carry it cross-site (SameSite=Strict).
- **Enroll a rogue agent.** Enrollment needs a token minted by an authorized
  operator; the token embeds the control plane's certificate fingerprint, so
  the first connection cannot be intercepted; tokens are short-lived and
  target-bound.
- **A compromised agent attacks the fleet.** An agent is not a principal: it
  can only receive work for the one target its certificate names, report
  results for jobs of that target, and ship metrics rollups that are dropped
  unless they name applications on that target (tested). It cannot read
  another host's secrets: values travel only inside that target's own jobs.
- **A compromised forge triggers deployments.** A webhook is only a nudge —
  "look at git now". The attacker needs to move git itself, which is outside
  HEIMDALL's boundary and inside signed-commit verification
  (`require_signature`) when enabled.
- **Guess device or user codes.** SESAME bounds attempts and collapses all
  terminal poll failures to one answer; the approval page requires a live
  session and warns about phished codes.
- **Read secrets from the store.** No route returns a secret value; there is
  no `secret:read` action in the vocabulary (tested); persisted documents
  carry references only (tested); local values are sealed with AES-256-GCM.
- **Rewrite history.** `hd-audit` is authoritative and never rebuilt — an
  audit log you can regenerate is one you can rewrite (ADR 0005). Export is a
  read-only stream.
- **Starve the API.** Token-bucket rate limiting per bearer/IP; health
  endpoints exempt so load balancers never eject a busy instance.
- **SSRF through the gateway or webhooks.** The web-tier gateway allowlists
  method and path shape (tested); outbound webhook URLs are operator-declared
  standing config behind an authorized route.

## Residual risks, accepted and named

- The control plane host itself: root on that machine owns everything the
  deployment directory holds. Mitigation is ops hygiene, not code.
- Outbound webhook subscribers receive operation metadata; subscribing is an
  authorized act and payloads carry no secret values.
- The `local/` seal key lives beside the sealed files; it protects against
  backup and disk exposure, not against a live root compromise. Cloud stores
  carry their own IAM.
- Availability of clouds and forges is inherited, bounded by timeouts
  everywhere.
