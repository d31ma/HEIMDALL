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

**Action** — a verb from the closed vocabulary in `app/server/services/auth`, such as
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

## Function workloads

**Native function** — a service with an explicit function runtime and invocation
contract in `DeploySpec`. AWS Lambda and Azure Functions require their own host
and event contracts; a general container image is not assumed to run unchanged
on either. Cloud Run remains a container provider with its own request lifecycle.

**Observed function version** — the provider version or revision read back for a
function service. It describes a deployed artifact and readiness, not a resident
execution instance. The UI does not invent container counts for function versions.

**Direct invocation** — an authorized control-plane `app:run` request for a
supported function contract. A timeout can leave execution in progress, so the
browser does not retry automatically.

## Browser and BFF boundaries

**Tac view** — the browser route template and its scoped components. The web app
has five operational views: home, application, instance, device approval, and
registry. The separate public website has eleven component-backed page routes.

**Client service** — an annotated class under `app/client/services/` coordinating
lifecycle, page state, and events. Templates live under `pages/` and `components/`;
named contracts live under `models/`; static utility classes live under
`shared/utils/`. Display decisions do not confer authorization.

**Yon BFF** — the thin server layer under `app/server/`, implemented by annotated
Go HTTP controllers, services, helpers, and models. Its three HTTP entries are
session exchange/revocation, the gateway, and optional configured audit sign-in.
The native request includes URL queries, while the browser retains the existing
POST gateway envelope. Its 23 Go files use four source roles: three controllers,
two services, nine helpers and nine models. `GatewayCall`, `ProxyFrame` and
`ProxyRequest` are native transport models; their raw JSON, HTTP, file, context,
cancellation and error fields retain their Go types. Model ownership does not
imply that the compiler can generate a schema for every native field. Qualified
compiler 32 supports custom `@FromBody` models on annotated Go controllers;
the requested framework-owned parsing path remains a migration goal requiring
runtime proof for status, headers, cookies, body and native model fields.

**Control-plane server package** — a native Go domain package under `app/server/`
owning API, storage, authorization supervision, reconciliation, provider or agent
behavior. Controllers, services, helpers, models, repositories and `utils` are
source roles inside the existing Go monolith. Native command packages live under
`app/server/commands/`. They share the Go 1.27 module with the 23 Yon BFF fragments,
while remaining distinct from Tac client classes and BFF forwarding mechanics.
Operational application implementation belongs under `app/`; the separate
public site belongs in sibling `website/`. No root backend is retained. Go's
former `internal/` import
restriction is removed by this requested move; explicit CI dependency checks
must preserve the authority and SDK boundaries between co-located native domain
packages and BFF fragments. The BFF accesses the control plane over public HTTP.
**Native API service** — the ordinary Go HTTP server package
`app/server/services/api`, retaining package `api` and mirrored tests under
`tests/server/services/api`. It is separate from the three actual annotated Yon
controllers, whose filenames end in `Controller`; placing this native package
under services does not register it in BFF dependency injection.

Only `heimdall serve` holds the SESAME engine and the single Go-owned FYLO writer;
the BFF calls the public API rather than opening their state.

These terms describe the authored Go/Tac migration. Qualified compiler 31
passed the fresh eight-route app build, generated semantic check and all 22
ordinary/function/instance browser assertions. Its original QA driver failed
during cleanup; the preserved failure and verified process recovery are
separate evidence. The same compiler passed 49 local Go HTTP boundary tests
without skips, plus a fresh eleven-route website build, semantic check and five
browser tests. Their source hashes were unchanged and owned cleanup completed.
The native qualification checkpoint passed all 31 Go packages, vet, measured
complexity for 1,062 changed methods (maximum CRAP 13.125), and an isolated
Go 1.26 control-plane rebuild. The 13 full-path browser scenarios remain
unqualified because setup failed before browser execution; a separate diagnostic
isolated the second sync after a revision change. Diagnosis and any repair need
their own validation. These local checks do not establish native function-host,
Linux CI or live-cloud qualification. These receipts qualify the pre-relocation
source snapshots. Consolidating the native packages and commands into the
`app/server/` Go 1.27 module requires fresh scoped builds, regressions,
dependency-boundary checks and framework companion-source qualification. The three
native BFF transport models' move into `app/server/models/` also requires fresh
compiler schema and runtime checks. The earlier 49 HTTP and 22 app assertions
precede that model move and do not qualify its generated schemas.

## Terms deliberately not used

**Namespace** — Kubernetes' word for a boundary. HEIMDALL has projects.

**Cluster** — ambiguous across providers and runtimes. Say target.

**Manifest** — implies rendered YAML applied verbatim. Say `DeploySpec`.
