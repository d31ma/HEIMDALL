# HEIMDALL domain language

The words this codebase uses, and what they mean here specifically. When code
and this file disagree, one of them is wrong — resolve it, do not paper over it.

## Core

**Application** — the unit a user manages: a repository, a path within it, a
target, a sync policy, and sync waves. HEIMDALL's analogue of ArgoCD's
`Application`. Not "an app" in the sense of a container image.

**Target** — where an application runs: a provider, a region, a credential
reference, and for Docker Engine an agent id. One application deploys to one
target.

**Revision** — a commit resolved to a rendered `DeploySpec` plus its content
hash. Immutable, and the audit spine: a rollback re-applies a stored revision
rather than rewriting git.

**DeploySpec** — the provider-neutral canonical model. Rendered from compose
plus overlays plus secret references, CHEX-validated, canonicalized to exactly
one byte sequence, content-addressed. Everything downstream reads this and not
the compose file. See ADR 0001.

**Desired state** — the `DeploySpec` at the target revision.
**Live state** — what `Observe()` reads back from the runtime.
**Drift** — a difference between the two that no sync caused.

**Plan** — the diff plus the ordered operations that would close it. Producing
one is where an unsupported directive is rejected; a plan never half-applies.

**Operation** — one document in `hd-operations` holding a sync's whole state
machine. The only authoritative record of what happened. See ADR 0005.

**Projection** — a view folded from operations: `hd-livestate`, `hd-events`,
`hd-rollups`. Rebuildable, never authoritative. `hd-audit` is deliberately not
a projection: an audit log you can regenerate is one you can rewrite.

## Status vocabulary

**Sync status** — `Synced | OutOfSync | Unknown`. About agreement between
desired and live.

**Health** — `Healthy | Progressing | Degraded | Suspended | Missing`. About
the workload itself, per service and rolled up per application.

These are independent. An application can be `Synced` and `Degraded`.

## Sync mechanics

**Sync** — apply the desired state to the target. Manual, automatic, or
selective (a chosen subset of services).

**Self-heal** — revert an out-of-band change, restoring the desired state.

**Prune** — delete a resource that is live but no longer in the desired state.
Opt-in per application, because the failure mode is deleting something real.

**Wave** — an ordering group. All services in wave *n* settle before wave *n+1*
starts.

**Hook** — a one-shot task at a phase boundary: pre-sync, post-sync, or
on-failure. Migrations and smoke tests.

## Identity

**Principal** — a SESAME identity, human or workload. HEIMDALL stores a
`principal_id` and nothing else about it.

**Action** — a verb from the closed vocabulary in `internal/auth`, such as
`app:sync`. Named by a route, never inferred from a path.

**Resource** — a hierarchical, colon-separated string such as
`project:alpha:app:checkout`. Coarse to fine, so a grant on `project:alpha:*`
covers everything in the project.

**Grant** — a role bound to a principal or group within a tenant. Roles are
grant bundles; nothing in HEIMDALL branches on a role name.

**Reason code** — SESAME's explanation for a decision, passed through
verbatim. `deny_no_grant` is not the same as a step-up requirement, and
guessing between them would be inventing authorization logic.

**Policy version** — the version of the policy snapshot a decision was made
under. Stamped into every audit record so "why was this allowed in March" is
answerable without replaying policy.

## Terms deliberately not used

**Namespace** — Kubernetes' word for a boundary. HEIMDALL has projects.

**Cluster** — ambiguous across four runtimes. Say target.

**Manifest** — implies rendered YAML applied verbatim. Say `DeploySpec`.
