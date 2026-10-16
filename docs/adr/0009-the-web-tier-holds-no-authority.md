# ADR 0009 — The web tier holds no authority

**Status:** accepted (Phase 1); implementation description updated 2026-10-03

## Context

The Go control plane owns reconciliation, persistence, provider adapters, and
SESAME authorization. Tachyon serves the browser interface and a thin BFF in
front of that API. The language of a Yon handler does not determine which tier
has authority: the latest qualified compiler supports native Go HTTP handlers,
and the authored HEIMDALL migration uses them.

A BFF that interprets a token or decides whether a deployment is permitted would
create a second authority over the same question. Changing the BFF language or
source layout must preserve the existing control-plane boundary.

## Decision

**The web tier makes no business or authorization decision.** Its routes forward
to the control plane:

- `POST/DELETE /session` exchanges credentials for an httpOnly cookie and revokes
  the control-plane session. Authentication remains a control-plane SESAME call.
- `POST /gateway` validates the forwarding envelope, attaches the session, and
  returns the control-plane status and body within the response limits. An empty
  gateway response retains the existing `{}` representation.
- `GET /audit-login` retains the optional configured demo sign-in path. It is
  disabled without its configuration and uses the same control-plane login flow.

A forwarded 403 retains SESAME's `reason_code`; a forwarded 422 retains the
compose diagnostic. The BFF reports transport and size failures separately and
never infers permission from a browser button, role name, or token.

**The session is an httpOnly cookie.** The browser receives identity and expiry,
not the bearer token. `SameSite=Strict` restricts cross-site delivery, and `Secure`
is enabled by default. The plaintext local-development exception must be
configured explicitly.

**One gateway route serves the control-plane API.** The method and path allowlist
is maintained in one Go helper: `GET`, `POST`, `PATCH`, and `DELETE`, paths under
`/api/v1/`, with authentication session issuance routes excluded. Traversal,
ambiguous path encodings, schemes, and hosts are refused. Yon dependency injection
shares the cookie, transport, and response helpers; handler isolation no longer
requires copying those implementations.

**The forwarding contract remains a POST body.** Native Go Yon handlers receive
an `http.Request`, including its URL and query. Query access is therefore no
longer a framework limitation. HEIMDALL retains its existing gateway envelope,
whose downstream path can contain query parameters, to preserve browser callers.

**The transport has explicit bounds.** Configured `timeoutMs` is clamped to 25
seconds, including the historical example value of 60 seconds, leaving room
inside Yon's 30-second invocation deadline. Gateway responses are limited to 8
MiB of raw UTF-8 and 15 MiB after encoding the Yon response frame, below the 16
MiB framework limit. A direct function invocation timeout reports that execution
may continue and forbids automatic retry. Cancellation of the generated Go
request context has not been established to follow a browser disconnect;
qualification must not claim that propagation.

## Directory roles

| Directory | Responsibility |
|---|---|
| `app/client/pages/` | Tac route templates that compose scoped components |
| `app/client/components/` | Scoped `tac.html` and `tac.css` views |
| `app/client/services/` | Annotated client services coordinating page state and events |
| `app/client/models/` | Named browser data contracts |
| `app/client/shared/utils/` | Stateless utility classes; shared assets, styles, and supported script facades remain under `shared/` |
| `app/server/controllers/` | Three annotated Go Yon HTTP entry points, named `Controller` |
| `app/server/services/api/` | Native package `api`: authoritative HTTP API service, independent of Yon/BFF DI |
| `app/server/services/` | Session and gateway coordination |
| `app/server/helpers/` | Injected configuration, cookie, input, TLS transport, and response mechanics |
| `app/server/models/` | Nine Go request, session, configuration, response and native transport models |
| `app/server/commands/` and native domain subpackages | Authoritative control-plane/agent/CLI implementation, co-located with BFF fragments in one Go 1.27 module |

The BFF does not open FYLO, hold a SESAME engine, or import provider SDKs. Those
remain in native domain packages under `app/server/`. Samuel's permanent rule
places operational application logic under `app/` and public-site logic under `website/`, including control-plane and agent
commands under `app/server/commands/`. The merged module is
`github.com/d31ma/heimdall/app/server`, Go 1.27. The 23 BFF fragments remain
three controllers, two services, nine helpers and nine models. Co-location and
module membership do not transfer authority into the BFF; it still calls the
public HTTP API and opens no FYLO root or SESAME engine.

`GatewayCall`, `ProxyFrame` and `ProxyRequest` belong in `app/server/models/`,
as Samuel requested. Their source move preserves their native Go fields
byte-for-byte: raw JSON, HTTP headers and request/response values, files,
contexts, cancellation and errors. They are not JSON-only DTOs. The corrected
source ownership requires supported native-model schema inference from Tachyon;
it must not be simulated by replacing Go fields with JSON approximations,
editing vendored type metadata or restoring a hidden shared-role exception.
The earlier compiler 31 HTTP and app receipts precede this move. Schema
compatibility and runtime validation of the new model layout remain pending.
Compiler 31/32 source review predicts `TY2015` through model schema checking for
imported types such as `json.RawMessage`, `http.Header`, `os.File`,
`http.Request`, `context.Context`, `context.CancelFunc`, `http.Client` and
`http.Response`. The diagnostic can point at the controller while naming the
model path. It is a source-level diagnosis with no compiler execution yet.
A qualified native Go model admission path is the framework prerequisite.

Go's `internal/` import restriction no longer protects this boundary. CI must
explicitly deny BFF imports from authoritative native domain packages and constrain provider
SDKs to `app/server/services/provider/` and secret SDKs to `app/server/helpers/secrets/`.
The source move preserves existing contracts by intent and requires fresh build,
regression and dependency-boundary validation. The merged module and co-located
native companion/framework intake also remain unqualified. Static review predicts
`TY1363` for unregistered native role packages and `TY1310` for native model/package layout,
in addition to native model schema `TY2015`; no compiler execution is claimed. Native regression
fixes are implemented but unrun. The earlier local qualification below remains
evidence for its original source snapshot, not the relocated tree.

## Typed model parsing goal

Qualified compiler 32 supports custom request models through `@FromBody` on
annotated Go controllers. Restrictions of a plain Go handler do not remove that
annotated-controller capability. The requested BFF migration should use Yon's
request/response model processing at the public boundary while preserving its
transport and security contracts. The current native request/response BFF has
not completed that migration; typed status/header/cookie/body handling, native
field admission and the package source bridge remain to be proven.

The native HTTP API is the separate Go service package `api` under
`app/server/services/api/`, with mirrored tests under `tests/server/services/api/`.
It owns control-plane API behavior and the existing SESAME boundary, and is not
one of the three actual Yon controllers. Do not rename or annotate it as a fake
BFF controller/service to bypass native source-intake checks. Expected static
`TY1363`, `TY1310` and `TY2015` framework blockers remain unqualified.

## Consequences and qualification

- Adding an allowed control-plane endpoint generally needs only a browser API
  call, rather than another BFF route.
- A visible button never grants authority. The control plane makes the single
  boundary authorization decision and returns any refusal.
- Go helper sharing replaces the former duplicated JavaScript route mechanics;
  the gateway and session wire contracts remain stable.
- Qualified Tachyon compiler 31, source
  `3b6589f22acbe73a1b3ddcc75ea4438ec286d3de`, passed the fresh eight-route app
  build, semantic check and all 22 ordinary/function/instance browser assertions.
  Its original QA cleanup failure and verified recovery are preserved separately.
  The actual Go HTTP suite passed 49 tests without skips covering cookies, TLS,
  forwarding, byte limits, malformed input, redirects and ambiguous deadlines.
  The fresh eleven-route website build, semantic check and five browser tests
  also passed with unchanged source hashes and complete owned cleanup.
  The native checkpoint passed all 31 Go packages, vet, measured complexity for
  1,062 changed methods (maximum CRAP 13.125), and an isolated Go 1.26
  control-plane rebuild. The 13 full-path browser scenarios remain unqualified:
  setup failed before browser execution, and a separate diagnostic isolated the
  second sync after a revision change. A causal repair has not been qualified.
  Test fixture responses do not establish native provider or live-cloud operation.
- This migration does not establish native function-host or live-cloud support,
  and authorizes no publication or deployment.
