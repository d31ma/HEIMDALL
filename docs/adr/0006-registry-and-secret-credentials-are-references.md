# ADR 0006 — Registry and secret credentials are references, resolved once

**Status:** accepted (Phase 1)

## Context

Most real workloads pull at least one private image. Portainer carries an open
bug here — *"Updating a service fails when the image is from a private
registry"* — and it is not an edge case; without private pulls the product
cannot deploy what people actually run.

The obvious implementation stores a registry username and password on the
target or the application. That puts a live credential in a document that is
listed by an API, rendered into a UI, included in a backup, and grepped by
support. `${secret:...}` already exists to avoid exactly that for environment
variables, and a second mechanism with weaker rules would undo it.

## Decision

One rule for both: **a credential is a reference in every persisted document,
and a value only inside one provider call.**

- `hd-registries` holds a server, a username, and a `password_ref`. There is no
  field that can hold a password, so there is nothing to forget to redact.
- At apply time the reference resolves in process through the configured
  secret resolver and becomes an `X-Registry-Auth` header on the pull. It is
  not returned, stored, or logged, and the resolved value does not outlive the
  request.
- A resolver that errors fails the apply. Pulling anonymously after a
  credential lookup failed turns a misconfiguration into a confusing 404 from
  the registry, hours later.
- A target-scoped registry beats a project-wide one, so a per-host credential
  is not shadowed by a default.
- Creating a registry is authorized with `secret:bind`. That is precisely what
  the verb was written for — attaching a reference — so the action vocabulary
  is unchanged and `secret:bind` finally has a route. There is deliberately no
  `secret:read`, because no route returns a value.

## Consequences

- A registry entry is safe to list, back up, and screenshot.
- The resolver is one function. Phase 1 ships one that refuses every reference
  with a message naming it, which is better than starting a container with a
  variable quietly missing; Phase 5's secret-manager integrations replace that
  function and nothing else.
- Every adapter takes a `provider.RegistryResolver` and maps it onto its own
  mechanism — a Docker header, an ECS repository credential, a Cloud Run
  image-pull service account. The resolver travels with the apply rather than
  sitting on the adapter, because one adapter serves every application.
- The runtime assertion is a test, not a promise: after a private-image
  deploy, every stored document is searched for the resolved value.
