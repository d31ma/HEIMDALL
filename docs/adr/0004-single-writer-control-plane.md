# ADR 0004 — The control plane is single-writer, end to end

**Status:** accepted (Phase 0)

## Context

FYLO is a local filesystem engine. One host process owns one engine and one
root; active-active is explicitly blocked on coordination semantics FYLO does
not yet offer. SESAME stores its state in FYLO and inherits the same ceiling.

Phase 0 discovered the constraint is stricter than the plan assumed. FYLO
takes an **exclusive lock per root**: a second engine on the same root fails
the handshake with `EROOTLOCKED`. The plan's "SESAME and HEIMDALL share one
FYLO root" is therefore not achievable at all, not merely inadvisable.

## Decision

One writer, everywhere:

- One `heimdall serve` process is the leader. Reconcile concurrency comes from
  FYLO queue consumers inside that process, never from more control planes.
- HEIMDALL keeps **its own FYLO root** at `<deployment>/fylo-root`, beside
  SESAME's at `<deployment>/sesame/fylo-root`. Collections stay namespaced
  `hd-*` (SESAME uses `sesame-*`) so the two remain distinguishable in a
  combined export and so a future FYLO that permits a shared root needs no
  migration.
- The **deployment directory** is the backup and restore unit — both roots
  plus `sesame/keys/`. The keys live `0600` outside every FYLO root, so a root
  snapshot alone restores a control plane that cannot verify any session.
  Backing up one without the other is the DR failure this bullet exists to
  prevent.
- FYLO roots must be on **block storage** (EBS, Persistent Disk, Azure Disk).
  EFS, Azure Files, and NFS are excluded by FYLO's own locking rules.
  `store.Open` refuses a root under a known file-sync tree outright, because
  the failure mode is silent corruption discovered much later.
- HA is active/passive with a lease document, not active/active.

## Consequences

- Horizontal write scale, if ever needed, is sharding by project: one FYLO
  root and one SESAME engine per project. The constraint is at least
  consistent across the stack and lifts for both at once when FYLO ships
  replication.
- Collection names use `-`, not `_`. FYLO's `validate_collection_name` accepts
  lowercase alphanumerics and `-` only, so the plan's `hd_projects` is
  rejected by the engine.
- Designed in from Phase 0. Retrofitting single-writer assumptions onto a
  codebase that assumed otherwise is the expensive version of this decision.
