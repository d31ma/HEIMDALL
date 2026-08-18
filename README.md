<div align="center">

<img src="website/client/shared/assets/heimdall-mark.svg" alt="HEIMDALL" width="96" height="96">

# HEIMDALL

**GitOps continuous delivery and observability for Docker Compose workloads.**

Docker Engine &middot; Docker Swarm &middot; Amazon ECS &middot; Azure Container Apps &middot; Google Cloud Run

<a href="https://github.com/d31ma/HEIMDALL/actions/workflows/ci.yml"><img src="https://github.com/d31ma/HEIMDALL/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
<a href="https://github.com/d31ma/HEIMDALL/releases/latest"><img src="https://img.shields.io/github/v/release/d31ma/HEIMDALL?label=release&color=e8590c" alt="Release"></a>
<a href="LICENSE"><img src="https://img.shields.io/badge/licence-Apache--2.0-blue" alt="Licence: Apache-2.0"></a>
<img src="https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26">
<img src="https://img.shields.io/badge/platforms-linux%20%C2%B7%20macOS%20%C2%B7%20windows-lightgrey" alt="Platforms">

<a href="#quickstart"><b>Quickstart</b></a> &middot;
<a href="docs/QUICKSTART.md"><b>Docs</b></a> &middot;
<a href="#architecture"><b>Architecture</b></a> &middot;
<a href="docs/adr/"><b>ADRs</b></a> &middot;
<a href="CONTRIBUTING.md"><b>Contributing</b></a>

</div>

---

ArgoCD's value is not Kubernetes. It is four mechanics: git is the only source of
truth; a desired state renders deterministically from a commit; live state is read
back continuously; the controller reports the diff and closes it. None of that is
Kubernetes-specific.

The differentiator is the second half: **click a running instance and see its
metrics, logs, events, and the exact commit that put it there** — without standing
up Prometheus, Grafana, and Loki alongside.

## What you get

| | |
|---|---|
| **Git is the truth** | A commit renders to a provider-neutral, content-hashed `DeploySpec`. Rollback re-applies a stored revision, never a git operation — a force-push cannot change what rolling back means. |
| **Five runtimes, one compose file** | Docker Engine, Swarm, ECS, Cloud Run, Container Apps. A directive a runtime cannot express is **rejected at plan time naming the line**, never silently dropped. |
| **Drift detection and self-heal** | Live state is read back continuously. A killed container and a new commit arrive as the same observation, and policy decides whether to close the gap. |
| **Observability built in** | Per-instance CPU, memory, network, block IO, PIDs and throttling, with deploy markers on the charts and a live log tail. Provider-native metrics first — HEIMDALL never builds a time-series database. |
| **Hosts you cannot reach** | `heimdall agent` connects outbound over mTLS and opens no port. Enrollment pins the control plane's certificate fingerprint, so the first connection cannot be intercepted. |
| **Declared access, declared registry** | SESAME decides every authorization; SCIM provisions it. Applications, targets and repositories are declared in a root repository and reconciled like workloads. |
| **Secrets are references** | `${secret:...}` resolves at apply time inside one provider call. No value enters a revision, a plan, a diff, a log, or any stored document. |

## Status

**26.34.02 — GA, and self-hosted.** HEIMDALL deploys its own website to Amazon
ECS, runs its own control plane, and reconciles its production, staging and
develop environments from a root repository — three branches, one declaration,
promoted by pull request. Every fix in the last release was found by that live
use rather than by a test fake.

Each phase's evidence file (`docs/PHASE_*_EVIDENCE.md`) records exactly what is
proven and what is not, including the standing caveats.

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

That puts `fylo`, `sesame`, and `chex` in `~/.local/bin`. Then build:

```bash
go build -ldflags "-X main.version=$(cat VERSION)" -o heimdall ./cmd/heimdall
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
| `heimdall serve` | Control plane. API, git sync, render, diff, reconcile workers, provider adapters, metrics proxy. **Owns every listener** |
| ├─ `sesame` | Child process. Every authentication and authorization decision. NDJSON on stdin/stdout, no port |
| ├─ `fylo` | Child process. Documents and queues. NDJSON on stdin/stdout, no port |
| `heimdall agent` | Optional, on a Docker Engine host. Outbound-only mTLS. Applies plans, scrapes container stats |
| `ty serve` | Tachyon web UI and BFF, proxying to the control-plane API |

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
go test ./...          # 176 tests, against real git/fylo/sesame/chex binaries
scripts/ci-gates.sh    # standing gates: no auth logic, no leaked secrets, no cloud SDK outside adapters
scripts/e2e-docker.sh  # end to end against a live Docker Engine
go vet ./...
```

Tests that need a binary skip when it is absent, so a partial toolchain gives an
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
cmd/heimdall/        init | doctor | serve | contract | version
                     login | app | diff | sync | enroll | agent
internal/
  spec/              DeploySpec, canonical JSON, content hash
  render/            compose → DeploySpec: overlays, interpolation, secret refs
  git/               mirror, ref resolution, file reads at a commit
  schema/            CHEX validation of compose input and DeploySpec output
  diff/              field-level diff, sync status, health rollup
  reconcile/         refresh, status, sync, dry run, rollback
  provider/          the adapter interface, Capabilities, conformance suite
    docker/          the Docker Engine adapter, stdlib only
  enroll/            enrollment tokens, the agent CA, the server certificate
  dispatch/          rendezvous for outbound-only agents; Remote provider
  agent/             the host process: enrol, poll, apply, report
  store/             FYLO collections and typed documents
  auth/              SESAME supervision, action vocabulary, Decide
  api/               REST routes, authorization boundary, contract generation
web/                 Tachyon web tier: two Tac pages, two Yon proxy routes
schemas/             *.schema.json for CHEX
testdata/compose/    conformance corpus
docs/adr/            architecture decision records
scripts/             dependency install, CI gates, e2e, client vendoring
```

## Licence

Apache-2.0. See [LICENSE](LICENSE).
