# Serverless workload providers

Status: the native qualification checkpoint passed all 31 Go packages, vet,
an isolated Go 1.26 control-plane build, and the measured changed-method
complexity gate on 2026-10-03: 1,062
changed methods, maximum CRAP 13.125 against a limit of 15. Native Lambda,
Azure Functions and Cloud Run package coverage was 90.5%, 92.4% and 74.7%.
These are local adapter/SDK and regression results. Native image hosts and live
cloud operation have not been qualified.
These results qualify the recorded pre-relocation snapshots. Samuel's subsequent
permanent rule places operational application logic under `app/` and public-site logic under `website/`: native control-plane,
agent and command packages share the Go 1.27 `app/server` module with the thin
Yon BFF fragments. Co-location preserves the public-HTTP authority boundary;
the BFF imports no authoritative native domain package and opens no FYLO root
or SESAME engine. Explicit CI checks must enforce that and SDK ownership.
The consolidated module, command/domain-package scopes and companion/framework
source intake require fresh qualification. Native fixes are implemented but
unrun; prior qualification drivers using root package paths are stale. Static
companion-source review predicts `TY1363` and `TY1310`, alongside the native-model
`TY2015` below. These identify framework admission blockers by source inspection,
not by a compiler run. The separate public website now lives at `website/`
and also requires fresh consumer qualification after relocation.
The three native BFF transport structs also move byte-for-byte into
`app/server/models/`, making nine models alongside three controllers, two
services and nine helpers. Their native field types require separate Tachyon
model/schema qualification. The 49 HTTP and 22 app browser assertions below
precede that model move; no current runtime pass is claimed for the new layout.
Static compiler 31/32 review predicts `TY2015` from the model schema checker for
imported native fields. Native Go model admission is a framework blocker awaiting
a qualified fix; no compiler execution or replacement JSON types are asserted.
The native HTTP API package now belongs under `app/server/services/api/`,
retaining package `api` and mirrored tests under `tests/server/services/api/`.
The three actual Yon controllers remain separate and named `Controller`.
Qualified compiler 32 supports custom `@FromBody` inputs on annotated Go
controllers, but the current BFF has not completed the requested framework-owned
request/response model migration. Status, headers, cookies, body and native
model fields need distinct proof; no current typed-model runtime pass is claimed.
Research checked against official provider documentation on 2026-10-03.

The project is DELMA/HEIMDALL. The original Drive working copy and existing
uncommitted work were preserved before the approved canonical migration.
Canonical authored source is under `myOrganizations/DELMA/HEIMDALL` in Drive;
dependencies, runtime state and build products stay in the external Developer
Build tree. Redacted source hashes, receipts and the implementation queue are
under Dropbox `AI/shared/codex/recovery/DELMA/2026-10-03/heimdall-web-qualification`.

## Accepted scope

The control plane stays a long-lived Go process with one FYLO writer per root
and its existing supervised SESAME/FYLO processes. Provider support manages
workloads. It does not move the control plane into an invocation environment.
Ordinary containers, manual jobs and existing scheduled workloads retain their
current behavior. No live cloud resource changes are part of this task.

Samuel accepted these first slices through the coordinating task:

- Existing Google Cloud Run services and jobs: audit and correct the actual
  container contract while preserving zero minimum instances by default.
- AWS Lambda: explicit Runtime API compatible images, direct JSON invocation.
  Function URLs and API Gateway HTTP event contracts are separate extensions.
- Azure Functions: Functions v4 HTTP images on native Container Apps hosting,
  with existing environment, managed identity and host storage references.

## Runtime compatibility

| Concern | Cloud Run service | Default Lambda image function | Native Functions on Container Apps |
|---|---|---|---|
| Image contract | HTTP server on `0.0.0.0:$PORT`; entrypoint and args | Linux, one architecture, Lambda Runtime API client; same-region ECR | Functions v4 host plus HTTP trigger metadata; ordinary ACA containers are insufficient |
| On-demand lifecycle | Zero minimum instances; request CPU depends on billing mode | Init/invoke/freeze/shutdown; cold starts possible | Min replicas zero; cold starts possible; host controls concurrent executions |
| Background work | Request CPU cannot guarantee work after response; jobs are explicit | No durable post-invocation work guarantee | Finish work within host invocation or use a separately declared durable pipeline |
| Local state | Ephemeral writable filesystem; persistent mounts are separate features | Read-only root, ephemeral `/tmp`; reuse is not durability | Replica-local state is disposable; Functions host storage is mandatory and separate |
| Networking | One HTTP ingress port; platform TLS; explicit VPC settings | No published port; VPC attachment is not an internet route | Managed environment/ingress; existing identity and network references |
| Access | Existing service identity/IAM; do not disable invoker checks | Existing execution role; direct Invoke uses provider authorization | Internal ingress initially; host/access policy remains explicit |
| Logs and metrics | Cloud Logging/Monitoring | CloudWatch invocation metrics and logs | Azure Monitor/Application Insights and environment logging |
| Readiness | Revision convergence differs from active instance count | Active state AND successful last update AND matching code/config | Native function resource, successful provisioning, matching ready revision; zero replicas are valid |
| Cost | Billing mode, memory/CPU, requests, minimum instances, egress | Requests and duration/memory; extra storage/concurrency options separately charged | ACA hosting/resources and associated storage/logging; not Flex Consumption pricing |

These are different deployment contracts. A declaration does not establish
that an opaque image implements its claimed runtime. Local image tests and
provider read-back are separate evidence.

Sources: [Cloud Run container contract](https://cloud.google.com/run/docs/container-contract),
[Lambda container requirements](https://docs.aws.amazon.com/lambda/latest/dg/images-create.html),
[Lambda lifecycle](https://docs.aws.amazon.com/lambda/latest/dg/lambda-runtime-environment.html),
[native Functions hosting](https://learn.microsoft.com/en-us/azure/container-apps/functions-overview),
[Functions custom handlers](https://learn.microsoft.com/en-us/azure/azure-functions/functions-custom-handlers).

## Canonical function declaration

The initial compose extension is explicit:

```yaml
services:
  handler:
    image: 123456789012.dkr.ecr.us-east-1.amazonaws.com/handler@sha256:<digest>
    x-heimdall-function:
      runtime_contract: lambda-runtime-api
      invocation: json
      timeout: 30s
      architecture: amd64
```

An Azure image declares `runtime_contract: azure-functions-v4` and
`invocation: http`. Image packaging supplies the Functions host and its binding
metadata; HEIMDALL does not transparently wrap a Tachyon HTTP container.

`Service.Function *spec.Function` is optional, with `runtime_contract`,
`invocation`, `timeout_ms` and `architecture`. Overlay declarations replace the
whole function object. Function configuration participates in revision hashes.
Ordinary services omit the field and keep their existing canonical bytes.
Function-bearing specs select wire schema 2; ordinary specs keep schema 1.
Function revisions require the updated control plane and matching restore
tooling. Do not run older control planes over function revisions.

Reject contradictory container fields during render: ports, container replica
configuration, healthcheck, restart, volumes, depends_on, schedule and container
rollout directives. Reject unsupported runtime/invocation pairs explicitly.
Every existing adapter answers the function capability with a rejection until
it implements a function-specific contract. A function-aware provider must
also reject an ordinary image declaration rather than treating it as a handler.

## Lambda adapter contract

Keep AWS SDK imports under `app/server/services/provider/lambda`. Target configuration
supplies an existing `execution_role_arn`; it must never create a role or attach
policies. Use default credential resolution consistently with the ECS adapter.
An image must be digest-pinned in ECR in `Target.Region`. The initial mode is
default Lambda, excluding Managed Instances and Durable Functions.

Plan is read-only. It compares actual image/configuration as well as HEIMDALL
identity metadata, not just a stored hash tag. Create/update uses the Lambda
SDK; Apply waits for successful update readiness per wave and respects context
cancellation. A failed update must report failure even if old code is Active.
Observe supplies deployment health, image, revision and configuration. Lambda
does not expose stable execution-environment inventory; Instances must not
manufacture one. Logs/metrics identify functions or versions explicitly.

`Invoke(ctx, InvocationRequest)` belongs to an optional provider
invocation interface, distinct from `Runner.RunNow`, which starts existing
batch jobs. Bound JSON input/output. Map transport failures separately from
function execution errors; preserve request identity and function version.
When an API route is added it gets one existing SESAME boundary decision.
The web BFF remains a proxy. Avoid inventing authentication in either adapter.

The initial timeout is whole seconds in [1, 900], memory allocation is bounded
by provider limits, and independent container CPU requests are rejected.
No automatic retries of direct invocations: non-idempotent handler work must
not run twice because the control plane repeated an ambiguous request.
External event registrations, scheduled function triggers, provisioned
concurrency and public endpoints require separate explicit desired-state
contracts and conformance coverage.

Sources: [function states](https://docs.aws.amazon.com/lambda/latest/dg/functions-states.html),
[Lambda quotas](https://docs.aws.amazon.com/lambda/latest/dg/gettingstarted-limits.html),
[Function URL payloads](https://docs.aws.amazon.com/lambda/latest/dg/urls-invocation.html),
[VPC internet access](https://docs.aws.amazon.com/lambda/latest/dg/configuration-vpc-internet.html),
[Lambda pricing](https://aws.amazon.com/lambda/pricing/).

## Azure Functions adapter contract

Use `Microsoft.App/containerApps` with `kind=functionapp`, Functions v4 HTTP
packaging, single active revision and zero minimum replicas. Existing target
references supply the managed environment, storage account, subscription,
resource group and user-assigned identity. No provisioning or IAM grant occurs.
Validate IDs and resource consistency before SDK access. Private/internal
ingress is the initial contract; public exposure is an explicit later choice.

Identity-based Functions host storage references are configuration, separate
from FYLO. Storage must already be authorized for the workload identity. Do not
read account keys or persist connection strings. Reject conflicting reserved
host settings instead of silently overriding user declarations.

Nonzero function timeout must initially be rejected: ARM cannot prove that an
opaque image's `host.json` has the requested timeout. An HTTP response timeout
and a function execution timeout are different. Packaging validation is the
future path to honoring the latter. Architecture support must be checked for
the selected hosting environment, not inferred from Lambda's architecture list.

Readiness and drift must include resource kind, managed environment, host
settings, actual image and ready revision. Keep zero active replicas distinct
from deployment failure. Multi-active revisions are excluded because storage
and pull-trigger isolation need explicit contracts. Functions access rules
remain native; no custom role/token logic belongs in HEIMDALL.

The pinned `armappcontainers/v3 v3.1.0` has no ContainerApp `Kind` field. The
current official ARM reference exposes it. Verify a released SDK/API mapping
before registration; do not silently omit `kind` or label an ordinary ACA
deployment as Functions support. A typed azcore/ARM pipeline extension is a
possible narrow SDK boundary if a generated model is unavailable, but needs
its own real HTTP-boundary qualification and documented API version.

Sources: [ARM resource contract](https://learn.microsoft.com/en-us/azure/templates/microsoft.app/containerapps),
[container hosting comparison](https://learn.microsoft.com/en-us/azure/azure-functions/container-concepts),
[Functions storage](https://learn.microsoft.com/en-us/azure/azure-functions/storage-considerations),
[Functions timeouts](https://learn.microsoft.com/en-us/azure/azure-functions/functions-scale),
[Functions pricing](https://azure.microsoft.com/en-us/pricing/details/functions/).

## Verification queue and release boundary

Extend provider conformance before registering adapters. Preserve the existing
container cases, and add explicit function rejection, pure/idempotent planning,
actual configuration drift, failed/pending updates, cancellation, wave order,
prune, ownership isolation and matching-spec refusal. Distinguish deployed
versions/readiness from execution instances; zero inventory is legitimate.

Cloud Run first: prove entrypoint/args reach the actual SDK HTTP boundary,
reject UDP and untranslated Compose healthchecks before cloud access, retain
zero minimum scaling, and detect image/command drift despite unchanged tags.
Job and schedule conformance must remain green.

Next prove function render/hash/schema/capability behavior, then each adapter
through real SDK clients talking to bounded local protocol servers. Test images
with the Lambda Runtime Interface Emulator and Azure Functions host where
those runtimes are available. Those tests establish local runtime behavior,
not live-cloud qualification.

The accepted Tachyon handoff is qualified-scoped compiler 31, source
`3b6589f22acbe73a1b3ddcc75ea4438ec286d3de`, SHA-256
`9f83f0636968438548eccc6a81c5c49e9395462f564cb18feef3f4488cd60608`.
Its manifest and exact Mac arm64 bytes were verified. The fresh app build,
generated semantic check and all 22 browser assertions passed. The original QA
driver failed during cleanup; its failure and verified process cleanup are
retained separately in `app-validation04-compiler31-recovered.json`.
Compiler 31 subsequently passed all 49 Go HTTP boundary checks without skips,
plus the fresh eleven-route website build, semantic check and five browser tests.
The granted consumer scope completed in 23.481 seconds with unchanged source
hashes and no owned processes remaining; see `HEIMDALL-LANE-RETURN31-02.json`
and `website-validation06-compiler31.json` in the evidence directory above.

The 13 ordinary full-path browser scenarios remain unqualified. The first
attempt failed before build or browser execution because `HD_PUBLIC_URL` was
referenced before assignment. The corrected retry built the Go 1.27 control
plane and fixture engine, initialized and logged in, but timed out after
30 seconds during API setup; no browser scenario ran. Both failures and verified
cleanup are preserved in `HEIMDALL-LANE-RETURN31-FULLPATH01.json` and
`HEIMDALL-LANE-RETURN31-FULLPATH02.json`. A separate setup-only diagnostic
isolated the second sync after changing the web service revision while leaving
the cache service unchanged. Repository, target and application creation and
the first sync succeeded. The diagnostic establishes the failed stage, not a
qualified causal repair. No blanket timeout increase or successful full-path
result is asserted.

No qualified Linux artifact has been handed off for CI. Each build/test requires
the coordinating task's shared-machine lane grant.

No live provider account has been qualified by this task. Source edits,
fake-protocol passes, local image runtime tests, web build and real browser
results have separate receipts. None implies deployment or provider support
completion. Full local regression, applicable security gates and reviewed
operational documentation precede any later release request.


## API and web integration

The invocation route is
`POST /api/v1/projects/{project}/apps/{app}/services/{service}/invoke`, distinct
from scheduled-job `Runner.RunNow`. The control plane applies the existing
SESAME `app:run` boundary decision, resolves stored application/target identity,
rejects suspended or undeclared functions and incompatible adapters, and keeps
payloads transient. Requests cannot select a target or native version.

Input is bounded to 256 KiB JSON and a 384 KiB envelope; the control-plane wait
ends after 45 seconds. The Go BFF clamps its upstream deadline to 25 seconds,
within Yon's 30-second deadline, and rejects oversized or malformed responses.
Its gateway bounds are 8 MiB raw UTF-8 and 15 MiB encoded frames, below the
framework's 16 MiB limit. Cancellation or a transport timeout does not prove
native work stopped. Direct invocation is not automatically retried.

Function versions/readiness are distinct from execution-instance inventory.
The browser offers declared Lambda JSON invocation without retry, preserves
handler errors separately from transport errors, and does not manufacture
replicas. Azure has no qualified HTTP invocation or execution telemetry
contract yet; the interface reports unavailable capability explicitly.
Local API/provider regressions passed in the 31-package run. The Go BFF's 49
HTTP checks passed without skips under qualified compiler 31.
Compiler 31 passed the app semantic check and 22 browser assertions, including
function/version and stale-response fixtures. Intercepted browser responses
are not native-host or live-cloud evidence.

## Azure telemetry follow-up

Container Apps ingress `Requests` is platform traffic, not a function invocation
series. Its status codes cannot establish execution errors or native throttles.
Keep platform and execution metrics distinct. Version-scoped `FunctionObserver`
metrics require an existing, verified Application Insights binding: component
resource ID, exact app role, and a telemetry property identifying the native
ARM revision. Do not assume `AppVersion` or `AppRoleInstance` has that meaning.

A later read-only logs slice can discover the existing environment logging
configuration using the pinned managed-environment client. Extract the logging
destination and workspace/customer identifier only; never read or retain shared
keys. Query a verified console table profile through the official `azquery`
LogsClient, with fixed query templates and exact environment/app/revision/workload
container filters. Console and system logs are not invocation statistics.

Before exposing execution telemetry, extend `FunctionSeries` with provenance,
estimation/collection quality and unavailable metric reasons. Application Insights
sampling, host collection filters and ingestion limits can omit executions;
weighted `ItemCount` values do not establish exact counts. Empty or inaccessible
query results cannot establish zero activity. No documented equivalent execution
throttle metric was found; keep it unavailable. Reject partial query results.
Bound the UTC window, rows, response bytes and request duration. New telemetry
SDKs need exact compatible pins and stay inside the provider package. Existing
read permissions and configured telemetry are prerequisites; this task creates
no telemetry resources, instrumentation credentials, IAM grants or networking.

This design remains queued, with no telemetry SDK installation or cloud calls.
Sources: [Container Apps metrics](https://learn.microsoft.com/en-us/azure/azure-monitor/reference/supported-metrics/microsoft-app-containerapps-metrics),
[logging destinations](https://learn.microsoft.com/en-us/azure/container-apps/log-options),
[Functions telemetry](https://learn.microsoft.com/en-us/azure/azure-functions/analyze-telemetry-data),
[collection configuration](https://learn.microsoft.com/en-us/azure/azure-functions/configure-monitoring),
[AppRequests schema](https://learn.microsoft.com/en-us/azure/azure-monitor/reference/tables/apprequests),
[resource-query permissions](https://learn.microsoft.com/en-us/azure/azure-monitor/logs/api/azure-resource-queries).


## Integration candidate after the provider checkpoint

The canonical authored source registers Lambda and Azure Functions, validates native
routing before agent dispatch and target/registry writes, and uses a shared
first-error sequence model for cohesive provider orchestration. This candidate
passed compilation, native protocol/conformance and focused API/registry tests
in the next bounded validation slot. The preceding `7bb10d8` checkpoint remains
preserved; the exact candidate and repair hashes are in the slot receipt.

The API adds the declared-service invocation route above using the existing
SESAME `app:run` action. Input is limited to 256 KiB JSON and a 384 KiB envelope;
the wait ends after 45 seconds. Unknown SDK errors are redacted. The request
cannot select a target or native version, and input is not persisted.

Native observations use service/version `/metrics` and `/logs` routes and the
existing observation actions. Missing versions resolve from actual provider
read-back; absent read-back is a conflict, never an invented version. Log reads
are finite, bounded to 4 MiB and closed even on failure. Lambda supplies native
version-scoped telemetry. Azure has no qualified FunctionObserver contract yet
and returns an explicit unavailable result.

The application page projects declared function contracts and observed versions,
keeps empty execution inventory separate from deployment health, and offers one
JSON invocation for the Lambda contract without retry. Ordinary and scheduled
workload behavior remains under regression checks. New API/registry checks and Node function/gateway checks passed. The corrected native
packages recorded 90.5% Lambda and 92.4% Azure statement coverage. Generated
contract files were refreshed through `heimdall contract`; the full 31-package
run passed. Compiler 31 HTTP and website gates passed; the 13 full-path browser
scenarios remain blocked by the setup sync timeout and have not executed.
