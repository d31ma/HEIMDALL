# Slice 4R evidence — rollout strategy and convergence

Package references below use the 2026-10-03 `app/server/` layout. These results
remain historical evidence from their original source snapshots; the package
move does not requalify them. External runtime receipts retain original paths
and hashes.

The slice designed in [PLAN.md](../PLAN.md#rollout-strategy-and-convergence). It
closes two standing risks (14 and 15 in the plan's risk table) rather than
adding a feature: before it, `Apply` returned when the calls were submitted, so
a failed rollout reported success and came back minutes later as drift.

## What is proven

| Claim | Where |
|---|---|
| `deploy.update_config` parses, defaults (`stop-first`, `pause`), and rejects bad values by name | `app/server/services/render` — `TestUpdateConfigRendersWithDefaults`, `TestUpdateConfigRejectsBadValues` (HD0219) |
| The strategy is part of the revision's content hash | `spec.Service.Update`, `schemas/deployspec.schema.json`; a converged re-apply is still a noop with `update_config` set — conformance `RolloutStrategyConverges`, on all five adapters |
| Swarm receives its native `UpdateConfig`, durations in nanoseconds | `TestSwarmUpdateConfigIsPassedThrough` |
| ECS receives a `DeploymentConfiguration`: start-first 100/200, stop-first 0/100, `rollback` → deployment circuit breaker | `TestUpdateConfigReachesTheDeploymentConfiguration` |
| Standalone Docker performs a real start-first swap for a portless service, and the survivor holds the canonical name | `TestStartFirstSwapsOnlyAfterTheReplacementIsHealthy` |
| A replacement that never becomes healthy is removed and the old container keeps serving (HD0339) | `TestStartFirstKeepsTheOldContainerWhenTheReplacementIsUnhealthy` |
| `start-first` with a published host port is rejected at plan time naming the port and the ways out (HD0338) | `TestStartFirstWithPublishedPortsIsRejected` |
| Two consecutive start-first updates both settle — the second is what catches scratch state the first left behind | conformance `StartFirstRolloutSurvivesAndReportsHealthy`, on all five adapters |
| A sync does not report success until live state is healthy at the new revision, held for `monitor` (HD0273) | `TestBrokenRolloutRollsBackAndTheSyncFails` |
| `failure_action: rollback` re-applies the previous revision's stored spec, and the sync still ends `Failed` with the reason | same test — the image running afterwards is the previous one |
| The wait does not turn healthy deploys into failures | `TestConvergedSyncStillReportsSucceeded`, plus the whole existing suite unchanged |

Both new conformance assertions were mutation-checked: disabling the Docker
rename step leaves `rev-sf-3` failing with a name conflict, which is the failure
the second update exists to catch.

## What is not proven

- **No live Docker daemon, Swarm cluster, or cloud account.** As everywhere
  else in this tree, the runtimes are fakes speaking the real wire protocols.
  The ECS deployment configuration is asserted as *sent*, not as *honoured* —
  only AWS can prove the circuit breaker rolls back.
- **"Never drops below one healthy instance" is asserted per adapter, not in
  the neutral suite.** Sampling `Observe` across an apply from the conformance
  suite is a race dressed up as an assertion: it passed against a deliberately
  broken adapter, so it was removed rather than kept as decoration. The
  property is asserted where a runtime can be made to fail on demand — the
  Docker adapter, via the unhealthy-replacement path.
- **The lease heartbeat is a seam, not a wire.** `reconcile.Engine.Heartbeat`
  is called throughout the wait and is nil today, because nothing consumes a
  FYLO queue yet. The consumer that lands next extends its own visibility
  lease there.
- **Parallelism and delay reach Swarm only.** Both are passed through to the
  orchestrators that own rollout scheduling; the Docker Engine adapter runs one
  container per service, so neither has anything to schedule.

## Notable decisions

- **ECS moved from `partial` to `full` for stop-first.** The old caveat said
  the scheduler always starts the replacement first. With
  `minimumHealthyPercent: 0` and `maximumPercent: 100` it drains first, so the
  caveat was describing a limitation the deployment configuration removes.
- **Rollback triggers on an apply failure too, not only on a convergence
  timeout.** On a standalone Engine a start-first replacement that never
  becomes healthy fails inside `Apply`; treating that as "not a rollout
  failure" would leave `failure_action` meaning different things per runtime.
- **The strictest `failure_action` in the application wins.** Mixed strategies
  across services are legal compose, but a sync is applied as a unit, so a
  single `rollback` decides for the whole operation.
