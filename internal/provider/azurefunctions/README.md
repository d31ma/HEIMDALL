# Native Azure Functions adapter source

Status: source remains unregistered and live Azure qualification is absent. The
parent reports Azure, Lambda, all-provider and Node tests passed at checkpoint
`7bb10d847fd7559537b4aa4cb31febe4bff7ab10`. The subsequent cohesion refactor and
new diagnostic/target-definition/version assertions are source-only: they have
not been compiled or tested. No Functions image is qualified by this work.

The adapter uses the pinned azcore ARM pipeline and armappcontainers/v3 models,
adding a typed resource envelope for `kind=functionapp` at API `2026-07-01`.
Typed properties/template/scale envelopes retain
`allowScalingRuleOverride=false`. Provider-generated KEDA rules are platform
owned and do not count as desired-state drift. An enabled scaling override does.
It does not use raw credential headers or a separate HTTP authentication layer.
Automatic ARM resource-provider registration is disabled.

Targets require `subscription_id`, `resource_group`,
`storage_account_resource_id`, `identity_resource_id` and `identity_client_id`;
`Endpoint` names an existing managed environment and `Region` its placement.
Every other target configuration key is rejected before SDK use, including
network, concurrency and workload profile overrides.
References must belong to the target subscription. The workload identity and
storage permissions must already exist. The initial ARM credential path is the
ambient Azure credential chain. Explicit credential references are rejected.

Services explicitly declare Functions v4 HTTP images, pinned by image digest.
The image supplies its Functions host and HTTP binding metadata. This adapter
does not inspect image contents or prove that declaration true. The deployment
uses internal ingress on port 80, a single active revision, zero minimum and ten
maximum replicas, and 0.5 CPU/1Gi memory. Functions host storage uses the existing
user-assigned identity. Reserved storage/runtime environment declarations,
secret environment references, resource overrides, non-amd64 architectures,
function timeout overrides and incompatible container directives are rejected.

Plan compares actual kind, placement, identity, host environment, image,
configuration and readiness alongside ownership metadata. Apply rechecks
ownership before mutation and waits for matching configuration and the latest
ready revision before progressing. Plan, Apply and Observe read that immutable
revision through `GET containerApps/{name}/revisions/{latestReadyRevisionName}`
and compare its template with the app's accepted desired template. Equal old
latest/ready names therefore cannot prove a changed image is deployed. A
metadata-only update may reuse the same matching revision, and image reverts
do not depend on a generated revision suffix. Each revision read is limited to
30 seconds and 1MiB; Apply respects caller cancellation and has a five-minute
convergence bound. Pruning requires explicit opt-in.
Apply options attach through `ApplyOptions.WithContext(ctx)`.

Health describes deployment readiness. Instance inventory is empty because
execution inventory is not implemented. Revision replica counts are read only
to distinguish readiness at scale zero; they do not become execution instances
or a separate observed replica inventory. Observed images come from the
immutable ready revision, with its read-back name as the function version; an absent revision leaves the image explicitly
unobserved, and a failed read surfaces an error. An accepted new app template cannot replace that actual
image while the old revision remains ready.
Metrics and logs explicitly report that a native function/revision scope is
needed. Events return a timestamped deployment observation, not an invocation
history. These limitations must remain visible until the native observability
contract is implemented.

Local HTTP protocol tests use the real ARM SDK pipeline with a TLS httptest
server. They cover conformance, the serialized native kind and pinned API,
storage settings, private ingress, stale-ready-revision polling, cancellation,
failed provisioning, actual drift despite unchanged tags, ownership changes,
prune opt-in, resource-reference validation, pagination confinement and native
KEDA response defaults. Adapter-owned diagnostics are typed `AdapterError`
values with stable codes and wrapped causes where relevant.
The added fixtures also exercise ignored-option attacks, immutable old revision
readback while both app revision names remain equal, application-only revision
reuse, image reverts, actual revision health and zero-replica state. Those fixtures passed at the parent-reported checkpoint; execution after the
current source refactor is pending.

Readiness requires an active, provisioned revision with healthy running or
processing state. A stopped active provisioned revision is accepted at exactly
zero reported replicas and minimum zero when health is None or Healthy. This
last combination models an idle scale-to-zero deployment; the exact running/
health state combination returned by native Functions at API `2026-07-01` has
not been observed live. Missing or unknown states stay Progressing.
The child resource version is explicitly listed in Microsoft Learn at
`containerApps/revisions@2026-07-01`. This supports documented version existence,
not the GET response contract. Revision endpoint/fields are supported by the
pinned SDK and retrievable official REST schema `2025-07-01`; the exact
`2026-07-01` GET schema remains unretrieved. The older GET example omits health
and provisioning states, so live population of those required fields remains
unqualified.
The `2026-07-01` revision endpoint, populated defaults (including generated
probes) and native Functions behavior remain qualification gates. The existing
template comparison rejects nonempty probes; automatic probe defaults could
require a narrowly justified comparison change after provider evidence.

All production source files have one matching named type. Public Provider
methods coordinate private domain units. A source audit counts at most one
explicit `if`, `switch`, `select` or loop in each production method; there are
no production free functions, new explanatory comments or file-level variables.
Target configuration, workload declarations, ARM requests/readback/pagination,
comparisons, ownership, per-service planning, mutation, polling and observation
have separate named units. First-error stages use the shared `utils.Steps`
runner. Changing-state stages use pointer receivers so later steps read actual
results rather than method-value snapshots. Repeated provider bounds, names,
configuration keys and resource positions have associated named getters.
This static audit is not coverage or CRAP-score evidence. Test execution and
review of boolean decision complexity are still required.

`ValidateTargetDefinition` checks references and allowed configuration without
credentials or SDK requests and permits an unstored target with no ID; provider
operations separately require an ID. `AdapterError.DiagnosticCode` and
`DiagnosticMessage` expose the adapter-owned message without the SDK cause.
Observation's function version comes from the identified immutable ready
revision. Missing readback leaves version and image unobserved. Mutation and
missing-resource response close failures now propagate, and pagination no
longer discards a base URL parsing error.

Pending: compile and execute tests in the parent-controlled lane; review further
new API response defaults; qualify
an actual Functions v4 image locally; implement native invocation observability;
then seek separately authorized live-cloud qualification. No registration or
deployment is included in this source slice.

Sources checked on 2026-10-03:

- [Native Functions hosting](https://learn.microsoft.com/en-us/azure/container-apps/functions-overview)
- [ARM containerApps schema](https://learn.microsoft.com/en-us/azure/templates/microsoft.app/containerapps)
- [Functions storage](https://learn.microsoft.com/en-us/azure/azure-functions/storage-considerations)
- [Pinned ARM client source](https://github.com/Azure/azure-sdk-for-go/blob/sdk/azcore/v1.23.0/sdk/azcore/arm/client.go)
- [Revision lifecycle and scope](https://learn.microsoft.com/en-us/azure/container-apps/revisions)
- [Official REST revision schema](https://raw.githubusercontent.com/Azure/azure-rest-api-specs/main/specification/app/resource-manager/Microsoft.App/ContainerApps/stable/2025-07-01/ContainerAppsRevisions.json)

- [Versioned revision resource reference](https://learn.microsoft.com/en-us/azure/templates/microsoft.app/2026-07-01/containerapps/revisions)
- [Older REST GET example](https://raw.githubusercontent.com/Azure/azure-rest-api-specs/main/specification/app/resource-manager/Microsoft.App/ContainerApps/stable/2025-07-01/examples/Revisions_Get.json)
