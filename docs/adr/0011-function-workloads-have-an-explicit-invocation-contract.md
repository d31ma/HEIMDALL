# ADR 0011 - Function workloads declare an invocation contract

Status: model implemented and local regression validated; native adapters pending.

## Context

An ordinary container image can implement a long-running HTTP server. Lambda
container images instead participate in the Lambda Runtime API. Azure Functions
images include a Functions host and trigger metadata. Deploying an image through
an existing container adapter does not establish function runtime compatibility.

The existing canonical model cannot describe those differences. Reusing ports,
replicas or scheduled batch jobs would conceal incompatible contracts and risk
making a plan promise behavior the provider cannot deliver.

## Decision

Function workloads declare `x-heimdall-function`, rendered into optional
`Service.Function`. The first accepted contracts are `lambda-runtime-api` with
`json` invocation and `azure-functions-v4` with `http` invocation. Runtime,
invocation, architecture and normalized timeout participate in revision hashes.
An overlay replaces the declaration as one object.

Function-bearing specs use wire schema 2. Ordinary and existing scheduled specs
retain schema 1 and omit the new field, preserving their canonical bytes and
stored revision hashes. Unknown schema versions and invalid function contracts
are rejected before canonical persistence. Function revisions require the
updated control plane and matching backup/restore tooling; old binaries must
not process them. No mixed control-plane version operation is supported.

Every adapter explicitly answers for function workloads. Existing container
adapters reject them. Native adapters must implement their declared invocation
and packaging contracts and pass the extended conformance suite before they
are registered. No native provider is registered by this model slice.

Function deployment readiness and execution inventory are distinct. Lambda
does not offer a stable list of running execution environments, and idle
Functions can have zero active replicas. Native harnesses can explicitly allow
an empty execution inventory; ordinary container harnesses keep their current
nonempty inventory assertion. Readiness must still converge and carry the
actual deploying revision.

## Consequences

The original Go/FYLO/SESAME control plane remains long-lived and single-writer.
No function lifecycle, local ephemeral disk or request CPU guarantee can be
used as a persistence or reconciliation substrate.

Lambda HTTP endpoints are a later payload/access contract, separate from the
accepted direct JSON invocation slice. Azure uses native Functions on Container
Apps with single-revision hosting and existing storage/identity references;
ordinary ACA deployment is not Functions qualification.

The declaration is an image compatibility assertion, not proof. Protocol
tests, local image runtime checks and live-cloud qualification have separate
evidence. Cloud deployment, IAM changes and publication require a later
authorized task.
