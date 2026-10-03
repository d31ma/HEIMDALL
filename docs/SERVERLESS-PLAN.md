# Serverless workload providers

Status: approved first-slice design; adapter and runtime qualification pending.
Research checked against official provider documentation on 2026-10-03.

The project is DELMA/HEIMDALL. The working baseline includes existing uncommitted
Drive changes, preserved in an independent local snapshot. The source inventory,
baseline commit, compiler checksum, validation receipts and implementation queue
live beside the checkout in `../heimdall-evidence/`.

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

Keep AWS SDK imports under `internal/provider/lambda`. Target configuration
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

Tachyon qualification uses immutable compiler 27, source
`92f45321c4da369cc0461a9db9502b96cccef4f4`, SHA-256
`657ca58ba18b59d9effdbabc07dea4ebcd59086fa6935a1991822c43568cc3f3`.
Fresh bundle and browser checks preserve the existing cookie/BFF boundary.
Do not substitute a compiler candidate. Run each build/test only after the
coordinating task grants the shared machine lane.

No live provider account has been qualified by this task. Source edits,
fake-protocol passes, local image runtime tests, web build and real browser
results have separate receipts. None implies deployment or provider support
completion. Full local regression, applicable security gates and reviewed
operational documentation precede any later release request.


## API and web integration queue

Source inspection found the existing `run` endpoint is a scheduled-job command
with no payload or synchronous result. Function invocation must remain a
separate route and provider `Invoker` call; it must not change `Runner.RunNow`.
The existing `app:run` action is the appropriate execution permission candidate,
with exactly one existing SESAME boundary decision on the application resource.
Before implementing it, verify the action description and generated contract
against this broadened workload execution scope; do not add authorization logic.

The planned route is `POST /api/v1/projects/{project}/apps/{app}/services/{service}/invoke`.
Resolve the application and target from stored identity, reject suspended apps,
require a declared function in the rendered revision, and fail explicitly when
the adapter does not implement `Invoker`. Do not let body fields select a target,
app, service or version. Payload and response are transient; the existing audit
records path/action/decision metadata, never handler input or output.

The current server has a three-minute write timeout and the BFF defaults to a
60-second upstream timeout with a configurable ceiling. Lambda allows a longer
execution timeout. The first synchronous API must therefore document a bounded
request deadline and an ambiguous timeout outcome: cancellation ends the wait,
not native work that already started. Do not automatically retry, report a
transport timeout as a handler failure, or claim cancellation stopped native
execution. Long-running/asynchronous delivery is a later explicit contract.
The BFF remains a proxy and preserves its httpOnly session and CA verification.

The existing BFF permits a 1MiB JSON envelope and uses a 32MiB response ceiling.
The provider permits 6MiB Lambda payloads. The first browser-facing API must use
an explicit smaller byte limit compatible with the envelope, while preserving
native limits for direct adapter callers. Source inspection also found that the
BFF client currently truncates oversized responses rather than rejecting them;
fix and test that transport behavior before exposing function results.

Function observations need service/version routes using `FunctionObserver`,
separate from instance CPU/memory routes. The UI should show deployment readiness
and native invocation counts/errors/throttles/duration units. Empty execution
inventory must not render as missing or failed deployment. Existing container
and scheduled-job pages retain their current behavior.

Pending evidence: focused API tests proving one boundary decision and deny/error
handling; suspended/undeclared function and incompatible provider refusal;
byte/deadline bounds; no invocation payload in persistence/audit; handler error
separate from transport failure; BFF byte-bound and no-retry checks; real browser
acceptance under the selected qualified compiler. These are planned, not passed.
