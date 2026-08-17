# HEIMDALL — Implementation Plan

## Context

`DELMA/HEIMDALL/` is empty. The goal is a GitOps continuous-delivery and observability
control plane for **Docker Compose** workloads — ArgoCD's model, but where the desired
state is a `docker-compose.yaml` and the runtime is a Docker Engine, Amazon ECS, Azure
Container Apps, or Google Cloud Run.

ArgoCD's value is not Kubernetes. It is four mechanics: git is the only source of truth;
a **desired state** renders deterministically from a commit; **live state** is read back
continuously; the controller reports the diff and closes it. None of that is
Kubernetes-specific.

The differentiator over ArgoCD is the second half of the request: **click a running
instance and see its metrics, logs, events, and the exact commit that put it there** —
without standing up Prometheus/Grafana/Loki alongside.

Built on the in-house stack: [FYLO](https://fylo.del.ma) (store + queue),
[SESAME](https://sesame.del.ma) (authn/authz), [Tachyon](https://tachyon.del.ma) (web),
[DuVay](https://duvay.del.ma) (UI), CHEX (schema validation), TTID (identifiers), Go 1.26.

### Confirmed with the user

| Decision | Answer |
|---|---|
| "ACS" | **Azure Container Apps (ACA)** |
| Time-series charts | **Build `<hd-timeseries>` inside HEIMDALL**, zero-dependency |
| Swarm | **Standalone Engine in v1**, Swarm in Phase 4 |
| Tenancy | **Self-hosted only.** Projects are an RBAC scope, not a hard isolation boundary |

---

## What the exploration found

Five findings from reading the sibling repos changed the design. They are the reason
this plan looks the way it does.

**1. SESAME opens no port.** `SESAME/README.md:36-46` — it is a binary the application
*spawns*, speaking NDJSON over stdin/stdout. There is no listener, no admin console, no
redirect target. So SESAME cannot be "the SSO server"; `heimdall serve` supervises it as
a child process and owns every HTTP route itself, implementing SESAME's framework-neutral
host-adapter contract (`SESAME/api/standards/v1/`) for the OIDC endpoints.

**2. FYLO has no cross-collection transactions.** `FYLO/README.md` Limitations — writes
are atomic within *one* collection only. A sync that wants to update `syncs` +
`livestate` + `events` + `audit` atomically cannot. This forces the operation-document
pattern below. SESAME already solves it the same way ("hash-chained ledger with
rebuildable projections"), so the pattern is proven in-family.

**3. FYLO forbids network filesystems.** Same table: *"Use local POSIX filesystems or
NTFS, not network/sync filesystems without equivalent atomic semantics."* This kills EFS,
Azure Files, and NFS as the HA substrate — block storage (EBS/PD/Azure Disk) only. It
also means **do not put a FYLO root inside this Dropbox tree during development**; the
dev harness must point `HD_FYLO_ROOT` at `/tmp` or `~/.heimdall`.

**4. DuVay has no time-series chart.** `DUVAY/src/components/chart.js:1` is literally
commented *"simple bar chart shell"* — percent-clamped bars, no axes.
`DUVAY/src/components/sparkline.js` is real (trend/bar, smoothing, gradients, markers,
interactive tooltip with crosshair) but has no time axis, no y-scale labels, no
annotation layer, no live append. The instance-metrics view needs all four.
Confirmed resolution: build `<hd-timeseries>` in HEIMDALL's own web tree from inline SVG
using DuVay tokens; reuse `w-sparkline` for the inline mini-charts in list rows.

**5. Tachyon already streams.** `TACHYON/README.md:257` — *"bounded append-only topic
logs exposed as server-sent events."* Live log tail and live metric append ride that
rather than a bespoke websocket layer. Also `TACHYON/README.md:229` confirms Yon handlers
run JS/TS/Python or registered executables — **not Go** — which independently forces the
separate Go control plane.

Conventions to inherit from `TACHYON/docs/ENGINEERING_STANDARDS.md`: vertical slices;
public contracts under `api/`; ADRs for hard-to-reverse decisions; typed errors never
parsed from text; bounded input/recursion/queues/concurrency/retries; no shell invocation
of subprocesses; no `utils`/`helpers`/`common`/`manager` dumping grounds; diagnostic codes
as a prefix plus four digits (`HD0001`); one env namespace (`HD_`).

---

## Architecture

### Processes

| Process | Role |
|---|---|
| `heimdall serve` | Control plane. API, git sync, render, diff, reconcile workers, provider adapters, metrics proxy. **Owns every listener** |
| ├─ `sesame` | Child process. Every auth decision. NDJSON on stdin/stdout |
| ├─ `fylo` | Child process. Documents and queues. NDJSON on stdin/stdout |
| `heimdall agent` | Optional, on a Docker Engine host. Outbound-only mTLS. Applies plans, scrapes container stats |
| `ty serve` | Tachyon web UI (Tac) + BFF (Yon) proxying to the control-plane API |

One container image bundles `heimdall`, `sesame`, `fylo`, and the `chex`/`ttid` helpers
FYLO drives. Supervised startup order: `fylo` → `sesame doctor` → `heimdall serve`. Any
child failing is fatal and fails closed — the API returns `503`, never serves unauthenticated.

**Only `heimdall serve` may hold a SESAME engine.** A Yon route spawning its own would be
a second authority over shared security state. Tachyon stays a pure proxy: it must not
interpret, cache, or rewrite an OAuth response.

### The one hard problem: compose is not portable

Compose is a *deployment* format for Docker Engine and only a *hint* for the clouds.
Docker's own compose→ECS/ACI integration was deprecated in 2023 and **must not be built
on**. Cloud Run has no compose support. ACA's `az containerapp compose create` is real but
partial. So HEIMDALL owns the translation, through one canonical type:

```
compose.yaml + overlays + secret refs ──render──▶ DeploySpec (canonical JSON, content-hashed)
                                                        │
                          ┌──────────┬──────────────────┼──────────────┐
                      docker        ecs             cloudrun          aca
```

`DeploySpec` is provider-neutral, CHEX-validated, and stored immutably per revision.
Diffs, rollback, audit, and drift all operate on it. Adapters are the only code that
knows a cloud exists.

### The adapter interface

```go
type Provider interface {
    Plan(ctx, Target, spec.DeploySpec) (Plan, error)
    Apply(ctx, Target, Plan) (OpID, error)
    Observe(ctx, Target, AppRef) (LiveState, error)      // drift + health
    Instances(ctx, Target, AppRef) ([]Instance, error)
    Metrics(ctx, Target, InstanceRef, Window) (Series, error)
    Logs(ctx, Target, InstanceRef, LogFilter) (Stream, error)
    Events(ctx, Target, AppRef) ([]Event, error)
    Capabilities() Capabilities
}
```

`Capabilities()` is load-bearing. It drives **fail-closed render-time validation**: a
compose file using a named volume against Cloud Run is rejected at plan time with the
offending line, not half-applied at 3am. The published capability matrix is *generated*
from these values so it cannot go stale, and unsupported entries are enum values rather
than prose.

Known lossy edges to encode: Cloud Run rejects `depends_on` and named volumes and allows
one HTTP port; ECS maps volumes to EFS and ports to ALB/NLB; ACA has a single ingress;
resource requests snap to discrete tiers on Cloud Run and ACA.

### Data model (FYLO)

Collections, namespaced `hd-*` to match SESAME's `sesame-*`. The separator is a hyphen
because FYLO's `validate_collection_name` accepts lowercase alphanumerics and `-` only —
`hd_projects` is rejected outright by the engine.

`hd-projects`, `hd-repos`, `hd-targets`, `hd-target-groups`, `hd-registries`,
`hd-applications`, `hd-revisions` (commit → `DeploySpec` + content hash, immutable — the
audit spine), `hd-operations`, `hd-livestate` (cache, rebuildable), `hd-events`,
`hd-rollups`, `hd-audit`.

`hd-registries` holds private-registry *credential refs* and never credential values —
same rule as `${secret:...}`, resolved at apply time only. `hd-target-groups` holds tag
expressions rather than member lists, so membership is derived and a retagged host moves
groups without editing either side.

**HEIMDALL and SESAME do not share a FYLO root.** FYLO takes an exclusive lock per root
and a second engine fails the handshake with `EROOTLOCKED`, so HEIMDALL keeps its own
root at `<deployment>/fylo-root` beside SESAME's at `<deployment>/sesame/fylo-root`. The
**deployment directory** is the single backup and restore unit — both roots plus
`sesame/keys/`. A root-only backup was never sufficient anyway, since the key material
deliberately lives outside every root.

Users, sessions, roles, grants, groups, and API tokens are **deliberately absent** —
SESAME owns them. HEIMDALL stores a `principal_id` reference and nothing else.

**The operation-document pattern** (forced by finding #2): a sync is *one document* in
`hd-operations` holding the whole state machine — target revision, plan, per-resource
outcomes, timestamps, principal, `policy_version`. Every write during a sync is a patch
to that single document, so it is atomic. `hd-livestate`, `hd-events`, and `hd-rollups`
are **projections rebuilt by folding `hd-operations`**, never independently
authoritative, and the fold runs in ascending TTID order because byte-for-byte
reproducibility is unreachable from an unordered fold. `hd-audit` is deliberately *not* a
projection: an audit log you can regenerate is one you can rewrite. A
crash mid-sync leaves one document in a known state, not four in disagreement.

Queues (FYLO's brokerless durable queue — no Redis, no NATS): `repo.poll`, `app.render`,
`app.reconcile`, `app.observe`, `metric.rollup`. At-least-once is safe because the dedupe
key is `appID + revisionHash + operation` and every apply is a compare-and-set against
live state read inside the visibility lease.

Explicitly rejected: FYLO's POSTIX uid/gid/mode per-record access control. SESAME is the
single authority for authorization; two authorities is a security bug.

### Identity and access — SESAME

HEIMDALL writes **no** auth logic. No password hashing, no session table, no token
minting, no role comparison in a handler.

Already implemented in SESAME and therefore a call, not a project: Argon2id with upgrade
path, TOTP with replay prevention, recovery codes, WebAuthn passkeys, revocable sessions
cascading to refresh families, inbound OIDC federation, inbound SAML 2.0, **SCIM 2.0**,
and HEIMDALL-as-OIDC-provider (auth code + mandatory PKCE, discovery, JWKS, introspection,
revocation, consent, RP-initiated logout, **device authorization**, PAR, DPoP).

Two of those change the roadmap outright: SCIM drops from "if a customer asks" to
configuration work, and device authorization makes `heimdall login` a real
browser-confirmed flow rather than a pasted token.

Every mutating route resolves to exactly one decide call at the middleware boundary:

```go
sesame.Decide(ctx, sesame.Decision{
    TenantID:    org,
    PrincipalID: principal,
    Action:      "app:sync",                        // never inferred from the route
    Resource:    "project:alpha:app:checkout",      // hierarchical: project:alpha:* covers its apps
})
```

Action vocabulary, committed as a Go enum in Phase 0:

| Domain | Actions |
|---|---|
| `app` | `read create update delete sync rollback prune suspend` |
| `target` / `repo` | `read create update delete` |
| `project` | `read create update delete grant` |
| `secret` | `bind` only — no route ever returns a value |
| `observe` | `metrics logs events` |
| `audit` | `read export` |

Colon is the only separator SESAME's `ValidatePattern` accepts — it rejects `/` — and its
wildcard is legal only as a trailing segment, so `project:alpha:*` is what covers a
project. Ordering is a security decision: coarse to fine, always.

`observe:logs` is separate from `app:read` on purpose: container logs routinely contain
data an operator who may deploy still should not read.

Roles (`viewer`, `operator`, `admin`, `owner`) are grant bundles over that list, not code.
Nothing in HEIMDALL branches on a role name — enforced by a CI gate.

Rules: **fail closed** (a decide call that errors, times out, or returns an unrecognised
code is a deny). Decide **once, at the boundary** — handlers receive an already-authorized
request. Stamp `policy_version` and `reason_code` into every audit record so "why was this
allowed in March" is answerable without replaying policy. SESAME's ES256 signing key,
sealed-secrets key, and snapshot key live `0600` **outside** the FYLO root — the DR runbook
must back them up separately or a restored control plane cannot verify any session.

### Metrics: no time-series database

**HEIMDALL does not build a TSDB.** ECS→CloudWatch, ACA→Azure Monitor, Cloud
Run→Cloud Monitoring: query on demand, cache 30s, render. That data already exists and is
already retained and paid for. Only Docker Engine has nothing — there the agent scrapes
`/containers/{id}/stats` at 10s into a bounded in-memory ring buffer (24h ≈ a few MB/host)
and pushes 1m/5m/1h rollups to `hd-rollups`. If the operator already runs Prometheus,
point a target at it and skip the agent rollups entirely.

This deletes ingest, compaction, retention, cardinality control, and a query language from
the roadmap.

**Drift detection** is event-driven first (ECS→EventBridge, ACA→Event Grid, Cloud
Run→audit-log sink, Docker→the Engine event stream over the agent), with a jittered
adaptive poll as backstop: 30s after a sync, decaying to 5m when stable, reset on any
event. Compare provider revision/task-definition identifiers before pulling full
descriptions, or the API bills will be the story.

### GitOps semantics

Shipping: desired-vs-live diff with secret redaction; manual sync, dry-run, selective
sync; auto-sync, self-heal, prune; sync waves and pre/post/fail hooks; health rollup
(`Healthy | Progressing | Degraded | Suspended | Missing`); sync status (`Synced |
OutOfSync | Unknown`); rollback by re-applying a stored `DeploySpec` with no git rewrite;
`compose.yaml` + `compose.<env>.yaml` overlays with standard merge semantics; image digest
pinning with an updater that opens a PR rather than mutating live.

Not shipping in v1: multi-source applications, a notification engine (emit webhooks
instead), a plugin/CMP system.

---

## Competitive position — Portainer

[Portainer](https://github.com/portainer/portainer) (38k stars, Go + TypeScript, zlib) is
the incumbent for this audience and the closest thing to a direct competitor. It is worth
being precise about what it is: **a Docker management GUI with GitOps attached**, not a
GitOps control plane. Reading its handler and dataservice packages gives the real feature
surface, and its open issues give an unusually honest defect map.

### The distinction that defines HEIMDALL

Portainer's own [comparison against Argo CD](https://medium.com/@portainerio/argo-cd-vs-portainer-gitops-an-implementation-level-comparison-fbab593fa0c3)
concedes the central point: reconciliation happens **only at deployment and update
events**. Between deploys, live state is never observed or corrected. A container removed
by hand goes unnoticed until the next push.

HEIMDALL's continuous read-back loop is therefore not a nice-to-have — it is the entire
reason to build rather than adopt. Every claim in this plan about drift, self-heal, and
detection latency is a claim Portainer structurally cannot make.

### What Portainer proves we are missing

Each of these is now folded into the roadmap below rather than left as an observation:

| Gap | Evidence | Landed in |
|---|---|---|
| **Registry credentials** | A whole `registries` dataservice. Open issue: *"Updating a service fails when the image is from a private registry"* | **Phase 1** — blocking; no private image deploys without it |
| **Target groups and tags** | `endpointgroups`, `tags`, `endpointrelation` | **Phase 2** — projects scope RBAC; groups organise fleets |
| **Pending actions for offline targets** | A `pendingactions` dataservice | **Phase 3** — our agents are outbound-only, so offline is routine |
| **Fan-out deploy (one app → N targets)** | `edgegroups` + `edgestacks` as core objects | **Phase 4** — was deferred as "ApplicationSets"; that was too aggressive |
| **Fingerprint-pinned agent enrollment** | Their edge key encodes server URL, tunnel address, **server fingerprint**, and endpoint ID, defeating MITM at enrollment | **Phase 1** — cleaner than anything we had specified |
| **Template catalog** | `templates` + `customtemplates` | **Post-GA** — high adoption, but not on the critical path |

### Where we are structurally better

Not marketing claims — each is anchored to something observable in their repo.

1. **Their paywall is our free baseline.** RBAC is the CE/BE dividing line: CE ships
   admin/user, Business Edition adds the role hierarchy, SSO, and activity logging. SESAME
   gives HEIMDALL roles, grants, groups, OIDC/SAML federation, SCIM, and a hash-chained
   audit ledger in the open core, at Phase 0.
2. **Monitoring is their #4 most-reacted open issue** (*"Monitoring and alerting"*, 32
   reactions) and is unimplemented. That is this plan's entire second half, validated by
   demand rather than assumed.
3. **Their compose bug cluster is an architectural symptom.** *"`stack.env` does not
   work"* (29), *"Save stack compose without updating the stack"* (30), *"A stack with the
   name is already running"* (16), *"Sub-directory stack does not find `.env` file"* (8),
   *"Environment variables not passed to containers"* (8) — roughly 90 reactions with one
   root cause: no canonical, validated, content-addressed render step. `DeploySpec` plus
   CHEX validation is the structural fix; the golden-hash test is what keeps it fixed.
4. **Their auth defects are the argument for ADR 0002.** *"Unable to hash data"* for
   passwords over 72 characters is the bcrypt input limit leaking to end users; Argon2id
   has no such limit. Add *"Origin invalid behind reverse proxies"* (21) and *"Proxy
   BasicAuth not compatible"* (17). SESAME's host-adapter contract answers all three.
5. **Durability.** BoltDB is one opaque file, and recovering from its corruption is a
   genre of blog post. FYLO is one readable JSON document per record with rebuildable
   indexes — and because git holds desired state and `hd-revisions` is immutable, losing
   our store loses metadata, not intent.
6. **No cloud story.** Docker, Swarm, Kubernetes, ACI. No ECS, no Cloud Run, no Container
   Apps. The compose→cloud translation is uncontested.

### Deliberately not copied

The container/image/volume/network management GUI — an enormous surface, and a Docker UI
is not this product. Kubernetes and Helm. KaaS provisioning.

**The interactive web console into containers is rejected on principle.** It is both a
serious security surface and the anti-GitOps escape hatch that draws the fair criticism
that Portainer lets you click a button to change live state. Streaming logs, yes. An
attached shell, no. If a future customer forces it, it gets its own SESAME action, is
always audited, and is off by default.

---

## Repository layout

```
HEIMDALL/
  README.md CHANGELOG.md VERSION LICENSE SECURITY.md CONTRIBUTING.md
  AGENTS.md CLAUDE.md CONTEXT.md
  api/                    versioned public contracts: openapi.json, schemas
  cmd/heimdall/           serve | agent | init | doctor | login | app | diff | sync
  internal/
    git/                  clone, fetch, revision resolve, signature verification
    render/               compose parse, interpolation, overlays, secret-ref rewrite
    spec/                 DeploySpec, canonical JSON, content hash
    diff/                 structural diff, redaction, health rollup
    reconcile/            queue consumer, sync waves, hooks, prune, backoff
    provider/             provider.go + docker/ ecs/ cloudrun/ aca/ conformance/
    observe/              metric queries, rollups, log and event streams
    secrets/              ${secret:...} resolvers per provider
    store/                FYLO collections, queues, indexes, projection rebuild
    auth/                 SESAME supervision, Decide(), host-adapter routes
    api/                  REST + SSE, authz middleware, audit emit
  schemas/                *.schema.json for CHEX
  web/                    Tachyon: client/pages, client/components (hd-timeseries), server/routes
  docs/
    PROJECT_PLAN.md ENGINEERING_STANDARDS.md THREAT_MODEL.md
    architecture/ operations/ PHASE_N_EVIDENCE.md
    adr/
      0001-deployspec-as-the-canonical-model.md
      0002-sesame-owns-every-security-decision.md
      0003-no-time-series-database.md
      0004-single-writer-control-plane.md
      0005-operation-document-over-cross-collection-writes.md
  testdata/compose/       conformance corpus
  scripts/ .github/workflows/
```

---

## Roadmap

Two engineers. Each phase ends with `docs/PHASE_N_EVIDENCE.md`, matching Tachyon's
convention.

### Phase 0 — Foundations + identity substrate (3 weeks)
Repo scaffold, Go module, CI (fmt, vet, golangci-lint, test, `govulncheck`, license scan),
single-binary release with checksums and provenance. `DeploySpec` type and CHEX schemas
for compose input and spec output. FYLO wrapper with collection/index bootstrap and the
projection-rebuild path.

Identity lands here because it constrains every route written afterwards: child-process
supervision for `fylo` and `sesame` with fail-closed ordering; `heimdall init` wrapping
`sesame init`/`sesame doctor` and establishing the key boundary outside the FYLO root; the
action/resource enum; one-Decide-at-the-boundary middleware; the four role bundles as seed
grants.

*Exit:* `heimdall version` ships from CI; schemas validate the compose corpus; a route with
no grant returns `403` carrying SESAME's `reason_code`; the same route with the engine
stopped returns `503`. Both proven by test.

### Phase 1 — Vertical slice: Docker Engine (5 weeks)
Git clone/fetch → render → `DeploySpec` → diff against a live Docker Engine → manual sync.
Tachyon UI: app list, app detail, diff view, sync button. Local login with Argon2id + TOTP;
`heimdall login` over device authorization.

**Registry credentials** (`hd-registries`): per-target and per-project credential refs for
private image pulls, resolved at apply time and never persisted as values. Without this the
product cannot deploy a private image, which is most real workloads — Portainer carries an
open bug here and it is not an edge case.

**Agent enrollment**, outbound-only over mTLS. The enrollment token encodes control-plane
URL, target ID, and the **server certificate fingerprint**, so an agent cannot be
MITM'd during its first connection. Adopted from Portainer's edge key, which gets this right.

*Exit:* a compose repo pulling a private image deploys to a real host; drift visible within
60s; a one-service change syncs cleanly; every mutating call lands in `hd-audit` with a
principal and `policy_version`. **This slice proves the whole model — do not start Phase 4
before it lands.**

### Phase 2 — GitOps completeness (3 weeks)
Auto-sync, self-heal, prune, sync waves, hooks, rollback, sync history, webhook receiver,
selective sync, dry-run.

**Target groups and tags** (`hd-target-groups`, tags on `hd-targets`): projects scope
authorization, groups organise a fleet. Distinct concerns — do not overload one onto the
other. Selection is by tag expression so a group's membership is derived, not hand-maintained.

*Exit:* kill a container out-of-band → self-heal restores it. Rollback to `HEAD~3` succeeds
without touching git. A tag change moves a host between groups with no edit to either group.

### Phase 3 — Observability v1 (3 weeks)
Agent stats scrape → ring buffer → rollups. **`<hd-timeseries>`**: inline SVG, DuVay
tokens, time axis, y-scale labels, deploy-marker annotation layer, live append over
Tachyon's SSE topic logs. Instance drawer: CPU, memory, network I/O, block I/O, restarts,
health, uptime, owning revision. Live log tail. Unified event timeline. `w-sparkline` for
list-row mini-charts.

**Pending actions** for offline targets. Agents are outbound-only, so a disconnected target
is normal operation, not an incident. A sync against an unreachable target parks its
`hd-operations` document in `Pending` with a bounded TTL and a bounded queue depth per
target, and the agent drains it on reconnect. Superseded operations for the same app
collapse to the newest rather than replaying a backlog on wake.

*Exit:* click any running container and see 24h of metrics plus streaming logs in under 2s,
with the deploying commit linked and deploy markers on the chart. A target offline for an
hour reconnects and converges to the newest desired revision, not through every intermediate one.

### Phase 4 — Cloud adapters, Swarm, and fan-out (8 weeks)
Adapter conformance suite **first**, then ECS → Cloud Run → ACA (descending demand,
ascending API weirdness), then Swarm on the `docker` adapter. Each delivers
plan/apply/observe plus metrics and log mapping. Capability matrix generated from code.

**Fan-out deploy**: one application, N targets selected by group or tag expression, each
with its own `hd-operations` document, health, and drift state, rolled up to one view.
Previously deferred as "ApplicationSets"; Portainer shipping this as a core object
(`edgegroups` + `edgestacks`) says that was too aggressive a cut. Edge, retail, and branch
fleets make it the primary use case rather than an advanced one. Rollout is bounded:
configurable concurrency and a failure threshold that halts the wave.

*Exit:* one compose repo deploys to all five runtimes, with unsupported features rejected
at plan time with a clear message naming the offending line. One application deploys to a
50-host group, and a failure on host 7 halts the rollout with the remaining hosts untouched.

### Phase 5 — Enterprise controls (3 weeks)
Since this is self-hosted only, projects are an RBAC scope rather than a hard isolation
boundary — this phase is smaller than it would otherwise be. Enterprise SSO (inbound OIDC
federation, inbound SAML 2.0). SCIM 2.0 mapping directory groups onto role bundles.
WebAuthn passkeys enabled. Secret-manager integrations (AWS Secrets Manager, Key Vault,
GCP Secret Manager, local sealed store). Correlated audit export across both ledgers.
Outbound webhooks.

*Exit:* an Okta or Entra ID group grants `operator` on a project through SCIM alone, with
no HEIMDALL-side user administration.

### Phase 6 — Hardening and HA (3 weeks)
Leader election, active/passive failover, backup/restore covering **the FYLO root *and*
SESAME's key material together**, DR runbook, rate limiting, load test to 500 apps × 4
targets, threat model, external security review package. Chaos: provider API 500s, expired
credentials, git unreachable, agent partition, SESAME engine killed mid-request.

*Exit:* documented RTO/RPO; a restore from cold backup produces a control plane that can
verify a pre-existing session; sustained load with p99 sync latency inside SLO.

### Phase 7 — GA (2 weeks)
Docs site (Tachyon + DuVay), quickstart, **migration guides from raw `docker compose` and
from Portainer**, support tiers, versioned public API contract under `api/`, CalVer release.

**Total ≈ 27 weeks.**

### Deferred past GA
Template catalog (`templates`/`customtemplates` equivalent) — high adoption in Portainer,
but it sells a first deploy rather than improving the tenth, so it is not on the critical
path. Multi-source applications. A notification engine beyond outbound webhooks. An
interactive container shell, which is rejected on principle rather than deferred.

**Phases 8–10: Terraform/OpenTofu infrastructure provisioning** — see
[`PLAN-IAC.md`](PLAN-IAC.md). ≈13 weeks post-GA. Terraform does *not* fit the
`DeploySpec`/`Provider` model (its own plan/apply/state lifecycle inverts ours), so it lands
as a second reconciler shape in `internal/infra/` rather than a fifth adapter. The
differentiator is the seam nobody else covers: infra outputs flowing into compose overlays
as typed refs, so one control plane provisions the cluster *and* deploys the workload onto
it. That plan's Phase 9 is the phase that justifies the feature.

---

## Verification

Per-phase gates, each runnable:

- **Schema/render** — `go test ./internal/render ./internal/spec`. A corpus in
  `testdata/compose/` renders to golden `DeploySpec` JSON; content hashes are stable across
  runs and machines (this also proves canonical JSON is actually canonical).
- **Adapter conformance** — `go test ./internal/provider/conformance` runs the *same* suite
  against every adapter behind a fake, so an adapter cannot silently diverge. This is the
  single highest-value test asset in the project; it is written before the second adapter.
- **Authorization** — `go test ./internal/auth` against a **real compiled `sesame` binary**
  and a real deployment, mirroring SESAME's own `test/adversarial` posture. Assertions: no
  grant → `403` with the expected `reason_code`; engine killed → `503`, never a bypass;
  every route in the table maps to exactly one action.
- **CI gates that fail the build** — no secret value in any persisted document (grep gate
  over golden fixtures); no handler comparing a role name or hashing a password; the
  generated capability matrix matches `Capabilities()`.
- **End-to-end, Phase 1 onward** — `scripts/e2e-docker.sh`: start `fylo` + `sesame` +
  `heimdall serve` against a scratch root under `/tmp` (**never inside this Dropbox tree** —
  FYLO forbids sync filesystems), point at a fixture compose repo, deploy to a local Docker
  Engine, `docker rm -f` a container out-of-band, assert self-heal restores it within 30s,
  then rollback and assert the prior digest is live.
- **Crash safety** — kill `heimdall serve` mid-sync, restart, assert `hd-operations` holds
  one document in a resumable state and that rebuilding `hd-livestate`/`hd-events` from it
  reproduces the pre-crash projection byte-for-byte.
- **UI** — Playwright, matching the sibling repos' setup: instance drawer renders 24h of
  metrics under 2s at p95; `<hd-timeseries>` axis labels and deploy markers assert against
  fixed data; visual-regression snapshots in light, dark, and high-contrast DuVay themes.

---

## Risks

| # | Risk | Sev | Mitigation |
|---|---|---|---|
| 1 | **FYLO is single-engine/single-root, and SESAME inherits the same ceiling.** The control plane is single-writer end to end | High | Active/passive with a lease document; FYLO root on **block storage only** (EBS/PD/Azure Disk + snapshots) — EFS/Azure Files/NFS are excluded by FYLO's own locking rules. Reconcile concurrency comes from queue consumers on the leader. If write scale is ever needed, shard by project: one FYLO root and one SESAME engine per project. The constraint is at least consistent across the stack and lifts for both at once when FYLO ships replication. **Design in at Phase 0; retrofitting is expensive.** |
| 2 | Compose→cloud translation is lossy and will disappoint | High | `Capabilities()` + fail-closed plan-time rejection + a generated matrix. Never silently drop a directive |
| 3 | **SESAME is developer preview** — no 72-hour native gate passed, no independent security review, no version claimed production-supported | High | HEIMDALL's GA gate is downstream of SESAME's. Pin an exact version, track open gates in ADR 0002, and schedule Phase 6 to land *with* SESAME's production-evidence matrix rather than after it |
| 4 | SESAME is **not OpenID certified**; its SAML is proven only against pinned Keycloak 26.0 — Okta, Entra ID, Google Workspace, ADFS unproven | Med | Enterprise SSO is the top procurement blocker in this category. Phase 5 budgets real interop testing against at least Okta and Entra ID, and treats findings as SESAME issues rather than HEIMDALL workarounds. Do not put "SSO" on a datasheet before that passes |
| 5 | No cross-collection transactions in FYLO | Med | Operation-document pattern + rebuildable projections. Verified by the crash-safety test above |
| 6 | Provider API rate limits and cost under drift polling | Med | Event-driven primary, jittered adaptive poll backstop, cheap identity comparison before full describes |
| 7 | Cloud Run's one-service-per-service model breaks multi-service compose | Med | Map each compose service to its own Cloud Run service; reject cross-service `depends_on`; document the pattern rather than emulating it |
| 8 | Metrics scope creep into building a TSDB | Med | ADR 0003 is a standing decision. Provider-native first, agent ring buffer second, Prometheus passthrough third |
| 9 | Four coupled binaries with a startup order | Low | One image, one supervised entrypoint, `heimdall doctor` that runs `sesame doctor` plus FYLO's checks and reports a single verdict. Version-pin all four and test the matrix in CI |
| 10 | **Portainer is entrenched** — 38k stars, free tier, and a management GUI HEIMDALL deliberately will not build. "Why not just Portainer?" is the first question every evaluator asks | Med | Do not compete on GUI breadth; compete on the two things it structurally lacks — continuous reconciliation and instance observability — plus RBAC/SSO/audit in the open core rather than behind a licence. The Phase 7 migration guide should import Portainer stacks and environments so the switching cost is hours, not weeks |
| 11 | Fan-out to large groups turns one mistake into a fleet-wide outage | Med | Bounded rollout concurrency, a failure threshold that halts the wave, and per-target `hd-operations` documents so a partial rollout is inspectable and resumable rather than an all-or-nothing retry |

---

## Success criteria

- One application definition deploys to Docker Engine, Swarm, ECS, ACA, and Cloud Run
- Out-of-band change detected under 60s, self-healed under 30s — **the claim Portainer
  cannot make, so it must hold under test, not just in the happy path**
- Instance metrics view renders under 2s at p95
- A private-registry image deploys with no credential value in any persisted document
- One application fans out to a 50-target group with bounded concurrency and a halting
  failure threshold
- 500 applications on one control plane, p99 reconcile under 10s
- Zero secret values in any persisted document, proven by a CI gate
- Every mutating action attributable to a principal with its `policy_version` and `reason_code`
- Zero authentication or authorization logic in the HEIMDALL codebase, proven by a CI gate
