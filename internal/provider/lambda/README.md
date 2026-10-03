# Lambda workload adapter

Status: local SDK/protocol conformance passed; adapter is not registered.
Native image/runtime and live AWS validation remain missing.

The control plane stays long-lived. This adapter manages image-based default
Lambda functions whose service declares `lambda-runtime-api` and `json`
invocation. The target supplies an existing execution role and region. Images
must use immutable same-region ECR digests. Other target settings are rejected;
there is no role creation, permission grant, VPC change or event registration.

The official Lambda SDK is pinned to v1.98.0, which fits the repository's
existing AWS dependency versions. Credentials follow the existing AWS adapter
boundary: ambient chain or a reference resolved only in the provider call.
Resolved environment values enter native requests and never identity metadata.
Plan compares secret references and environment keys without reading values;
same-reference secret rotations need a later explicit desired-state contract.

Plan is read-only and checks actual code/configuration in addition to tags.
Apply verifies target/spec correspondence, ownership and each operation, waits
for successful native update readiness, publishes an immutable version, and
waits for revision metadata before completing. A failed update cannot be
concealed by old code remaining Active. Native idle Inactive suspension does
not cause an unchanged function to redeploy. Publication conflicts recover only
when an existing immutable version matches actual desired code/configuration.

Direct JSON invocation addresses the verified immutable version. Handler errors
and SDK/transport errors are distinct. Payload reads are bounded at the HTTP
boundary before SDK decoding. Invocations have one attempt: an ambiguous
transport failure never causes automatic repeated handler work. Context
cancellation stops waiting; it does not promise cancellation of native handler
execution that has already started. HTTP URLs, asynchronous event delivery,
durable functions and managed instances are outside this initial contract.

`Instances` returns no invented execution-environment inventory. Optional
function/version observability uses native CloudWatch invocation/error/throttle
counts and duration in milliseconds, and scopes logs to actual native streams
for the requested version. Per-container CPU/memory queries return actionable
diagnostics. Function observability routes and UI remain to be integrated.

The bounded offline test run used Go1.26.6/GOMAXPROCS2/-p1 and passed on first
execution (package1.215s). It exercised the real Lambda, CloudWatch RPCv2/CBOR
and CloudWatch Logs SDK clients against local HTTP fixtures. Tests include
adapter conformance, actual drift, failure/cancellation, idle behavior, published
invocation, no retries, bounded payloads, secret metadata exclusion, metrics
units/dimensions and log version isolation. This establishes SDK protocol
behavior, not native runtime compatibility or account readiness.

Follow-up source now includes publication-conflict recovery and ownership
adversarial cases, native-name collision refusal, and execution class extraction.
Ownership is rechecked after configuration, before publication and before
metadata mutation. These follow-up changes have not been executed; the passed
checkpoint remains `a3421d763932730740cacf31409054cf6176a085`.

Before registration/release: execute publication recovery and ownership
adversarial tests, the code cohesion review, existing provider/full local
regression, applicable security gates, generated contracts, invocation/API and
UI integration, and actual image/runtime qualification. Live cloud qualification
requires a separately authorized task.

Sources checked on 2026-10-03:

- [Image runtime contract](https://docs.aws.amazon.com/lambda/latest/dg/images-create.html)
- [Function state lifecycle](https://docs.aws.amazon.com/lambda/latest/dg/functions-states.html)
- [Native invocation API](https://docs.aws.amazon.com/lambda/latest/api/API_Invoke.html)
- [Configuration update API](https://docs.aws.amazon.com/lambda/latest/api/API_UpdateFunctionConfiguration.html)
- [Function metrics](https://docs.aws.amazon.com/lambda/latest/dg/monitoring-metrics-types.html)
- [Native logs](https://docs.aws.amazon.com/lambda/latest/dg/monitoring-cloudwatchlogs-view.html)


Continuation source review adds structured diagnostic codes, named code values,
and a bounded-response reader split into single-decision helpers. It also
rejects missing/mismatched target identity and unimplemented agent/endpoint
routing before SDK access in Plan, Observe and Invoke. Target role/settings
validation is shared across those provider paths. These continuation changes
and their target refusal cases remain unexecuted while the framework takes the
first heavy lane. The initial passed checkpoint and later untested candidates
remain separate receipts.
