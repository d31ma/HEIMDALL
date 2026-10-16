# HEIMDALL

GitOps continuous delivery and observability for **Docker Compose** workloads —
across Docker Engine, Docker Swarm, Amazon ECS, Azure Container Apps, and Google
Cloud Run.

ArgoCD's value is not Kubernetes. It is four mechanics: git is the only source of
truth; a desired state renders deterministically from a commit; live state is read
back continuously; the controller reports the diff and closes it. None of that is
Kubernetes-specific.

The differentiator is the second half: **click a running instance and see its
metrics, logs, events, and the exact commit that put it there** — without standing
up Prometheus, Grafana, and Loki alongside.

> **Status: 26.33.1, GA.** A compose repository deploys to Docker Engine,
> Swarm, ECS, Cloud Run, and Azure Container Apps — directly or through an
> outbound-only agent, one host or a fifty-host fleet — with drift detection,
> self-heal, rollback, metrics with deploy markers, SCIM-provisioned access,
> and drilled backup/restore. Each phase's evidence file
> (docs/PHASE_*_EVIDENCE.md) records exactly what is proven and what is not.

The current authored version is `26.34.01-1`. Its migration adds explicit native
AWS Lambda and Azure Functions contracts alongside the existing Cloud Run
container adapter; images and invocation contracts are provider-specific.
At source baseline `5459b2973a45c6b43f2d18564ff492c54624ea06`, the Lambda and Azure
Functions package suites passed with 90.5% and 92.4% statement coverage,
respectively. These adapter/SDK-boundary results do not establish function-host
or live-cloud qualification. Live-cloud validation remains absent.

The Tac app and public website passed local consumer checks with qualified
Tachyon compiler 31, source `3b6589f22acbe73a1b3ddcc75ea4438ec286d3de`.
The fresh eight-route app build, semantic check and 22 browser assertions passed;
the original QA driver's cleanup failure and verified recovery remain recorded
separately. The subsequent 49 Go Yon HTTP boundary tests passed without skips,
and the fresh eleven-route website build, semantic check and five browser tests
passed with source hashes unchanged and owned cleanup complete.
The native qualification checkpoint passed all 31 Go packages, vet, an isolated
Go 1.26 control-plane build and the measured complexity gate across 1,062 changed
methods (maximum CRAP 13.125, limit 15). The five earlier complexity failures
are repaired.
The 13 ordinary full-path browser scenarios remain unqualified: the first
attempt failed before building; the second built the Go 1.27 control plane and
fixture engine, initialized and logged in, then timed out during API setup
before any browser test. A setup-only diagnostic isolated the second sync after
a revision change; its cause and repair remain under investigation.
No qualified Linux CI artifact or native function-host validation is available.
See [the web tier README](app/README.md) and
[the serverless plan](docs/SERVERLESS-PLAN.md) for their distinct gates.

## How it works

```
compose.yaml + overlays + secret refs ──render──▶ DeploySpec (canonical JSON, content-hashed)
                                                        │
                          ┌──────────┬──────────────────┼──────────────┐
                      docker        ecs             cloudrun          aca
```

`DeploySpec` is provider-neutral, CHEX-validated, and stored immutably per
revision. Diffs, rollback, audit, and drift all operate on it. Adapters are the
only code that knows a cloud exists. A compose directive a target cannot express
is rejected at plan time with the offending line — never silently dropped.

## Install

HEIMDALL supervises three sibling binaries. Install them first:

```bash
scripts/install-deps.sh
```

That puts `fylo`, `sesame`, and `chex` in `~/.local/bin`. In an isolated execution
checkout, build the native command from the merged Go 1.27 module into an
external directory. This new module layout still requires build qualification:

```bash
export HD_BUILD_DIR=/tmp/heimdall-build
mkdir -p "$HD_BUILD_DIR"
go -C app/server build -ldflags "-X main.version=$(cat VERSION)" -o "$HD_BUILD_DIR/heimdall" ./commands/heimdall
```

## Quickstart

```bash
export HD_ADMIN_PASSWORD='...' HD_PUBLIC_URL=https://heimdall.example:8443
heimdall init --deployment ~/.heimdall --issuer https://heimdall.example --admin alice
```

`init` runs `sesame init` and `sesame doctor`, bootstraps the installation tenant,
seeds the four role bundles, creates the FYLO collections, generates the TLS
certificate and agent CA, and creates the first administrator. It is idempotent.

The administrator has to be created here: once `heimdall serve` is running it
holds the only SESAME engine, and FYLO's exclusive root lock means the `sesame`
CLI cannot open the same root alongside it.

```bash
heimdall doctor --deployment ~/.heimdall
heimdall serve  --deployment ~/.heimdall --addr 127.0.0.1:8080
```

`doctor` reports one verdict across git and all four binaries, so an operator
debugging a startup failure does not have to know which child broke.

Then, from another terminal:

```bash
HD_PASSWORD=... heimdall login --user alice
heimdall app list --project alpha
heimdall diff --project alpha --app checkout
heimdall sync --project alpha --app checkout --dry-run
heimdall sync --project alpha --app checkout
```

The CLI is a client of the same public API the web tier and CI use — there is
no privileged path into the control plane, so there is no second authorization
surface to keep correct. Passwords are read from the environment and never
from a flag, so they stay out of shell history and `ps` output. A control
plane initialised by `heimdall init` serves its own certificate, so point
`HD_CA_FILE` at `<deployment>/keys/agent-ca.crt`.

### Deploying to a host you cannot reach

An agent connects outbound over mTLS and opens no port:

```bash
heimdall enroll --target <target-id>          # on the control plane
heimdall agent enroll --token '<token>'       # on the Docker host
heimdall agent run
```

The token carries the control plane's TLS certificate fingerprint, and the
agent pins it before sending the token — so its first connection, the one
moment it has no other way to tell the real control plane from an impostor,
cannot be intercepted. See [ADR 0008](docs/adr/0008-agents-are-outbound-only-and-not-principals.md).

## Architecture

| Process | Role |
|---|---|
| `heimdall serve` | Go control plane. API, git sync, render, diff, reconcile workers, provider adapters, metrics proxy. Owns the control-plane listeners and security/persistence authorities |
| ├─ `sesame` | Child process. Every authentication and authorization decision. NDJSON on stdin/stdout, no port |
| ├─ `fylo` | Child process. Documents and queues. NDJSON on stdin/stdout, no port |
| `heimdall agent` | Optional, on a Docker Engine host. Outbound-only mTLS. Applies plans, scrapes container stats |
| `ty serve` | Tachyon Tac browser UI and annotated Go Yon BFF, forwarding to the control-plane API |

Startup order is fixed and fail-closed: `fylo` → `sesame doctor` → `heimdall serve`.
A child that will not start is fatal; the API returns `503` rather than serving
unauthenticated.

**HEIMDALL contains no authentication or authorization logic.** No password
hashing, no session table, no token minting, no role comparison in a handler.
SESAME decides, once, in middleware, before any handler runs. A denial is a `403`
carrying SESAME's own `reason_code`; an engine that does not answer is a `503`,
never a bypass. `scripts/ci-gates.sh` fails the build if that ever stops being
true. See [ADR 0002](docs/adr/0002-sesame-owns-every-security-decision.md).

## Configuration

One environment namespace, `HD_`. Credentials are read from the environment and
never from flags, so they stay out of shell history and `ps` output.

| Variable | Default | Meaning |
|---|---|---|
| `HD_DEPLOYMENT` | `~/.heimdall` | Deployment directory. The backup and restore unit |
| `HD_ADDR` | `127.0.0.1:8080` | Listen address |
| `HD_ISSUER` | `https://localhost:8443` | Token issuer base URL. Must be `https` |
| `HD_TENANT` | `heimdall` | Installation tenant name |
| `HD_FYLO_ROOT` | `<deployment>/fylo-root` | HEIMDALL's document root |
| `HD_PUBLIC_URL` | derived from `HD_ADDR` | URL agents connect to; bound into enrollment tokens |
| `HD_TLS` | `true` | Serve TLS. Enrollment pins a fingerprint, and there is none without it |
| `HD_CA_FILE` | — | CA the CLI trusts, usually `<deployment>/keys/agent-ca.crt` |
| `HD_ADMIN_PASSWORD` | — | Password for the administrator `init --admin` creates |
| `HD_AGENT_DIR` | `~/.heimdall-agent` | Where an agent keeps its credentials |
| `SESAME_BINARY` | from `PATH` | SESAME executable |
| `FYLO_BINARY` | from `PATH` | FYLO executable |
| `CHEX_BINARY` | from `PATH` | CHEX executable |

**A FYLO root must be on a local filesystem.** Not EFS, not Azure Files, not NFS,
and never inside a Dropbox, iCloud, or OneDrive tree — FYLO's locking forbids it
and the failure mode is silent corruption found much later. `store.Open` refuses
the obvious offenders outright.

## Development

```bash
scripts/native-go.sh test
scripts/native-go.sh vet
scripts/ci-gates.sh
scripts/e2e-docker.sh
```

The native runner must select explicit domain-package and command scopes and
include the relocated `tests/server/` sources through an official Go overlay.
The operational UI, HTTP and public-site browser suites live under `tests/app/`. The overlay must
preserve private package access and prove test discovery; validation is pending. An unfiltered `./...`
would include package-less Yon fragments, which require Tachyon-generated Go
qualification instead. Keep runtime state, dependency caches and build products
outside the canonical sync-backed source. Tests that need a binary skip when it is absent, so a partial toolchain gives an
honest partial result rather than a false green. CI installs all three and fails
if any is missing.

The public API contract is generated, never hand-edited:

```bash
heimdall contract --write api/openapi.json
```

The vendored FYLO and CHEX Go shims are refreshed from their upstreams with
`scripts/vendor-clients.sh`. SESAME is a real Go module and is a `go get`.

## Repository layout

```
api/                 versioned public contracts (generated)
app/                 HEIMDALL application implementation
  client/pages/      five Tac views: home, application, instance, approval, registry
  client/components/ scoped tac.html/tac.css views
  client/services/   annotated page lifecycle and event coordination
  client/models/     named browser data contracts
  client/shared/     assets, styles, supported script facades and static utils
  server/            merged Go 1.27 module: github.com/d31ma/heimdall/app/server
    commands/heimdall/ native init, serve, CLI and agent entry point
    commands/fake-engine/ isolated protocol-test command
    controllers/     three annotated BFF HTTP fragments
    services/        two BFF fragments plus native domain packages
      api/ agent/ auth/ dispatch/ enroll/ observe/ provider/ reconcile/ registry/ render/
      provider/      docker, ecs, aca, cloudrun, lambda, azurefunctions, conformance
    helpers/         nine BFF fragments plus native git/, schema/ and secrets/
      schema/chex/   vendored CHEX client
    models/          nine BFF models plus native spec/ package
      spec/          DeploySpec, canonical JSON and content hash
    utils/          native cron/, diff/ and sequence/ packages
    repositories/store/ native FYLO documents, collections and vendored fylo/ client
website/             separate public Tachyon project: eleven page routes
schemas/             *.schema.json for CHEX
tests/server/        mirrored native Go package tests (official Go overlay)
tests/app/           operational UI/HTTP and public-site browser suites
testdata/compose/    conformance corpus
docs/adr/            architecture decision records
scripts/             dependency install, CI gates, e2e, client vendoring
```

All HEIMDALL operational application logic belongs under `app/`; the separate
public site belongs in sibling `website/`. The native Go
control plane, agent and commands and the 23 thin BFF fragments share the merged
`app/server/` Go 1.27 module. Commands live under `app/server/commands/`; no root
`cmd/`, `server/`, `internal/` or Go module remains. Public API contracts, schemas
and fixtures remain root assets rather than application implementations.
Samuel's latest layout explicitly renames the operational `web/` root to `app/`
and retains the public `website/` as its sibling; it supersedes the earlier
all-under-`web/` arrangement.

The co-located BFF has three controllers, two services, nine helpers and nine
models. `GatewayCall`, `ProxyFrame` and `ProxyRequest` preserve their native Go
fields. Native domain packages retain control-plane authority; the BFF still
calls public HTTP and opens no FYLO root or SESAME engine. It imports no native
authoritative package. The native HTTP API belongs to package `api` under
`app/server/services/api/`; it is a normal native service, separate from the
three actual Yon `Controller` fragments. Its tests mirror that service path.
Qualified compiler 32 supports custom `@FromBody` models on annotated Go
controllers. Moving the native API service does not complete the BFF's requested
typed-model parsing/response migration: status, headers, cookies, body and native
field admission still require distinct qualification.
Provider SDKs stay in `app/server/services/provider/` and
secret SDKs stay in `app/server/helpers/secrets/`.

Explicit CI checks must enforce these authority and SDK boundaries. The merged
module, native command scopes and co-located companion/framework source intake
have not been qualified. Compiler 31/32 source review predicts `TY2015` for the
native BFF model fields; a qualified native Go model admission path is required.
Static companion-source review also predicts `TY1363` for unregistered native
role packages and `TY1310` for native model/package layout checks on co-located
native packages. These are source diagnoses with no current
compiler execution; the native package admission bridge remains unqualified.
No JSON approximation or vendor-type metadata substitutes for those models.
The earlier 49 HTTP, 22 app browser and native 31-package/complexity results
apply to their recorded earlier layouts. New native fixes are implemented but
have not run their regression tests. No current runtime pass is claimed.

The public site's eleven routes are the landing page, documentation overview,
and nine topic pages. Its scoped components belong to `website/client/components/`;
the five operational app views belong to `app/client/`.

## Licence

Apache-2.0. See [LICENSE](LICENSE).
