# ADR 0008 — Agents are outbound-only, and are not principals

**Status:** accepted (Phase 1)

## Context

A Docker host that HEIMDALL deploys to is usually not reachable from the
control plane: it sits behind NAT, in a branch office, or on a network whose
owner will not open a port. The obvious design — the control plane connects to
an agent — fails on exactly the hosts the product is for.

Inverting it raises two questions that are easy to answer badly. How does the
control plane give work to something it cannot dial? And what authority does
an agent hold?

## Decision

**The agent connects out and asks for work.** It opens no port. It long-polls
`GET /api/v1/agent/work`, which parks for up to 90 seconds and returns 204 when
there is nothing to do. That empty round trip is also the heartbeat: it keeps
the connection warm through proxies and NAT tables without a second protocol
that could disagree with the first.

**An agent is not a principal.** It holds no grant and can ask for nothing. It
receives work that an already-authorized human sync produced, for exactly the
one target its client certificate names. The authorization decision was made
when the sync was requested; asking SESAME again at the agent boundary would
be a second decision about a request nobody made. This is why the three agent
routes sit outside the SESAME boundary, and it is a reasoned position rather
than an exception carved out for convenience.

What stands in its place is mTLS. `enroll.IdentityOf` reads the target from a
verified client certificate and reads it from `VerifiedChains` only, never
from `PeerCertificates` — the latter holds what the client sent, verified or
not, and trusting it would accept any self-signed certificate.

**The target comes from the certificate, never from the request.** An agent
that could name its own target could poll for another host's work, and results
are checked the same way on the way back.

**The agent plans locally.** A sync sends the desired spec, not a plan
computed elsewhere: live state may have moved since, and the agent is the only
process that can see it.

**A remote target is a `provider.Provider`.** `dispatch.Remote` implements the
same interface the Docker adapter does, so the reconciler never branches on
"is this behind an agent". Refresh, plan, diff, sync, rollback, and the
operation document are one code path, not two that drift.

## Consequences

- **Secret values travel to the agent**, over mTLS, in a job. That is the one
  place values leave the control plane, and it is the lesser evil: the
  alternative is every host holding secret-manager credentials of its own.
  Both ends keep them in memory only; a job is never persisted. ADR 0006's
  rule — a reference in every persisted document — still holds.
- **Capability rejection happens control-plane side**, using the local
  adapter's answer. Telling an operator their compose file is unsupported must
  not require a reachable host.
- **An offline target fails the sync immediately**, with when the agent was
  last seen. Durable pending actions — a TTL, a bounded depth per target, and
  superseded jobs collapsing to the newest — are a Phase 3 deliverable.
  Building half of one now would be a queue with none of the guarantees.
- **Metrics, logs, and events over an agent are refused with a clear message**
  rather than returning empty. An empty series reads as "this container is
  doing nothing", which during an incident is worse than an error. They arrive
  in Phase 3 with the agent's stats and log channels.
- **The control plane must serve TLS**, because enrollment pins a certificate
  fingerprint and there is no fingerprint without one. `heimdall init`
  generates the certificate and the agent CA; `HD_TLS=false` exists only for a
  local loop behind a terminating proxy.
