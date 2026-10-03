# Phase 4 evidence — Cloud adapters, Swarm, and fan-out

## Exit criteria

> One compose repo deploys to all five runtimes, with unsupported features
> rejected at plan time with a clear message naming the offending line. One
> application deploys to a 50-host group, and a failure on host 7 halts the
> rollout with the remaining hosts untouched.

| Criterion | State | Evidence |
|---|---|---|
| Five runtimes | **Proven against fakes** | `docker`, `swarm`, `ecs`, `cloudrun`, `aca` all pass the same eleven-case conformance suite |
| Plan-time rejection with the offending service named | **Proven** | The suite's rejection case runs against every adapter's own `Unsupported` spec |
| 50-host fan-out halting on host 7 | **Proven** | `TestFanOutHaltsOnFailure`: hosts 1–6 deployed, host 7 failed, hosts 8–50 asserted untouched down to "saw no pull" |
| Capability matrix generated from code | **Proven** | `api/capabilities.md`, written by `heimdall contract` from the same `Capabilities()` answers plan-time validation enforces |
| Metrics and log mapping per adapter | **Partial** | ECS answers from CloudWatch (metrics and logs); Swarm streams service logs; Cloud Run and ACA refuse with the store named — wiring their query surfaces is deferred until a deployment needs charts in HEIMDALL rather than the cloud console |

## The suite came first

Three cases were added to the conformance suite before any adapter:
instances carry the deploying revision, observing an absent app is empty not
an error, and removing a service plans a visible delete. The standalone
Docker adapter had to pass them before Swarm was written, and every cloud
adapter passed them before registration.

## Decisions worth recording

- **Swarm is the same package as Docker.** It is the same Engine speaking two
  more endpoint families, and sharing the transport, labels, hashing, and log
  framing is what keeps the two from disagreeing about any of them.
- **Fakes at the HTTP boundary, or the SDK's own.** dockertest gained the
  Swarm surface; ECS gets a hand-rolled fake speaking JSON 1.1 — and
  CloudWatch's *actual* wire protocol, which turned out to be Smithy RPC v2
  CBOR, not the Query XML the docs still show; Cloud Run gets a REST fake;
  ACA uses the Azure SDK's generated server fakes.
- **Cloud Run identity rides in annotations, not labels.** The label alphabet
  forbids colons, and a `sha256:` hash that had to be mangled to fit would
  never compare equal to itself again — which reads as an update on every
  plan, forever. The conformance suite caught this before it shipped.
- **`provider.Target` grew `Project` and `Config`.** Region had been
  overloaded to carry the project; a cloud adapter needs the real region, and
  needs somewhere for subnets, execution roles, and log groups that is
  explicitly never a place for credentials.
- **Fan-out halts between waves.** Syncs already in flight run to
  completion — halting means not starting new hosts, never killing
  half-applied ones. Stable name ordering is what makes "hosts 8–50
  untouched" a statement rather than a hope.

## Not done in Phase 4

- **Live-cloud verification.** No AWS, GCP, or Azure account was touched;
  every adapter is proven against its fake. The fakes speak the real wire
  protocols through the real SDK clients, which is the strongest claim
  available without credentials.
- **EFS / Azure Files volumes**, ECS registry credentials for private pulls,
  Cloud Run and ACA metrics/log query surfaces, and auto-sync for group
  applications — each refused or rejected with a message naming the path,
  none silently dropped.
