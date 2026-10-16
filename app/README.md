# HEIMDALL application

HEIMDALL application code lives under `app/`: Tac browser views, the Go Yon BFF,
and native Go control-plane packages and commands under `app/server/`. The public
website is a separate Tachyon application in sibling `website/`. The BFF remains
a thin proxy in front of the authoritative control-plane API. The migration targets the
verified qualified compiler 31 artifact, source
`3b6589f22acbe73a1b3ddcc75ea4438ec286d3de`; the local app, HTTP, and website
consumer receipts identify compiler 31. A version string alone does not identify
an artifact.

## Authority and routes

The browser and immediate Yon BFF coordinate display, validate forwarding input,
attach a session, and forward calls. Reconciliation, persistence, provider integration, and
SESAME authorization belong to the Go control plane. The BFF holds no SESAME
engine or FYLO writer and makes no deployment or permission decision.

| Route | Job |
|---|---|
| `POST/DELETE /session` | Exchange credentials for an httpOnly cookie and revoke the control-plane session |
| `POST /gateway` | Attach the session to one bounded control-plane request |
| `GET /audit-login` | Optional configured demo sign-in through the same control-plane login flow; disabled without configuration |

The bearer token stays in an httpOnly cookie. Browser code receives identity and
expiry rather than a replayable credential. `SameSite=Strict` and a default
`Secure` cookie preserve the existing session boundary.

The gateway accepts `GET`, `POST`, `PATCH`, and `DELETE` requests only to permitted
paths under `/api/v1/`; session issuance paths, traversal, and ambiguous path
aliases are refused. Qualified compiler 32 supports custom Go request models via `@FromBody` and custom JSON response models. The current controllers still receive a native `http.Request`, including
URL queries. HEIMDALL keeps its existing POST envelope with a downstream path and
body, including query parameters in that path, to preserve browser callers.
Shared injected helpers provide cookie parsing, request validation, verified TLS,
and bounded responses.

## Source layout

| Directory | Role |
|---|---|
| `client/pages/` | Route templates composed from scoped components |
| `client/components/` | Scoped `tac.html` and `tac.css` views |
| `client/services/` | Annotated page services with lifecycle hooks and template event methods |
| `client/models/` | One named browser data contract per file |
| `client/shared/utils/` | Stateless utility classes |
| `client/shared/scripts/` | Supported existing API, paging, and custom-element facades |
| `client/shared/assets/`, `client/shared/styles/` | Vendored assets and shared styles |
| `server/controllers/` | Three immediate Go `@RequestRoute` Controller fragments |
| `server/services/` | Immediate Yon session/gateway orchestration and nested native domain packages |
| `server/helpers/` | Immediate injected Yon transport helpers and nested native git/schema/secret packages |
| `server/models/` | Nine immediate BFF models and the native `spec/` domain package |
| `server/utils/`, `server/repositories/` | Native utility and store packages |
| `server/commands/` | Native `heimdall` CLI/control-plane entry and isolated fixture engine |
| `../website/` | Sibling public website application |
| `server/data/` | Example configuration; active runtime configuration belongs in the isolated execution copy |

Public service methods coordinate calls and store results; decisions live in
private methods. Components bind to services, and models have matching named
files. Utilities contain static methods and no state. The Go BFF uses the
qualified compiler's annotated controller/service/helper/model conventions;
it replaces the former JavaScript route handlers and local module layout.

The canonical source has no root `internal/`, `server/`, `cmd/`, or `web/` tree,
and no `app/internal/`. All authored application code lives under `app/`; the
public website lives in sibling `website/`. The 23 immediate Yon BFF fragments
remain three controllers,
two services, nine helpers, and nine models. They omit a Go `package` declaration
and use only the standard library. The nested native Go files have ordinary
`package` declarations and keep the existing control-plane behavior and domain
package names. A source location check does not establish compiler admission.

The merged module is `github.com/d31ma/heimdall/app/server`, declares Go 1.27,
and retains the pinned control-plane SDK and SESAME dependencies. The immediate
BFF fragments do not import those dependencies or the module's native
subpackages. Controllers depend on services, and services depend on injected
helpers. The browser and BFF never own the authoritative backend simply because
its source is now in the same `app/server/` tree.

`GatewayCall`, `ProxyFrame`, and `ProxyRequest` are owned by `server/models/`.
Their Go declarations retain the original native fields: `GatewayCall` preserves
`json.RawMessage`; `ProxyFrame` carries native HTTP headers; `ProxyRequest` holds
file handles, HTTP requests/responses, context and cancellation, and a transport
error for one request. Moving these files does not change JSON payload handling,
header semantics, or transport ownership. The models have no methods,
persistence, authorization, or provider behavior. No field is hidden from schema
intake and no value is converted merely to make the schema checker accept it.

Compiler support for the native fields in the model role remains pending.
Source review of the qualified compiler 31/32 native `check_all` path predicts
`TY2015` when imported native field types are treated as unsupported schema
identifiers. This is a source diagnosis; model-intake execution has not run. The
appropriate compiler bridge must be qualified before this layout can claim fresh build, HTTP, or browser support.
The earlier compiler 31 HTTP 49 and app 22 checkpoints describe the former model
locations. The source CI gate rejects revived legacy roots or `app/server/shared/`,
unclassified native packages or Yon fragments, extra Go modules, backend
JavaScript, native authority dependencies in browsers/BFF fragments, and
executable code or global state in immediate BFF models.

The authoritative native Go control plane now lives under `app/server/`, in
ordinary Go packages nested below the requested role groupings:

| Directory relative to `app/` | Existing domain packages |
|---|---|
| `server/controllers/` | `AuditLoginController`, `GatewayController`, `SessionController` (Yon fragments) |
| `server/services/` | `api` (native HTTP service), `agent`, `auth`, `dispatch`, `enroll`, `observe`, `provider`, `reconcile`, `registry`, `render` |
| `server/utils/` | `cron`, `diff`, `sequence` |
| `server/helpers/` | `git`, `schema`, `secrets` |
| `server/models/` | `spec` |
| `server/repositories/` | `store` |
| `server/commands/` | `heimdall`, `fake-engine` |

These are native Go packages, while the immediate controller/service/helper/model
files are Yon fragments. Only `heimdall serve` owns the SESAME engine and
Go-owned FYLO writer. Provider SDKs are confined to
`app/server/services/provider/`, and secret-store SDKs to
`app/server/helpers/secrets/`. The source gate covers all first-party Go security
rules, exempts only the existing FYLO/CHEX vendor shims, and denies native
subpackage or external-library imports from immediate BFF fragments. It applies
BFF DTO and browser restrictions to those fragments and clients, while the native
`models/spec` package retains its domain behavior. The separate public site's
existing `hd-site-theme` preference has a source-hash fence around its reviewed
light/dark utility and sole writer. Other website storage accesses, changed
preference code, or additional writers fail. The application's browser and BFF
retain the unconditional browser-storage ban; sessions remain in httpOnly
cookies.

Qualified compiler 32 source review identifies additional intake blockers for
this unified tree: `TY1363` for nested role packages, `TY1310` for the native
`spec` model package, and `TY2015` for native model field types. A native companion
bridge is not yet qualified. No compiler or native build has run against this
renamed layout; source gates are not compiler admission, build, HTTP, or runtime
qualification. Earlier compiler 31 receipts describe the former source locations; renaming
`web/` to `app/` and restoring sibling `website/` is not new qualification.

## Running an isolated copy

Use a qualified `ty` artifact on PATH and matching Go, Node, SESAME, FYLO, and
other repository tool prerequisites. Keep dependencies, generated bundles, BFF
configuration, and deployment state outside a sync-backed source checkout. Copy
source into a checked execution directory, install its frozen dependencies there,
and retain authored edits in the canonical source.

From the repository root, prepare the pinned DuVay asset with
`scripts/vendor-duvay.sh` if it is absent. From the isolated application execution root:

```bash
npm ci
cp server/data/bff.example.json server/data/bff.json
$EDITOR server/data/bff.json
ty bundle . --out-dir dist --no-incremental
ty serve . --port 8081 --out-dir dist --no-bundle
```

The BFF reads `server/data/bff.json` relative to that execution root. Keep active
configuration and any configured demo credentials out of synced evidence.

| Field | Meaning |
|---|---|
| `apiUrl` | Control-plane HTTPS address, for example `https://127.0.0.1:8443` |
| `caFile` | Trusted control-plane CA file |
| `secureCookie` | Defaults to `true`; use `false` only for a plaintext local loop |
| `timeoutMs` | Per-call deadline, clamped to 25,000 ms; nonpositive values use that ceiling |

The Go transport loads the configured CA and verifies TLS. It does not offer a
certificate-verification bypass and does not follow redirects.

## Request and response limits

The 25-second BFF deadline leaves headroom inside Yon's 30-second invocation
limit. An example configuration requesting 60 seconds still receives the
25-second ceiling. Gateway responses have an 8 MiB raw UTF-8 limit and a 15 MiB
encoded-frame limit below Yon's 16 MiB bound. Invalid or oversized upstream bodies
return a typed transport error; valid responses retain the upstream status and
body, with the existing `{}` representation for an empty gateway response.

A direct function invocation timeout reports that execution may continue; the UI
must not retry it automatically. The transport derives its deadline from the
native request context, but browser-disconnect propagation through the Yon worker
has not been qualified. These are bounded buffered calls, not an unbounded live
stream.

## Pages

| Page | Shows |
|---|---|
| `/` | Sign in, then projects and applications with search |
| `/apps/{project}/{app}` | Sync, health, revisions, instances or observed functions, actions, and history |
| `/apps/{project}/{app}/i/{instance}` | Instance metrics, logs, and events |
| `/approve` | Device-code lookup followed by explicit approval or denial |
| `/registry` | Root binding, sync history, and confirmed unbinding |

Function rows describe observed provider versions separately from execution
instances. The direct invocation control applies only where the provider contract
supports it. Browser fixture evidence cannot establish native provider runtime or
live-cloud qualification.

## Validation status

The current source includes the Tac layout/cohesion migration and Go BFF.
Before the native model relocation, qualified compiler 31 freshly compiled eight
app routes and three Go entries.
The generated TypeScript semantic gate and all 22 browser assertions passed:
13 ordinary UI tests and nine function/instance fixtures. The original app
harness then failed during process cleanup; its failure and independently
verified manual cleanup are recorded separately, so that harness is not reported
as an overall pass. The fixtures cover observed function versions, zero
execution inventory, single invocation, ambiguous timeouts, stale metrics, panel
replacement, navigation disposal, and scheduled pagination.

A later granted compiler 31 run, also before the native model relocation, passed
all 49 local Go HTTP boundary tests,
and freshly compiled all 11 website routes, passed the website semantic gate,
and passed all five website browser tests. That run completed with unchanged
source and no remaining owned processes. Before the root and subsequent all-web control-plane directory
moves, the repaired native source passed all 31 Go packages, vet, the measured
changed-method complexity gate (1,062 methods; maximum CRAP 13.125), and the isolated control-plane rebuild.

The 13 ordinary full-path browser scenarios have not run. Their latest harness
attempt built the local control plane and fixture engine and completed
initialization and login, then timed out on the second setup sync request before
browser execution. Diagnosis remains separate from the passing browser/HTTP
checks. These local checks do not establish native function-host or live-cloud
qualification.

Existing JavaScript checks can be invoked from the isolated execution root with
`npm test`; the migration must also validate the generated Go Yon program and
its HTTP boundaries. `npm run typecheck` names the repository's semantic gate;
annotated source and emitted programs must be checked using the qualified
compiler-compatible validation route. UI, HTTP boundary, and site browser suites
live in sibling `tests/app/`.
Native Go tests live in mirrored sibling `tests/server/` package paths and are
absent from production `app/server/`. Use `scripts/native-go.sh` for native
`test`, `vet`, and package `list` operations through an isolated source/test
overlay. This relocated-test runner has not been build/runtime qualified; direct
Go package tests without that overlay omit the relocated tests. `scripts/e2e-web.sh` is the existing full-stack
harness; its local Docker interaction and runtime configuration need an intended
isolated environment before execution.

CI requires a qualified Linux AMD64 compiler artifact and its pinned original
manifest through `TACHYON_LINUX_AMD64_BINARY_URL`,
`TACHYON_LINUX_AMD64_MANIFEST_URL`, and
`TACHYON_LINUX_AMD64_MANIFEST_SHA256`. Those inputs are currently unavailable;
CI fails explicitly until a genuinely qualified matching artifact is supplied.
The qualified macOS compiler does not satisfy that Linux requirement.
Publication, deployment, and live-cloud validation are outside this migration.

The typed request migration is recorded in the implementation queue. Dynamic HTTP status, cookies and headers use the supported native response boundary; a custom JSON body model does not set transport metadata. Native gateway body bytes and agent mTLS cannot be replaced by a lossy model or forwarded header. These contracts require qualified framework support and runtime proof before parser removal.
