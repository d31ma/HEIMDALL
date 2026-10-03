# ADR 0010 — The application registry is declared in git

**Status:** accepted (implemented)

## Context

HEIMDALL's product claim is that git is the truth for what runs. Today that
claim stops one level short of the top: what runs is declared in git, but
*what applications exist* — projects, repositories, targets, applications,
groups — is registered through authorized API routes into `hd-*` collections.
Recreating a control plane means replaying registrations from a runbook or a
backup, not from a repository, and the app inventory has no diff, no
review, and no rollback.

SwarmCD gets this right in miniature: its `stacks.yaml` *is* the registry —
adding a stack is a commit. ArgoCD's app-of-apps pattern is the same idea at
scale. Both prove the operator experience; neither has an authorization
boundary worth copying.

The forces:

- The registry changes rarely but expensively. A wrong target on an
  application deploys workloads to the wrong place; that is exactly the class
  of change that wants review, history, and rollback — git's strengths.
- Registration is currently a mutating API route with one SESAME `Decide`
  per call. Whatever replaces interactive registration must not dilute that
  boundary or create a second authority over the same documents.
- The registry names credentials (`credential_ref`, `password_ref`,
  `webhook_secret_ref`). Those are references by ADR 0006, so a registry
  manifest carries nothing secret — a property this design inherits rather
  than invents.
- A root repository that can declare targets is high-privilege by
  definition: whoever can merge to it can redirect deployments. The trust
  boundary must be stated, not implied.

## Decision

A tenant may bind one **root repository**. The binding itself is the one
interactive act — an authorized API route (`registry:bind`, SESAME-decided,
audited) naming a repository and a path. Everything below the binding is
declarative.

The root repository contains a **registry manifest**: CHEX-validated YAML
declaring projects, repositories, targets, groups, and applications by
*name* (TTIDs remain internal; the manifest never sees them). The manifest
is rendered, canonicalized, and content-hashed exactly as a `DeploySpec` is,
and each rendered registry revision is stored immutably — the registry gets
the same diff, history, and rollback mechanics as a workload, from the same
code.

Reconciliation reuses the loop that exists. A registry sync is one operation
document in `hd-operations`: refresh reads the manifest at a commit, the
plan is the diff between the manifest and the `hd-*` collections, and the
apply creates, updates, or deletes registry documents. Deletion is
prune-gated exactly as workload pruning is: opt-in, marked in the plan,
never a surprise.

**One authority per document.** A document created by the registry loop is
stamped `managed_by: registry`. The mutating API routes refuse to edit a
managed document, with an error that names the root repository — the same
rule that gives FYLO one writer and SESAME one engine, applied one level up.
Documents registered interactively before (or without) a binding stay
API-managed; the manifest may adopt one only by declaring it, and the plan
shows the adoption before it happens.

**What the manifest may never declare.** Principals, roles, and grants —
authorization is SESAME's and arrives through SCIM or its own admin surface,
never through a workload repository (ADR 0002). Secret *values* — the
manifest carries references only (ADR 0006). Agent enrollment — an agent
joins by presenting an enrollment token against a pinned fingerprint
(ADR 0007), and no manifest can mint one. The registry loop therefore runs
with registry authority only; a compromised root repository can misroute
deployments — which its review process exists to catch — but cannot widen
anyone's grants, read a secret, or enroll an agent.

**Trust is explicit.** The binding may require signed commits
(`RequireSignature` already exists per-repository and applies unchanged),
and the recommendation for any multi-operator deployment is to bind a
repository whose forge enforces review. The webhook receiver nudges the
registry like any other repository.

## Alternatives considered

- **Keep API-only registration.** Honest, simple, and the status quo — but
  it leaves the top of the truth chain outside git, and disaster recovery
  depends on a backup instead of a clone. Rejected as failing the product's
  own claim.
- **Templating/generators (ArgoCD ApplicationSet).** Powerful for fleets,
  but it makes the registry a program rather than a declaration, and the
  rendered result can no longer be reviewed by reading the file. Deferred:
  fan-out groups already cover the fleet case HEIMDALL has today.
- **The manifest as just another DeploySpec kind.** Tempting reuse, but a
  registry entry is not a workload: it has no waves, no health, no
  instances, and its "adapter" writes FYLO documents rather than driving a
  runtime. Sharing the revision/diff/operation machinery without pretending
  the two are the same type keeps both honest.

## Consequences

- `heimdall registry bind` (plus unbind/status) is the entire interactive
  surface. Everything else is a commit, which means the registry gains
  review, blame, and revert for free.
- Disaster recovery becomes: restore the deployment directory (ADR 0004),
  bind the root repository, sync. The runbook shrinks to one authorized act.
- A second control plane pointed at the same root repository reproduces the
  same registry — the property that makes staging environments honest.
- The API grows refusals: mutating a `managed_by: registry` document fails
  with the root repository named. Operators lose the ability to hotfix a
  managed application's target from the CLI, deliberately — the hotfix is a
  commit, like every other change to truth.
- The registry loop is a second consumer of the refresh/diff/operation
  machinery, which constrains refactors of that machinery to preserve both.
- Bootstrapping order becomes: `heimdall init` → bind → sync — and the
  first registry sync can declare the repositories that workloads then
  deploy from, so a whole deployment is two commands and a repository.
