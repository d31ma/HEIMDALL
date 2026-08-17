# Phase 2 evidence — GitOps completeness

What the phase claimed, what is true, and how to check it.

## Exit criteria

> Kill a container out-of-band → self-heal restores it. Rollback to `HEAD~3`
> succeeds without touching git. A tag change moves a host between groups with
> no edit to either group.

| Criterion | State | Evidence |
|---|---|---|
| Self-heal restores an out-of-band kill | **Proven** | `TestSelfHealRestoresAnOutOfBandRemoval` — the loop restores it and the operation records `self_heal` with no principal |
| Rollback without touching git | **Proven in Phase 1** | `TestRollbackReAppliesAStoredRevision`; a force-push cannot change what rolling back means |
| A tag change moves a host between groups | **Proven** | `TestGroupMembershipIsDerivedFromTags` — membership is derived on read, never stored |

## What was built

- **Auto-sync and self-heal as one loop** (`internal/reconcile/auto.go`).
  `Status` refreshes git and reads live state, so a new commit and a killed
  container arrive as the same OutOfSync observation; the two policy flags
  only decide which of them an application consented to. Suspend beats every
  policy. An automated sync records its reason instead of attributing itself
  to a principal who was not there.
- **Target groups** as named tag selectors, membership derived per read. An
  empty selector matches nothing — a group accidentally covering the whole
  fleet is a worse failure than one covering none of it.
- **Webhook receiver** (`POST /api/v1/webhooks/{repo}`): HMAC over the raw
  body, forge-agnostic because the payload is never parsed — a push means
  "look now", nothing more. An unknown repository and a bad signature are
  byte-identical, so ids cannot be enumerated.
- **Device authorization** (RFC 8628) and the introspection boundary, proven
  live end to end; see CHANGELOG and `TestDeviceAuthorizationEndToEnd`.

## Not done in Phase 2

- **Sync waves and hooks.** No caller yet; the plan's exit criteria never
  exercised them.
- **Tag selector expressions.** Equality only, by design, until a real group
  needs `region in (...)`.
