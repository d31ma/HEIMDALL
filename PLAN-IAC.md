# HEIMDALL — Infrastructure as Code

> Terraform/OpenTofu provisioning under the same GitOps mechanics as compose workloads.
> Companion to [`PLAN.md`](PLAN.md). Post-GA: **Phases 8–10**.

---

## Context

[`PLAN.md`](PLAN.md) delivers GitOps for Docker Compose workloads. This document extends
the same four mechanics — git as truth, deterministic desired state, continuous live-state
read-back, diff and close — to cloud infrastructure via Terraform/OpenTofu.

The request is natural: the compose app needs an ECS cluster, an RDS instance, and a load
balancer, and today those come from somewhere else entirely. But the honest answer is that
**this is not the same mechanic wearing a different hat**, and the plan has to say why
before it says how.

---

## Why Terraform does not fit the existing model

Compose works in HEIMDALL because we own the whole pipeline: parse the file, render a
provider-neutral `DeploySpec`, read live state ourselves, compute the diff ourselves, and
close it. Terraform inverts every one of those.

| | Compose (Phases 0–7) | Terraform (Phases 8–10) |
|---|---|---|
| Desired state | We render `DeploySpec` | HCL, rendered by the engine — opaque to us |
| Diff | We compute it structurally | **`tofu plan` is the diff.** We parse its JSON output |
| Live state | We read it per service from the provider API | The engine reads it, mediated by a state file |
| State | Stateless; git + runtime are the only truth | **A state file is authoritative**, lockable, and corruptible |
| Apply safety | Converges; worst case a container restarts | **Can irreversibly destroy a database** |
| Rollback | Re-apply a stored `DeploySpec` | No undo exists — see below |
| Provider surface | Our four adapters | Thousands of third-party providers |

Two consequences drive the entire design:

**1. Terraform is a second reconciler shape, not a fifth `Provider` adapter.** Forcing it
into the `Provider` interface from `PLAN.md` would mean faking a `Plan()` that wraps an
engine that already has one, and an `Observe()` that cannot answer without a state file.
It gets its own package, `internal/infra/`, and its own state machine.

**2. Rollback is not rollback.** Re-applying revision N−1 is a *forward* apply with its own
plan and its own approval. Reverting the commit that created an RDS instance destroys the
instance; it does not restore the one you deleted. The compose product's rollback story
sets exactly the wrong expectation here, and the UI must use different words — `Apply
previous revision`, never `Roll back`.

---

## Positioning — and the scope discipline it buys

The compose space has one real incumbent that lacks continuous reconciliation. **The IaC
space has the opposite problem.** Atlantis (open source, PR-driven), Spacelift, env0,
Scalr, Digger, Terrateam, and HCP Terraform all exist, and drift detection, OPA policy,
RBAC, approval workflows, and cost visibility are already table stakes among them.

**We will not win by being a better Spacelift.** The one seam none of them cover:

> Every IaC platform provisions infrastructure and stops. Portainer deploys workloads and
> has no idea where they run. HEIMDALL is one control plane that provisions the
> infrastructure *and* deploys the compose workload onto it — with Terraform outputs
> flowing into compose overlays as typed references, so the VPC ID, cluster ARN, and
> database endpoint are never copied by hand.

That framing is also a scope weapon. Because we are not competing on IaC management
breadth, we explicitly do **not** build: an OPA/Rego policy engine, cost estimation or
FinOps reporting, per-run billing, a plan-time policy DSL, Terragrunt orchestration,
module registries, or a workspace-sprawl management UI. We build enough Terraform to
provision what our workloads run on, plus the wiring seam. Anything beyond that is a
customer telling us they wanted Spacelift, and the right answer is that they should use it.

---

## Engine: OpenTofu only

Terraform moved to BUSL-1.1 at v1.6 (August 2023). BUSL forbids embedding or hosting the
product to build a competing commercial offering, and distributors shipping managed
Terraform need a separate agreement with HashiCorp/IBM. HEIMDALL is a control plane that
runs IaC for customers — squarely in the contested zone.

- **Ship and supervise `tofu` only** (OpenTofu, MPL-2.0, Linux Foundation governance,
  1.12.x as of mid-2026). It shares HCL, the provider protocol, and the state model, so
  this is a drop-in for nearly all real configurations.
- **Never bundle a `terraform` binary.** An operator may point `HD_INFRA_ENGINE_PATH` at
  their own licensed copy, and we execute it; we do not distribute it. That is their
  licence to hold, not ours.
- Pin an exact engine version per application (`hd-applications.infra.engine_version`), the
  same discipline as pinning SESAME. An engine upgrade is a reviewable change, because a
  state-format bump is a one-way door.
- **Gate: legal review of the distribution model before Phase 8 ships.** Not a
  nice-to-have — this is the one risk in this document that is not an engineering problem.

---

## State: bring your own backend

**Terraform state contains sensitive values in plaintext.** A database password passed as a
variable lands in state. This collides head-on with `PLAN.md`'s standing rule — *zero
secret values in any persisted document, proven by a CI gate.*

The lazy and correct resolution: **HEIMDALL does not store state in v1.** The application
declares the customer's existing backend (S3, GCS, AzureRM, or any other), we pass it
through, and state never touches a FYLO document. We orchestrate runs; we are not a state
custodian. Everything the GitOps loop needs — plan, drift, apply, output extraction — works
without owning state.

The tempting alternative, implementing Terraform's HTTP backend over FYLO, is genuinely
small: `GET`/`POST`/`DELETE` on a state path plus `LOCK`/`UNLOCK`, returning `423` or `409`
with the holding lock's info on conflict and `404` for empty state. FYLO's per-document
locking maps onto it cleanly and would give state history and versioning for free.

**It is still deferred**, because it would make HEIMDALL responsible for the durability,
encryption, and access control of the most secret-dense artifact in the customer's estate,
and it would require carving an exception into the CI gate that currently protects us. If
customers demand managed state, it ships behind its own ADR, with FYLO field encryption,
its own threat-model section, and an explicit narrowly-scoped gate exemption — never as a
quiet extension of this feature.

---

## Where runs execute

Running `tofu apply` centrally means the control plane holds cloud credentials broad enough
to create and destroy infrastructure. That is a far larger prize than the compose product's
credentials, and it concentrates blast radius in one process.

- **Default for small deployments:** the control plane executes runs directly, with
  credentials from workload identity / OIDC federation rather than static keys.
- **Recommended for production:** runs execute on a **runner** — the existing
  `heimdall agent`, extended — inside the customer's own account, so cloud credentials never
  leave their blast radius and the control plane only ever sees plan JSON, logs, and
  outputs. Same outbound-only mTLS and fingerprint-pinned enrollment as Phase 1.
- Provider plugin downloads are supply chain. Require a committed `.terraform.lock.hcl`,
  support a provider mirror, and bound egress. A run whose lockfile is missing fails
  closed rather than resolving versions live.

---

## Data model

Reuse, not new parallel structures.

- **`hd-applications` gains a `kind` discriminator**: `compose | infra`. Repo wiring,
  project scoping, RBAC resource strings, target association, and the UI shell are then
  shared rather than duplicated. An infra application carries `path`, `workspace`,
  `var_refs`, `backend_ref`, and `engine_version`.
- **`InfraSpec`** is the canonical type — repo revision, path, workspace, variable refs,
  backend ref, engine version — CHEX-validated and content-hashed exactly like
  `DeploySpec`, preserving the immutable-revision and audit story. The asymmetry worth
  naming: `DeploySpec` is a *desired-state document* we render, while `InfraSpec` is an
  *execution descriptor*. We hash what we fed the engine, not what the engine will do.
- **Runs reuse `hd-operations`.** A plan-and-apply cycle is one document holding the whole
  state machine — `Pending → Planning → Planned → AwaitingApproval → Applying → Applied |
  Failed | Discarded` — patched in place. Same single-collection atomicity that FYLO's lack
  of cross-collection transactions forces, and it is why this feature does not need a new
  durability design.
- **`hd-infra-outputs`** is the one new collection. Non-sensitive outputs (VPC ID, cluster
  ARN, ALB DNS name) are stored as values because they are not secrets. Outputs marked
  `sensitive = true` in HCL are stored as **refs into the secret manager, never values** —
  the existing rule, applied without exception.

New queues: `infra.plan`, `infra.apply`, `infra.drift`.

**One long-running-work concern to design for:** an apply can run 30+ minutes, well past a
default queue visibility lease. FYLO's queue supports lease extension, so the run
heartbeats and extends while the engine works. Without that, a slow apply gets redelivered
and runs twice — which for infrastructure is not a retry, it is an incident.

---

## Authorization

New SESAME actions. Two splits carry real weight:

| Action | Notes |
|---|---|
| `infra:read` | View applications, plans, outputs |
| `infra:plan` | Trigger a plan. Safe, read-only against the cloud |
| `infra:approve` | **Separately grantable from `infra:apply`** — this is the four-eyes control. A principal who may apply must not be able to self-approve |
| `infra:apply` | Execute an approved plan |
| `infra:destroy` | **Separate from `infra:apply`.** Destroying infrastructure is its own privilege |
| `infra:unlock` | Force-release a stuck state lock. Dangerous, always audited |
| `infra:output-read` | Read outputs, including resolving sensitive refs. Separate because outputs leak infrastructure topology |

Same rules as `PLAN.md`: one `Decide` call at the boundary, fail closed, `policy_version`
and `reason_code` stamped into every audit record.

---

## The apply gate

`docker compose up` converges. `tofu apply` can drop a production database. The compose
reconciler's auto-sync default is actively wrong here.

- **Plan runs automatically** on every commit — it is read-only and cheap in risk.
- **Apply never runs without explicit approval** by default.
- **Auto-apply is opt-in per application**, and even when enabled it is **blocked whenever
  the plan contains a `delete` or `replace` action**. Destructive changes always require a
  human, regardless of configuration.
- **Parse `tofu show -json <planfile>`, never human-readable output.** The structured
  `resource_changes[].change.actions` array drives both the diff UI and the destructive-change
  guard. This is the existing rule — never parse human-readable text — applied to a place
  where it is tempting to cheat.
- **One run per workspace at a time**, enforced by our own per-workspace serialization in
  addition to whatever the state backend locks. Concurrent applies against one workspace are
  a corruption path.

---

## Drift detection

`tofu plan -detailed-exitcode` returns `0` for no changes, `1` for error, `2` for drift —
so drift detection is a scheduled plan whose exit code we read. Reuse the compose
reconciler's jittered adaptive schedule, with a much longer floor: infra plans cost money
and hit provider rate limits, and infrastructure drifts on a scale of days, not seconds.
Default hourly, decaying to daily when stable, immediate on commit or webhook.

Drift on infrastructure is **reported, never auto-closed** unless auto-apply is explicitly
enabled and the plan is non-destructive. Self-heal is right for a missing container and
wrong for a security group someone changed during an incident.

---

## Observability reuse

A Terraform-managed resource maps onto the existing `Instance` shape well enough that the
Phase 3 instance drawer works with no new UI: click an RDS instance in the resource graph
and get its CloudWatch metrics through the existing `Provider.Metrics` path, its recent
events, and the revision and run that last touched it. `<hd-timeseries>` and the event
timeline are reused as-is. This is the cheapest win in the document.

---

## The seam — why this feature exists

Terraform outputs become typed references available to compose overlays:

```yaml
# compose.prod.yaml
services:
  api:
    environment:
      DATABASE_HOST: ${infra:platform-db.endpoint}
      CLUSTER_ARN: ${infra:platform-ecs.cluster_arn}
```

Rules, all bounded deliberately:

- Resolution happens at **render time** for non-sensitive outputs and at **apply time** for
  sensitive ones, which stay refs the whole way through — so no output value ever enters
  `hd-revisions`, a plan, or a diff.
- The dependency is **declared explicitly** on the compose application, not inferred by
  scanning strings. Inference would make the ordering graph implicit and unreviewable.
- The graph is **acyclic and one-directional**: compose reads infra outputs; infra never
  reads compose state. Cycles are rejected at validation, not detected at runtime.
- A sync of the dependent application **blocks** while the infra application's outputs are
  stale, and surfaces *why* it is blocked rather than deploying against last week's endpoint.
- An unresolvable ref **fails the render closed**, naming the missing output — the same
  posture as `Capabilities()` rejecting an unsupported compose directive.

---

## Roadmap

Post-GA. Phases 0–7 ship the compose product first; folding this in earlier doubles the
surface and delays the thing that has a validated gap to fill.

### Phase 8 — Infra vertical slice (5 weeks)
OpenTofu supervision as a bounded child process (no shell). `InfraSpec` + CHEX schema +
content hash. Bring-your-own backend passthrough. Plan on commit, plan JSON parsing,
structured diff UI. Approve → apply state machine over `hd-operations`. Run log streaming
over the existing SSE topic logs. The seven SESAME actions. Runner execution path.

*Exit:* a repo provisions real cloud infrastructure; the plan is visible and human-readable
in the UI; apply is impossible without a distinct approval; a destroy in the plan is flagged
before anyone clicks; every run is attributable in `hd-audit`.

### Phase 9 — The seam (4 weeks)
`${infra:<app>.<output>}` refs. `hd-infra-outputs` with the sensitive/non-sensitive split.
Explicit dependency edges, cycle rejection, staleness blocking, fail-closed resolution.

*Exit:* **one repository provisions an ECS cluster and an RDS instance, then deploys a
compose application onto it, wired entirely by outputs with no hand-copied values.** This is
the phase that justifies the feature — if it slips, the rest is a worse Spacelift.

### Phase 10 — Drift and hardening (4 weeks)
Scheduled drift plans with adaptive backoff. Drift UI. Auto-apply opt-in with the
destructive-change guard. Per-workspace serialization. Lock recovery and audited
force-unlock. Engine version pinning and upgrade path. Lease extension for long applies.
Provider mirror and lockfile enforcement. Chaos: engine killed mid-apply, state lock
orphaned, provider API throttling, credentials expired mid-run.

*Exit:* an apply killed at its midpoint leaves a `hd-operations` document that explains
exactly what was and was not applied, and a documented recovery path that does not involve
editing state by hand.

**Total ≈ 13 weeks post-GA.**

---

## Risks

| # | Risk | Sev | Mitigation |
|---|---|---|---|
| 1 | **Terraform's BUSL licence.** Bundling ≥1.6 in a commercial control plane needs a HashiCorp/IBM agreement | High | OpenTofu (MPL-2.0) is the only engine we ship. Customer-supplied `terraform` is executed, never distributed. **Legal review of the distribution model gates Phase 8** |
| 2 | **Terraform state holds plaintext secrets**, contradicting our zero-secrets CI gate | High | Do not own state. Bring-your-own backend; state never enters a FYLO document. Managed state, if ever, needs its own ADR, field encryption, and a narrow explicit gate exemption |
| 3 | **A crowded, mature market** — Atlantis, Spacelift, env0, Scalr, Digger, Terrateam, HCP Terraform, with drift/policy/RBAC already table stakes | High | Do not compete on IaC breadth. The infra↔workload seam (Phase 9) is the only defensible claim. If Phase 9 slips, reconsider shipping this at all rather than shipping a weaker competitor |
| 4 | **Apply can irreversibly destroy production** | High | Mandatory approval by default; `infra:approve` separately grantable from `infra:apply`; auto-apply blocked on any delete/replace; `infra:destroy` its own privilege |
| 5 | Control plane holding cloud admin credentials concentrates blast radius | High | Runner execution in the customer's account is the recommended path; OIDC federation over static keys; central execution only as a small-deployment default |
| 6 | Long applies exceed queue visibility leases and get redelivered — a double apply is an incident, not a retry | Med | Heartbeat + FYLO lease extension for the run's duration; per-workspace serialization as the second line of defence |
| 7 | Provider plugin downloads are an unbounded supply chain and egress surface | Med | Require a committed `.terraform.lock.hcl`, fail closed without one, support a provider mirror, bound egress |
| 8 | Users expect compose-style rollback and get a forward apply | Med | Different vocabulary in the UI (`Apply previous revision`), an explicit warning when the resulting plan contains destroys, and documentation that states plainly that there is no undo |
| 9 | Scope creep toward a full IaC management platform (policy engines, cost, module registries) | Med | The non-goals list above is a standing decision. A request beyond the seam is a signal the customer wants Spacelift |
| 10 | Engine version drift and state-format one-way doors | Low | Pin per application; treat an upgrade as a reviewable change with its own plan |

---

## Open questions

1. **Is Phase 9's seam worth 13 weeks post-GA?** It is the only defensible differentiator,
   but it is also the narrowest possible slice of a crowded market. A design partner who
   actually wants infra-plus-workload in one place would settle this; without one, this is
   the highest-uncertainty investment in either plan.
2. **Terragrunt.** Widely used, and its orchestration model conflicts with ours. Current
   assumption: unsupported, rejected at validation with a clear message.
3. **CDKTF, Pulumi, Crossplane.** Assumed out of scope permanently — HCL only.
4. **Does the compose product need infra at all to sell?** If provisioning is genuinely
   blocking compose adoption, this moves earlier. If it is a "would be nice," it stays
   post-GA or gets dropped.
