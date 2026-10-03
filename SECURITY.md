# Security

## Reporting a vulnerability

Report privately through the repository's security advisory form. Do not open a
public issue. Include a reproduction, the affected version, and what an
attacker gains. You will get an acknowledgement within three working days.

## Supported versions

**None.** HEIMDALL is pre-release and no version is claimed supported for
production use. Its GA gate is downstream of SESAME's, which is itself
developer preview: no 72-hour native gate passed, no independent security
review, not OpenID certified, and SAML proven only against a pinned Keycloak
26.0.

## Design commitments

These are properties the codebase is built to hold, and where each is enforced.

**No authentication or authorization logic in HEIMDALL.** No password hashing,
no session table, no token minting, no role comparison. SESAME decides.
Enforced by `scripts/ci-gates.sh`, which is itself tested to fire.

**Fail closed.** A decision that errors, times out, or returns an unrecognised
code is a deny. An engine that does not answer is a `503`, never a bypass.
Proven by `TestStoppedEngineGets503AndNeverBypasses` against a real engine.

**Decide once, at the boundary.** Middleware maps route to action to resource
before the handler runs. No second check, and no check inside a loop.

**Secrets are references, never values.** `${secret:name}` and
`${secret:vault/path#key}` resolve at apply time, in process. No value enters a
revision, a plan, a diff, a log, or any persisted document. No route returns a
secret value, and the action vocabulary contains `secret:bind` with no
corresponding read verb.

**Key material lives outside every FYLO root.** SESAME's ES256 signing key,
sealed-secrets key, and snapshot key are `0600` under `<deployment>/sesame/keys`.
A FYLO snapshot alone restores a control plane that cannot verify any session,
so backup covers the deployment directory or it covers nothing.

**Identifiers are not secrets.** TTIDs appear in URLs. They are not
capabilities and must never be treated as ones.

**Credentials come from the environment, never from flags**, so they stay out
of shell history and `ps` output.

**Render runs with no shell**, no `!!python` tags, bounded interpolation depth,
and CHEX validation on both input and output.

**Agent to control plane is mTLS, agent-initiated, outbound only.** No inbound
port is opened on a customer host.

## Threat model

`docs/THREAT_MODEL.md` is a Phase 6 deliverable, landing with the external
security review package.
