# Phase 5 evidence — Enterprise controls

## Exit criterion

> An Okta or Entra ID group grants `operator` on a project through SCIM
> alone, with no HEIMDALL-side user administration.

**Proven**, against the real engine:
`TestSCIMGroupGrantsOperatorThroughProvisioningAlone` registers a SCIM
provisioning client, pushes a user and a group through `/scim/v2/*` exactly
as an IdP does, and asserts the provisioned principal may `app:sync` on the
mapped project, may not on any other project, and did not receive admin
actions. The one HEIMDALL-side act is the administrator declaring the
mapping — which is authorization shape, not user administration.

## What was built

- **The SCIM 2.0 host** (`/scim/v2/Users`, `/scim/v2/Groups`). Bodies pass
  through to SESAME verbatim — a host that "understood" SCIM would be a
  second implementation of a spec the engine already implements. The IdP's
  bearer token is authenticated by SESAME on every call; refusals are
  uniform.
- **Group→role mappings** (`hd-group-mappings`): a directory group's
  displayName maps to a shipped role bundle scoped to one project's subtree
  (`project:alpha:*`). Enforcement happens as group documents flow past;
  half-failed attempts resume without minting duplicate roles.
- **Secret-manager integrations** (`internal/secrets`): `local/` (AES-256-GCM
  sealed files beside the TLS keys), `aws-sm/`, `azure-kv/`, `gcp-sm/`.
  Unknown schemes are refused with directions — a typo that silently resolved
  from the wrong store would be a wrong-environment secret in a production
  container. `heimdall secret set` reads the value from env or stdin, never
  argv.
- **Outbound webhooks** (`hd-outbound-webhooks`): completed operations POST
  to subscribers, HMAC-signed in the GitHub header shape so existing receiver
  libraries verify them. Fire-and-forget with a bounded timeout — a slow
  subscriber must never slow a deploy.
- **Correlated audit export** (`GET /api/v1/audit/export`): both ledgers as
  one NDJSON stream, each line tagged, correlated by principal and policy
  version.
- **Device authorization** (RFC 8628) landed earlier and is part of this
  phase's surface.

## Engine constraints surfaced

- SESAME's group and role name alphabets have no spaces or `@`. A directory
  group named "Platform Operators" must be pushed as `platform-operators` —
  every IdP's outbound attribute mapping does this. Scoped role names join
  with dashes.
- SESAME offers no role lookup by name (deliberately — roles are immutable).
  Mappings therefore remember the role id they created.

## Deferred, with reasons

- **Inbound OIDC / SAML login flows and passkeys.** SESAME implements the
  entire protocol surface (provider registration, login start/complete,
  passkey ceremonies); what remains is HEIMDALL's host layer: browser
  redirects, the OIDC token fetch the engine deliberately does not perform,
  and the WebAuthn ceremony in the web tier. Deferred until a deployment
  federates — the SCIM exit criterion did not require them, and a login flow
  built without an IdP to test against would be exactly the untested-security
  code this project refuses to ship.
