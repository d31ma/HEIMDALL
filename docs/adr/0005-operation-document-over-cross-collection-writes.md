# ADR 0005 — One operation document, projections folded from it

**Status:** accepted (Phase 0)

## Context

FYLO's writes are atomic within one collection and only one. A sync that
wanted to update sync history, live state, the event stream, and the audit log
together cannot do so atomically. A crash between those writes leaves four
collections disagreeing about what happened, and nothing that can adjudicate.

SESAME solves the same problem the same way — a hash-chained ledger with
rebuildable projections — so the pattern is already proven in-family.

## Decision

A sync is **one document** in `hd-operations` holding its whole state machine:
target revision, plan, per-resource outcomes, timestamps, principal, and
`policy_version`. Every write during a sync is a patch to that single
document, so it is atomic by construction.

`hd-livestate`, `hd-events`, and `hd-rollups` are **projections**, folded from
`hd-operations` and never independently authoritative. `store.Rebuild` drops
them and replays the operation history through a `Fold`.

Rebuild folds in ascending document-id order. TTIDs are time-ordered, so that
is chronological order — and it is what makes two rebuilds of the same history
produce the same projection rather than merely the same set. "Byte-for-byte
reproducible" is not achievable from an unordered fold.

## Consequences

- A crash mid-sync leaves one document in a known, resumable state instead of
  four in disagreement.
- `store.Authoritative` and `store.Projections` are disjoint, and a test
  enforces it: `Rebuild` drops projections, so a mislabelled authoritative
  collection would be deleted.
- The crash-safety gate is: kill `heimdall serve` mid-sync, restart, assert
  `hd-operations` holds one resumable document and that rebuilding reproduces
  the pre-crash projection exactly. It lands with Phase 1, when an operation
  first has a shape; Phase 0 fixes the `Fold` contract it depends on.
- `hd-audit` is authoritative rather than a projection: it is append-only and
  hash-chained, and an audit log you can regenerate is an audit log you can
  rewrite.
