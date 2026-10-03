# ADR 0001 — DeploySpec is the canonical model

**Status:** accepted (Phase 0)

## Context

Compose is a deployment format for Docker Engine and only a hint for the
clouds. Docker's own compose→ECS/ACI integration was deprecated in 2023 and
cannot be built on. Cloud Run has no compose support. ACA's
`az containerapp compose create` is real but partial.

Diff, rollback, drift, and audit all need something stable to compare. If they
compared compose text, then a whitespace change would read as a deployment
change and a semantically identical file written two ways would read as drift.

## Decision

Everything flows through one provider-neutral type: `spec.DeploySpec`.
Compose plus overlays plus secret references render into it; adapters consume
only it. It is CHEX-validated on the way out, canonicalized to exactly one
byte sequence, content-addressed with SHA-256, and stored immutably per
revision.

Canonicalization has one rule that drives the type's shape: **no maps**. Go
map iteration order is unstable, so a map would make the hash
non-reproducible. CHEX also cannot express a record whose values are objects,
so a map would break the schema too. Every collection is therefore a slice
sorted by an explicit key. Argv-shaped fields (`entrypoint`, `command`) are
the deliberate exception — order is meaning there and sorting them would
change what the container runs.

## Consequences

- A revision identity is a hash of meaning, not of text. Reformatting a
  compose file produces the same revision.
- `TestHashIsPinned` freezes the hash of a fixture spec. A struct edit that
  changes it fails the build, which is correct: every stored revision hash in
  every deployment would change with it. Breaking it is a deliberate
  `SchemaVersion` bump, never a drive-by.
- Adding a compose feature means adding a `DeploySpec` field, a schema entry,
  and a `Capabilities()` answer from every adapter. That friction is the point.
- A directive HEIMDALL does not model fails CHEX validation by name rather
  than being dropped. See ADR 0003's sibling reasoning on failing closed.
